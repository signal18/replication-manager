// plugin-security-weak-auth raises SEC0101 for accounts that use weak or
// deprecated authentication plugins.
//
// Strong plugins (no finding):
//   MariaDB: ed25519, auth_gssapi, unix_socket, gssapi, parsec
//   MySQL:   caching_sha2_password, sha256_password, auth_socket, gssapi
//
// Weak / deprecated plugins that trigger SEC0101:
//   mysql_native_password  — SHA-1 based, deprecated in MySQL 8.0, removed in 8.4
//   mysql_old_password     — DES-based, dangerously weak
//   (empty plugin string)  — falls back to server default, often native password
//
// Advisory: accounts using mysql_native_password should migrate to
//   ed25519 (MariaDB) or caching_sha2_password (MySQL 8+).
//   MariaDB 11.6+ supports the PARSEC plugin for PAM-based strong auth.
//
// Config (env):
//
//	REPMAN_IGNORED_USERS   string  default: ""  comma-separated 'user'@'host'
//	REPMAN_INCLUDE_EMPTY   bool    default: true include accounts with empty plugin
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

var strongPlugins = map[string]bool{
	// MariaDB strong
	"ed25519":       true,
	"parsec":        true,
	"auth_gssapi":   true,
	"gssapi":        true,
	"unix_socket":   true,
	"auth_socket":   true,
	"authentication_pam": true,
	"auth_pam":      true,
	// MySQL strong
	"caching_sha2_password": true,
	"sha256_password":       true,
}

var weakPlugins = map[string]bool{
	"mysql_native_password": true,
	"mysql_old_password":    true,
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	ignored := parseList(os.Getenv("REPMAN_IGNORED_USERS"))
	includeEmpty := envBool("REPMAN_INCLUDE_EMPTY", true)

	var findings []wire.Finding

	for _, u := range req.DatabaseUsers {
		plugin := strings.ToLower(strings.TrimSpace(u.Plugin))
		key := fmt.Sprintf("'%s'@'%s'", u.User, u.Host)

		if ignored[key] || ignored[u.User] {
			continue
		}
		if strongPlugins[plugin] {
			continue
		}

		var reason string
		switch {
		case plugin == "":
			if !includeEmpty {
				continue
			}
			reason = "no authentication plugin set (falls back to server default, typically mysql_native_password)"
		case weakPlugins[plugin]:
			reason = fmt.Sprintf("uses weak/deprecated plugin %q — migrate to ed25519 (MariaDB) or caching_sha2_password (MySQL 8+)", plugin)
		default:
			continue // unknown plugin — do not flag
		}

		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0101",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: account %s has weak authentication: %s",
				req.ServerURL, key, reason),
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

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	if v == "true" || v == "1" || v == "yes" {
		return true
	}
	return def
}
