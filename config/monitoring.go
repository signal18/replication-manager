// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

// MonitoringConfig contains all monitoring-related configuration
type MonitoringConfig struct {
	// System Paths
	SystemUser    string `mapstructure:"user" toml:"-" json:"-" scope:"server"`
	BaseDir       string `mapstructure:"monitoring-basedir" toml:"monitoring-basedir" json:"monitoringBasedir" scope:"server"`
	WorkingDir    string `mapstructure:"monitoring-datadir" toml:"monitoring-datadir" json:"monitoringDatadir" scope:"server"`
	ShareDir      string `mapstructure:"monitoring-sharedir" toml:"monitoring-sharedir" json:"monitoringSharedir"`
	ConfDir       string `mapstructure:"monitoring-confdir" toml:"monitoring-confdir" json:"monitoringConfdir" scope:"server"`
	ConfDirBackup string `mapstructure:"monitoring-confdir-backup" toml:"monitoring-confdir-backup" json:"monitoringConfdirBackup" scope:"server"`
	ConfDirExtra  string `mapstructure:"monitoring-confdir-extra" toml:"monitoring-confdir-extra" json:"monitoringConfdirExtra" scope:"server"`

	// Configuration Management
	ConfRewrite              bool `mapstructure:"monitoring-save-config" toml:"monitoring-save-config" json:"monitoringSaveConfig" scope:"server"`
	ConfRestoreOnStart       bool `mapstructure:"monitoring-restore-config-on-start" toml:"monitoring-restore-config-on-start" json:"monitoringRestoreConfigOnStart" scope:"server"`
	MergeConfigOnStart       bool `mapstructure:"monitoring-merge-config-on-start" toml:"monitoring-merge-config-on-start" json:"monitoringMergeConfigOnStart" scope:"server"`

	// SSL/TLS
	SSLCert string `mapstructure:"monitoring-ssl-cert" toml:"monitoring-ssl-cert" json:"monitoringSSLCert" scope:"server"`
	SSLKey  string `mapstructure:"monitoring-ssl-key" toml:"monitoring-ssl-key" json:"monitoringSSLKey" scope:"server"`
	KeyPath string `mapstructure:"monitoring-key-path" toml:"monitoring-key-path" json:"monitoringKeyPath" scope:"server"`
	KeyPathGitOverwrite bool `mapstructure:"monitoring-key-path-git-overwrite" toml:"monitoring-key-path-git-overwrite" json:"monitoringKeyPathGitOverwrite" scope:"server"`

	// Monitoring Behavior
	Ticker         int64  `mapstructure:"monitoring-ticker" toml:"monitoring-ticker" json:"monitoringTicker" validate:"min=1,max=60"`
	WaitRetry      int64  `mapstructure:"monitoring-wait-retry" toml:"monitoring-wait-retry" json:"monitorWaitRetry" validate:"min=1,max=999999"`
	Address        string `mapstructure:"monitoring-address" toml:"monitoring-address" json:"monitoringAddress" scope:"server"`
	Pause          bool   `mapstructure:"monitoring-pause" toml:"monitoring-pause" json:"monitoringPause"`
	QueryTimeout   int    `mapstructure:"monitoring-query-timeout" toml:"monitoring-query-timeout" json:"monitoringQueryTimeout" validate:"min=100,max=300000"`

	// Tunnel Configuration
	Socket           string `mapstructure:"monitoring-socket" toml:"monitoring-socket" json:"monitoringSocket"`
	TunnelHost       string `mapstructure:"monitoring-tunnel-host" toml:"monitoring-tunnel-host" json:"monitoringTunnelHost"`
	TunnelCredential string `mapstructure:"monitoring-tunnel-credential" toml:"monitoring-tunnel-credential" json:"monitoringTunnelCredential"`
	TunnelKeyPath    string `mapstructure:"monitoring-tunnel-key-path" toml:"monitoring-tunnel-key-path" json:"monitoringTunnelKeyPath"`

	// Heartbeat
	WriteHeartbeat           bool   `mapstructure:"monitoring-write-heartbeat" toml:"monitoring-write-heartbeat" json:"monitoringWriteHeartbeat"`
	WriteHeartbeatCredential string `mapstructure:"monitoring-write-heartbeat-credential" toml:"monitoring-write-heartbeat-credential" json:"monitoringWriteHeartbeatCredential"`

	// Feature Monitoring
	VariableDiff    bool   `mapstructure:"monitoring-variable-diff" toml:"monitoring-variable-diff" json:"monitoringVariableDiff"`
	SchemaChange    bool   `mapstructure:"monitoring-schema-change" toml:"monitoring-schema-change" json:"monitoringSchemaChange"`
	SchemaChangeScript string `mapstructure:"monitoring-schema-change-script" toml:"monitoring-schema-change-script" json:"monitoringSchemaChangeScript"`
	CheckGrants     bool   `mapstructure:"monitoring-check-grants" toml:"monitoring-check-grants" json:"monitoringCheckGrants"`
	QueryRules      bool   `mapstructure:"monitoring-query-rules" toml:"monitoring-query-rules" json:"monitoringQueryRules"`
	Queries         bool   `mapstructure:"monitoring-queries" toml:"monitoring-queries" json:"monitoringQueries"`
	Plugins         bool   `mapstructure:"monitoring-plugins" toml:"monitoring-plugins" json:"monitoringPlugins"`
	InnoDBStatus    bool   `mapstructure:"monitoring-innodb-status" toml:"monitoring-innodb-status" json:"monitoringInnoDBStatus"`

	// Performance Schema
	PFS             bool `mapstructure:"monitoring-performance-schema" toml:"monitoring-performance-schema" json:"monitoringPerformanceSchema"`
	PFSInstruments  bool `mapstructure:"monitoring-performance-schema-instruments" toml:"monitoring-performance-schema-instruments" json:"monitoringPerformanceSchemaInstruments"`
	PFSMutex        bool `mapstructure:"monitoring-performance-schema-mutex" toml:"monitoring-performance-schema-mutex" json:"monitoringPerformanceSchemaMutex"`
	PFSLatch        bool `mapstructure:"monitoring-performance-schema-latch" toml:"monitoring-performance-schema-latch" json:"monitoringPerformanceSchemaLatch"`
	PFSMemory       bool `mapstructure:"monitoring-performance-schema-memory" toml:"monitoring-performance-schema-memory" json:"monitoringPerformanceSchemaMemory"`

	// Process List Monitoring
	ProcessList                  bool   `mapstructure:"monitoring-processlist" toml:"monitoring-processlist" json:"monitoringProcesslist"`
	ProcessListLimit             string `mapstructure:"monitoring-processlist-limit" toml:"monitoring-processlist-limit" json:"monitoringProcesslistLimit"`
	ProcessListInactive          bool   `mapstructure:"monitoring-processlist-inactive" toml:"monitoring-processlist-inactive" json:"monitoringProcesslistInactive"`
	ProcessListTransactions      bool   `mapstructure:"monitoring-processlist-transactions" toml:"monitoring-processlist-transactions" json:"monitoringProcesslistTransactions"`
	ProcessListInformationSchema bool   `mapstructure:"monitoring-processlist-information-schema" toml:"monitoring-processlist-information-schema" json:"monitoringProcesslistInformationSchema"`

	// Long Query Monitoring
	LongQueryWithProcess bool   `mapstructure:"monitoring-long-query-with-process" toml:"monitoring-long-query-with-process" json:"monitoringLongQueryWithProcess"`
	LongQueryTime        int    `mapstructure:"monitoring-long-query-time" toml:"monitoring-long-query-time" json:"monitoringLongQueryTime" validate:"min=0"`
	LongQueryScript      string `mapstructure:"monitoring-long-query-script" toml:"monitoring-long-query-script" json:"monitoringLongQueryScript"`
	LongQueryWithTable   bool   `mapstructure:"monitoring-long-query-with-table" toml:"monitoring-long-query-with-table" json:"monitoringLongQueryWithTable"`
	LongQueryLogLength   int    `mapstructure:"monitoring-long-query-log-length" toml:"monitoring-long-query-log-length" json:"monitoringLongQueryLogLength" validate:"min=0,max=10000"`

	// Log Lengths
	ErrorLogLength    int `mapstructure:"monitoring-error-log-length" toml:"monitoring-error-log-length" json:"monitoringErrorLogLength" validate:"min=0,max=10000"`
	SqlErrorLogLength int `mapstructure:"monitoring-sql-error-log-length" toml:"monitoring-sql-error-log-length" json:"monitoringSqlErrorLogLength" validate:"min=0,max=10000"`
	AuditLogLength    int `mapstructure:"monitoring-audit-log-length" toml:"monitoring-audit-log-length" json:"monitoringAuditLogLength" validate:"min=0,max=10000"`

	// Capture
	Capture          bool   `mapstructure:"monitoring-capture" toml:"monitoring-capture" json:"monitoringCapture"`
	CaptureFileKeep  int    `mapstructure:"monitoring-capture-file-keep" toml:"monitoring-capture-file-keep" json:"monitoringCaptureFileKeep" validate:"min=0,max=100"`
	CaptureTrigger   string `mapstructure:"monitoring-capture-trigger" toml:"monitoring-capture-trigger" json:"monitoringCaptureTrigger"`

	// Disk Usage
	DiskUsage    bool `mapstructure:"monitoring-disk-usage" toml:"monitoring-disk-usage" json:"monitoringDiskUsage"`
	DiskUsagePct int  `mapstructure:"monitoring-disk-usage-pct" toml:"monitoring-disk-usage-pct" json:"monitoringDiskUsagePct" validate:"min=0,max=100"`

	// Miscellaneous
	IgnoreErrors     string `mapstructure:"monitoring-ignore-errors" toml:"monitoring-ignore-errors" json:"monitoringIgnoreErrors"`
	Tenant           string `mapstructure:"monitoring-tenant" toml:"monitoring-tenant" json:"monitoringTenant"`
	AlertTrigger     string `mapstructure:"monitoring-alert-trigger" toml:"monitoring-alert-trigger" json:"monitoringAlertTrigger"`
	OpenStateScript  string `mapstructure:"monitoring-open-state-script" toml:"monitoring-open-state-script" json:"monitoringOpenStateScript"`
	CloseStateScript string `mapstructure:"monitoring-close-state-script" toml:"monitoring-close-state-script" json:"monitoringCloseStateScript"`
	Scheduler        bool   `mapstructure:"monitoring-scheduler" toml:"monitoring-scheduler" json:"monitoringScheduler"`
}

// Validate performs validation on MonitoringConfig
func (m *MonitoringConfig) Validate() error {
	// Ticker must be reasonable
	if m.Ticker < 1 || m.Ticker > 60 {
		return NewValidationError("monitoring-ticker", m.Ticker, "must be between 1 and 60 seconds")
	}

	// Query timeout must be positive and reasonable
	if m.QueryTimeout < 100 || m.QueryTimeout > 300000 {
		return NewValidationError("monitoring-query-timeout", m.QueryTimeout, "must be between 100ms and 300s")
	}

	// Log lengths must be reasonable
	if m.LongQueryLogLength < 0 || m.LongQueryLogLength > 10000 {
		return NewValidationError("monitoring-long-query-log-length", m.LongQueryLogLength, "must be between 0 and 10000")
	}

	// Disk usage percentage must be valid
	if m.DiskUsage && (m.DiskUsagePct < 0 || m.DiskUsagePct > 100) {
		return NewValidationError("monitoring-disk-usage-pct", m.DiskUsagePct, "must be between 0 and 100")
	}

	return nil
}
