//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package clients

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	cliRegisterURI   string
	cliRegisterPlan  string
	cliRegisterEmail string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this instance with Signal18 Cloud18",
	Long: `Register this replication-manager instance with Signal18 Cloud18 (#2159).

Your Cloud18 account: the command asks its password (never stored). No account yet:
it offers to create it, Cloud18 sends a confirmation email whose link lets you choose
your password, and the command goes on with it. The instance declares itself in GitLab
(domain group, config and pull repositories, its own project tokens); a URI already
registered by this instance or one of its peers is reconnected, one of another
instance is refused.

Example:
  replication-manager-cli register --uri mycompany.ovh.fr-1 --email admin@mycompany.com`,
	Run: func(cmd *cobra.Command, args []string) {
		if cliRegisterURI == "" || cliRegisterEmail == "" {
			fmt.Fprintln(os.Stderr, "Error: --uri (domain.subdomain.zone) and --email (your Cloud18 account) are required")
			cmd.Usage()
			os.Exit(1)
		}
		var err error
		cliToken, err = cliLogin()
		if err != nil {
			cliPassword = cliGetpasswd()
			cliToken, err = cliLogin()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Login failed: %s\n", err)
				os.Exit(1)
			}
		}
		in := bufio.NewReader(os.Stdin)
		password := cliRegisterReadPassword("Cloud18 password for " + cliRegisterEmail + ": ")
		for {
			state, message := cliRegisterAndPoll(map[string]interface{}{"uri": cliRegisterURI, "plan": cliRegisterPlan, "email": cliRegisterEmail, "password": password})
			switch state {
			case "complete":
				fmt.Println(message)
				return
			case "credentials":
				fmt.Fprintln(os.Stderr, message)
				if !cliRegisterAsk(in, "Create a Cloud18 account for "+cliRegisterEmail+"? [y/N] ") {
					os.Exit(1)
				}
				signUp := map[string]string{}
				for _, f := range [][2]string{{"firstName", "First name"}, {"lastName", "Last name"}, {"company", "Company"}, {"phone", "Phone"}} {
					signUp[f[0]] = cliRegisterLine(in, f[1]+": ")
				}
				state, message = cliRegisterAndPoll(map[string]interface{}{"uri": cliRegisterURI, "plan": cliRegisterPlan, "email": cliRegisterEmail, "signUp": signUp})
				if state != "verify" {
					fmt.Fprintf(os.Stderr, "Registration %s: %s\n", state, message)
					os.Exit(1)
				}
				fallthrough
			case "verify":
				fmt.Println(message)
				cliRegisterLine(in, "Press Enter once the email is confirmed and your password chosen...")
				password = cliRegisterReadPassword("Cloud18 password for " + cliRegisterEmail + ": ")
			default:
				fmt.Fprintf(os.Stderr, "Registration %s: %s\n", state, message)
				os.Exit(1)
			}
		}
	},
}

func cliRegisterReadPassword(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	b, err := terminal.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading password: %s\n", err)
		os.Exit(1)
	}
	return string(b)
}

func cliRegisterLine(in *bufio.Reader, prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func cliRegisterAsk(in *bufio.Reader, prompt string) bool {
	a := strings.ToLower(cliRegisterLine(in, prompt))
	return a == "y" || a == "yes"
}

// cliRegisterAndPoll posts a registration (202), then polls GET /api/register/status until it
// ends; it returns the final state and message.
func cliRegisterAndPoll(payload map[string]interface{}) (string, string) {
	b, _ := json.Marshal(payload)
	body, status, err := cliDoPost("https://"+cliHost+":"+cliPort+"/api/register", b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	if status != http.StatusAccepted {
		var errBody map[string]interface{}
		if json.Unmarshal(body, &errBody) == nil {
			if msg, ok := errBody["error"].(string); ok {
				return "error", msg
			}
		}
		return "error", fmt.Sprintf("HTTP %d: %s", status, string(body))
	}
	urlStatus := "https://" + cliHost + ":" + cliPort + "/api/register/status"
	last := ""
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		statusBody, httpCode, err := cliDoGet(urlStatus)
		if err != nil || httpCode != http.StatusOK {
			continue
		}
		var st struct {
			State   string `json:"state"`
			Message string `json:"message"`
		}
		if json.Unmarshal(statusBody, &st) != nil {
			continue
		}
		switch st.State {
		case "pending", "idle", "":
			if st.Message != last {
				last = st.Message
				fmt.Printf("  %s\n", st.Message)
			}
			continue
		}
		return st.State, st.Message
	}
	return "timeout", "no answer from the server"
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
