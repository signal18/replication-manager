// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// Package logplugin provides a generic log-tailer plugin interface for replication-manager.
//
// Each plugin:
//   - receives a LogSource snapshot of log ring buffers + graphite context
//   - fetches its own history from the graphite render API
//   - computes a dynamic multi-granularity baseline (per-minute, per-hour, per-day, per-week)
//   - detects spikes using mean ± N×stddev on the appropriate granularity
//   - returns an EvaluateResult with Findings, counts, and correlated graphite metrics
//
// Per-plugin enable/disable:
//
//	[mycluster.plugin-config.slowlog]
//	enabled = false
package logplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// Severity mirrors the two states a log plugin can raise.
type Severity string

const (
	SeverityWarning  Severity = "WARNING"
	SeverityError    Severity = "ERROR"
	// SeveritySecurity is used by security-audit plugins (SEC0xxx error keys).
	// It is rendered distinctly from operational WARNING/ERROR in the UI and
	// API responses, allowing operators to filter security findings separately.
	SeveritySecurity Severity = "SECURITY"
	// SeveritySchema is used by schema advisory plugins (SCH0xxx error keys,
	// SCHTAG0xxx inventory tags). Findings are routed to the cluster
	// SchemaStateMachine.
	SeveritySchema Severity = "SCHEMA"
	// SeverityWorkload is used by workload/performance spike detection plugins.
	// These detect Graphite-based anomalies (slow query regressions, connection
	// storms, tmp-table storms, etc.) and are tracked in the WorkloadStateMachine
	// separately from HA findings so performance noise does not pollute the HA view.
	SeverityWorkload Severity = "WORKLOAD"
)

// Remediation is a machine-readable fix proposal attached to a Finding.
// Type is one of "sql", "my_cnf", or "repman_config".
// Risk is one of "safe" (non-disruptive), "moderate" (may affect connections),
// or "disruptive" (requires restart or may break workloads).
type Remediation struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	SQL         string `json:"sql,omitempty"`
	MyCnf       string `json:"my_cnf,omitempty"`
	ConfigKey   string `json:"config_key,omitempty"`
	ConfigValue string `json:"config_value,omitempty"`
	Risk        string `json:"risk"`
}

// Finding is a single alert raised by a plugin evaluation.
type Finding struct {
	ErrKey       string
	Severity     Severity
	Description  string
	Remediations []Remediation
	Count        int64
	Total        int64
}

// ToState converts a Finding to a cluster state.State.
func (f Finding) ToState(from string) state.State {
	return state.State{
		ErrKey:  f.ErrKey,
		ErrType: string(f.Severity),
		ErrDesc: f.Description,
		ErrFrom: from,
	}
}

// EvaluateResult is returned by Evaluate.
type EvaluateResult struct {
	Findings      []Finding
	ScoreChecks   []ScoreCheck // compliance score contributions, may be nil
	CurrentCount  int          // events counted in current window
	PreviousCount int          // events in previous same-length window (for fallback when graphite unavailable)
	// MetricName is the graphite metric name this plugin writes its count under.
	// Empty if graphite is not configured.
	MetricName string
}

// ScoreCheck is a single compliance check result returned by a score plugin.
// The Tag matches one of the SecurityScore boolean fields (e.g. "HasSSL").
type ScoreCheck struct {
	Tag    string `json:"tag"`              // e.g. "HasSSL", "NoEmptyPassword"
	Pass   bool   `json:"pass"`             // true = check passed
	Detail string `json:"detail,omitempty"` // human-readable explanation
}

// SpikeCheckInterval is how often DetectSpike is actually called.
// Between checks the previous result is reused so graphite is not
// queried on every 5-second monitoring tick.
const SpikeCheckInterval = 60 * time.Second

// SpikeCache holds the last DetectSpike result for one plugin+server+metric tuple.
// DetectSpike reads and writes the cache to avoid querying graphite on every
// monitoring tick. The cache is keyed and managed by srv_log_plugins.go.
type SpikeCache struct {
	// Result is the last computed SpikeResult (nil = no spike was detected).
	Result     *SpikeResult
	// MetricName is the graphite metric that was analyzed, stored so the
	// re-injected finding description matches the original exactly.
	MetricName string
	// CheckedAt is when DetectSpike last ran the full graphite computation.
	CheckedAt time.Time
	// OpenedAt is when the spike was first confirmed. The state is held open
	// for at least SpikeHoldDuration after OpenedAt regardless of sigma
	// oscillations, preventing RESOLV/OPEN churn while the baseline is thin.
	OpenedAt time.Time
}

