//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package clients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// User-issued API tokens (issue #1835): create / list / revoke, and the
// --api-token flag on every command to authenticate with one.

var (
	cliTokenLabel      string
	cliTokenGrants     string
	cliTokenClusters   string
	cliTokenExpireDays int
	cliTokenJSON       bool
	cliTokenCluster    string
)

// cliTokenView mirrors server.APITokenView.
type cliTokenView struct {
	ID           string    `json:"id"`
	User         string    `json:"user"`
	Label        string    `json:"label"`
	Grants       []string  `json:"grants"`
	Clusters     []string  `json:"clusters"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
	LastUsedFrom string    `json:"lastUsedFrom"`
	RevokedAt    time.Time `json:"revokedAt"`
	RevokedBy    string    `json:"revokedBy"`
	Token        string    `json:"token"`
	Expired      bool      `json:"expired"`
	Revoked      bool      `json:"revoked"`
}

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage your API tokens",
	Long: `Create, list and revoke user-issued API tokens.
A token is a bearer credential narrowed to a subset of your own grants and a
cluster scope; use it with --api-token on any command instead of --user/--password.`,
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an API token for the logged-in user",
	Long: `Create an API token. The token string is printed once; store it now.
  --grants   space separated grant prefixes, e.g. "db-show proxy" (default: every grant you hold)
  --clusters comma separated cluster names (default: every cluster you can see, scope "*")
  --expire-days lifetime in days, -1 for never (default: the server setting, 120 days)`,
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		if cliTokenLabel == "" {
			fmt.Fprintln(os.Stderr, "--label is required")
			os.Exit(2)
		}
		form := map[string]interface{}{
			"label":      cliTokenLabel,
			"grants":     cliTokenGrants,
			"expireDays": cliTokenExpireDays,
		}
		if cliTokenClusters != "" {
			form["clusters"] = strings.Split(cliTokenClusters, ",")
		}
		body, err := cliTokenRequest(http.MethodPost, "/api/tokens", form)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if cliTokenJSON {
			fmt.Println(string(body))
			return
		}
		var t cliTokenView
		if err := json.Unmarshal(body, &t); err != nil {
			fmt.Fprintln(os.Stderr, "cannot parse response:", err)
			os.Exit(1)
		}
		fmt.Printf("id:       %s\nlabel:    %s\nuser:     %s\ngrants:   %s\nclusters: %s\nexpires:  %s\n",
			t.ID, t.Label, t.User, strings.Join(t.Grants, " "), strings.Join(t.Clusters, ","), cliTokenTime(t.ExpiresAt))
		fmt.Printf("token:    %s\n", t.Token)
	},
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your API tokens, or every token covering a cluster with --cluster (needs grant-show there)",
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		path := "/api/tokens"
		if cliTokenCluster != "" {
			path = "/api/clusters/" + cliTokenCluster + "/tokens"
		}
		body, err := cliTokenRequest(http.MethodGet, path, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if cliTokenJSON {
			fmt.Println(string(body))
			return
		}
		var list []cliTokenView
		if err := json.Unmarshal(body, &list); err != nil {
			fmt.Fprintln(os.Stderr, "cannot parse response:", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tLABEL\tUSER\tGRANTS\tCLUSTERS\tCREATED\tEXPIRES\tLAST USED\tSTATE")
		for _, t := range list {
			state := "active"
			if t.Revoked {
				state = "revoked"
			} else if t.Expired {
				state = "expired"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Label, t.User, strings.Join(t.Grants, " "),
				strings.Join(t.Clusters, ","), cliTokenTime(t.CreatedAt), cliTokenTime(t.ExpiresAt), cliTokenTime(t.LastUsedAt), state)
		}
		w.Flush()
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke <token-id>",
	Short: "Revoke an API token (yours, or another user's with cluster-grant on its clusters)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cliTokenInit()
		body, err := cliTokenRequest(http.MethodDelete, "/api/tokens/"+args[0], nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if cliTokenJSON {
			fmt.Println(string(body))
			return
		}
		fmt.Printf("token %s revoked\n", args[0])
	},
}

// cliTokenInit authenticates only: the token commands need no cluster or server
// discovery (cliInit would exit with "Servers not found" on an empty monitor).
func cliTokenInit() {
	var err error
	if cliAPIToken != "" {
		cliToken = cliAPIToken
		return
	}
	cliToken, err = cliLogin()
	if err != nil {
		cliPassword = cliGetpasswd()
		cliToken, err = cliLogin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(14)
		}
	}
}

func cliTokenTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// cliTokenRequest performs an authenticated JSON call and returns the body.
func cliTokenRequest(method string, path string, form interface{}) ([]byte, error) {
	var reader io.Reader
	if form != nil {
		b, err := json.Marshal(form)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(method, "https://"+cliHost+":"+cliPort+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cliToken)
	if form != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cliConn.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(strings.TrimSpace(string(body)))
	}
	return body, nil
}

func initTokenFlags() {
	tokenCreateCmd.Flags().StringVar(&cliTokenLabel, "label", "", "Label of the token (required)")
	tokenCreateCmd.Flags().StringVar(&cliTokenGrants, "grants", "", "Space separated grant prefixes to embed (default: all yours)")
	tokenCreateCmd.Flags().StringVar(&cliTokenClusters, "clusters", "", "Comma separated cluster scope (default: every cluster)")
	tokenCreateCmd.Flags().IntVar(&cliTokenExpireDays, "expire-days", 0, "Lifetime in days, -1 never, 0 server default")
	tokenListCmd.Flags().StringVar(&cliTokenCluster, "cluster", "", "List every token covering this cluster (needs grant-show)")
	for _, c := range []*cobra.Command{tokenCreateCmd, tokenListCmd, tokenRevokeCmd} {
		c.Flags().BoolVar(&cliTokenJSON, "json", false, "Print the raw JSON response")
		initServerApiFlags(c)
		tokenCmd.AddCommand(c)
	}
}
