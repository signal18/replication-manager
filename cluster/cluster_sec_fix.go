package cluster

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// RemediationFix is one concrete fix option for a security finding.
//
// Type values:
//
//	"add_tag"      — add a compliance module tag (deploys .cnf + runs mariadb_command SQL)
//	"drop_tag"     — remove a compliance module tag (runs mariadb_default SQL)
//	"cnf_template" — informational: suggested .cnf file to add to the compliance module
type RemediationFix struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Risk        string `json:"risk"` // "safe", "moderate", "disruptive"

	// add_tag / drop_tag
	Tag string `json:"tag,omitempty"`

	// cnf_template
	FileName string `json:"file_name,omitempty"` // suggested filename (e.g. with_sec_xxx.cnf)
	MyCnf    string `json:"my_cnf,omitempty"`    // suggested file content
}

// RemediationEntry groups all fix options for one open security finding.
type RemediationEntry struct {
	ErrKey      string           `json:"err_key"`
	Server      string           `json:"server"`
	Description string           `json:"description"`
	AutoFixable bool             `json:"auto_fixable"` // true when POST /security/fix-state/{err_key} is supported
	Fixes       []RemediationFix `json:"fixes"`
}

// RemediationPlan is the full remediation response for a cluster.
type RemediationPlan struct {
	Cluster      string             `json:"cluster"`
	GeneratedAt  string             `json:"generated_at"`
	OpenFindings int                `json:"open_findings"`
	Remediations []RemediationEntry `json:"remediations"`
}

// secTagEntry describes how to remediate a server-level SEC finding via the
// compliance module tag mechanism (AddDBTag / DropDBTag).
type secTagEntry struct {
	Action      string // "add_tag" or "drop_tag"
	Tag         string // compliance module fset_name
	Description string
	Risk        string
}

// secTagMap maps server-level SEC error keys to compliance module tag actions.
// AddDBTag / DropDBTag deploy the .cnf file to each server AND execute the
// mariadb_command / mariadb_default SQL automatically — no direct Exec() needed.
//
// Per-account findings (SEC0100, SEC0107, SEC0108) are intentionally excluded:
// user account changes are too risky to automate and must be handled manually.
var secTagMap = map[string]secTagEntry{
	// with_sec_localinfile enables local_infile; dropping it runs:
	//   mariadb_default: SET GLOBAL local_infile=0
	"SEC0102": {
		Action:      "drop_tag",
		Tag:         "with_sec_localinfile",
		Description: "Drop 'with_sec_localinfile' tag — executes SET GLOBAL local_infile=0 and removes local-infile=1 from my.cnf",
		Risk:        "safe",
	},
	// with_log_general enables the general query log; dropping it runs:
	//   mariadb_default: SET GLOBAL general_log=0
	"SEC0104": {
		Action:      "drop_tag",
		Tag:         "with_log_general",
		Description: "Drop 'with_log_general' tag — executes SET GLOBAL general_log=0 and removes general_log=1 from my.cnf",
		Risk:        "safe",
	},
	// with_log_audit installs the MariaDB server_audit plugin and enables
	// audit logging via INSTALL SONAME + SET GLOBAL server_audit_logging=ON.
	// The mariadb_command: line in the .cnf runs these at runtime so no
	// restart is needed.
	"SEC0112": {
		Action:      "add_tag",
		Tag:         "with_log_audit",
		Description: "Add 'with_log_audit' tag — installs server_audit.so at runtime and enables audit logging (CONNECT,QUERY,TABLE events to syslog)",
		Risk:        "safe",
	},
	// with_sec_keyfileencrypt enables InnoDB/Aria/binlog/tmp encryption via
	// the file-key-management plugin.  The tag deploys the .cnf and sets a
	// restart cookie — no mariadb_command: line is present because all
	// encryption variables are read-only at runtime.
	// SEC0109, SEC0110, SEC0111 all map to the same tag; applying it once
	// covers all three findings.
	"SEC0109": {
		Action: "add_tag",
		Tag:    "with_sec_keyfileencrypt",
		Description: "Add 'with_sec_keyfileencrypt' tag — deploys file-key-management plugin config " +
			"(innodb_encrypt_tables, encrypt_binlog, encrypt_tmp_files) then triggers a rolling restart. " +
			"Requires pre-configured encryption key file.",
		Risk: "disruptive",
	},
	"SEC0110": {
		Action: "add_tag",
		Tag:    "with_sec_keyfileencrypt",
		Description: "Add 'with_sec_keyfileencrypt' tag — deploys file-key-management plugin config " +
			"(innodb_encrypt_tables, encrypt_binlog, encrypt_tmp_files) then triggers a rolling restart. " +
			"Requires pre-configured encryption key file.",
		Risk: "disruptive",
	},
	"SEC0111": {
		Action: "add_tag",
		Tag:    "with_sec_keyfileencrypt",
		Description: "Add 'with_sec_keyfileencrypt' tag — deploys file-key-management plugin config " +
			"(innodb_encrypt_tables, encrypt_binlog, encrypt_tmp_files) then triggers a rolling restart. " +
			"Requires pre-configured encryption key file.",
		Risk: "disruptive",
	},
}

