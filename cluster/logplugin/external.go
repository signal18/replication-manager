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
	"crypto/ed25519"
	"encoding/json"
	"errors"
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
	PFSQueries      []StdioPFSQuery   `json:"pfs_queries"`
	PFSExplainPlans []StdioPFSExplain `json:"pfs_explain_plans,omitempty"`
	PFSExplainCount int               `json:"pfs_explain_count"`
	PFSLastTruncate string            `json:"pfs_last_truncate,omitempty"`

	// Current processlist (populated when processlist monitoring is on)
	ProcessList []StdioProcess `json:"process_list"`

	// Metadata lock waits (populated when METADATA_LOCK_INFO plugin is installed)
	MetaDataLocks []StdioMDL `json:"metadata_locks"`

	// BinlogEvents contains recent binlog QUERY events (populated when monitoring-binlog-events is on)
	BinlogEvents []StdioBinlogEvent `json:"binlog_events"`

	// ServerVersion is the pre-parsed database version derived from the live connection.
	ServerVersion   StdioServerVersion `json:"server_version"`
	// ServerVariables is a snapshot of SHOW GLOBAL VARIABLES (non-sensitive).
	ServerVariables map[string]string  `json:"server_variables"`
	// ServerStatus is a snapshot of SHOW GLOBAL STATUS.
	ServerStatus    map[string]string  `json:"server_status"`

	// DatabaseUsers is a snapshot of mysql.user rows (no password hashes).
	DatabaseUsers []StdioDBUser `json:"database_users"`

	// Tables is the schema dictionary snapshot (wire v3, master request only).
	Tables []StdioTable `json:"tables,omitempty"`

	// ClusterContext carries cluster-level facts (proxies, backup encryption, etc.)
	ClusterContext ClusterContext `json:"cluster_context"`

	// PluginDataDir is the directory where plugin sidecar data files live.
	PluginDataDir string `json:"plugin_data_dir"`

	// Config carries per-plugin settings from the cluster TOML / GUI.
	// Keys are kebab-case (e.g. "timeframe-hours"). Plugins read these via
	// wire.CfgInt / wire.CfgFloat / wire.CfgStr with REPMAN_* env var fallback.
	Config map[string]string `json:"config,omitempty"`
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
	SortRows      int64   `json:"sort_rows"`
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
	Findings    []stdioFinding    `json:"findings"`
	ScoreChecks []ScoreCheck      `json:"score_checks"`
}

type stdioRemediation struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	SQL         string `json:"sql,omitempty"`
	MyCnf       string `json:"my_cnf,omitempty"`
	ConfigKey   string `json:"config_key,omitempty"`
	ConfigValue string `json:"config_value,omitempty"`
	Risk        string `json:"risk"`
}

type stdioFinding struct {
	ErrKey       string             `json:"err_key"`
	Severity     string             `json:"severity"`
	Description  string             `json:"description"`
	Count        int64              `json:"count,omitempty"`
	Total        int64              `json:"total,omitempty"`
	Remediations []stdioRemediation `json:"remediations,omitempty"`
}

// ---- ExternalLogPlugin ------------------------------------------------------

// ExternalLogPlugin wraps a downloaded plugin binary as a LogPlugin.
// It is created by LoadPluginsFromDir and registered in the GlobalRegistry.
//
// If a sidecar file <binary-name>.prerequisites.json exists alongside the
// binary, its contents are parsed and the plugin implements
// LogPluginWithPrerequisites so the orchestrator can raise WARN0312 when a
// required monitoring feed is disabled.
type ExternalLogPlugin struct {
	name         string
	binPath      string
	timeout      time.Duration
	prerequisites []Prerequisite  // loaded from manifest or legacy sidecar, may be nil
	manifest     *PluginManifest  // loaded from <binary>.manifest.json, may be nil
}

// prerequisitesSidecar is the JSON structure of the sidecar file.
type prerequisitesSidecar struct {
	Prerequisites []struct {
		ConfigKey   string `json:"config_key"`
		Description string `json:"description"`
	} `json:"prerequisites"`
}

