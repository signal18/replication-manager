// plugin-security-hardening checks a set of MySQL/MariaDB server hardening
// best practices derived from the CIS MySQL/MariaDB Benchmarks and common
// security guidance.
//
// Each check has its own SEC code so findings can be suppressed individually.
//
//	SEC0103  require_secure_transport=OFF — plaintext connections allowed
//	SEC0104  general_log=ON — all queries (including passwords) are logged in cleartext
//	SEC0105  secure_file_priv=''  — server can read/write any filesystem path
//	SEC0106  skip_name_resolve=OFF — DNS lookups enabled; hostname spoofing possible
//	SEC0107  Anonymous user account exists ('') — anyone can connect without a username
//	SEC0108  User with wildcard host '%' and elevated privilege (SUPER/ADMIN/ALL)
//
// All findings use Severity "SECURITY" so they are visually distinct from
// operational WARNING/ERROR states in the dashboard.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// elevatedPrivPlugins are not privilege indicators but we check plugin as proxy
// for privilege via a separate variable approach; see elevated user detection below.
var elevatedPrivsWildcardIgnored = parseList(os.Getenv("REPMAN_WILDCARD_PRIV_IGNORED_USERS"))

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	var findings []wire.Finding

	v := req.ServerVariables

	// SEC0103 — require_secure_transport
	if val, ok := v["require_secure_transport"]; ok {
		if strings.ToUpper(strings.TrimSpace(val)) == "OFF" {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0103",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: require_secure_transport=OFF — unencrypted client connections are permitted."+
						" Set require_secure_transport=ON to enforce TLS for all connections.",
					req.ServerURL),
			})
		}
	}

	// SEC0104 — general_log
	if val, ok := v["general_log"]; ok {
		if strings.ToUpper(strings.TrimSpace(val)) == "ON" {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0104",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: general_log=ON — all SQL statements (including those containing"+
						" plaintext passwords) are written to the general query log."+
						" Disable in production: SET GLOBAL general_log=OFF.",
					req.ServerURL),
			})
		}
	}

	// SEC0105 — secure_file_priv
	if val, ok := v["secure_file_priv"]; ok {
		trimmed := strings.TrimSpace(val)
		if trimmed == "" {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0105",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: secure_file_priv is empty — LOAD DATA INFILE and SELECT INTO OUTFILE"+
						" can access any path on the server filesystem."+
						" Set secure_file_priv to a dedicated, restricted directory.",
					req.ServerURL),
			})
		}
	}

	// SEC0106 — skip_name_resolve
	if val, ok := v["skip_name_resolve"]; ok {
		if strings.ToUpper(strings.TrimSpace(val)) == "OFF" {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0106",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: skip_name_resolve=OFF — MySQL performs DNS lookups for connecting"+
						" clients, which can be slow and is vulnerable to DNS spoofing."+
						" Consider setting skip_name_resolve=ON and using IP-based grants.",
					req.ServerURL),
			})
		}
	}

	// SEC0107 — anonymous users (empty username)
	for _, u := range req.DatabaseUsers {
		if u.User == "" {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0107",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: anonymous user account ''@'%s' exists —"+
						" any client can connect without specifying a username."+
						" DROP USER ''@'%s' to remove it.",
					req.ServerURL, u.Host, u.Host),
			})
		}
	}

	// SEC0108 — wildcard-host users with elevated plugins
	// We flag users with host='%' that are not using a socket plugin and not
	// in the ignore list.  In the absence of privilege data in the wire
	// protocol we flag all non-socket '%'-host accounts as a best-practice
	// advisory; operators can suppress specific accounts via REPMAN_WILDCARD_PRIV_IGNORED_USERS.
	socketPlugins := map[string]bool{
		"unix_socket": true, "auth_socket": true, "gssapi": true,
		"authentication_pam": true, "auth_pam": true,
	}
	for _, u := range req.DatabaseUsers {
		if u.Host != "%" {
			continue
		}
		if socketPlugins[strings.ToLower(u.Plugin)] {
			continue
		}
		key := fmt.Sprintf("'%s'@'%s'", u.User, u.Host)
		if elevatedPrivsWildcardIgnored[key] || elevatedPrivsWildcardIgnored[u.User] {
			continue
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0108",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: account %s uses wildcard host '%%' — it can connect from any IP address."+
					" Restrict to specific hosts or CIDR ranges where possible.",
				req.ServerURL, key),
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