// SpikeHoldDuration is the minimum wall-clock time a spike state stays open
// after first detection. Graphite baselines for hourly/daily granularities can
// be thin early in the hour/day, causing sigma to oscillate around the threshold
// as new data points accumulate. Holding the state prevents RESOLV/OPEN churn.
const SpikeHoldDuration = 30 * time.Minute

// IsHeld returns true when the spike was opened recently enough that the
// hold period has not yet expired, even if DetectSpike currently returns nil.
func (c *SpikeCache) IsHeld() bool {
	if c == nil || c.OpenedAt.IsZero() {
		return false
	}
	return time.Since(c.OpenedAt) < SpikeHoldDuration
}

// IsFresh returns true when the cache entry is recent enough to reuse.
func (c *SpikeCache) IsFresh() bool {
	if c == nil {
		return false
	}
	return time.Since(c.CheckedAt) < SpikeCheckInterval
}

// StdioDBUser is one row from mysql.user, stripped of sensitive credential data.
// Used by security plugins to audit authentication configuration.
type StdioDBUser struct {
	User          string `json:"user"`
	Host          string `json:"host"`
	Plugin        string `json:"plugin"`          // e.g. "mysql_native_password", "ed25519", "caching_sha2_password"
	PasswordEmpty bool   `json:"password_empty"`  // true when authentication_string / password is empty
	AccountLocked bool   `json:"account_locked"`  // true when account_locked = 'Y' — account cannot be used to log in
}

// StdioServerVersion carries the pre-parsed database version.
type StdioServerVersion struct {
	Flavor  string `json:"flavor"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Release int    `json:"release"`
}

// StdioPFSExplain is one cached EXPLAIN plan for a PFS digest.
type StdioPFSExplain struct {
	Digest     string              `json:"digest"`
	DigestText string              `json:"digest_text"`
	ExecCount  int64               `json:"exec_count"`
	Plan       []StdioExplainRow   `json:"plan"`
}

// StdioExplainRow is one row from an EXPLAIN output.
type StdioExplainRow struct {
	Table string `json:"table"`
	Type  string `json:"type"`  // ALL, index, range, ref, eq_ref, const, system
	Key   string `json:"key"`
	Rows  string `json:"rows"`
	Extra string `json:"extra"`
}

// StdioBinlogEvent is a single QUERY_EVENT captured from a server's binary log.
// Passed to plugins that inspect binlog content (e.g. cleartext-password, credit-card).
type StdioBinlogEvent struct {
	Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" UTC
	Schema    string `json:"schema"`    // default schema at query time
	Query     string `json:"query"`     // raw SQL statement text
	ServerID  uint32 `json:"server_id"` // originating server-id
}

// LogSource is passed to every plugin Evaluate() call.
// All buffer fields are lock-free value copies.
type LogSource struct {
	ServerURL       string
	ErrorLog        []s18log.HttpMessage
	SqlErrorLog     []s18log.HttpMessage
	// SlowLog is a snapshot of slow query entries with full metrics.
	SlowLog         []StdioSlowMsg
	AuditLog        []s18log.HttpMessage
	// Config is the resolved per-plugin config map (nil = use defaults).
	Config          map[string]string
	// GraphiteAPIURL is the base URL of the graphite render API,
	// e.g. "http://127.0.0.1:10002". Empty when graphite is disabled.
	GraphiteAPIURL  string
	// GraphiteHostname is the hostname key used in graphite metric names,
	// e.g. "db1-belair-svc-cloud18" (dots replaced with dashes by carbon).
	GraphiteHostname string
	// SpikeCache carries the previous DetectSpike result so plugins skip
	// the graphite HTTP call when the cache is still fresh (< SpikeCheckInterval).
	// Managed by srv_log_plugins.go — nil means no cache yet.
	SpikeCache *SpikeCache
	// PFSQueries is a snapshot of performance_schema digest statistics.
	// Populated when Conf.MonitoringPFS is enabled. Empty slice when unavailable.
	PFSQueries []StdioPFSQuery
	// PFSLastTruncate is when the PFS digest table was last truncated.
	// Zero value means never truncated. Used to query Graphite for the
	// queries delta over the same window as the PFS counters.
	PFSLastTruncate time.Time
	// PFSExplainCount is the number of digests that have a cached EXPLAIN plan.
	PFSExplainCount int
	// PFSExplainPlans carries the cached EXPLAIN plans keyed by digest hash.
	PFSExplainPlans []StdioPFSExplain
	// ProcessList is a snapshot of the current processlist.
	// Populated when Conf.MonitoringProcesslist is enabled.
	ProcessList []StdioProcess
	// MetaDataLocks is a snapshot of current MDL waits.
	// Populated when METADATA_LOCK_INFO plugin is installed.
	MetaDataLocks []StdioMDL
	// BinlogEvents is a snapshot of recent binlog QUERY events.
	// Populated when Conf.MonitorBinlogEvents is enabled.
	BinlogEvents []StdioBinlogEvent
	// ServerVersion is the pre-parsed database version derived from the live connection.
	// Use this instead of parsing ServerVariables["version"] — it has correct flavor/case.
	ServerVersion   StdioServerVersion
	// ServerVariables is a snapshot of SHOW GLOBAL VARIABLES (non-sensitive).
	// Always populated when the server is reachable.
	ServerVariables map[string]string
	// ServerStatus is a snapshot of SHOW GLOBAL STATUS.
	// Always populated when the server is reachable.
	ServerStatus map[string]string
	// DatabaseUsers is a snapshot of mysql.user rows (no password hashes).
	// Always populated when the server is reachable.
	DatabaseUsers []StdioDBUser
	// ClusterContext carries cluster-level facts that cannot be derived from
	// a single server snapshot (e.g. whether proxies are configured, whether
	// backups are encrypted). Populated by srv_log_plugins.go.
	ClusterContext ClusterContext
	// PluginDataDir is the directory where plugin sidecar data files are stored
	// (e.g. lts-versions.json). Plugins should read data files from this path.
	PluginDataDir string
	// Tables is the schema dictionary snapshot (wire v3), refreshed with the
	// schema monitor. Populated only for the master server's evaluation.
	Tables []StdioTable
}

// StdioTableColumn mirrors wire.TableColumn.
type StdioTableColumn struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Nullable  bool   `json:"nullable"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
}

