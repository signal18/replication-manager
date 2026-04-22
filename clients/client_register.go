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
	Long: `Two-step registration workflow:

  Step 1: Creates a GitLab account at gitlab.signal18.io.
          GitLab sends a confirmation email to the provided address.

  Step 2: After clicking the confirmation link in the email,
          run the same command with --confirm to complete registration.
          The server verifies the email is confirmed, creates the GitLab
          group and projects, and activates the Cloud18 connect flow.

Examples:
  # Step 1 — create account and trigger confirmation email
  replication-manager-cli register \
    --email admin@mycompany.com \
    --uri   mycompany.ovh.fr-1

  # Step 2 — complete registration after confirming email
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
			cliRegisterInitiate(password)
		}
	},
}

// cliRegisterInitiate calls POST /api/register (step 1).
// The server creates the GitLab account and GitLab sends the confirmation email.
func cliRegisterInitiate(password string) {
	urlpost := "https://" + cliHost + ":" + cliPort + "/api/register"

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

	if status == http.StatusAccepted {
		fmt.Println("GitLab account created.")
		fmt.Printf("A confirmation email has been sent to %s by GitLab.\n", cliRegisterEmail)
		fmt.Println("Click the link in the email to confirm your account, then run:")
		fmt.Printf("\n  replication-manager-cli register --email %s --uri %s --confirm\n\n", cliRegisterEmail, cliRegisterURI)
		return
	}

	// Non-202: print error body and exit non-zero
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

// cliRegisterConfirm calls POST /api/register/confirm (step 2).
// The server checks the GitLab account is confirmed, creates group + projects,
// and activates the Cloud18 connect flow.
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

	// Non-201: surface the error message
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
