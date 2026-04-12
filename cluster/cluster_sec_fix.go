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
// Tag names must match the fset_name suffix in the compliance module JSON
// (share/opensvc/moduleset_mariadb.svc.mrm.db.json).  The configurator uses
// strings.HasSuffix(fset_name, tag) for file generation and
// strings.Contains(fset_name, tag) for SQL execution — both require the tag
// to be a suffix/substring of the fset_name, not an arbitrary label.
var secTagMap = map[string]secTagEntry{
	// fset_name: mariadb.security.strictssl
	// mariadb_command: SET GLOBAL require_secure_transport = ON
	// mariadb_default: SET GLOBAL require_secure_transport = OFF
	// Deploys cnf at runtime and executes SET GLOBAL — no restart needed.
	"SEC0103": {
		Action:      "add_tag",
		Tag:         "strictssl",
		Description: "Add 'strictssl' tag — executes SET GLOBAL require_secure_transport=ON and deploys loose_require_secure_transport=ON to my.cnf",
		Risk:        "moderate",
	},
	// fset_name: mariadb.security.localinfile
	// mariadb_default: SET GLOBAL local_infile=0
	"SEC0102": {
		Action:      "drop_tag",
		Tag:         "localinfile",
		Description: "Drop 'localinfile' tag — executes SET GLOBAL local_infile=0 and removes local-infile=1 from my.cnf",
		Risk:        "safe",
	},
	// fset_name: mariadb.log.general
	// mariadb_default: SET GLOBAL general_log=0
	"SEC0104": {
		Action:      "drop_tag",
		Tag:         "general",
		Description: "Drop 'general' tag — executes SET GLOBAL general_log=0 and removes general_log=1 from my.cnf",
		Risk:        "safe",
	},
	// fset_name: mariadb.log.audit
	// mariadb_command: INSTALL SONAME 'server_audit'; SET GLOBAL server_audit_logging=ON
	// No restart required — plugin loads at runtime.
	"SEC0112": {
		Action:      "add_tag",
		Tag:         "audit",
		Description: "Add 'audit' tag — installs server_audit.so at runtime and enables audit logging (CONNECT,QUERY,TABLE events to syslog)",
		Risk:        "safe",
	},
	// fset_name: mariadb.security.pwdchecksimple
	// mariadb_command: INSTALL SONAME 'simple_password_check'
	// No restart required — plugin loads at runtime. MariaDB 10.1+.
	"SEC0113": {
		Action: "add_tag",
		Tag:    "pwdchecksimple",
		Description: "Add 'pwdchecksimple' tag — runs INSTALL SONAME 'simple_password_check' " +
			"immediately (MariaDB 10.1+) and deploys plugin_load_add to my.cnf for persistence. " +
			"Enforces minimum length, digit, and mixed-case requirements on new passwords.",
		Risk: "safe",
	},
	// fset_name: mariadb.security.pwdcheckcracklib
	// mariadb_command: INSTALL SONAME 'cracklib_password_check'
	// No restart required — plugin loads at runtime. MariaDB 10.1+. Requires cracklib OS library.
	"SEC0114": {
		Action: "add_tag",
		Tag:    "pwdcheckcracklib",
		Description: "Add 'pwdcheckcracklib' tag — runs INSTALL SONAME 'cracklib_password_check' " +
			"immediately (MariaDB 10.1+) and deploys plugin_load_add to my.cnf for persistence. " +
			"Requires cracklib OS library (libcracklib2 / cracklib-dicts). " +
			"INSTALL SONAME will fail silently if the library is absent.",
		Risk: "safe",
	},
	// fset_name: mariadb.security.pwdcheckreuse
	// mariadb_command: INSTALL SONAME 'password_reuse_check'
	// No restart required — plugin loads at runtime. MariaDB 10.7+.
	"SEC0115": {
		Action: "add_tag",
		Tag:    "pwdcheckreuse",
		Description: "Add 'pwdcheckreuse' tag — runs INSTALL SONAME 'password_reuse_check' " +
			"immediately (MariaDB 10.7+) and deploys plugin_load_add to my.cnf for persistence. " +
			"Prevents users from immediately reusing old passwords.",
		Risk: "safe",
	},
	// fset_name: mariadb.security.keyfileencrypt
	// All encryption variables are read-only — no mariadb_command SQL runs.
	// Config is deployed and a restart cookie is set; the monitoring loop
	// deploys config.tar.gz before processing the restart.
	// SEC0109, SEC0110, SEC0111 all map to the same tag.
	"SEC0109": {
		Action: "add_tag",
		Tag:    "keyfileencrypt",
		Description: "Add 'keyfileencrypt' tag — deploys file-key-management plugin config " +
			"(innodb_encrypt_tables, encrypt_binlog, encrypt_tmp_files) then triggers a rolling restart. " +
			"Requires pre-configured encryption key file.",
		Risk: "disruptive",
	},
	"SEC0110": {
		Action: "add_tag",
		Tag:    "keyfileencrypt",
		Description: "Add 'keyfileencrypt' tag — deploys file-key-management plugin config " +
			"(innodb_encrypt_tables, encrypt_binlog, encrypt_tmp_files) then triggers a rolling restart. " +
			"Requires pre-configured encryption key file.",
		Risk: "disruptive",
	},
	"SEC0111": {
		Action: "add_tag",
		Tag:    "keyfileencrypt",
		Description: "Add 'keyfileencrypt' tag — deploys file-key-management plugin config " +
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
	"SEC0103": true, // add_tag strictssl: SET GLOBAL require_secure_transport=ON + cnf deploy (moderate)
	"SEC0104": true,
	"SEC0107": true,
	"SEC0112": true, // audit plugin: add_tag (runtime install, no restart)
	// Encryption findings: add_tag triggers .cnf deploy + restart cookie.
	// Marked auto-fixable so the UI shows a Fix button, but the risk is
	// "disruptive" — the operator must confirm restart separately.
	"SEC0109": true,
	"SEC0110": true,
	"SEC0111": true,
	// Password strength plugins: each raises its own finding.
	// All use INSTALL SONAME at runtime (no restart needed) + plugin_load_add for persistence.
	"SEC0113": true, // simple_password_check (MariaDB 10.1+)
	"SEC0114": true, // cracklib_password_check (MariaDB 10.1+, requires cracklib OS package)
	"SEC0115": true, // password_reuse_check (MariaDB 10.7+)
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

		// ErrKey is stored as the composite key "SEC0103@server:3306" because
		// AddState(compositeKey, st) sets s.ErrKey = compositeKey.  Strip the
		// server suffix so map lookups against secTagMap / autoFixable work.
		baseKey := st.ErrKey
		if i := strings.Index(baseKey, "@"); i >= 0 {
			baseKey = baseKey[:i]
		}

		if entry, ok := secTagMap[baseKey]; ok {
			fixes = append(fixes, RemediationFix{
				Type:        entry.Action,
				Tag:         entry.Tag,
				Description: entry.Description,
				Risk:        entry.Risk,
			})
		} else if tmpl, ok := secCnfTemplates[baseKey]; ok {
			// SEC0106 (skip_name_resolve) must not be applied in Docker:
			// containers use DNS for discovery and IPs are reassigned on restart.
			if baseKey == "SEC0106" && cluster.Configurator.IsFilterInDBTags("docker") {
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
		} else if baseKey == "SEC0107" {
			fixes = append(fixes, RemediationFix{
				Type:        "drop_anon_users",
				Description: "Drop all anonymous (user='') accounts on this server",
				Risk:        "safe",
			})
		} else if baseKey == "SEC0100" {
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
			ErrKey:      baseKey,
			Server:      st.ServerUrl,
			Description: st.ErrDesc,
			AutoFixable: autoFixable[baseKey],
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
// For disruptive fixes (SEC0109/0110/0111) the compliance tag is added via
// AddDBTag(false), which mirrors the UI add-tag path: cookies are set and
// the monitoring loop deploys the new config before applying the restart.
//
// Supported codes:
//
//	SEC0100 — lock all no-password, non-socket accounts (ACCOUNT LOCK — reversible)
//	SEC0102 — drop localinfile tag     (SET GLOBAL local_infile=0)
//	SEC0104 — drop general tag         (SET GLOBAL general_log=0)
//	SEC0107 — drop all anonymous (user='') accounts
//	SEC0109 — add keyfileencrypt tag + rolling restart (table encryption)
//	SEC0110 — add keyfileencrypt tag + rolling restart (binlog encryption)
//	SEC0111 — add keyfileencrypt tag + rolling restart (tmp encryption)
//	SEC0112 — add audit tag            (runtime INSTALL SONAME)
//	SEC0113 — add pwdchecksimple tag   (simple_password_check, MariaDB 10.1+)
//	SEC0114 — add pwdcheckcracklib tag (cracklib_password_check, MariaDB 10.1+, requires cracklib OS lib)
//	SEC0115 — add pwdcheckreuse tag    (password_reuse_check, MariaDB 10.7+)
func (cluster *Cluster) FixSecState(errKey string, preferredTag ...string) error {
	switch errKey {
	case "SEC0100":
		return cluster.fixNoPasswordUsers()
	case "SEC0103":
		return cluster.ApplyRemediationTag("add_tag", "strictssl")
	case "SEC0102":
		return cluster.ApplyRemediationTag("drop_tag", "localinfile")
	case "SEC0112":
		return cluster.ApplyRemediationTag("add_tag", "audit")
	case "SEC0104":
		return cluster.ApplyRemediationTag("drop_tag", "general")
	case "SEC0107":
		return cluster.fixAnonUsers()
	case "SEC0109", "SEC0110", "SEC0111":
		// Encryption variables are read-only — the dynamic SQL path sets restart
		// cookies per server; AddDBTag also calls SetConfigChangeCookie so the
		// config is updated before the rolling restart runs.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"security remediation %s: adding keyfileencrypt tag then triggering rolling restart", errKey)
		cluster.AddDBTag("keyfileencrypt", true)
		go cluster.RollingRestart()
		return nil
	case "SEC0113":
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"security remediation SEC0113: adding pwdchecksimple tag (dynamic INSTALL SONAME, no restart required)")
		return cluster.installPasswordPlugin("pwdchecksimple")
	case "SEC0114":
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"security remediation SEC0114: adding pwdcheckcracklib tag (dynamic INSTALL SONAME, requires cracklib OS library)")
		return cluster.installPasswordPlugin("pwdcheckcracklib")
	case "SEC0115":
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"security remediation SEC0115: adding pwdcheckreuse tag (dynamic INSTALL SONAME, no restart required)")
		return cluster.installPasswordPlugin("pwdcheckreuse")
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

// pwdPluginSpec maps tag names to their SONAME library and minimum MariaDB version.
// The mariadb_command in the compliance cnf file already runs INSTALL SONAME; this
// map is used only for the per-server version gate in installPasswordPlugin.
var pwdPluginSpec = map[string]struct {
	soname   string
	minMajor int
	minMinor int
}{
	"pwdchecksimple":   {soname: "simple_password_check", minMajor: 10, minMinor: 1},
	"pwdcheckcracklib": {soname: "cracklib_password_check", minMajor: 10, minMinor: 1},
	"pwdcheckreuse":    {soname: "password_reuse_check", minMajor: 10, minMinor: 7},
}

// installPasswordPlugin version-gates the tag before applying it.
// Servers below the minimum version are skipped with a warning so they don't
// receive a plugin_load_add for a library that doesn't exist on that release.
// Servers that meet the requirement get the tag applied with dynamic=true, which
// deploys plugin_load_add to my.cnf (persistence) and runs INSTALL SONAME
// immediately via the mariadb_command embedded in the compliance cnf file.
func (cluster *Cluster) installPasswordPlugin(tag string) error {
	spec := pwdPluginSpec[tag]

	allSkipped := true
	for _, srv := range cluster.Servers {
		if srv == nil || srv.IsDown() || !srv.IsMariaDB() {
			continue
		}
		maj, min := srv.DBVersion.Major, srv.DBVersion.Minor
		if maj < spec.minMajor || (maj == spec.minMajor && min < spec.minMinor) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"password plugin %s: skipping %s — requires MariaDB %d.%d+, server is %d.%d",
				tag, srv.URL, spec.minMajor, spec.minMinor, maj, min)
			continue
		}
		allSkipped = false
	}

	if allSkipped {
		return fmt.Errorf("password plugin %s: no servers meet the minimum version requirement (MariaDB %d.%d+)",
			tag, spec.minMajor, spec.minMinor)
	}

	// AddDBTag with dynamic=true deploys the cnf and runs INSTALL SONAME on all
	// reachable servers. Servers below the version threshold logged above will still
	// receive the cnf but INSTALL SONAME will fail gracefully (plugin not found).
	cluster.AddDBTag(tag, true)
	return nil
}