// NewExternalLogPlugin creates a wrapper for the executable at binPath.
// If a <binPath>.manifest.json sidecar exists, its metadata, prerequisites,
// and config schema are loaded. Falls back to legacy .prerequisites.json.
func NewExternalLogPlugin(name, binPath string, timeout time.Duration) *ExternalLogPlugin {
	p := &ExternalLogPlugin{name: name, binPath: binPath, timeout: timeout}
	p.manifest = loadManifestSidecar(binPath + ".manifest.json")
	if p.manifest != nil {
		p.prerequisites = manifestToPrerequisites(p.manifest)
	} else {
		p.prerequisites = loadPrerequisitesSidecar(binPath + ".prerequisites.json")
	}
	return p
}

// loadPrerequisitesSidecar reads and parses a prerequisites sidecar file.
// Returns nil if the file does not exist or cannot be parsed.
func loadPrerequisitesSidecar(path string) []Prerequisite {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s prerequisitesSidecar
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	if len(s.Prerequisites) == 0 {
		return nil
	}
	out := make([]Prerequisite, 0, len(s.Prerequisites))
	for _, p := range s.Prerequisites {
		if p.ConfigKey != "" {
			out = append(out, Prerequisite{ConfigKey: p.ConfigKey, Description: p.Description})
		}
	}
	return out
}

func (p *ExternalLogPlugin) Name() string { return p.name }

// Prerequisites implements LogPluginWithPrerequisites when the sidecar was loaded.
func (p *ExternalLogPlugin) Prerequisites() []Prerequisite { return p.prerequisites }

// Manifest implements LogPluginWithManifest when a .manifest.json was loaded.
func (p *ExternalLogPlugin) Manifest() *PluginManifest { return p.manifest }

// loadManifestSidecar reads and parses a .manifest.json sidecar file.
func loadManifestSidecar(path string) *PluginManifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m PluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &m
}

// manifestToPrerequisites converts manifest prerequisites to the internal type.
func manifestToPrerequisites(m *PluginManifest) []Prerequisite {
	if m == nil || len(m.Prerequisites) == 0 {
		return nil
	}
	out := make([]Prerequisite, 0, len(m.Prerequisites))
	for _, p := range m.Prerequisites {
		if p.ConfigKey != "" {
			out = append(out, Prerequisite{ConfigKey: p.ConfigKey, Description: p.Description})
		}
	}
	return out
}

