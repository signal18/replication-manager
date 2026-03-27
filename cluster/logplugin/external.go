// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// Package logplugin — external binary plugin loader.
//
// External plugins are standalone executables placed under
//
//	<cluster.WorkingDir>/plugins/
//
// Directory layout (source):
//
//	cluster/logplugin/
//	└── plugins/
//	    └── plugin-innodb-corruption/
//	        └── main.go
//
// Wire protocol (JSON over stdin/stdout):
//
//	stdin  ← stdioRequest  (server URL + all log/PFS/processlist snapshots)
//	stdout → stdioResponse (findings array)
//	exit 0  = OK  (empty findings = all clear)
//	exit ≠0 = error (repman logs WARN0203)
package logplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

const (
	// PluginDirName is the subdirectory inside cluster.WorkingDir where
	// runtime plugin binaries are placed (by GitLab pull or manual copy).
	PluginDirName = "plugins"

	// DefaultPluginTimeout is the maximum time a plugin binary may run.
	DefaultPluginTimeout = 5 * time.Second
)

// ---- wire types -------------------------------------------------------------
// These are the JSON structs sent to / received from plugin binaries.
// Plugin authors copy these into their own package — no shared dependency needed.

// stdioRequest is written to the plugin's stdin.
type StdioRequest struct {
	// Core identification
	ServerURL        string `json:"server_url"`
	GraphiteAPIURL   string `json:"graphite_api_url"`
	GraphiteHostname string `json:"graphite_hostname"`

	// Log ring buffers
	ErrorLog    []StdioMsg     `json:"error_log"`
	SqlErrorLog []StdioMsg     `json:"sql_error_log"`
	SlowLog     []StdioSlowMsg `json:"slow_log"`
	AuditLog    []StdioMsg     `json:"audit_log"`

	// Performance Schema query statistics (populated when PFS monitoring is on)
	PFSQueries []StdioPFSQuery `json:"pfs_queries"`

	// Current processlist (populated when processlist monitoring is on)
	ProcessList []StdioProcess `json:"process_list"`

	// Metadata lock waits (populated when METADATA_LOCK_INFO plugin is installed)
	MetaDataLocks []StdioMDL `json:"metadata_locks"`
}

// stdioMsg is a generic log entry (error log, SQL error log, audit log).
type StdioMsg struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

// stdioSlowMsg is a slow-query log entry with full metrics.
type StdioSlowMsg struct {
	Timestamp     string             `json:"timestamp"`
	Query         string             `json:"query"`
	User          string             `json:"user"`
	Host          string             `json:"host"`
	Db            string             `json:"db"`
	TimeMetrics   map[string]float64 `json:"time_metrics"`   // query_time, lock_time, etc.
	NumberMetrics map[string]uint64  `json:"number_metrics"` // rows_sent, rows_examined, etc.
}

