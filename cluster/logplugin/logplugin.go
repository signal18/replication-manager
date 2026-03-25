// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package logplugin provides a generic log-tailer plugin interface for replication-manager.
//
// Each plugin receives a LogSource snapshot of all four server log ring buffers
// plus a per-plugin Config map resolved from cluster.Conf.PluginConfig[name].
//
// Config resolution order (highest wins):
//   - root /etc/replication-manager/config.toml [DEFAULT.plugin-config.<name>] → immutable
//   - cluster TOML [<cluster>.plugin-config.<name>]                             → operator
//   - plugin hard-coded default                                                  → fallback
//
// Adding a new built-in plugin requires only a single file with an init():
//
//	func init() { Register(&MyPlugin{}) }
package logplugin

import (
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// Severity mirrors the two states a log plugin can raise.
type Severity string

const (
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

// Finding is a single alert raised by a plugin evaluation.
type Finding struct {
	// ErrKey is the state-machine key, e.g. "WARN0200".
	ErrKey string
	// Severity is either SeverityWarning or SeverityError.
	Severity Severity
	// Description is the human-readable message shown in the UI / logs.
	Description string
}

// ToState converts a Finding to the state.State used by the cluster state machine.
func (f Finding) ToState(from string) state.State {
	return state.State{
		ErrKey:  f.ErrKey,
		ErrType: string(f.Severity),
		ErrDesc: f.Description,
		ErrFrom: from,
	}
}

// LogSource groups the log ring-buffer snapshots and resolved config available
// to a plugin.  All fields are value copies so plugins run lock-free.
type LogSource struct {
	// ServerURL is the URL (host:port) of the server being checked.
	ServerURL string
	// ErrorLog is a snapshot of the database error log ring buffer.
	ErrorLog []s18log.HttpMessage
	// SqlErrorLog is a snapshot of the SQL error log ring buffer.
	SqlErrorLog []s18log.HttpMessage
	// SlowLog is a snapshot of the slow-query log ring buffer.
	SlowLog []s18log.HttpMessage
	// AuditLog is a snapshot of the MariaDB SERVER_AUDIT log ring buffer.
	AuditLog []s18log.HttpMessage
	// Config is the resolved per-plugin config map for this plugin.
	// Keys and values are strings; use ConfigStr / ConfigInt / ConfigBool helpers.
	// nil means no config was set — helpers fall back to their defaults.
	Config map[string]string
}

// LogPlugin is the interface every log-tailer plugin must implement.
type LogPlugin interface {
	// Name returns the unique plugin identifier used in config keys and log tags.
	// It must match the key under plugin-config in the TOML:
	//   [plugin-config.errorlog]  →  Name() == "errorlog"
	Name() string

	// Evaluate inspects the LogSource snapshot and returns zero or more Findings.
	// Returning nil or an empty slice means "all clear".
	Evaluate(src LogSource) []Finding
}

// Registry holds all registered plugins.
type Registry struct {
	plugins []LogPlugin
}

// GlobalRegistry is the package-level registry populated during init().
var GlobalRegistry = &Registry{}

// Register adds a plugin to the global registry.  Call from init().
func Register(p LogPlugin) {
	GlobalRegistry.plugins = append(GlobalRegistry.plugins, p)
}

// All returns a snapshot copy of the plugin slice.
func (r *Registry) All() []LogPlugin {
	out := make([]LogPlugin, len(r.plugins))
	copy(out, r.plugins)
	return out
}

// ---- Config helpers ---------------------------------------------------------
// These are shared by built-in and external plugins alike.
// They read from LogSource.Config with a typed default fallback.

// ConfigStr returns the string value for key from cfg, or defaultVal if absent.
func ConfigStr(cfg map[string]string, key, defaultVal string) string {
	if cfg == nil {
		return defaultVal
	}
	if v, ok := cfg[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

// ConfigInt returns the integer value for key from cfg, or defaultVal if absent
// or unparseable.
func ConfigInt(cfg map[string]string, key string, defaultVal int) int {
	s := ConfigStr(cfg, key, "")
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return defaultVal
	}
	return n
}

// ConfigBool returns the boolean value for key from cfg, or defaultVal if absent.
// Accepted truthy values: "true", "1", "yes" (case-insensitive).
func ConfigBool(cfg map[string]string, key string, defaultVal bool) bool {
	s := strings.ToLower(strings.TrimSpace(ConfigStr(cfg, key, "")))
	switch s {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return defaultVal
}
