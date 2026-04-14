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
//	SEC0113  simple_password_check not loaded or not enforced       (MariaDB only)
//	SEC0114  cracklib_password_check not loaded or not enforced     (MariaDB only)
//	SEC0115  password_reuse_check not loaded or not enforced        (MariaDB only)
//	SEC0116  validate_password plugin/component not loaded          (MySQL only)
//	SEC0117  password_history and password_reuse_interval both 0    (MySQL 8.0+ only)
//	SEC0118  skip_name_resolve=ON but hostname-based user grants exist — silent login breakage
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
	"net"
	"os"
	"sort"
	"strconv"
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
		snrVal := strings.ToUpper(strings.TrimSpace(val))
		if snrVal == "OFF" || snrVal == "0" {
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
	var anonHosts []string
	for _, u := range req.DatabaseUsers {
		if u.User == "" {
			anonHosts = append(anonHosts, u.Host)
		}
	}
	if len(anonHosts) > 0 {
		sort.Strings(anonHosts)
		hosts := make([]string, len(anonHosts))
		for i, h := range anonHosts {
			hosts[i] = fmt.Sprintf("''@'%s'", h)
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0107",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: %d anonymous user account(s) exist — any client can connect without a username."+
					" DROP USER each: %s",
				req.ServerURL, len(anonHosts), strings.Join(hosts, ", ")),
		})
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
	var wildcardAccounts []string
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
		wildcardAccounts = append(wildcardAccounts, key)
	}
	if len(wildcardAccounts) > 0 {
		// Sort for a stable description across monitoring ticks.
		sort.Strings(wildcardAccounts)
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0108",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: %d account(s) use wildcard host '%%' and can connect from any IP address —"+
					" restrict to specific hosts or CIDR ranges where possible: %s",
				req.ServerURL, len(wildcardAccounts), strings.Join(wildcardAccounts, ", ")),
		})
	}

	// SEC0118 — skip_name_resolve=ON but hostname-based user grants exist
	//
	// When skip_name_resolve is enabled, MySQL/MariaDB skips all DNS lookups.
	// Any account whose Host is a DNS hostname (not an IP, not '%', not 'localhost')
	// will silently fail every connection attempt — the server never resolves the
	// hostname to match the connecting client IP.  These grants are effectively dead.
	snrOnVal := strings.ToUpper(strings.TrimSpace(v["skip_name_resolve"]))
	if snrOnVal == "ON" || snrOnVal == "1" {
		var hostnameAccounts []string
		for _, u := range req.DatabaseUsers {
			if isHostname(u.Host) {
				hostnameAccounts = append(hostnameAccounts, fmt.Sprintf("'%s'@'%s'", u.User, u.Host))
			}
		}
		if len(hostnameAccounts) > 0 {
			sort.Strings(hostnameAccounts)
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0118",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: skip_name_resolve=ON but %d account(s) use DNS hostnames as host —"+
						" with DNS resolution disabled these accounts can never connect."+
						" Convert each grant to an IP address (RENAME USER ... TO ...@'<ip>'): %s",
					req.ServerURL, len(hostnameAccounts), strings.Join(hostnameAccounts, ", ")),
			})
		}
	}

	// SEC0113 / SEC0114 / SEC0115 — password strength validation plugins (MariaDB only)
	//
	// Each plugin exposes a characteristic system variable when loaded:
	//   simple_password_check     → simple_password_check_minimal_length   (SEC0113)
	//   cracklib_password_check   → cracklib_password_check_dictionary     (SEC0114)
	//   password_reuse_check      → password_reuse_check_interval          (SEC0115)
	//
	// These variables only appear in SHOW GLOBAL VARIABLES when the plugin is loaded.
	// MySQL uses validate_password instead — checked separately by plugin-score-auth.
	//
	// A state is also opened when strict_password_validation=OFF: in that mode
	// plugin failures are advisory only (users can still set non-compliant passwords),
	// so having a plugin loaded but validation not enforced is as bad as no plugin.
	//
	// Password validation is considered secured (HasStrongPwd score check) when
	// strict_password_validation=ON AND (simple_password_check OR cracklib) is loaded.
	// password_reuse_check (SEC0115) is a separate complementary requirement.
	if req.ServerVersion.Flavor == "MariaDB" {
		// isOn handles both ON/OFF and 1/0 representations — information_schema can
		// return "1" instead of "ON" for boolean variables on some MariaDB versions.
		strictVal := strings.ToUpper(strings.TrimSpace(v["strict_password_validation"]))
		strictOn := strictVal == "ON" || strictVal == "1"

		_, hasSimple   := v["simple_password_check_minimal_length"]
		_, hasCracklib := v["cracklib_password_check_dictionary"]
		_, hasReuse    := v["password_reuse_check_interval"]

		// SEC0113 — simple_password_check
		if !hasSimple || !strictOn {
			var desc string
			switch {
			case !hasSimple && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: simple_password_check is not loaded and strict_password_validation=OFF "+
						"— no password complexity is checked or enforced. "+
						"Install SONAME 'simple_password_check' (MariaDB 10.1+) and set strict_password_validation=ON.",
					req.ServerURL)
			case hasSimple && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: strict_password_validation=OFF — simple_password_check is loaded "+
						"but validation failures are advisory only; users can still set non-compliant passwords. "+
						"Set strict_password_validation=ON to enforce the policy.",
					req.ServerURL)
			default:
				desc = fmt.Sprintf(
					"Server %s: simple_password_check plugin is not loaded. "+
						"Without it, passwords have no minimum length or complexity requirements. "+
						"Install via INSTALL SONAME 'simple_password_check' (MariaDB 10.1+).",
					req.ServerURL)
			}
			findings = append(findings, wire.Finding{
				ErrKey:      "SEC0113",
				Severity:    "SECURITY",
				Description: desc,
			})
		}

		// SEC0114 — cracklib_password_check
		if !hasCracklib || !strictOn {
			var desc string
			switch {
			case !hasCracklib && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: cracklib_password_check is not loaded and strict_password_validation=OFF "+
						"— dictionary-based weak passwords are neither detected nor rejected. "+
						"Requires cracklib OS library. Install SONAME 'cracklib_password_check' (MariaDB 10.1+) and set strict_password_validation=ON.",
					req.ServerURL)
			case hasCracklib && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: strict_password_validation=OFF — cracklib_password_check is loaded "+
						"but validation failures are advisory only. Set strict_password_validation=ON.",
					req.ServerURL)
			default:
				desc = fmt.Sprintf(
					"Server %s: cracklib_password_check plugin is not loaded. "+
						"Without it, dictionary-based weak passwords are not rejected. "+
						"Requires cracklib OS library. Install via INSTALL SONAME 'cracklib_password_check' (MariaDB 10.1+).",
					req.ServerURL)
			}
			findings = append(findings, wire.Finding{
				ErrKey:      "SEC0114",
				Severity:    "SECURITY",
				Description: desc,
			})
		}

		// SEC0115 — password_reuse_check
		if !hasReuse || !strictOn {
			var desc string
			switch {
			case !hasReuse && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: password_reuse_check is not loaded and strict_password_validation=OFF "+
						"— password reuse is neither tracked nor rejected. "+
						"Install SONAME 'password_reuse_check' (MariaDB 10.7+) and set strict_password_validation=ON.",
					req.ServerURL)
			case hasReuse && !strictOn:
				desc = fmt.Sprintf(
					"Server %s: strict_password_validation=OFF — password_reuse_check is loaded "+
						"but reuse policy is advisory only. Set strict_password_validation=ON.",
					req.ServerURL)
			default:
				desc = fmt.Sprintf(
					"Server %s: password_reuse_check plugin is not loaded. "+
						"Without it, users can immediately reuse old passwords. "+
						"Install via INSTALL SONAME 'password_reuse_check' (MariaDB 10.7+).",
					req.ServerURL)
			}
			findings = append(findings, wire.Finding{
				ErrKey:      "SEC0115",
				Severity:    "SECURITY",
				Description: desc,
			})
		}
	}

	// SEC0116 — validate_password plugin/component not loaded (MySQL only)
	//
	// MySQL 5.x uses the validate_password plugin (INSTALL PLUGIN ... SONAME).
	// MySQL 8.0+ uses the validate_password component (INSTALL COMPONENT).
	// The two differ in variable naming:
	//   plugin    → validate_password_policy   (underscore)
	//   component → validate_password.policy   (dot)
	// Unlike MariaDB there is no strict_password_validation gate — the plugin/
	// component always enforces once loaded.
	if req.ServerVersion.Flavor != "MariaDB" {
		_, hasPlugin    := v["validate_password_policy"]   // MySQL 5.x plugin
		_, hasComponent := v["validate_password.policy"]   // MySQL 8.0+ component
		if !hasPlugin && !hasComponent {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0116",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: validate_password plugin/component is not loaded. "+
						"Without it, any password — including empty ones — is accepted. "+
						"MySQL 5.x: INSTALL PLUGIN validate_password SONAME 'validate_password.so'; "+
						"MySQL 8.0+: INSTALL COMPONENT 'file://component_validate_password'.",
					req.ServerURL),
			})
		}
	}

	// SEC0117 — password reuse not restricted (MySQL 8.0+ only)
	//
	// MySQL 8.0 introduced server-level password history controls:
	//   password_history         — number of recent passwords that cannot be reused (0 = disabled)
	//   password_reuse_interval  — days before a password can be reused (0 = disabled)
	// Both being 0 means users can immediately reuse the same password.
	// These are my.cnf / SET PERSIST variables — no plugin required.
	if req.ServerVersion.Flavor != "MariaDB" && req.ServerVersion.Major >= 8 {
		history, _  := strconv.Atoi(strings.TrimSpace(v["password_history"]))
		interval, _ := strconv.Atoi(strings.TrimSpace(v["password_reuse_interval"]))
		if history == 0 && interval == 0 {
			findings = append(findings, wire.Finding{
				ErrKey:   "SEC0117",
				Severity: "SECURITY",
				Description: fmt.Sprintf(
					"Server %s: password reuse is not restricted (password_history=0 and "+
						"password_reuse_interval=0). Users can immediately reuse old passwords. "+
						"Set password_history > 0 or password_reuse_interval > 0 in my.cnf "+
						"(or use SET PERSIST on MySQL 8.0+).",
					req.ServerURL),
			})
		}
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

// isHostname returns true if host appears to be a DNS hostname rather than
// an IP address, a wildcard ('%'), or a local alias ('localhost').
// Accounts with DNS hostname grants silently fail to connect when skip_name_resolve=ON.
func isHostname(host string) bool {
	if host == "" || host == "%" || strings.EqualFold(host, "localhost") {
		return false
	}
	if net.ParseIP(host) != nil {
		return false
	}
	return true
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