// secCnfTemplates holds suggested .cnf file templates for SEC findings that
// do not yet have a compliance module tag.  The admin creates the tag in the
// compliance module using the provided content, then adds it to db-servers-tags.
// These are informational only — no automatic action is taken.
var secCnfTemplates = map[string]RemediationFix{
	"SEC0103": {
		Type:     "cnf_template",
		FileName: "with_sec_securetransport.cnf",
		Description: "No compliance module tag exists for require_secure_transport. " +
			"Create 'with_sec_securetransport' in the module with this content, " +
			"then add the tag to db-servers-tags.",
		MyCnf: "# require_secure_transport — enforce TLS for all client connections\n" +
			"# mariadb_command: SET GLOBAL require_secure_transport = ON;\n" +
			"# mariadb_default: SET GLOBAL require_secure_transport = OFF;\n\n" +
			"[mysqld]\nrequire_secure_transport = ON\n",
		Risk: "moderate",
	},
	"SEC0105": {
		Type:     "cnf_template",
		FileName: "with_sec_securefilepriv.cnf",
		Description: "No compliance module tag exists for secure_file_priv. " +
			"Create 'with_sec_securefilepriv' in the module with this content " +
			"(requires server restart), then add the tag to db-servers-tags.",
		MyCnf: "# secure_file_priv — restrict LOAD DATA / SELECT INTO OUTFILE to a dedicated directory\n" +
			"# (read-only at runtime; requires server restart)\n\n" +
			"[mysqld]\nsecure_file_priv = /var/lib/mysql-files\n",
		Risk: "disruptive",
	},
	"SEC0106": {
		Type:     "cnf_template",
		FileName: "with_sec_skipnameresolve.cnf",
		Description: "No compliance module tag exists for skip_name_resolve. " +
			"Create 'with_sec_skipnameresolve' in the module with this content " +
			"(requires restart + hostname grants converted to IPs), " +
			"then add the tag to db-servers-tags.",
		MyCnf: "# skip_name_resolve — disable DNS lookups for connecting clients\n" +
			"# (read-only at runtime; requires server restart)\n" +
			"# Convert all GRANT statements to use IP addresses before enabling.\n\n" +
			"[mysqld]\nskip_name_resolve = ON\n",
		Risk: "disruptive",
	},
}

// autoFixable lists SEC codes that can be fully resolved via FixSecState.
// The UI uses this to show a "Fix" button for the finding.
var autoFixable = map[string]bool{
	"SEC0100": true,
	"SEC0102": true,
	"SEC0104": true,
	"SEC0107": true,
	"SEC0112": true, // audit plugin: add_tag with_log_audit (runtime install, no restart)
	// Encryption findings: add_tag triggers .cnf deploy + restart cookie.
	// Marked auto-fixable so the UI shows a Fix button, but the risk is
	// "disruptive" — the operator must confirm restart separately.
	"SEC0109": true,
	"SEC0110": true,
	"SEC0111": true,
}

// GetRemediationPlan assembles the current remediation plan from all open security findings.
//
// Each entry carries AutoFixable=true when POST /security/fix-state/{err_key} is supported,
// so the UI can show a one-click fix button.
//
// SEC0108 (wildcard-host users) is informational only — no automated fix.
func (cluster *Cluster) GetRemediationPlan() RemediationPlan {
	openStates := cluster.SecurityStateMachine.GetOpenStates()
	entries := make([]RemediationEntry, 0)

	for _, st := range openStates {
		var fixes []RemediationFix

		if entry, ok := secTagMap[st.ErrKey]; ok {
			fixes = append(fixes, RemediationFix{
				Type:        entry.Action,
				Tag:         entry.Tag,
				Description: entry.Description,
				Risk:        entry.Risk,
			})
		} else if tmpl, ok := secCnfTemplates[st.ErrKey]; ok {
			// SEC0106 (skip_name_resolve) must not be applied in Docker:
			// containers use DNS for discovery and IPs are reassigned on restart.
			if st.ErrKey == "SEC0106" && cluster.Configurator.IsFilterInDBTags("docker") {
				fixes = append(fixes, RemediationFix{
					Type: "informational",
					Description: "skip_name_resolve=ON is not recommended in Docker deployments — " +
						"container IPs are dynamic and DNS is used for service discovery. " +
						"Mitigate via Docker network segmentation and network policies instead.",
					Risk: "safe",
				})
			} else {
				fixes = append(fixes, tmpl)
			}
		} else if st.ErrKey == "SEC0107" {
			fixes = append(fixes, RemediationFix{
				Type:        "drop_anon_users",
				Description: "Drop all anonymous (user='') accounts on this server",
				Risk:        "safe",
			})
		} else if st.ErrKey == "SEC0100" {
			fixes = append(fixes, RemediationFix{
				Type:        "lock_no_password_users",
				Description: "Lock all no-password, non-socket accounts on this server (ACCOUNT LOCK — reversible)",
				Risk:        "safe",
			})
		}
		// SEC0108 (wildcard host) — no automated fix, informational only.

		if len(fixes) == 0 {
			continue
		}

		entries = append(entries, RemediationEntry{
			ErrKey:      st.ErrKey,
			Server:      st.ServerUrl,
			Description: st.ErrDesc,
			AutoFixable: autoFixable[st.ErrKey],
			Fixes:       fixes,
		})
	}

	return RemediationPlan{
		Cluster:      cluster.Name,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		OpenFindings: len(openStates),
		Remediations: entries,
	}
}

