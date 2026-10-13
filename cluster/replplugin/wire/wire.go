// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package wire is the contract between replication-manager and its replication plugins
// (#2015): a long-running process per cluster and plugin that carries a source's changes
// to a target where native replication cannot (ColumnStore applies no row UPDATE or
// DELETE). replication-manager writes the plugin's TOML in the cluster's datadir, starts the
// plugin with its passwords in the environment, reads one Status JSON line per period on
// its stdout and restarts it when it exits; the plugin keeps its position in its State
// file. Standard library only: the plugins are separate Go modules that import it.
package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// WireVersion is the version of this contract; a plugin built for another one is refused.
const WireVersion = 1

// BinaryPrefix names a replication plugin binary: replplugin-<plugin>. Not "plugin-": the
// log plugin loader takes every plugin-* it finds.
const BinaryPrefix = "replplugin-"

// The environment of a plugin process: the passwords are never in its TOML.
const (
	EnvSourcePassword = "REPMAN_REPLPLUGIN_SOURCE_PASSWORD"
	EnvTargetPassword = "REPMAN_REPLPLUGIN_TARGET_PASSWORD"
	EnvWireVersion    = "REPMAN_REPLPLUGIN_WIRE_VERSION"
)

// Endpoint is a database server the plugin connects to.
type Endpoint struct {
	Host string `toml:"host" json:"host"`
	Port int    `toml:"port" json:"port"`
	User string `toml:"user" json:"user"`
}

// Rule selects the tables a plugin carries: a schema and its tables ("*" or empty: every
// table of the schema).
type Rule struct {
	Schema string   `toml:"schema" json:"schema"`
	Tables []string `toml:"tables" json:"tables"`
}

// Config is a plugin's TOML, <cluster datadir>/replication-plugins/<name>.toml.
type Config struct {
	Plugin  string `toml:"plugin" json:"plugin"` // the binary: replplugin-<plugin>
	Enabled bool   `toml:"enabled" json:"enabled"`
	Mode    string `toml:"mode" json:"mode"` // columnstore: "sql"
	// ServerID: the plugin's binlog client id on the source, unique among its replicas
	ServerID       uint32   `toml:"server_id" json:"serverId"`
	Source         Endpoint `toml:"source" json:"source"`
	Target         Endpoint `toml:"target" json:"target"`
	Rules          []Rule   `toml:"rule" json:"rules"`
	BatchSize      int      `toml:"batch_size" json:"batchSize"`
	BatchTimeoutMs int      `toml:"batch_timeout_ms" json:"batchTimeoutMs"`
	// StartGTID: where the plugin starts when it has no State yet (a ColumnStore child:
	// the GTID of its seed)
	StartGTID string `toml:"start_gtid" json:"startGtid"`
	// SourceChannel: the target's native replication channel from the same source, which
	// ignores the tables this plugin carries (replicate_ignore_table), "" when none
	SourceChannel string `toml:"source_channel" json:"sourceChannel"`
}

// Tables lists the rules' tables as schema.table; ok false when a rule takes a whole
// schema (no exact list then).
func (c *Config) Tables() (tables []string, ok bool) {
	for _, r := range c.Rules {
		if len(r.Tables) == 0 {
			return nil, false
		}
		for _, t := range r.Tables {
			if t == "*" {
				return nil, false
			}
			tables = append(tables, r.Schema+"."+t)
		}
	}
	return tables, true
}

// Matches tells whether a rule selects schema.table.
func (c *Config) Matches(schema, table string) bool {
	for _, r := range c.Rules {
		if !strings.EqualFold(r.Schema, schema) {
			continue
		}
		if len(r.Tables) == 0 {
			return true
		}
		for _, t := range r.Tables {
			if t == "*" || strings.EqualFold(t, table) {
				return true
			}
		}
	}
	return false
}

// Plugin states, in Status.State.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateError    = "error"
)

// Status is one line of a plugin's stdout, printed every batch period and on an error.
type Status struct {
	Wire        int    `json:"wire"`
	Time        string `json:"time"`
	State       string `json:"state"`
	GTID        string `json:"gtid"`       // the last source transaction applied to the target
	LagSeconds  int64  `json:"lagSeconds"` // now minus the source time of that transaction
	Inserts     int64  `json:"inserts"`    // rows, since the plugin started
	Updates     int64  `json:"updates"`
	Deletes     int64  `json:"deletes"`
	Skipped     int64  `json:"skipped"` // rows of tables no rule selects
	Error       string `json:"error,omitempty"`
	Description string `json:"description,omitempty"`
}

// State is a plugin's position, <name>.state.json next to its TOML: where it restarts.
type State struct {
	GTID string `json:"gtid"`
}

// ReadState reads a State file; an empty State when there is none yet.
func ReadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// WriteState writes a State file atomically (a temporary file renamed over it).
func WriteState(path string, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// StatePath is the State file of a plugin TOML: name.toml -> name.state.json.
func StatePath(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".state.json"
}