// stdioPFSQuery is one row from performance_schema.events_statements_summary_by_digest.
type StdioPFSQuery struct {
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

// stdioProcess is one row from INFORMATION_SCHEMA.PROCESSLIST (extended).
type StdioProcess struct {
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

// stdioMDL is one metadata lock wait entry.
type StdioMDL struct {
	ThreadID     uint64 `json:"thread_id"`
	LockMode     string `json:"lock_mode"`
	LockDuration string `json:"lock_duration"`
	LockTimeMs   int64  `json:"lock_time_ms"`
	LockType     string `json:"lock_type"`
	Schema       string `json:"schema"`
	Table        string `json:"table"`
}

// stdioResponse is read from the plugin's stdout.
type stdioResponse struct {
	Findings []stdioFinding `json:"findings"`
}

type stdioFinding struct {
	ErrKey      string `json:"err_key"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// ---- ExternalLogPlugin ------------------------------------------------------

// ExternalLogPlugin wraps a downloaded plugin binary as a LogPlugin.
// It is created by LoadPluginsFromDir and registered in the GlobalRegistry.
type ExternalLogPlugin struct {
	name    string
	binPath string
	timeout time.Duration
}

// NewExternalLogPlugin creates a wrapper for the executable at binPath.
func NewExternalLogPlugin(name, binPath string, timeout time.Duration) *ExternalLogPlugin {
	return &ExternalLogPlugin{name: name, binPath: binPath, timeout: timeout}
}

func (p *ExternalLogPlugin) Name() string { return p.name }

func (p *ExternalLogPlugin) Evaluate(src LogSource) EvaluateResult {
	req := StdioRequest{
		ServerURL:        src.ServerURL,
		GraphiteAPIURL:   src.GraphiteAPIURL,
		GraphiteHostname: src.GraphiteHostname,
		ErrorLog:         msgsToWire(src.ErrorLog),
		SqlErrorLog:      msgsToWire(src.SqlErrorLog),
		SlowLog:          src.SlowLog,
		AuditLog:         msgsToWire(src.AuditLog),
		PFSQueries:       pfsToWire(src.PFSQueries),
		ProcessList:      processToWire(src.ProcessList),
		MetaDataLocks:    mdlToWire(src.MetaDataLocks),
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("marshal error: %v", err))}
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.binPath) // #nosec G204 — path validated in LoadPluginsFromDir
	cmd.Stdin = bytes.NewReader(payload)

	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, "plugin timed out")}
	}
	if err != nil {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("exec error: %v", err))}
	}

	var resp stdioResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("bad JSON response: %v", err))}
	}

	findings := make([]Finding, 0, len(resp.Findings))
	for _, sf := range resp.Findings {
		sev := Severity(strings.ToUpper(sf.Severity))
		if sev != SeverityWarning && sev != SeverityError {
			sev = SeverityWarning
		}
		findings = append(findings, Finding{
			ErrKey:      sf.ErrKey,
			Severity:    sev,
			Description: sf.Description,
		})
	}
	return EvaluateResult{Findings: findings}
}

// ---- loader -----------------------------------------------------------------

// LoadPluginsFromDir scans pluginDir for executable files, creates an
// ExternalLogPlugin for each one, and registers/replaces it in reg.
// Returns the number of plugins loaded and any scan error.
//
// Rules:
//   - Non-executable files and dotfiles are silently skipped.
//   - A plugin whose name already exists in reg is hot-replaced in place.
func LoadPluginsFromDir(pluginDir string, reg *Registry) (int, error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("scan plugin dir %s: %w", pluginDir, err)
	}

	loaded := 0
	for _, e := range entries {
		// Skip directories, dotfiles, and known non-binary names (e.g. the
		// "wire" library package which lives alongside the plugin sources).
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Convention: all plugin binaries are named plugin-*.
		// Anything that doesn't match is silently skipped so library packages
		// or helper scripts in the same directory don't cause exec errors.
		if !strings.HasPrefix(e.Name(), "plugin-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0111 == 0 { // not executable
			continue
		}
		binPath := filepath.Join(pluginDir, e.Name())
		name := pluginNameFromBinary(e.Name())
		reg.replace(name, NewExternalLogPlugin(name, binPath, DefaultPluginTimeout))
		loaded++
	}
	return loaded, nil
}

// PluginDir returns the canonical plugin directory for a cluster given its
// WorkingDir (= Conf.WorkingDir + "/" + cluster.Name).
func PluginDir(clusterWorkingDir string) string {
	return filepath.Join(clusterWorkingDir, PluginDirName)
}

// pluginNameFromBinary derives the plugin name from the binary filename.
func pluginNameFromBinary(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func (r *Registry) replace(name string, p LogPlugin) {
	for i, existing := range r.plugins {
		if existing.Name() == name {
			r.plugins[i] = p
			return
		}
	}
	r.plugins = append(r.plugins, p)
}

// ---- conversion helpers -----------------------------------------------------

func msgsToWire(msgs []s18log.HttpMessage) []StdioMsg {
	out := make([]StdioMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, StdioMsg{Level: m.Level, Timestamp: m.Timestamp, Text: m.Text})
	}
	return out
}

func pfsToWire(queries []StdioPFSQuery) []StdioPFSQuery {
	return queries // already in wire format, populated by snapshotPFSQueries in srv_log_plugins.go
}

func processToWire(procs []StdioProcess) []StdioProcess {
	return procs // already in wire format
}

func mdlToWire(mdls []StdioMDL) []StdioMDL {
	return mdls // already in wire format
}

func pluginErrFinding(pluginName, serverURL, msg string) []Finding {
	return []Finding{{
		ErrKey:      "WARN0203",
		Severity:    SeverityError,
		Description: fmt.Sprintf("Plugin %s on %s: %s", pluginName, serverURL, msg),
	}}
}