// StdioTable mirrors wire.Table.
type StdioTable struct {
	Schema     string             `json:"schema"`
	Name       string             `json:"name"`
	Engine     string             `json:"engine"`
	RowFormat  string             `json:"row_format"`
	Rows       int64              `json:"rows,omitempty"`
	DataLength int64              `json:"data_length,omitempty"`
	Columns    []StdioTableColumn `json:"columns,omitempty"`
}

// ClusterContext carries cluster-level facts passed to every plugin.
type ClusterContext struct {
	HasProxies       bool              `json:"has_proxies"`        // cluster has at least one proxy configured
	BackupEncrypted  bool              `json:"backup_encrypted"`   // backup configured with encryption (e.g. restic password set)
	ConfigClearPwd   bool              `json:"config_clear_pwd"`   // repman detected cleartext passwords in TOML config
	HistoryClearPwd  bool              `json:"history_clear_pwd"`  // previous binlog scan found cleartext passwords
	DockerDeployment bool              `json:"docker_deployment"`  // servers run as Docker containers (DNS-based discovery, dynamic IPs)
	// ToolVersions maps tool/product names to their version strings.
	// Plugins can use this to check advisory version ranges for non-database
	// products (repman itself, proxies, backup tools, etc.).
	// Keys: "repman", "mariadb", "mysql", "proxysql", "maxscale", "haproxy", "restic", etc.
	ToolVersions     map[string]string `json:"tool_versions,omitempty"`
}

// IsEnabled returns false only when config explicitly sets enabled=false/0/no.
func (src LogSource) IsEnabled() bool {
	return ConfigBool(src.Config, "enabled", true)
}

// HasGraphite returns true when a graphite API endpoint is configured.
func (src LogSource) HasGraphite() bool {
	return src.GraphiteAPIURL != "" && src.GraphiteHostname != ""
}

// ErrKeyMissingMonitoringFeed is raised when a plugin is enabled but the
// monitoring feed it depends on (e.g. monitoring-binlog-events) is disabled.
const ErrKeyMissingMonitoringFeed = "WARN0312"

// ErrKeyNoExternalPlugins is raised when log-plugin=true but no external plugins
// are installed for the cluster.  It points operators to the plugin marketplace.
const ErrKeyNoExternalPlugins = "WARN0313"