// DefaultSeverity implements LogPluginWithDefaultSeverity.
// Used by RunLogPlugins to route debug messages to the correct dedicated log
// when Evaluate returns zero findings (compliant server — no issue detected).
//
// Naming conventions used for inference:
//   plugin-security-* and plugin-score-*  → SeveritySecurity (security/compliance audit)
//   plugin-binlog-*                        → SeveritySecurity (binlog audit for cleartext creds / PII)
//   plugin-*privilege* or plugin-*off-hours* → SeveritySecurity (access-control auditing)
//   everything else                        → SeverityWorkload  (performance / spike detection)
func (p *ExternalLogPlugin) DefaultSeverity() Severity {
	n := p.name
	if strings.HasPrefix(n, "plugin-security-") ||
		strings.HasPrefix(n, "plugin-score-") ||
		strings.HasPrefix(n, "plugin-binlog-") ||
		strings.Contains(n, "privilege") ||
		strings.Contains(n, "off-hours") {
		return SeveritySecurity
	}
	return SeverityWorkload
}

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
		PFSExplainPlans:  src.PFSExplainPlans,
		PFSExplainCount:  src.PFSExplainCount,
		PFSLastTruncate:  formatTime(src.PFSLastTruncate),
		ProcessList:      processToWire(src.ProcessList),
		MetaDataLocks:    mdlToWire(src.MetaDataLocks),
		BinlogEvents:     src.BinlogEvents,
		ServerVersion:    src.ServerVersion,
		ServerVariables:  src.ServerVariables,
		ServerStatus:     src.ServerStatus,
		DatabaseUsers:    src.DatabaseUsers,
		ClusterContext:   src.ClusterContext,
		PluginDataDir:    src.PluginDataDir,
		Config:           src.Config,
		Tables:           src.Tables,
	}

	// SECURITY NOTE: req (wire.Request) includes a full SHOW GLOBAL VARIABLES snapshot,
	// user account data, binlog events, and cluster context.  This payload is passed to
	// the plugin subprocess on stdin on every monitoring tick.  Only plugin binaries
	// that have been verified against plugin-signing.pub are executed; when
	// PluginSigningPublicKey is empty (dev/CI builds without credentials), signature
	// verification is skipped and any plugin-* executable in the plugin directory will
	// receive the full server configuration.  On production deployments always ensure
	// the signing public key is deployed so untrusted binaries are rejected.
	payload, err := json.Marshal(req)
	if err != nil {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, fmt.Sprintf("marshal error: %v", err))}
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, p.binPath) // #nosec G204 — path validated in LoadPluginsFromDir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = &limitWriter{w: &stderrBuf, remaining: 4096} // cap stderr — misbehaving plugin cannot OOM the process
	// WaitDelay ensures cmd.Output() returns promptly after the context deadline
	// fires even when the plugin subprocess has spawned children that inherited
	// the stdout/stderr pipes.  Without this, cmd.Wait() blocks forever waiting
	// for pipe EOF from those orphaned children, hanging the monitoring loop.
	cmd.WaitDelay = 2 * time.Second

	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded || errors.Is(err, exec.ErrWaitDelay) {
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL, "plugin timed out")}
	}
	if stderrBuf.Len() > 0 {
		// Plugin wrote to stderr — surface it as a finding so it appears in the log
		return EvaluateResult{Findings: pluginErrFinding(p.name, src.ServerURL,
			fmt.Sprintf("plugin stderr: %s", strings.TrimSpace(stderrBuf.String())))}
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
		if sev != SeverityWarning && sev != SeverityError && sev != SeveritySecurity && sev != SeverityWorkload {
			sev = SeverityWarning
		}
		remeds := make([]Remediation, 0, len(sf.Remediations))
		for _, sr := range sf.Remediations {
			remeds = append(remeds, Remediation{
				Type:        sr.Type,
				Description: sr.Description,
				SQL:         sr.SQL,
				MyCnf:       sr.MyCnf,
				ConfigKey:   sr.ConfigKey,
				ConfigValue: sr.ConfigValue,
				Risk:        sr.Risk,
			})
		}
		findings = append(findings, Finding{
			ErrKey:       sf.ErrKey,
			Severity:     sev,
			Description:  sf.Description,
			Count:        sf.Count,
			Total:        sf.Total,
			Remediations: remeds,
		})
	}
	return EvaluateResult{Findings: findings, ScoreChecks: resp.ScoreChecks}
}

// ---- signature verification -------------------------------------------------

// VerifyPluginSignature checks the Ed25519 signature of a plugin binary.
// sigDir is the directory that holds <plugin-name>.sig files (ShareDir/plugins/).
// pubKeyPath is the path to the raw 32-byte Ed25519 public key file.
// Returns nil when the signature is valid.
func VerifyPluginSignature(binPath, sigDir, pubKeyPath string) error {
	pubBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("cannot read public key %s: %w", pubKeyPath, err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length %d (expected %d)", len(pubBytes), ed25519.PublicKeySize)
	}

	data, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("cannot read plugin binary %s: %w", binPath, err)
	}

	name := filepath.Base(binPath)
	sigPath := filepath.Join(sigDir, name+".sig")
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("cannot read signature %s: %w", sigPath, err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pubBytes), data, sig) {
		return fmt.Errorf("signature mismatch for plugin %s — binary may have been tampered with", name)
	}
	return nil
}

// ---- loader -----------------------------------------------------------------