// ApplyRemediationTag adds or removes a compliance module tag on the cluster.
// AddDBTag / DropDBTag deploy the .cnf config file AND execute the
// mariadb_command / mariadb_default SQL on all reachable servers automatically.
func (cluster *Cluster) ApplyRemediationTag(action, tag string) error {
	switch action {
	case "add_tag":
		cluster.AddDBTag(tag, true)
	case "drop_tag":
		cluster.DropDBTag(tag, true)
	default:
		return fmt.Errorf("unknown tag action %q: must be add_tag or drop_tag", action)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"security remediation: %s %s", action, tag)
	return nil
}

// socketPlugins are authentication plugins that authenticate via OS identity.
// Accounts using these are not affected by SEC0100 since they cannot be
// accessed remotely without OS-level access.
var socketPlugins = map[string]bool{
	"unix_socket": true, "auth_socket": true, "gssapi": true,
	"authentication_pam": true, "auth_pam": true,
}

// FixSecState applies the automated remediation for a given SEC error key
// across all servers in the cluster.  Returns an error if the code has no
// automated fix or if any server-level operation fails.
//
// For disruptive fixes (SEC0109/0110/0111) the compliance tag is applied
// synchronously and then a rolling restart is launched in the background
// so the API returns immediately while the restart proceeds.
//
// Supported codes:
//
//	SEC0100 — lock all no-password, non-socket accounts (ACCOUNT LOCK — reversible)
//	SEC0102 — drop with_sec_localinfile tag  (SET GLOBAL local_infile=0)
//	SEC0104 — drop with_log_general tag      (SET GLOBAL general_log=0)
//	SEC0107 — drop all anonymous (user='') accounts
//	SEC0109 — add with_sec_keyfileencrypt tag + rolling restart (table encryption)
//	SEC0110 — add with_sec_keyfileencrypt tag + rolling restart (binlog encryption)
//	SEC0111 — add with_sec_keyfileencrypt tag + rolling restart (tmp encryption)
func (cluster *Cluster) FixSecState(errKey string) error {
	switch errKey {
	case "SEC0100":
		return cluster.fixNoPasswordUsers()
	case "SEC0102":
		return cluster.ApplyRemediationTag("drop_tag", "with_sec_localinfile")
	case "SEC0112":
		return cluster.ApplyRemediationTag("add_tag", "with_log_audit")
	case "SEC0104":
		return cluster.ApplyRemediationTag("drop_tag", "with_log_general")
	case "SEC0107":
		return cluster.fixAnonUsers()
	case "SEC0109", "SEC0110", "SEC0111":
		// All three encryption findings are resolved by adding the same tag.
		// AddDBTag deploys the .cnf; encryption variables are read-only so
		// no mariadb_command: SQL runs — the restart activates the settings.
		if err := cluster.ApplyRemediationTag("add_tag", "with_sec_keyfileencrypt"); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"security remediation %s: config deployed, launching rolling restart", errKey)
		go cluster.RollingRestart()
		return nil
	default:
		return fmt.Errorf("no automated fix available for %s", errKey)
	}
}

// fixAnonUsers drops all anonymous (user='') database accounts on every server.
// Anonymous accounts allow any client to connect without a username.
func (cluster *Cluster) fixAnonUsers() error {
	var errs []string
	for _, srv := range cluster.Servers {
		if srv.Users == nil {
			continue
		}
		for _, g := range srv.Users.ToNewMap() {
			if g.User != "" {
				continue
			}
			_, err := dbhelper.DropDBUser(srv.Conn, g.User, g.Host)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: drop ''@'%s': %v", srv.URL, g.Host, err))
				continue
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"dropped anonymous account ''@'%s' on %s", g.Host, srv.URL)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("fixAnonUsers: %s", strings.Join(errs, "; "))
	}
	return nil
}

// fixNoPasswordUsers locks every account that has an empty password and is not
// protected by a socket-based authentication plugin.
// ACCOUNT LOCK prevents login without altering the account definition,
// making it easier to review and re-enable if needed.
func (cluster *Cluster) fixNoPasswordUsers() error {
	var errs []string
	for _, srv := range cluster.Servers {
		if srv.Users == nil {
			continue
		}
		for _, g := range srv.Users.ToNewMap() {
			if g.Password != "" || g.AccountLocked {
				continue
			}
			if socketPlugins[strings.ToLower(g.Plugin)] {
				continue
			}
			_, err := dbhelper.LockDBUser(srv.Conn, g.User, g.Host)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: lock '%s'@'%s': %v", srv.URL, g.User, g.Host, err))
				continue
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"locked no-password account '%s'@'%s' on %s", g.User, g.Host, srv.URL)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("fixNoPasswordUsers: %s", strings.Join(errs, "; "))
	}
	return nil
}