// ErrKeyLogPluginDisabled is raised when plugin binaries are present in the
// cluster plugin directory but log-plugin=false.  It means plugins were
// delivered by the portal but the operator has not yet enabled evaluation.
// The advisory includes a direct API link to toggle log-plugin on.
const ErrKeyLogPluginDisabled = "WARN0314"

// ErrKeyMoreWorkloadPlugins is an informational nudge raised on the workload
// state machine when log-plugin=true but no external plugins are loaded.
// Built-in workload plugins are already running; this advisory signals that
// additional workload analysis plugins are available after signing up at
// gitlab.signal18.io.  Uses ErrType "INFO" — not a warning, just an upsell.
const ErrKeyMoreWorkloadPlugins = "INFO0315"

// Prerequisite declares that a plugin requires a named monitoring feature to
// be active in order to produce findings.
type Prerequisite struct {
	// ConfigKey is the monitoring flag name as it appears in the TOML /
	// CLI, e.g. "monitoring-binlog-events".
	ConfigKey string
	// Description is a human-readable explanation embedded in the WARN0312
	// finding, e.g. "binlog QUERY event scanning must be enabled".
	Description string
}

// LogPluginWithPrerequisites is an optional extension of LogPlugin.
// Plugins that depend on a specific monitoring feed implement this interface
// so the RunLogPlugins orchestrator can emit WARN0312 when the feed is off,
// rather than silently producing no findings.
type LogPluginWithPrerequisites interface {
	LogPlugin
	Prerequisites() []Prerequisite
}

// LogPluginWithDefaultSeverity is an optional extension of LogPlugin.
// Pure security plugins (hardening, local-infile, no-password-user, weak-auth)
// implement this so RunLogPlugins can route debug messages to the correct
// dedicated log even when Evaluate returns zero findings (compliant server).
// Score plugins do not need this — they always return ScoreChecks.
type LogPluginWithDefaultSeverity interface {
	LogPlugin
	DefaultSeverity() Severity
}

// PluginManifest describes a plugin's metadata, config schema, and prerequisites
// in a format suitable for the frontend. External plugins load this from a
// .manifest.json sidecar; built-in plugins may implement LogPluginWithManifest.
type PluginManifest struct {
	Description   string                `json:"description"`
	Tier          string                `json:"tier,omitempty"` // "free", "pro", "enterprise" — empty defaults to "free"
	Prerequisites []ManifestPrereq      `json:"prerequisites"`
	ConfigKeys    []ManifestConfigKey   `json:"config_keys"`
}

type ManifestPrereq struct {
	ConfigKey   string `json:"config_key"`
	Description string `json:"description"`
}

type ManifestConfigKey struct {
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Type    string          `json:"type"` // int, float, text, bool, enum
	Default string          `json:"default"`
	Help    string          `json:"help,omitempty"`
	Min     *float64        `json:"min,omitempty"`
	Max     *float64        `json:"max,omitempty"`
	Step    *float64        `json:"step,omitempty"`
	Options []ManifestEnumOption `json:"options,omitempty"`
}

type ManifestEnumOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// LogPluginWithManifest is an optional extension of LogPlugin.
// Plugins that carry a manifest allow the API to serve config schema
// and descriptions to the frontend without hardcoding.
type LogPluginWithManifest interface {
	LogPlugin
	Manifest() *PluginManifest
}

// LogPlugin is the interface every log-tailer plugin must implement.
type LogPlugin interface {
	Name() string
	Evaluate(src LogSource) EvaluateResult
}

// MySQL error log severity levels and their numeric weights.
// Higher weight = more severe. Used by the errorlog plugin's min-log-level filter.
//
// MySQL/MariaDB levels in order of severity:
//   System   (1) — startup/shutdown messages, always logged
//   Note     (2) — informational, e.g. "InnoDB: initializing..."
//   Warning  (3) — potentially problematic conditions
//   ERROR    (4) — errors that caused an operation to fail
//
// Setting min-log-level = "Warning" counts Warning + ERROR (weight >= 3).
// Setting min-log-level = "Note"    counts Note + Warning + ERROR (weight >= 2).
// Setting min-log-level = "ERROR"   counts only ERROR (weight >= 4).
var logLevelWeights = map[string]int{
	"SYSTEM":  1,
	"NOTE":    2,
	"WARNING": 3,
	"WARN":    3, // alias
	"ERROR":   4,
	"ERR":     4, // alias
}

