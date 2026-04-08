// plugin-security-no-password-user raises SEC0100 for every database account
// that has an empty password AND is not protected by a socket-based
// authentication plugin (unix_socket, auth_socket, gssapi, …).
//
// An account without a password and without socket auth can be logged into
// from any host that can reach the MySQL port with no credentials at all.
//
// Config (env):
//
//	REPMAN_IGNORED_USERS  string  default: ""
//	                               comma-separated list of 'user'@'host' pairs
//	                               to suppress (e.g. internal monitoring accounts)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// socketPlugins are authentication plugins that do not require a password;
// they authenticate via OS identity and are considered safe even with an
// empty authentication_string.
var socketPlugins = map[string]bool{
	"unix_socket":  true,
	"auth_socket":  true,
	"gssapi":       true,
	"authentication_pam": true,
	"auth_pam":     true,
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	ignored := parseList(os.Getenv("REPMAN_IGNORED_USERS"))

	var findings []wire.Finding

	for _, u := range req.DatabaseUsers {
		if !u.PasswordEmpty {
			continue
		}
		if socketPlugins[strings.ToLower(u.Plugin)] {
			continue // socket auth — no password needed, not a risk
		}
		key := fmt.Sprintf("'%s'@'%s'", u.User, u.Host)
		if ignored[key] || ignored[u.User] {
			continue
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0100",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: account %s has no password and no socket-based auth plugin"+
					" (plugin: %q) — any client can connect without credentials",
				req.ServerURL, key, u.Plugin),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func parseList(raw string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(item); t != "" {
			m[t] = true
		}
	}
	return m
}
