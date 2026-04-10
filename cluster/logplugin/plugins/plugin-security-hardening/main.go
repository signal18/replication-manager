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
//
// SEC0104 remediations are derived at plan-generation time from the compliance
// module (with_log_general tag).  SEC0103, SEC0105, SEC0106, SEC0107, SEC0108
// carry hardcoded remediations here since they have no module-tag equivalent.
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
				Remediations: []wire.Remediation{
					{
						Type:        "sql",
						Description: "Enforce TLS at runtime (takes effect immediately, lost on restart without my.cnf change)",
						SQL:         "SET GLOBAL require_secure_transport = ON;",
						Risk:        "moderate",
					},
					{
						Type:        "sql",
						Description: "Enforce TLS persistently without restart (MariaDB 10.3.5+ / MySQL 8.0+)",
						SQL:         "SET PERSIST require_secure_transport = ON;",
						Risk:        "moderate",
					},
					{
						Type:        "my_cnf",
						Description: "Add to [mysqld] section for permanent enforcement (requires restart)",
						MyCnf:       "require_secure_transport=ON",
						Risk:        "safe",
					},
				},
			})
		}
	}

	// SEC0104 — general_log
	// Remediations are derived from the compliance module (with_log_general tag) at
	// plan-generation time — no SQL hardcoded here.
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
				Remediations: []wire.Remediation{
					{
						Type:        "my_cnf",
						Description: "Restrict file operations to a dedicated directory (requires restart)",
						MyCnf:       "secure_file_priv=/var/lib/mysql-files",
						Risk:        "disruptive",
					},
				},
			})
		}
	}

	// SEC0106 — skip_name_resolve
	// In Docker deployments DNS is the primary service-discovery mechanism and
	// container IPs are reassigned on every restart, so skip_name_resolve=ON
	// would break all DNS-based grants without providing meaningful security
	// benefit (IPs are not stable either).  The finding is still raised — DNS
	// spoofing is a real risk — but the description and remediation differ
	// depending on whether the cluster runs on Docker.
	if val, ok := v["skip_name_resolve"]; ok {
		if strings.ToUpper(strings.TrimSpace(val)) == "OFF" {
			var desc string
			var remediations []wire.Remediation
			if req.ClusterContext.DockerDeployment {
				desc = fmt.Sprintf(
					"Server %s: skip_name_resolve=OFF — DNS lookups are enabled."+
						" In a Docker deployment container IPs are dynamic and DNS is used for"+
						" service discovery, so enabling skip_name_resolve would break DNS-based"+
						" grants without improving security (IPs rotate on restart)."+
						" Mitigate via network policies and Docker network segmentation instead.",
					req.ServerURL)
				// No my_cnf remediation for Docker — applying skip_name_resolve would be harmful.
			} else {
				desc = fmt.Sprintf(
					"Server %s: skip_name_resolve=OFF — MySQL performs DNS lookups for connecting"+
						" clients, which can be slow and is vulnerable to DNS spoofing."+
						" Set skip_name_resolve=ON and convert all hostname grants to IP addresses.",
					req.ServerURL)
				remediations = []wire.Remediation{
					{
						Type:        "my_cnf",
						Description: "Disable DNS lookups (requires restart; convert hostname grants to IPs first)",
						MyCnf:       "skip_name_resolve=ON",
						Risk:        "disruptive",
					},
				}
			}
			findings = append(findings, wire.Finding{
				ErrKey:       "SEC0106",
				Severity:     "SECURITY",
				Description:  desc,
				Remediations: remediations,
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
