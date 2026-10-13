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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	// MaxPendingMB: the plugin pauses (no more input read, no more binlog read) while the
	// checkpoints waiting to be applied take more than this; 0: 1024
	MaxPendingMB int `toml:"max_pending_mb" json:"maxPendingMb"`
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
// RestoreDone: the restore's rows were all received, the binlog streams from GTID; Tables:
// the tables it carries since then (schema.table), told by the restore's end; Seq: the
// last checkpoint written.
type State struct {
	GTID        string   `json:"gtid"`
	RestoreDone bool     `json:"restoreDone"`
	Tables      []string `json:"tables,omitempty"`
	Seq         int64    `json:"seq"`
}

// What replication-manager writes on a plugin's stdin (#2015): the restore sends the rows of
// the carried tables as it reads them, then its end; the plugin only then streams the
// binlog, from the GTID of the backup restored. A frame: an Input as a JSON line, then Len
// bytes of payload, the statement as the backup holds it (binary values included, which a
// JSON string would not keep).
const (
	InputRestoreRows = "restore_rows"
	InputRestoreDone = "restore_done"
)

// Input is a frame's header: an INSERT ... VALUES of the restore for schema.table (Len
// bytes follow), or the restore's end with the backup's GTID and the tables carried (those
// the restore made ColumnStore; a table it left as it was is not).
type Input struct {
	Type   string   `json:"type"`
	Schema string   `json:"schema,omitempty"`
	Table  string   `json:"table,omitempty"`
	Len    int      `json:"len,omitempty"`
	GTID   string   `json:"gtid,omitempty"`
	Tables []string `json:"tables,omitempty"`
}

// WriteInput writes a frame.
func WriteInput(w io.Writer, in Input, payload []byte) error {
	in.Len = len(payload)
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err = w.Write(payload)
	}
	return err
}

// ReadInput reads a frame.
func ReadInput(r *bufio.Reader) (Input, []byte, error) {
	var in Input
	line, err := r.ReadBytes('\n')
	if err != nil {
		return in, nil, err
	}
	if err := json.Unmarshal(line, &in); err != nil {
		return in, nil, fmt.Errorf("input frame: %w", err)
	}
	if in.Len < 0 {
		return in, nil, fmt.Errorf("input frame of %d bytes", in.Len)
	}
	payload := make([]byte, in.Len)
	if _, err := io.ReadFull(r, payload); err != nil {
		return in, nil, err
	}
	return in, payload, nil
}

// RulesOf turns schema.table names into rules.
func RulesOf(tables []string) []Rule {
	by := map[string][]string{}
	var order []string
	for _, t := range tables {
		s, n, ok := strings.Cut(t, ".")
		if !ok {
			continue
		}
		if _, seen := by[s]; !seen {
			order = append(order, s)
		}
		by[s] = append(by[s], n)
	}
	rules := make([]Rule, 0, len(order))
	for _, s := range order {
		rules = append(rules, Rule{Schema: s, Tables: by[s]})
	}
	return rules
}

// Applied is what replication-manager applied to the target, <name>.applied.json.
type Applied struct {
	Seq  int64  `json:"seq"`
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

// CheckpointDir is the directory of a plugin's checkpoints, <name>.checkpoints next to its TOML.
func CheckpointDir(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".checkpoints"
}

// AppliedPath is the file replication-manager records the applied checkpoints in.
func AppliedPath(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".applied.json"
}

// CheckpointName is the file of checkpoint seq, sorted by name in the order of seq.
func CheckpointName(seq int64) string {
	return fmt.Sprintf("%012d.tar.gz", seq)
}

// ParseCheckpointName returns the seq of a checkpoint file, false for any other file.
func ParseCheckpointName(name string) (int64, bool) {
	if !strings.HasSuffix(name, ".tar.gz") || len(name) != 12+len(".tar.gz") {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(name, ".tar.gz"), 10, 64)
	return n, err == nil
}

// ManifestName is the manifest inside a checkpoint tar.gz.
const ManifestName = "manifest.json"

// Manifest describes a checkpoint, a period of what the plugin received: the rows of each
// table to delete (by key) then to load. GTID is the source position the target is at once
// the checkpoint is applied, "" while the restore runs (consistent only at its end).
type Manifest struct {
	Wire   int          `json:"wire"`
	Seq    int64        `json:"seq"`
	GTID   string       `json:"gtid"`
	Tables []TableFiles `json:"tables"`
}

// TableFiles are the files of one table in a checkpoint, tab-separated in LOAD DATA's
// default format (EncodeTSV). Deletes holds the KeyColumns of the rows to delete, those of
// the loaded rows included (a checkpoint applied twice gives the same table); Inserts holds
// every column, in the table's order.
type TableFiles struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	KeyColumns []string `json:"keyColumns"`
	Deletes    string   `json:"deletes,omitempty"`
	DeleteRows int64    `json:"deleteRows"`
	Inserts    string   `json:"inserts,omitempty"`
	InsertRows int64    `json:"insertRows"`
}

// PendingBytes is the size of the checkpoints waiting in dir.
func PendingBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if _, ok := ParseCheckpointName(e.Name()); !ok {
			continue
		}
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}

// EncodeTSV appends one row in LOAD DATA's default format: fields separated by a tab, the
// row ended by a newline, NULL as \N, backslash, tab, newline, carriage return and NUL
// escaped by a backslash. A value is nil, a string or bytes (its text).
func EncodeTSV(buf []byte, fields []interface{}) []byte {
	for i, f := range fields {
		if i > 0 {
			buf = append(buf, '\t')
		}
		var s []byte
		switch v := f.(type) {
		case nil:
			buf = append(buf, '\\', 'N')
			continue
		case string:
			s = []byte(v)
		case []byte:
			s = v
		default:
			s = []byte(fmt.Sprint(v))
		}
		for _, c := range s {
			switch c {
			case '\\':
				buf = append(buf, '\\', '\\')
			case '\t':
				buf = append(buf, '\\', 't')
			case '\n':
				buf = append(buf, '\\', 'n')
			case '\r':
				buf = append(buf, '\\', 'r')
			case 0:
				buf = append(buf, '\\', '0')
			default:
				buf = append(buf, c)
			}
		}
	}
	return append(buf, '\n')
}

// DecodeTSV reads one row written by EncodeTSV (without its newline): nil for \N, else
// the bytes.
func DecodeTSV(line []byte) [][]byte {
	var out [][]byte
	var cur []byte
	null := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\t':
			if null {
				out = append(out, nil)
			} else {
				out = append(out, append([]byte{}, cur...))
			}
			cur, null = cur[:0], false
		case c == '\\' && i+1 < len(line):
			i++
			switch line[i] {
			case 'N':
				null = true
			case 't':
				cur = append(cur, '\t')
			case 'n':
				cur = append(cur, '\n')
			case 'r':
				cur = append(cur, '\r')
			case '0':
				cur = append(cur, 0)
			default:
				cur = append(cur, line[i])
			}
		default:
			cur = append(cur, c)
		}
	}
	if null {
		return append(out, nil)
	}
	return append(out, append([]byte{}, cur...))
}
