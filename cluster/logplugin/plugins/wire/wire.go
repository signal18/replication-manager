// Package wire contains the JSON types shared by all replication-manager
// external log plugins. Import or copy this package into your plugin binary.
// No replication-manager dependency is required at runtime.
package wire

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
	ServerVariables  map[string]string `json:"server_variables"` // SHOW GLOBAL VARIABLES snapshot
	DatabaseUsers    []DBUser          `json:"database_users"`   // mysql.user snapshot (no hashes)
}

// DBUser is one row from mysql.user, stripped of credential data.
type DBUser struct {
	User          string `json:"user"`
	Host          string `json:"host"`
	Plugin        string `json:"plugin"`         // e.g. "mysql_native_password", "ed25519"
	PasswordEmpty bool   `json:"password_empty"` // true when authentication_string is empty
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
	Findings []Finding `json:"findings"`
}

type Finding struct {
	ErrKey      string `json:"err_key"`
	Severity    string `json:"severity"` // "WARNING", "ERROR", or "SECURITY"
	Description string `json:"description"`
}
