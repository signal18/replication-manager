// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

// FailoverConfig contains all failover-related configuration
type FailoverConfig struct {
	// Failover Limits
	Limit     int   `mapstructure:"failover-limit" toml:"failover-limit" json:"failoverLimit" validate:"min=0"`
	TimeLimit int64 `mapstructure:"failover-time-limit" toml:"failover-time-limit" json:"failoverTimeLimit" validate:"min=0"`
	ResetTime int64 `mapstructure:"failcount-reset-time" toml:"failover-reset-time" json:"failoverResetTime" validate:"min=0"`

	// Failover Scripts
	PreScript  string `mapstructure:"failover-pre-script" toml:"failover-pre-script" json:"failoverPreScript"`
	PostScript string `mapstructure:"failover-post-script" toml:"failover-post-script" json:"failoverPostScript"`

	// Failover Behavior
	Mode              string `mapstructure:"failover-mode" toml:"failover-mode" json:"failoverMode" validate:"omitempty,oneof=manual auto sync"`
	ReadOnlyState     bool   `mapstructure:"failover-readonly-state" toml:"failover-readonly-state" json:"failoverReadOnlyState"`
	SuperReadOnlyState bool  `mapstructure:"failover-superreadonly-state" toml:"failover-superreadonly-state" json:"failoverSuperReadOnlyState"`
	SemiSyncState     bool   `mapstructure:"failover-semisync-state" toml:"failover-semisync-state" json:"failoverSemisyncState"`
	AtSync            bool   `mapstructure:"failover-at-sync" toml:"failover-at-sync" json:"failoverAtSync"`
	EventScheduler    bool   `mapstructure:"failover-event-scheduler" toml:"failover-event-scheduler" json:"failoverEventScheduler"`
	EventStatus       bool   `mapstructure:"failover-event-status" toml:"failover-event-status" json:"failoverEventStatus"`
	RestartUnsafe     bool   `mapstructure:"failover-restart-unsafe" toml:"failover-restart-unsafe" json:"failoverRestartUnsafe"`

	// Failover Constraints
	MaxSlaveDelay int64 `mapstructure:"failover-max-slave-delay" toml:"failover-max-slave-delay" json:"failoverMaxSlaveDelay" validate:"min=0"`
	MdevCheck     bool  `mapstructure:"failover-mdev-check" toml:"failover-mdev-check" json:"failoverMdevCheck"`
	MdevLevel     string `mapstructure:"failover-mdev-level" toml:"failover-mdev-level" json:"failoverMdevLevel"`

	// False Positive Detection
	FalsePositivePingCounter         int  `mapstructure:"failover-falsepositive-ping-counter" toml:"failover-falsepositive-ping-counter" json:"failoverFalsePositivePingCounter" validate:"min=0,max=100"`
	FalsePositiveHeartbeat           bool `mapstructure:"failover-falsepositive-heartbeat" toml:"failover-falsepositive-heartbeat" json:"failoverFalsePositiveHeartbeat"`
	FalsePositiveHeartbeatTimeout    int  `mapstructure:"failover-falsepositive-heartbeat-timeout" toml:"failover-falsepositive-heartbeat-timeout" json:"failoverFalsePositiveHeartbeatTimeout" validate:"min=0"`
	FalsePositiveMaxscale            bool `mapstructure:"failover-falsepositive-maxscale" toml:"failover-falsepositive-maxscale" json:"failoverFalsePositiveMaxscale"`
	FalsePositiveMaxscaleTimeout     int  `mapstructure:"failover-falsepositive-maxscale-timeout" toml:"failover-falsepositive-maxscale-timeout" json:"failoverFalsePositiveMaxscaleTimeout" validate:"min=0"`
	FalsePositiveExternal            bool `mapstructure:"failover-falsepositive-external" toml:"failover-falsepositive-external" json:"failoverFalsePositiveExternal"`
	FalsePositiveExternalPort        int  `mapstructure:"failover-falsepositive-external-port" toml:"failover-falsepositive-external-port" json:"failoverFalsePositiveExternalPort" validate:"omitempty,min=1,max=65535"`

	// Failover Logging
	LogFileKeep int `mapstructure:"failover-log-file-keep" toml:"failover-log-file-keep" json:"failoverLogFileKeep" validate:"min=0,max=1000"`

	// Post-Failover Actions
	SwitchToPrefered bool `mapstructure:"failover-switch-to-prefered" toml:"failover-switch-to-prefered" json:"failoverSwithToPrefered"`

	// Delay Statistics for Failover Decision
	CheckDelayStat bool `mapstructure:"failover-check-delay-stat" toml:"failover-check-delay-stat" json:"failoverCheckDelayStat"`
}

// Validate performs validation on FailoverConfig
func (f *FailoverConfig) Validate() error {
	// Validate mode
	if f.Mode != "" {
		validModes := map[string]bool{
			"manual": true,
			"auto":   true,
			"sync":   true,
		}
		if !validModes[f.Mode] {
			return NewValidationError("failover-mode", f.Mode, "must be one of: manual, auto, sync")
		}
	}

	// Validate limits
	if f.Limit < 0 {
		return NewValidationError("failover-limit", f.Limit, "must be non-negative")
	}

	if f.TimeLimit < 0 {
		return NewValidationError("failover-time-limit", f.TimeLimit, "must be non-negative")
	}

	// Validate false positive ping counter
	if f.FalsePositivePingCounter < 0 || f.FalsePositivePingCounter > 100 {
		return NewValidationError("failover-falsepositive-ping-counter", f.FalsePositivePingCounter, "must be between 0 and 100")
	}

	// Validate external port
	if f.FalsePositiveExternal && f.FalsePositiveExternalPort != 0 {
		if f.FalsePositiveExternalPort < 1 || f.FalsePositiveExternalPort > 65535 {
			return NewValidationError("failover-falsepositive-external-port", f.FalsePositiveExternalPort, "must be between 1 and 65535")
		}
	}

	// Validate log file keep
	if f.LogFileKeep < 0 || f.LogFileKeep > 1000 {
		return NewValidationError("failover-log-file-keep", f.LogFileKeep, "must be between 0 and 1000")
	}

	return nil
}
