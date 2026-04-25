// Package wire contains the JSON types shared by all replication-manager
// external log plugins. Import or copy this package into your plugin binary.
// No replication-manager dependency is required at runtime.
package wire

import (
	"os"
	"strconv"
	"strings"
)

// WireVersion is the version of the stdin/stdout JSON protocol.
// Increment this when a breaking change is made to Request or Response.
// The Makefile reads this value to organise the plugin distribution repository.
const WireVersion = 1

// ServerVersion carries the already-parsed database version so plugins
// do not need to re-parse the raw version string from ServerVariables.
// Populated from server.DBVersion which is derived from the live connection.
type ServerVersion struct {
	Flavor  string `json:"flavor"`  // "MariaDB", "MySQL", "Percona", "PostgreSQL"
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Release int    `json:"release"`
}

// Request is written to the plugin's stdin as a single JSON object.
type Request struct {
	ServerURL        string            `json:"server_url"`
	GraphiteAPIURL   string            `json:"graphite_api_url"`
	GraphiteHostname string            `json:"graphite_hostname"`
	ErrorLog         []Msg             `json:"error_log"`
	SqlErrorLog      []Msg             `json:"sql_error_log"`
	SlowLog          []SlowMsg         `json:"slow_log"`
	AuditLog         []Msg             `json:"audit_log"`
	PFSQueries       []PFSQuery        `json:"pfs_queries"`
	ProcessList      []Process         `json:"process_list"`
	MetaDataLocks    []MDL             `json:"metadata_locks"`
	BinlogEvents     []BinlogEvent     `json:"binlog_events"`
	ServerVersion    ServerVersion     `json:"server_version"`    // pre-parsed version (use instead of ServerVariables["version"])
	ServerVariables  map[string]string `json:"server_variables"`  // SHOW GLOBAL VARIABLES snapshot
	DatabaseUsers    []DBUser          `json:"database_users"`    // mysql.user snapshot (no hashes)
	ClusterContext   ClusterContext    `json:"cluster_context"`   // cluster-level facts
	PluginDataDir    string            `json:"plugin_data_dir"`   // path to plugin sidecar data files
	// Config carries per-plugin settings from the cluster TOML / GUI (plugin-config.<name>.*).
	// Keys are kebab-case (e.g. "timeframe-hours"). Use CfgInt/CfgFloat/CfgStr helpers to read.
	// REPMAN_* environment variables are still honoured as a fallback for backward compatibility.
	Config           map[string]string `json:"config,omitempty"`
}

// ClusterContext carries cluster-level facts that cannot be derived from
// a single server snapshot.
type ClusterContext struct {
	HasProxies        bool              `json:"has_proxies"`         // cluster has at least one proxy configured
	BackupEncrypted   bool              `json:"backup_encrypted"`    // backup configured with encryption
	ConfigClearPwd    bool              `json:"config_clear_pwd"`    // cleartext passwords detected in TOML config
	HistoryClearPwd   bool              `json:"history_clear_pwd"`   // previous binlog scan found cleartext passwords
	DockerDeployment  bool              `json:"docker_deployment"`   // servers run as Docker containers (DNS-based discovery, dynamic IPs)
	// ToolVersions maps tool/product names to their version strings.
	// Keys: "repman", "mariadb", "mysql", "proxysql", "maxscale", "haproxy", "restic", etc.
	ToolVersions      map[string]string `json:"tool_versions,omitempty"`
	// SubscriptionPlan is the Cloud18 subscription plan: "free", "support",
	// "support-services", or "partner". Empty when Cloud18 is not enabled.
	SubscriptionPlan  string            `json:"subscription_plan,omitempty"`
}

// DBUser is one row from mysql.user, stripped of credential data.
type DBUser struct {
	User          string `json:"user"`
	Host          string `json:"host"`
	Plugin        string `json:"plugin"`          // e.g. "mysql_native_password", "ed25519"
	PasswordEmpty bool   `json:"password_empty"`  // true when authentication_string is empty
	AccountLocked bool   `json:"account_locked"`  // true when account_locked = 'Y' — account cannot be used to log in
}

// BinlogEvent is a single QUERY_EVENT captured from a server's binary log.
type BinlogEvent struct {
	Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" UTC
	Schema    string `json:"schema"`    // default schema at query time
	Query     string `json:"query"`     // raw SQL statement text
	ServerID  uint32 `json:"server_id"` // originating server-id
}

type Msg struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

type SlowMsg struct {
	Timestamp     string             `json:"timestamp"`
	Query         string             `json:"query"`
	User          string             `json:"user"`
	Host          string             `json:"host"`
	Db            string             `json:"db"`
	TimeMetrics   map[string]float64 `json:"time_metrics"`
	NumberMetrics map[string]uint64  `json:"number_metrics"`
}