// LogLevelWeight returns the numeric severity weight for a MySQL error log
// level string, or 0 if unrecognised (will be filtered out by default).
func LogLevelWeight(level string) int {
	if w, ok := logLevelWeights[strings.ToUpper(strings.TrimSpace(level))]; ok {
		return w
	}
	return 0
}

// MinLogLevelWeight parses a min-log-level config string into its numeric
// weight.  Returns weight for "Warning" (3) as the default if unrecognised.
func MinLogLevelWeight(cfg map[string]string) int {
	level := ConfigStr(cfg, "min-log-level", "Warning")
	if w, ok := logLevelWeights[strings.ToUpper(strings.TrimSpace(level))]; ok {
		return w
	}
	return logLevelWeights["WARNING"]
}

// Registry holds all registered plugins.
type Registry struct {
	plugins []LogPlugin
}

// GlobalRegistry is the process-wide registry seeded by init() functions in each
// built-in plugin file.  It must only contain built-in (in-process) plugins.
// External (subprocess) plugins must be loaded into per-cluster registries via
// NewRegistry() so that one cluster's plugins do not pollute another's monitoring loop.
var GlobalRegistry = &Registry{}

func Register(p LogPlugin) {
	GlobalRegistry.plugins = append(GlobalRegistry.plugins, p)
}

func (r *Registry) All() []LogPlugin {
	out := make([]LogPlugin, len(r.plugins))
	copy(out, r.plugins)
	return out
}

// ExternalCount returns the number of external (subprocess) plugins in the registry.
// A zero count means no plugins have been loaded from disk for this cluster.
func (r *Registry) ExternalCount() int {
	n := 0
	for _, p := range r.plugins {
		if _, ok := p.(*ExternalLogPlugin); ok {
			n++
		}
	}
	return n
}

// NewRegistry returns a new per-cluster registry pre-seeded with the built-in
// plugins from GlobalRegistry.  External plugins are then added to this registry
// by LoadPluginsFromDir, keeping each cluster's set of external plugins isolated.
func NewRegistry() *Registry {
	reg := &Registry{}
	reg.plugins = make([]LogPlugin, len(GlobalRegistry.plugins))
	copy(reg.plugins, GlobalRegistry.plugins)
	return reg
}

// ---- Config helpers ---------------------------------------------------------

func ConfigStr(cfg map[string]string, key, defaultVal string) string {
	if cfg == nil {
		return defaultVal
	}
	if v, ok := cfg[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

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

func ConfigFloat(cfg map[string]string, key string, defaultVal float64) float64 {
	s := ConfigStr(cfg, key, "")
	if s == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return defaultVal
	}
	return f
}

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

// ---- Graphite helpers -------------------------------------------------------

// graphiteDatapoint is one [value, timestamp] pair from the render API.
type graphiteDatapoint struct {
	Value *float64
	Time  int64
}

// graphiteSeriesResponse is one target from the render JSON response.
type graphiteSeriesResponse struct {
	Target     string
	Datapoints []graphiteDatapoint
}

// UnmarshalJSON handles [[null|float, int], ...] graphite format.
func (d *graphiteDatapoint) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var ts int64
	if err := json.Unmarshal(raw[1], &ts); err != nil {
		return err
	}
	d.Time = ts

	var v float64
	if err := json.Unmarshal(raw[0], &v); err == nil {
		d.Value = &v
	}
	return nil
}

func (r *graphiteSeriesResponse) UnmarshalJSON(b []byte) error {
	var tmp struct {
		Target     string              `json:"target"`
		Datapoints []graphiteDatapoint `json:"datapoints"`
	}
	if err := json.Unmarshal(b, &tmp); err != nil {
		return err
	}
	r.Target = tmp.Target
	r.Datapoints = tmp.Datapoints
	return nil
}

// graphiteHTTPClient is shared by FetchGraphiteMetric and correlateGraphiteSpike.
// A 10-second timeout prevents the monitoring loop from blocking indefinitely
// when the Graphite host is unreachable or slow to respond.
var graphiteHTTPClient = &http.Client{Timeout: 10 * time.Second}