// LoadOptions controls optional behaviour of LoadPluginsFromDir.
type LoadOptions struct {
	// PubKeyPath is the path to the Ed25519 public key used to verify plugin
	// signatures. When non-empty, every plugin binary must have a valid .sig
	// sidecar in SigDir; plugins that fail verification are rejected and logged.
	// When empty, signature verification is skipped (dev/unsigned builds).
	PubKeyPath string

	// SigDir is the directory containing <plugin-name>.sig files.
	// Defaults to pluginDir when empty (sig files next to binaries).
	// In production this is typically <ShareDir>/plugins/.
	SigDir string
}

// LoadPluginsFromDir scans pluginDir for executable files, creates an
// ExternalLogPlugin for each one, and registers/replaces it in reg.
// Returns the number of plugins loaded, a slice of rejection messages for
// plugins that failed signature verification, and any scan error.
//
// Rules:
//   - Non-executable files and dotfiles are silently skipped.
//   - A plugin whose name already exists in reg is hot-replaced in place.
//   - When opts.PubKeyPath is set AND the key file exists, every plugin must
//     have a valid .sig in SigDir; plugins that fail are rejected.
//   - When opts.PubKeyPath is set but the file does not exist yet (e.g. first
//     boot before the package is fully installed), verification is skipped and
//     a "pubKeyMissing" message is returned as the first rejection entry so the
//     caller can log a warning.
func LoadPluginsFromDir(pluginDir string, reg *Registry, opts LoadOptions) (loaded int, rejections []string, err error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("scan plugin dir %s: %w", pluginDir, err)
	}

	// Resolve whether signature verification is active for this run.
	verifyEnabled := false
	if opts.PubKeyPath != "" {
		if _, statErr := os.Stat(opts.PubKeyPath); statErr == nil {
			verifyEnabled = true
		} else {
			// Key path configured but file absent — warn and proceed without verification.
			rejections = append(rejections, fmt.Sprintf("pubKeyMissing: %s not found — signature verification skipped", opts.PubKeyPath))
		}
	}

	sigDir := opts.SigDir
	if sigDir == "" {
		sigDir = pluginDir
	}

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasPrefix(e.Name(), "plugin-") {
			continue
		}
		// Plugin binaries have no file extension. Skip .sig, .pub, and any
		// other extension so non-binary files are never executed.
		if filepath.Ext(e.Name()) != "" {
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
		// Skip external plugins that duplicate a built-in enterprise plugin.
		// The built-in versions have additional protections (free plan warnings,
		// error handling, correct severity routing).
		// This check must run before signature verification so that portal-deployed
		// enterprise binaries are silently skipped even when .sig files are absent.
		builtinName := strings.TrimPrefix(name, "plugin-")
		if reg.Has(builtinName) {
			continue
		}

		if verifyEnabled {
			if verr := VerifyPluginSignature(binPath, sigDir, opts.PubKeyPath); verr != nil {
				rejections = append(rejections, fmt.Sprintf("%s: %v", e.Name(), verr))
				continue
			}
		}
		reg.replace(name, NewExternalLogPlugin(name, binPath, DefaultPluginTimeout))
		loaded++
	}
	return loaded, rejections, nil
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

// Has returns true if a plugin with the given name is already registered.
func (r *Registry) Has(name string) bool {
	for _, p := range r.plugins {
		if p.Name() == name {
			return true
		}
	}
	return false
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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func pluginErrFinding(pluginName, serverURL, msg string) []Finding {
	return []Finding{{
		ErrKey:      "WARN0203",
		Severity:    SeverityWarning,
		Description: fmt.Sprintf("Plugin %s on %s: %s", pluginName, serverURL, msg),
	}}
}

// limitWriter caps writes to a fixed number of bytes so a misbehaving plugin
// subprocess cannot grow the stderr buffer unboundedly on every monitoring tick.
// Replaces io.LimitWriter (added Go 1.22) for compatibility with older toolchains.
type limitWriter struct {
	w         *bytes.Buffer
	remaining int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return len(p), nil // silently discard once cap is reached
	}
	if int64(len(p)) > lw.remaining {
		p = p[:lw.remaining]
	}
	n, err := lw.w.Write(p)
	lw.remaining -= int64(n)
	return n, err
}
