//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	cliRegisterEmail string
	cliRegisterURI   string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this instance with Signal18 Cloud18",
	Long: `Register this replication-manager instance with Signal18 Cloud18.

The server creates a GitLab account, sends a confirmation email, and then
waits (up to 5 minutes) for the user to click the confirmation link.
This command returns immediately after the 202 response and polls the server
every 10 seconds until registration completes, times out, or errors.

Use --confirm for a manual single-shot confirmation check instead of polling.

Examples:
  # Start registration and wait for email confirmation (interactive)
  replication-manager-cli register \
    --email admin@mycompany.com \
    --uri   mycompany.ovh.fr-1

  # Manual confirm after clicking the link (advanced)
  replication-manager-cli register \
    --email   admin@mycompany.com \
    --uri     mycompany.ovh.fr-1 \
    --confirm`,
	Run: func(cmd *cobra.Command, args []string) {
		if cliRegisterEmail == "" {
			fmt.Fprintln(os.Stderr, "Error: --email is required")
			cmd.Usage()
			os.Exit(1)
		}
		if cliRegisterURI == "" {
			fmt.Fprintln(os.Stderr, "Error: --uri is required (format: domain.subdomain.zone)")
			cmd.Usage()
			os.Exit(1)
		}

		// Prompt for GitLab password (hidden input)
		fmt.Fprintf(os.Stderr, "GitLab password for %s: ", cliRegisterEmail)
		passwordBytes, err := terminal.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading password: %s\n", err)
			os.Exit(1)
		}
		password := string(passwordBytes)
		if len(password) < 8 {
			fmt.Fprintln(os.Stderr, "Error: password must be at least 8 characters")
			os.Exit(1)
		}

		// Obtain admin JWT
		cliToken, err = cliLogin()
		if err != nil {
			cliPassword = cliGetpasswd()
			cliToken, err = cliLogin()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Login failed: %s\n", err)
				os.Exit(1)
			}
		}

		confirm, _ := cmd.Flags().GetBool("confirm")
		if confirm {
			cliRegisterConfirm(password)
		} else {
			cliRegisterAndPoll(password)
		}
	},
}

// cliRegisterAndPoll calls POST /api/register (returns 202), then polls
// GET /api/register/status every 10 seconds until a terminal state is reached.
func cliRegisterAndPoll(password string) {
	urlPost := "https://" + cliHost + ":" + cliPort + "/api/register"

	payload, _ := json.Marshal(map[string]string{
		"email":    cliRegisterEmail,
		"password": password,
		"uri":      cliRegisterURI,
	})

	body, status, err := cliDoPost(urlPost, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if status != http.StatusAccepted {
		var errBody map[string]interface{}
		if json.Unmarshal(body, &errBody) == nil {
			if msg, ok := errBody["error"].(string); ok {
				fmt.Fprintf(os.Stderr, "Registration failed: %s\n", msg)
				os.Exit(1)
			}
		}
		fmt.Fprintf(os.Stderr, "Registration failed (HTTP %d): %s\n", status, string(body))
		os.Exit(1)
	}

	fmt.Printf("GitLab account requested for %s.\n", cliRegisterEmail)
	fmt.Println("A confirmation email will be sent by GitLab. Please click the link to confirm.")
	fmt.Println("Waiting for confirmation (up to 5 minutes) …")

	// Poll /api/register/status until terminal state
	urlStatus := "https://" + cliHost + ":" + cliPort + "/api/register/status"
	pollDeadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(pollDeadline) {
		time.Sleep(10 * time.Second)

		statusBody, httpCode, err := cliDoGet(urlStatus)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: status poll error: %s (retrying)\n", err)
			continue
		}
		if httpCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Warning: status returned HTTP %d (retrying)\n", httpCode)
			continue
		}

		var st map[string]interface{}
		if err := json.Unmarshal(statusBody, &st); err != nil {
			continue
		}
		state, _ := st["state"].(string)
		message, _ := st["message"].(string)

		switch state {
		case "complete":
			result, _ := st["result"].(map[string]interface{})
			if connectErr, ok := result["connect_error"].(string); ok {
				fmt.Printf("Registration complete for %s.\n", cliRegisterURI)
				fmt.Printf("Warning: Cloud18 connect failed (%s).\n", connectErr)
				fmt.Println("Set cloud18=true in global settings once GitLab is reachable.")
			} else {
				fmt.Printf("Registration complete. Cloud18 is now active for %s.\n", cliRegisterURI)
			}
			return
		case "timeout":
			fmt.Fprintf(os.Stderr, "Registration timed out: %s\n", message)
			os.Exit(1)
		case "error":
			fmt.Fprintf(os.Stderr, "Registration error: %s\n", message)
			os.Exit(1)
		default:
			fmt.Printf("  status: %s — %s\n", state, message)
		}
	}

	fmt.Fprintln(os.Stderr, "Client timeout waiting for registration to complete.")
	os.Exit(1)
}

// cliRegisterConfirm calls POST /api/register/confirm (manual single-shot).
// Useful when the background poller is not running or for scripted flows.
func cliRegisterConfirm(password string) {
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/register/confirm"

	payload, _ := json.Marshal(map[string]string{
		"email":    cliRegisterEmail,
		"password": password,
		"uri":      cliRegisterURI,
	})

	body, status, err := cliDoPost(urlpost, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if status == http.StatusCreated {
		var resp map[string]interface{}
		if json.Unmarshal(body, &resp) == nil {
			if connectErr, ok := resp["connect_error"].(string); ok {
				fmt.Printf("Registration complete for %s.\n", cliRegisterURI)
				fmt.Printf("Warning: Cloud18 connect failed (%s).\n", connectErr)
				fmt.Println("Set cloud18=true in global settings once GitLab is reachable.")
				return
			}
		}
		fmt.Printf("Registration complete. Cloud18 is now active for %s.\n", cliRegisterURI)
		return
	}

	var errBody map[string]interface{}
	if json.Unmarshal(body, &errBody) == nil {
		if msg, ok := errBody["error"].(string); ok {
			fmt.Fprintf(os.Stderr, "Confirmation failed: %s\n", msg)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "Confirmation failed (HTTP %d): %s\n", status, string(body))
	os.Exit(1)
}

// cliDoPost sends an authenticated POST request and returns (body, statusCode, error).
func cliDoPost(urlpost string, payload []byte) ([]byte, int, error) {
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cliToken)

	resp, err := cliConn.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// cliDoGet sends an authenticated GET request and returns (body, statusCode, error).
func cliDoGet(url string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cliToken)

	resp, err := cliConn.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