// FetchGraphiteMetric queries the graphite render API and returns datapoints.
// from/until use graphite relative syntax: "-7d", "-1h", "now".
func FetchGraphiteMetric(apiURL, target, from, until string) ([]graphiteDatapoint, error) {
	u, err := url.Parse(apiURL + "/render/")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("target", target)
	q.Set("from", from)
	q.Set("until", until)
	q.Set("format", "json")
	q.Set("noCache", "1")
	u.RawQuery = q.Encode()

	resp, err := graphiteHTTPClient.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("graphite fetch error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var series []graphiteSeriesResponse
	if err := json.Unmarshal(body, &series); err != nil {
		return nil, fmt.Errorf("graphite parse error: %w", err)
	}
	if len(series) == 0 {
		return nil, nil
	}
	return series[0].Datapoints, nil
}

// ---- Dynamic spike detection ------------------------------------------------

// Granularity describes a time-of-X aware baseline window.
type Granularity struct {
	Name        string        // "minute", "hour", "day", "week"
	FetchFrom   string        // graphite from= parameter (history to fetch)
	BucketSize  time.Duration // size of one bucket (1h, 1d, 1w)
	MinBaseline int           // minimum non-null baseline points required
}

// defaultGranularities is the ordered list used for spike detection.
// We try from finest to coarsest — report the first significant hit.
var defaultGranularities = []Granularity{
	{Name: "minute", FetchFrom: "-2h",  BucketSize: time.Minute,      MinBaseline: 10},
	{Name: "hour",   FetchFrom: "-7d",  BucketSize: time.Hour,        MinBaseline: 5},
	{Name: "day",    FetchFrom: "-28d", BucketSize: 24 * time.Hour,   MinBaseline: 3},
	{Name: "week",   FetchFrom: "-52w", BucketSize: 7 * 24 * time.Hour, MinBaseline: 3},
}

// SpikeResult describes a detected spike across one or more granularities.
type SpikeResult struct {
	// SpikeTime is when the spike was measured (timestamp of the current data point).
	SpikeTime      time.Time
	// Granularity is the finest granularity at which the spike was detected.
	Granularity    string
	// BaselineWindow describes the history window used for the baseline,
	// e.g. "same hour-of-day over the last 7 days".
	BaselineWindow string
	CurrentValue   float64
	BaselineMean   float64
	BaselineStddev float64
	Sigma          float64
	// AllGranularities lists every granularity where a spike was detected,
	// with the spike timestamp formatted for that granularity so users know
	// exactly which time window to investigate in the logs.
	AllGranularities []GranularitySpike
	// CorrelatedMetrics are other mysql.* metrics also spiking at the same time.
	CorrelatedMetrics []string
}

// GranularitySpike is one granularity-level detection result.
type GranularitySpike struct {
	Name           string    // "minute", "hour", "day", "week"
	SpikeTime      time.Time // exact timestamp of the spike data point
	// TimeWindow is the human-readable time range to look at in the logs,
	// e.g. "2026-03-26 14:00 – 2026-03-26 15:00" for an hourly spike.
	TimeWindow     string
	CurrentValue   float64
	BaselineMean   float64
	BaselineStddev float64
	Sigma          float64
}

// granularityWindowStr formats a human-readable investigation window
// for the given granularity bucket ending at ts.
func granularityWindowStr(name string, ts time.Time, bucket time.Duration) string {
	start := ts.Truncate(bucket)
	end := start.Add(bucket)
	switch name {
	case "minute":
		return fmt.Sprintf("%s – %s (1 min window)",
			start.Format("2006-01-02 15:04"),
			end.Format("15:04"))
	case "hour":
		return fmt.Sprintf("%s – %s (1 hour window)",
			start.Format("2006-01-02 15:00"),
			end.Format("15:00"))
	case "day":
		return fmt.Sprintf("%s (full day)",
			start.Format("2006-01-02 Mon"))
	case "week":
		return fmt.Sprintf("week of %s – %s",
			start.Format("2006-01-02"),
			end.Format("2006-01-02"))
	}
	return ts.Format(time.RFC3339)
}

// granularityBaselineDesc returns a human description of the baseline window.
func granularityBaselineDesc(name, fetchFrom string) string {
	switch name {
	case "minute":
		return "same minute-of-hour, last 2 hours"
	case "hour":
		return "same hour-of-day, last 7 days"
	case "day":
		return "same day-of-week, last 4 weeks"
	case "week":
		return "same week-of-month, last 52 weeks"
	}
	return fetchFrom
}

// DetectSpike fetches the given graphite metric and checks whether the most
// recent value is a statistical outlier relative to a time-aware baseline.
// It checks ALL granularities and returns a SpikeResult that lists every
// granularity where a spike was detected, so users can see which time frames
// are anomalous.  Returns nil when no spike is detected at any granularity
// or when there is insufficient history at every level.
//
// cache may be nil.  When the cache is fresh (< SpikeCheckInterval old) the
// previous result is returned immediately without querying graphite, preventing
// a flood of HTTP calls on every 5-second monitoring tick.
// The caller is responsible for updating the cache after a non-nil return.
func DetectSpike(apiURL, metricName string, sigma float64, correlatePrefix string, cache *SpikeCache) (*SpikeResult, error) {
	// Return cached result if still fresh — avoids graphite HTTP every tick
	if cache != nil && cache.IsFresh() {
		return cache.Result, nil
	}
	// Mark the check time immediately so concurrent calls (multiple servers)
	// don't pile up. We update Result after computation below.
	if cache != nil {
		cache.CheckedAt = time.Now()
	}
	var allSpikes []GranularitySpike
	var finest *GranularitySpike // finest granularity that fired

	for _, g := range defaultGranularities {
		points, err := FetchGraphiteMetric(apiURL, metricName, g.FetchFrom, "now")
		if err != nil || len(points) < g.MinBaseline+1 {
			continue
		}

		current, currentTS := lastNonNull(points)
		if current == nil {
			continue
		}

		baseline := sameTimeBucket(points[:len(points)-1], currentTS, g.BucketSize)
		if len(baseline) < g.MinBaseline {
			continue
		}

		mean, stddev := meanStddev(baseline)
		if stddev < 0.01 {
			continue
		}

		s := (*current - mean) / stddev
		if s < sigma {
			continue
		}

		gs := GranularitySpike{
			Name:           g.Name,
			SpikeTime:      currentTS,
			TimeWindow:     granularityWindowStr(g.Name, currentTS, g.BucketSize),
			CurrentValue:   *current,
			BaselineMean:   mean,
			BaselineStddev: stddev,
			Sigma:          s,
		}
		allSpikes = append(allSpikes, gs)
		if finest == nil {
			finest = &gs // first = finest granularity (list is ordered finest→coarsest)
		}
	}

	if len(allSpikes) == 0 {
		if cache != nil {
			cache.CheckedAt = time.Now()
			// Only clear the cached spike result once the hold period has expired.
			// While the hold is active, srv_log_plugins.go calls PreserveState so
			// WARN0205 stays in CurState without RESOLV/OPEN churn.
			if !cache.IsHeld() {
				cache.Result = nil
				cache.MetricName = ""
				cache.OpenedAt = time.Time{}
			}
		}
		return nil, nil
	}

	// A spike was detected.

	result := &SpikeResult{
		SpikeTime:        finest.SpikeTime,
		Granularity:      finest.Name,
		BaselineWindow:   granularityBaselineDesc(finest.Name, ""),
		CurrentValue:     finest.CurrentValue,
		BaselineMean:     finest.BaselineMean,
		BaselineStddev:   finest.BaselineStddev,
		Sigma:            finest.Sigma,
		AllGranularities: allSpikes,
	}

	if correlatePrefix != "" && apiURL != "" {
		result.CorrelatedMetrics = correlateGraphiteSpike(
			apiURL, correlatePrefix, defaultGranularities[0].FetchFrom, finest.SpikeTime, sigma,
			metricName, // exclude the analyzed metric from correlation results
		)
	}

	if cache != nil {
		cache.Result = result
		cache.MetricName = metricName
		cache.CheckedAt = time.Now()
		// Set OpenedAt only on the first detection so the hold period starts
		// from when the spike was first seen, not from each re-check.
		if cache.OpenedAt.IsZero() {
			cache.OpenedAt = time.Now()
		}
	}
	return result, nil
}

// lastNonNull returns the last non-null value and its timestamp.
func lastNonNull(points []graphiteDatapoint) (*float64, time.Time) {
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Value != nil {
			t := time.Unix(points[i].Time, 0)
			return points[i].Value, t
		}
	}
	return nil, time.Time{}
}

