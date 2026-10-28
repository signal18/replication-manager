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

	"github.com/spf13/cobra"
)

// The Cloud18 login of the replication-manager (#2159): status. The login is made with the account
// the replication-manager is registered with.

const cliCloud18IdentityPath = "/api/clusters/settings/actions/cloud18-identity"

type cliCloud18Identity struct {
	State  string `json:"state"`
	User   string `json:"user"`
	Reason string `json:"reason"`
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
	Long: `The Cloud18 login of the replication-manager, made with the account it is registered with:
it acts as that account for the S3 SSO archive storages and the Cloud18 vault. A refused account:
register the replication-manager again.`,
}

var cloud18StatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Cloud18 login",
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		cliCloud18IdentityPrint(cliCloud18IdentityCall(http.MethodGet, cliCloud18IdentityPath))
	},
}

func initCloud18Flags() {
	for _, c := range []*cobra.Command{cloud18StatusCmd} {
		initServerApiFlags(c)
		cloud18Cmd.AddCommand(c)
	}
}