type PFSQuery struct {
	Digest        string  `json:"digest"`
	DigestText    string  `json:"digest_text"`
	Schema        string  `json:"schema"`
	ExecCount     int64   `json:"exec_count"`
	ErrCount      int64   `json:"err_count"`
	WarnCount     int64   `json:"warn_count"`
	ExecTimeTotal string  `json:"exec_time_total"`
	ExecTimeMaxMs float64 `json:"exec_time_max_ms"`
	ExecTimeAvgMs float64 `json:"exec_time_avg_ms"`
	RowsSent      int64   `json:"rows_sent"`
	RowsSentAvg   int64   `json:"rows_sent_avg"`
	RowsScanned   int64   `json:"rows_scanned"`
	PlanFullScan  string  `json:"plan_full_scan"` // "YES" / "NO"
	PlanTmpDisk   int64   `json:"plan_tmp_disk"`
	PlanTmpMem    int64   `json:"plan_tmp_mem"`
	LastSeen      string  `json:"last_seen"`
}

type Process struct {
	Id            uint64  `json:"id"`
	User          string  `json:"user"`
	Host          string  `json:"host"`
	Db            string  `json:"db"`
	Command       string  `json:"command"`
	TimeSeconds   float64 `json:"time_seconds"`
	State         string  `json:"state"`
	Info          string  `json:"info"`
	RowsSent      uint64  `json:"rows_sent"`
	RowsExamined  uint64  `json:"rows_examined"`
	TrxTime       uint64  `json:"trx_time"`
	TrxRowsLocked uint64  `json:"trx_rows_locked"`
}

type MDL struct {
	ThreadID     uint64 `json:"thread_id"`
	LockMode     string `json:"lock_mode"`
	LockDuration string `json:"lock_duration"`
	LockTimeMs   int64  `json:"lock_time_ms"`
	LockType     string `json:"lock_type"`
	Schema       string `json:"schema"`
	Table        string `json:"table"`
}

type Response struct {
	Findings    []Finding    `json:"findings"`
	ScoreChecks []ScoreCheck `json:"score_checks,omitempty"`
}

// Remediation is a machine-readable fix proposal attached to a Finding.
// Type is one of "sql", "my_cnf", or "repman_config".
// Risk is one of "safe", "moderate", or "disruptive".
type Remediation struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	SQL         string `json:"sql,omitempty"`
	MyCnf       string `json:"my_cnf,omitempty"`
	ConfigKey   string `json:"config_key,omitempty"`
	ConfigValue string `json:"config_value,omitempty"`
	Risk        string `json:"risk"`
}

type Finding struct {
	ErrKey       string        `json:"err_key"`
	Severity     string        `json:"severity"` // "WARNING", "ERROR", or "SECURITY"
	Description  string        `json:"description"`
	Remediations []Remediation `json:"remediations,omitempty"`
}

// ScoreCheck is a single compliance check result.
// Tag matches a SecurityScore field name (e.g. "HasSSL", "NoEmptyPassword").
type ScoreCheck struct {
	Tag    string `json:"tag"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// ---- Config helper functions ------------------------------------------------
// These read from req.Config (wire protocol) with fallback to REPMAN_* env vars
// for backward compatibility with deployments that set env vars in systemd units.
// Import "os", "strconv", "strings" in your plugin to use them.

// CfgInt reads an integer from cfg[key], falling back to the envFallback value.
// Typical usage:  hours := wire.CfgInt(req.Config, "timeframe-hours", wire.EnvInt("REPMAN_TIMEFRAME_HOURS", 1))
func CfgInt(cfg map[string]string, key string, fallback int) int {
	if v, ok := cfg[key]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// CfgFloat reads a float64 from cfg[key], falling back to fallback.
func CfgFloat(cfg map[string]string, key string, fallback float64) float64 {
	if v, ok := cfg[key]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// CfgStr reads a string from cfg[key], falling back to fallback.
func CfgStr(cfg map[string]string, key, fallback string) string {
	if v, ok := cfg[key]; ok && v != "" {
		return v
	}
	return fallback
}

// CfgBool reads a boolean from cfg[key] ("true"/"false"/"1"/"0"), falling back to fallback.
func CfgBool(cfg map[string]string, key string, fallback bool) bool {
	if v, ok := cfg[key]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return fallback
}

// EnvInt reads an int from the named environment variable, returning def if absent or unparseable.
func EnvInt(envKey string, def int) int {
	if v := os.Getenv(envKey); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// EnvFloat reads a float64 from the named environment variable, returning def if absent.
func EnvFloat(envKey string, def float64) float64 {
	if v := os.Getenv(envKey); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// EnvStr reads a string from the named environment variable, returning def if absent.
func EnvStr(envKey, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}