// sameTimeBucket returns values from points that fall in the same
// "bucket position" as ts — e.g. same hour-of-day for hourly buckets.
func sameTimeBucket(points []graphiteDatapoint, ts time.Time, bucket time.Duration) []float64 {
	var out []float64
	for _, p := range points {
		if p.Value == nil {
			continue
		}
		pt := time.Unix(p.Time, 0)
		if sameBucketPosition(pt, ts, bucket) {
			out = append(out, *p.Value)
		}
	}
	return out
}

func sameBucketPosition(a, b time.Time, bucket time.Duration) bool {
	switch bucket {
	case time.Minute:
		return a.Minute() == b.Minute()
	case time.Hour:
		return a.Hour() == b.Hour()
	case 24 * time.Hour:
		return a.Weekday() == b.Weekday()
	case 7 * 24 * time.Hour:
		// same week-of-month
		_, aw := a.ISOWeek()
		_, bw := b.ISOWeek()
		return aw%4 == bw%4
	}
	return false
}

func meanStddev(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	variance := 0.0
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	return mean, math.Sqrt(variance / float64(len(vals)))
}

// FormatSpikeDescription builds the human-readable finding description for a
// spike, listing every granularity that fired with its exact investigation
// time window.  Example output:
//
//	Spike on db1:3306 (metric: mysql.db1.plugin_slowlog_count)
//	  minute  3.4σ  current=45 mean=8.2±3.1  → investigate: 2026-03-26 14:23 – 14:24
//	  hour    2.8σ  current=45 mean=12.1±5.6  → investigate: 2026-03-26 14:00 – 15:00
//	  Baseline: same minute-of-hour, last 2 hours
//	  Correlated: mysql.db1.mysql_global_status_com_select, mysql.db1.mysql_global_status_threads_running
func FormatSpikeDescription(serverURL, metricName string, spike *SpikeResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Spike on %s [%s]", serverURL, metricName))
	for _, g := range spike.AllGranularities {
		sb.WriteString(fmt.Sprintf(
			" | %s %.1fσ (cur=%.0f avg=%.1f±%.1f) → %s",
			g.Name, g.Sigma, g.CurrentValue, g.BaselineMean, g.BaselineStddev,
			g.TimeWindow,
		))
	}
	if spike.BaselineWindow != "" {
		sb.WriteString(fmt.Sprintf(" [baseline: %s]", spike.BaselineWindow))
	}
	if len(spike.CorrelatedMetrics) > 0 {
		sb.WriteString(" [correlated: " + strings.Join(spike.CorrelatedMetrics, ", ") + "]")
	}
	return sb.String()
}

