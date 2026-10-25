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
	"time"

	"github.com/spf13/cobra"
)

var (
	cliRegisterURI  string
	cliRegisterPlan string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this instance with Signal18 Cloud18",
	Long: `Register this replication-manager instance with Signal18 Cloud18 (#2159).

No password: the server shows a Cloud18 login code; open the page it prints in any
browser, log in to Cloud18 and enter the code. Your Cloud18 account needs a GitLab
account linked to it: when it has none, the command prints the page to link one,
then run it again. The instance declares itself in GitLab (domain group, config and
pull repositories, its own project tokens); a URI already registered by this
instance or one of its peers is reconnected, one of another instance is refused.

Example:
  replication-manager-cli register --uri mycompany.ovh.fr-1`,
	Run: func(cmd *cobra.Command, args []string) {
		if cliRegisterURI == "" {
			fmt.Fprintln(os.Stderr, "Error: --uri is required (format: domain.subdomain.zone)")
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
		cliRegisterAndPoll()
	},
}

// cliRegisterAndPoll calls POST /api/register (202), then polls GET /api/register/status
// until the registration ends, printing the Cloud18 login code and the steps.
func cliRegisterAndPoll() {
	payload, _ := json.Marshal(map[string]string{"uri": cliRegisterURI, "plan": cliRegisterPlan})
	body, status, err := cliDoPost("https://"+cliHost+":"+cliPort+"/api/register", payload)
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

	urlStatus := "https://" + cliHost + ":" + cliPort + "/api/register/status"
	shownCode, lastMessage := "", ""
	for deadline := time.Now().Add(17 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		statusBody, httpCode, err := cliDoGet(urlStatus)
		if err != nil || httpCode != http.StatusOK {
			continue
		}
		var st struct {
			State   string `json:"state"`
			Message string `json:"message"`
			Result  struct {
				Device *struct {
					UserCode                string `json:"userCode"`
					VerificationURI         string `json:"verificationUri"`
					VerificationURIComplete string `json:"verificationUriComplete"`
				} `json:"device"`
				LinkURL string `json:"linkUrl"`
			} `json:"result"`
		}
		if json.Unmarshal(statusBody, &st) != nil {
			continue
		}
		if d := st.Result.Device; d != nil && d.UserCode != shownCode {
			shownCode = d.UserCode
			fmt.Printf("Open %s in any browser, log in to Cloud18 and enter the code %s\n", d.VerificationURI, d.UserCode)
			continue
		}
		switch st.State {
		case "complete":
			fmt.Println(st.Message)
			return
		case "link":
			fmt.Fprintf(os.Stderr, "%s\nLink one at %s, then run this command again.\n", st.Message, st.Result.LinkURL)
			os.Exit(1)
		case "timeout", "error", "taken":
			fmt.Fprintf(os.Stderr, "Registration %s: %s\n", st.State, st.Message)
			os.Exit(1)
		}
		if st.Message != lastMessage {
			lastMessage = st.Message
			fmt.Printf("  %s\n", st.Message)
		}
	}
	fmt.Fprintln(os.Stderr, "Client timeout waiting for the registration to complete.")
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
