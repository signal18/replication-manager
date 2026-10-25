//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package clients

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// The Cloud18 login of the replication-manager (#2159): connect (device login), status,
// disconnect. Connect prints the code and the page, then waits until the person approved.

const cliCloud18IdentityPath = "/api/clusters/settings/actions/cloud18-identity"

type cliCloud18Identity struct {
	State  string `json:"state"`
	User   string `json:"user"`
	Reason string `json:"reason"`
	Device *struct {
		UserCode                string    `json:"userCode"`
		VerificationURI         string    `json:"verificationUri"`
		VerificationURIComplete string    `json:"verificationUriComplete"`
		ExpiresAt               time.Time `json:"expiresAt"`
	} `json:"device"`
}

func cliCloud18IdentityCall(method, path string) cliCloud18Identity {
	body, err := cliTokenRequest(method, path, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var st cliCloud18Identity
	if err := json.Unmarshal(body, &st); err != nil {
		fmt.Fprintln(os.Stderr, "cannot parse response:", err)
		os.Exit(1)
	}
	return st
}

func cliCloud18IdentityPrint(st cliCloud18Identity) {
	fmt.Printf("state: %s\n", st.State)
	if st.User != "" {
		fmt.Printf("user:  %s\n", st.User)
	}
	if st.Reason != "" {
		fmt.Printf("why:   %s\n", st.Reason)
	}
}

var cloud18Cmd = &cobra.Command{
	Use:   "cloud18",
	Short: "Cloud18 login of the replication-manager",
	Long: `Connect the replication-manager to the Cloud18 identity once (device login): it acts as
the person who connects it for the S3 SSO archive storages, the Cloud18 vault and its Cloud18
GitLab token. Each peer connects with its own login.`,
}

var cloud18StatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Cloud18 login",
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		cliCloud18IdentityPrint(cliCloud18IdentityCall(http.MethodGet, cliCloud18IdentityPath))
	},
}

var cloud18ConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect the replication-manager: open the page shown in any browser, log in and enter the code",
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		st := cliCloud18IdentityCall(http.MethodPost, cliCloud18IdentityPath+"/connect")
		if st.Device == nil {
			cliCloud18IdentityPrint(st)
			return
		}
		fmt.Printf("Open %s\nand enter the code %s\n", st.Device.VerificationURI, st.Device.UserCode)
		if st.Device.VerificationURIComplete != "" {
			fmt.Printf("(or open %s)\n", st.Device.VerificationURIComplete)
		}
		fmt.Printf("Waiting until %s ...\n", st.Device.ExpiresAt.Local().Format("15:04"))
		for st.State == "pending" {
			time.Sleep(3 * time.Second)
			st = cliCloud18IdentityCall(http.MethodGet, cliCloud18IdentityPath)
		}
		cliCloud18IdentityPrint(st)
		if st.State != "connected" {
			os.Exit(1)
		}
	},
}

var cloud18DisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Forget the Cloud18 login (the SSO storages and the Cloud18 vault refuse until connected again)",
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		cliCloud18IdentityPrint(cliCloud18IdentityCall(http.MethodPost, cliCloud18IdentityPath+"/disconnect"))
	},
}

func initCloud18Flags() {
	for _, c := range []*cobra.Command{cloud18StatusCmd, cloud18ConnectCmd, cloud18DisconnectCmd} {
		initServerApiFlags(c)
		cloud18Cmd.AddCommand(c)
	}
}