// correlateGraphiteSpike finds other metrics under prefix that also spike
// at the same timestamp using a simpler z-score check.
// excludeMetric is the metric being analyzed — it is excluded from results
// to prevent a metric from being listed as correlated with itself.
func correlateGraphiteSpike(apiURL, prefix, from string, spikeTime time.Time, sigma float64, excludeMetric string) []string {
	// Fetch metric names under prefix
	u, _ := url.Parse(apiURL + "/metrics/find/")
	q := u.Query()
	q.Set("query", prefix+".*")
	q.Set("format", "treejson")
	u.RawQuery = q.Encode()

	resp, err := graphiteHTTPClient.Get(u.String())
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var found []struct {
		ID   string `json:"id"`
		Leaf int    `json:"leaf"`
	}
	if err := json.Unmarshal(body, &found); err != nil {
		return nil
	}

	var correlated []string
	for _, m := range found {
		if m.Leaf != 1 {
			continue
		}
		// Never report the analyzed metric as correlated with itself
		if m.ID == excludeMetric {
			continue
		}
		points, err := FetchGraphiteMetric(apiURL, m.ID, from, "now")
		if err != nil || len(points) < 5 {
			continue
		}
		// Simple check: does the value at spikeTime deviate from mean by sigma?
		var vals []float64
		var spikeVal *float64
		for _, p := range points {
			if p.Value == nil {
				continue
			}
			pt := time.Unix(p.Time, 0)
			if math.Abs(pt.Sub(spikeTime).Seconds()) < 120 {
				v := *p.Value
				spikeVal = &v
			} else {
				vals = append(vals, *p.Value)
			}
		}
		if spikeVal == nil || len(vals) < 3 {
			continue
		}
		mean, stddev := meanStddev(vals)
		if stddev > 0.01 && (*spikeVal-mean)/stddev >= sigma {
			correlated = append(correlated, m.ID)
		}
		if len(correlated) >= 5 { // cap at 5 correlated metrics
			break
		}
	}
	return correlated
}
