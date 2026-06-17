# Log Plugin Developer Guide

This document describes how to write, build, and deploy external log plugins for
replication-manager. It covers the wire protocol, the `LogPlugin` interface,
configuration, signing, manifests, and distribution.

---

## Overview

Replication-manager evaluates log data on every monitoring tick (default: every
5 seconds per server). The evaluation is delegated to *plugins* — small programs
that receive a snapshot of log buffers and performance data, analyse it, and
return findings.

There are two plugin types:

| Type | Where it lives | How it is invoked |
|------|---------------|-------------------|
| **Built-in** | Compiled into the repman binary (`cluster/logplugin/plugin_*.go`) | Called directly as Go functions via the `LogPlugin` interface |
| **External** | Standalone executable in `<WorkingDir>/plugins/` | Spawned as a child process; communicates via JSON on stdin/stdout |

This guide focuses on **external plugins**, which is the recommended path for
custom or third-party detection logic.

---

## Quick Start

The fastest way to write a plugin is to copy an existing one:

```
cluster/logplugin/plugins/plugin-innodb-corruption/main.go   # minimal — error log only
cluster/logplugin/plugins/plugin-workload-tags/main.go        # uses ServerStatus + ServerVariables
cluster/logplugin/plugins/plugin-workload-pfs-digest/main.go  # uses PFS + EXPLAIN + Graphite
```

Each demonstrates the complete lifecycle: decode the JSON request from stdin,
evaluate data, and write the JSON response to stdout.

---

## Wire Protocol

The wire protocol is **JSON over stdin/stdout**. On each monitoring tick the
server writes one JSON object to the plugin's stdin. The plugin writes one JSON
object to stdout and exits with code 0.

```
stdin  ← Request  (JSON, one object, server closes stdin after writing)
stdout → Response (JSON, one object)
exit 0  = evaluation complete (empty findings = all clear)
exit ≠0 = error    (repman logs WARN0203 and skips state injection)
```

The plugin has **5 seconds** to complete. If it exceeds this deadline the server
kills it and records a timeout finding (WARN0203).

The current wire version is **2** (`WireVersion = 2` in `wire.go`).

### Request

Defined in `cluster/logplugin/plugins/wire/wire.go`:

```go
type Request struct {
    ServerURL        string            `json:"server_url"`
    GraphiteAPIURL   string            `json:"graphite_api_url"`
    GraphiteHostname string            `json:"graphite_hostname"`

    ErrorLog      []Msg             `json:"error_log"`
    SqlErrorLog   []Msg             `json:"sql_error_log"`
    SlowLog       []SlowMsg         `json:"slow_log"`
    AuditLog      []Msg             `json:"audit_log"`

    PFSQueries       []PFSQuery     `json:"pfs_queries"`
    PFSExplainPlans  []PFSExplain   `json:"pfs_explain_plans,omitempty"`  // wire v2
    PFSExplainCount  int            `json:"pfs_explain_count"`            // wire v2
    PFSLastTruncate  string         `json:"pfs_last_truncate,omitempty"`  // wire v2, RFC3339

    ProcessList   []Process         `json:"process_list"`
    MetaDataLocks []MDL             `json:"metadata_locks"`
    BinlogEvents  []BinlogEvent     `json:"binlog_events"`

    ServerVersion   ServerVersion     `json:"server_version"`
    ServerVariables map[string]string `json:"server_variables"`
    ServerStatus    map[string]string `json:"server_status"`              // wire v2
    DatabaseUsers   []DBUser          `json:"database_users"`
    ClusterContext  ClusterContext    `json:"cluster_context"`
    PluginDataDir   string            `json:"plugin_data_dir"`

    Config          map[string]string `json:"config,omitempty"`
}
```

**Field reference**

| Field | Populated when | Content |
|-------|----------------|---------|
| `server_url` | Always | `host:port` of the monitored server |
| `graphite_api_url` | Graphite configured | Base URL of the render API, e.g. `http://127.0.0.1:10002` |
| `graphite_hostname` | Graphite configured | Hostname key used in metric names (dots → dashes) |
| `error_log` | Always | MySQL error log ring buffer |
| `sql_error_log` | Always | SQL error log ring buffer |
| `slow_log` | Slow log monitoring on | Slow query entries with full metrics |
| `audit_log` | MariaDB Audit plugin active | Audit log ring buffer |
| `pfs_queries` | `monitoring-pfs = true` | `events_statements_summary_by_digest` snapshot |
| `pfs_explain_plans` | `monitoring-performance-schema-queries = true` | Cached EXPLAIN plans for PFS digests (wire v2) |
| `pfs_explain_count` | `monitoring-performance-schema-queries = true` | Number of digests that have EXPLAIN plans (wire v2) |
| `pfs_last_truncate` | PFS truncate tracked | RFC3339 timestamp of last PFS digest truncation (wire v2) |
| `process_list` | `monitoring-processlist = true` | Current `INFORMATION_SCHEMA.PROCESSLIST` |
| `metadata_locks` | `METADATA_LOCK_INFO` plugin installed | MDL wait snapshot |
| `binlog_events` | `monitoring-binlog-events = true` | Recent binlog QUERY events |
| `server_version` | Always | Pre-parsed version (flavor, major, minor, release) |
| `server_variables` | Always | `SHOW GLOBAL VARIABLES` snapshot |
| `server_status` | Always | `SHOW GLOBAL STATUS` snapshot (wire v2) |
| `database_users` | Always | `mysql.user` snapshot (no credential hashes) |
| `cluster_context` | Always | Cluster-level facts (proxies, backup, Docker, tool versions) |
| `plugin_data_dir` | Always | Path to plugin sidecar data files |
| `config` | Per-plugin config set | Plugin-specific settings from cluster TOML / GUI |

#### Msg (error log, SQL error log, audit log)

```go
type Msg struct {
    Level     string `json:"level"`     // "ERROR", "WARNING", "NOTE", "SYSTEM"
    Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" or ISO 8601
    Text      string `json:"text"`
}
```

#### SlowMsg

```go
type SlowMsg struct {
    Timestamp     string             `json:"timestamp"`
    Query         string             `json:"query"`
    User          string             `json:"user"`
    Host          string             `json:"host"`
    Db            string             `json:"db"`
    TimeMetrics   map[string]float64 `json:"time_metrics"`   // query_time, lock_time, …
    NumberMetrics map[string]uint64  `json:"number_metrics"` // rows_sent, rows_examined, …
}
```

#### PFSQuery

```go
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
    SortRows      int64   `json:"sort_rows"`
    PlanFullScan  string  `json:"plan_full_scan"` // "YES" or "NO"
    PlanTmpDisk   int64   `json:"plan_tmp_disk"`
    PlanTmpMem    int64   `json:"plan_tmp_mem"`
    LastSeen      string  `json:"last_seen"`
}
```

#### PFSExplain (wire v2)

```go
type PFSExplainRow struct {
    Table string `json:"table"`
    Type  string `json:"type"`  // ALL, index, range, ref, eq_ref, const, system
    Key   string `json:"key"`
    Rows  string `json:"rows"`
    Extra string `json:"extra"`
}

type PFSExplain struct {
    Digest     string          `json:"digest"`
    DigestText string          `json:"digest_text"`
    ExecCount  int64           `json:"exec_count"`
    Plan       []PFSExplainRow `json:"plan"`
}
```

#### Process

```go
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
```

#### MDL (metadata lock)

```go
type MDL struct {
    ThreadID     uint64 `json:"thread_id"`
    LockMode     string `json:"lock_mode"`
    LockDuration string `json:"lock_duration"`
    LockTimeMs   int64  `json:"lock_time_ms"`
    LockType     string `json:"lock_type"`
    Schema       string `json:"schema"`
    Table        string `json:"table"`
}
```

#### BinlogEvent

```go
type BinlogEvent struct {
    Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" UTC
    Schema    string `json:"schema"`
    Query     string `json:"query"`
    ServerID  uint32 `json:"server_id"`
}
```

#### ServerVersion

```go
type ServerVersion struct {
    Flavor  string `json:"flavor"`  // "MariaDB", "MySQL", "Percona", "PostgreSQL"
    Major   int    `json:"major"`
    Minor   int    `json:"minor"`
    Release int    `json:"release"`
}
```

#### DBUser

```go
type DBUser struct {
    User          string `json:"user"`
    Host          string `json:"host"`
    Plugin        string `json:"plugin"`
    PasswordEmpty bool   `json:"password_empty"`
    AccountLocked bool   `json:"account_locked"`
}
```

#### ClusterContext

```go
type ClusterContext struct {
    HasProxies       bool              `json:"has_proxies"`
    BackupEncrypted  bool              `json:"backup_encrypted"`
    ConfigClearPwd   bool              `json:"config_clear_pwd"`
    HistoryClearPwd  bool              `json:"history_clear_pwd"`
    DockerDeployment bool              `json:"docker_deployment"`
    ToolVersions     map[string]string `json:"tool_versions,omitempty"`
}
```

### Response

```go
type Response struct {
    Findings    []Finding    `json:"findings"`
    ScoreChecks []ScoreCheck `json:"score_checks,omitempty"`
}

type Finding struct {
    ErrKey       string        `json:"err_key"`
    Severity     string        `json:"severity"`    // "WARNING", "ERROR", "SECURITY", or "WORKLOAD"
    Description  string        `json:"description"`
    Count        int64         `json:"count,omitempty"`   // wire v2 — occurrence count
    Total        int64         `json:"total,omitempty"`   // wire v2 — total for ratio computation
    Remediations []Remediation `json:"remediations,omitempty"`
}

type Remediation struct {
    Type        string `json:"type"`         // "sql", "my_cnf", or "repman_config"
    Description string `json:"description"`
    SQL         string `json:"sql,omitempty"`
    MyCnf       string `json:"my_cnf,omitempty"`
    ConfigKey   string `json:"config_key,omitempty"`
    ConfigValue string `json:"config_value,omitempty"`
    Risk        string `json:"risk"`         // "safe", "moderate", or "disruptive"
}

type ScoreCheck struct {
    Tag    string `json:"tag"`
    Pass   bool   `json:"pass"`
    Detail string `json:"detail,omitempty"`
}
```

Return an empty `findings` array (or omit the field entirely) when no issue is
detected. Return a non-zero exit code only for fatal plugin errors — not for
"no findings".

---

## Plugin Manifest

Every plugin should ship a `.manifest.json` sidecar file alongside its binary.
The manifest makes the plugin self-describing — the frontend renders config UI,
descriptions, and help text dynamically from the manifest without hardcoded
switch/case statements.

File naming: `<plugin-binary-name>.manifest.json` in the same directory as the
binary source.

### Manifest Schema

```json
{
  "description": "Markdown description shown in the plugin info modal",
  "tier": "free",
  "prerequisites": [
    {
      "config_key": "monitoring-performance-schema-queries",
      "description": "Human-readable explanation of why this prerequisite is needed"
    }
  ],
  "config_keys": [
    {
      "key": "timeframe-hours",
      "label": "Timeframe (hours)",
      "type": "int",
      "default": "1",
      "min": 1,
      "max": 1440,
      "step": 1,
      "help": "Markdown help text for the ? tooltip"
    }
  ]
}
```

### Field Reference

| Field | Required | Description |
|-------|----------|-------------|
| `description` | Yes | Markdown text shown in the plugin info modal. First line is the title. |
| `tier` | No | Commercial tier: `"free"`, `"pro"`, `"enterprise"`. Omit for free. |
| `prerequisites` | No | Array of monitoring features the plugin depends on. If a prerequisite config key is disabled, repman emits WARN0312 instead of calling the plugin. |
| `config_keys` | No | Array of configurable parameters. Empty array or omit for plugins with no settings. |

### Config Key Fields

| Field | Required | Description |
|-------|----------|-------------|
| `key` | Yes | Config key name (kebab-case). Matches `wire.CfgInt(req.Config, "key", default)` in plugin code. |
| `label` | Yes | Human-readable label shown in the settings UI. |
| `type` | Yes | One of: `"int"`, `"float"`, `"text"`, `"bool"`, `"enum"`. |
| `default` | Yes | Default value as a string. |
| `help` | No | Markdown tooltip text for the `?` icon. |
| `min` | No | Minimum value (int/float types only). |
| `max` | No | Maximum value (int/float types only). |
| `step` | No | Step increment (int/float types only). |
| `options` | No | Array of `{"value": "...", "label": "..."}` objects (enum type only). |

### Example — Plugin with Config Keys

```json
{
  "description": "Connection Storm Detector — WARN0307\n\nMonitors the processlist for connection saturation.",
  "prerequisites": [],
  "config_keys": [
    {
      "key": "sleep-ratio-threshold",
      "label": "Sleep ratio threshold",
      "type": "float",
      "default": "0.6",
      "min": 0.05,
      "max": 1.0,
      "step": 0.05,
      "help": "Fraction of total connections in Sleep state to fire."
    },
    {
      "key": "lock-wait-count",
      "label": "Lock-wait thread count",
      "type": "int",
      "default": "3",
      "min": 1,
      "max": 10000,
      "step": 1,
      "help": "Number of threads simultaneously waiting on a lock to fire."
    }
  ]
}
```

### Example — Plugin with No Config

```json
{
  "description": "InnoDB Corruption Detector — WARN0300\n\nScans the error log for InnoDB corruption indicators in the last 24 hours.",
  "prerequisites": [],
  "config_keys": []
}
```

### Example — Plugin with Enum Config

Built-in plugins can use enum types for dropdown selectors:

```json
{
  "key": "min-log-level",
  "label": "Min log level",
  "type": "enum",
  "default": "Warning",
  "options": [
    {"value": "System", "label": "System — startup/shutdown only"},
    {"value": "Note", "label": "Note — informational+"},
    {"value": "Warning", "label": "Warning — warnings + errors (default)"},
    {"value": "ERROR", "label": "ERROR — errors only"}
  ],
  "help": "Only log entries at or above this severity are evaluated."
}
```

### How Manifests Are Loaded

1. At plugin discovery time, `NewExternalLogPlugin()` looks for
   `<binary-path>.manifest.json` alongside the binary.
2. If found, the manifest is parsed and stored on the `ExternalLogPlugin`.
   Prerequisites from the manifest replace the legacy `.prerequisites.json`.
3. If no manifest is found, the loader falls back to
   `<binary-path>.prerequisites.json` for backward compatibility.
4. The `/api/clusters/{name}/plugins` endpoint includes the manifest in each
   plugin's JSON response.
5. The frontend renders all config controls, descriptions, and help text
   dynamically from the manifest — no hardcoded plugin names.

### Legacy `.prerequisites.json`

The `.prerequisites.json` sidecar format is still supported as a fallback:

```json
{
  "prerequisites": [
    {
      "config_key": "monitoring-performance-schema-queries",
      "description": "PFS query digest monitoring must be enabled"
    }
  ]
}
```

New plugins should use `.manifest.json` instead, which subsumes prerequisites
and adds description + config schema.

---

## Configuration

Per-plugin configuration is read from the cluster config under
`plugin-config.<plugin-name>`:

```toml
[mycluster.plugin-config.plugin-connection-storm]
enabled               = true
sleep-ratio-threshold = 0.7
min-connections       = 20
```

The config map is forwarded to external plugins via `req.Config` in the wire
protocol. Use the helper functions from the `wire` package to read values:

```go
import "github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"

threshold := wire.CfgFloat(req.Config, "sleep-ratio-threshold",
    wire.EnvFloat("REPMAN_CONNECTION_STORM_SLEEP_RATIO_THRESHOLD", 0.60))
```

The helpers try `req.Config[key]` first, then fall back to the environment
variable. This supports both TOML/GUI configuration and legacy deployments that
set `REPMAN_*` environment variables in systemd units.

Available helpers:

| Function | Type | Signature |
|----------|------|-----------|
| `wire.CfgInt` | int | `CfgInt(cfg, key, fallback) int` |
| `wire.CfgFloat` | float64 | `CfgFloat(cfg, key, fallback) float64` |
| `wire.CfgStr` | string | `CfgStr(cfg, key, fallback) string` |
| `wire.CfgBool` | bool | `CfgBool(cfg, key, fallback) bool` |
| `wire.EnvInt` | int | `EnvInt(envKey, default) int` — env var fallback |
| `wire.EnvFloat` | float64 | `EnvFloat(envKey, default) float64` |
| `wire.EnvStr` | string | `EnvStr(envKey, default) string` |

The `enabled` key is honoured for all plugins. Setting it to `false` prevents
the plugin from being called on each tick.

---

## Writing a Plugin

### Option A — copy the wire types

The simplest approach. No dependency on the repman module. Copy the wire types
directly into your `main.go`:

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    "strings"
)

type Request struct {
    ServerURL string   `json:"server_url"`
    ErrorLog  []Msg    `json:"error_log"`
    // add other fields as needed
}

type Msg struct {
    Level     string `json:"level"`
    Timestamp string `json:"timestamp"`
    Text      string `json:"text"`
}

type Response struct {
    Findings []Finding `json:"findings"`
}

type Finding struct {
    ErrKey      string `json:"err_key"`
    Severity    string `json:"severity"`
    Description string `json:"description"`
}

func main() {
    var req Request
    if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
        fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
        os.Exit(1)
    }

    count := 0
    for _, msg := range req.ErrorLog {
        if strings.Contains(msg.Text, "Out of memory") {
            count++
        }
    }

    resp := Response{}
    if count > 0 {
        resp.Findings = []Finding{{
            ErrKey:      "WARN0400",
            Severity:    "ERROR",
            Description: fmt.Sprintf("%s: %d OOM event(s) in error log", req.ServerURL, count),
        }}
    }

    json.NewEncoder(os.Stdout).Encode(resp)
}
```

### Option B — import the wire package

If your plugin lives inside the repman source tree you can import the shared
wire package:

```go
import "github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"

func main() {
    var req wire.Request
    json.NewDecoder(os.Stdin).Decode(&req)
    // ...
    json.NewEncoder(os.Stdout).Encode(wire.Response{...})
}
```

---

## Naming Convention

Binaries **must** be named `plugin-<name>` (no extension). The loader at
`cluster/logplugin/external.go` skips any file that does not match this prefix.
The name (e.g. `plugin-innodb-corruption`) becomes the plugin's identifier in
the registry and in log messages.

---

## Building

```bash
# Static binary, no CGO — required for distribution
CGO_ENABLED=0 go build -ldflags "-w -s" -o plugin-mycheck .

# Cross-compile for Linux/amd64 from any platform
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-w -s" -o plugin-mycheck .
```

If your plugin lives in `cluster/logplugin/plugins/plugin-mycheck/main.go` the
Makefile will build it automatically:

```bash
make plugins
# produces build/plugins/plugin-mycheck
```

The Makefile discovers plugins by scanning for `main.go` files under
`cluster/logplugin/plugins/`.

---

## Deployment

Copy the binary and its manifest to the per-cluster plugins directory:

```bash
install -m 0755 plugin-mycheck /var/lib/replication-manager/<cluster-name>/plugins/
cp plugin-mycheck.manifest.json /var/lib/replication-manager/<cluster-name>/plugins/
```

Replication-manager hot-reloads plugins at runtime via `ReloadLogPlugins()`.
No restart is required. If a plugin with the same name is already registered it
is replaced in place.

The canonical path is:

```
<Conf.WorkingDir>/<cluster-name>/plugins/plugin-<name>
<Conf.WorkingDir>/<cluster-name>/plugins/plugin-<name>.manifest.json
```

---

## Error Key Assignment

Error keys follow the pattern `WARN<NNNN>` or a severity-specific prefix. The ranges in use:

| Range | Owner |
|-------|-------|
| WARN0201–WARN0209 | Built-in log plugins |
| WARN0203 | Reserved — plugin execution error (timeout, bad JSON, non-zero exit) |
| WARN0300–WARN0311 | Official external plugins — workload/security detection |
| WARN0312 | Reserved — missing prerequisite (plugin needs a monitoring feed that is off) |
| WARN0400+ | Custom / third-party plugins |
| SEC0100–SEC0199 | Security audit plugins |
| ENT0001+ | Enterprise security advisories |
| RPL0001+ | Enterprise replication advisories |
| WRK0001+ | Enterprise workload advisories |
| WTAG0001–WTAG0099 | Workload feature tags (STATUS counters) |
| WTAG0100–WTAG0199 | Workload optimizer path detection |
| WTAG0200–WTAG0299 | PFS digest workload findings |

Choose a key in the WARN0400+ range for custom plugins to avoid collisions.

---

## Signing and Distribution (CI)

Official plugins are signed and distributed via the signer repository:

```
https://github.com/signal18/replication-manager-plugin-signer
```

The repository layout maps wire protocol versions to platform directories:

```
replication-manager-plugin-signer/
├── plugin-signing.key          (private — CI only)
├── plugin-signing.pub          (public key deployed to repman servers)
└── plugins/
    └── linux-amd64/
        └── wire-v2/
            ├── plugin-connection-storm
            ├── plugin-connection-storm.sig
            └── ...
```

The wire version is read directly from source:

```go
// cluster/logplugin/plugins/wire/wire.go
WireVersion = 2
```

Makefile targets:

```bash
make plugins       # build + sign + push (CI, requires PLUGIN_SIGNER_USER + TOKEN)
make plugin-sigs   # sign already-built binaries
make plugins-clean # remove build artifacts
```

For dev builds without credentials, a local keypair is generated automatically.

---

## Existing Plugins

### Built-in

| Name | ErrKey | Input | Detects |
|------|--------|-------|---------|
| `slowlog` | WARN0202 | SlowLog | Slow query rate spikes (Graphite baseline) |
| `errorlog` | WARN0201 | ErrorLog | Error log rate spikes |
| `sqlerrorlog` | WARN0209 | SqlErrorLog | SQL error rate spikes |
| `auditlog` | WARN0204 | AuditLog | New query template drift |
| `enterprise-security` | ENT0001+, CVE-* | ServerVersion, ToolVersions | CVEs from NVD + GitHub security issues |
| `enterprise-replication` | RPL0001+, CVE-* | ServerVersion, ToolVersions | Replication bugs: MDEV-20821, MDEV-28310, MDEV-19577 + NVD |
| `enterprise-workload` | WRK0001+, CVE-* | ServerVersion, ToolVersions | CRITICAL/HIGH crash, deadlock, memory leak bugs |
| `enterprise-compliance` | — | Various | Compliance scoring and audit trail analysis |

### Official External — Workload

| Binary | ErrKey | Input | Detects |
|--------|--------|-------|---------|
| `plugin-workload-tags` | WTAG0001–WTAG0199 | ServerStatus, ServerVariables | MariaDB feature usage tags (CTE, JSON, GIS, subqueries, triggers, etc.), handler ratios, optimizer switch analysis |
| `plugin-workload-pfs-digest` | WTAG0200–WTAG0299 | PFSQueries, PFSExplainPlans, Graphite | PFS digest coverage, EXPLAIN access type distribution, disk tmp table usage, sort row analysis |
| `plugin-innodb-corruption` | WARN0300 | ErrorLog | InnoDB corruption keywords (last 24h) |
| `plugin-slow-query-regression` | WARN0301 | SlowLog + PFSQueries | Query regressions vs baseline |
| `plugin-connection-storm` | WARN0307 | ProcessList | Connection pool saturation |
| `plugin-error-storm` | WARN0302 | ErrorLog | Error rate bursts |
| `plugin-full-table-scan-spike` | WARN0304 | PFSQueries | Full table scan spike |
| `plugin-metadata-lock-contention` | WARN0305 | MetaDataLocks | MDL wait accumulation |
| `plugin-replication-lag-predictor` | WARN0303 | ProcessList + SlowLog | Replication lag prediction |
| `plugin-tmp-table-storm` | WARN0306 | PFSQueries | Temporary table creation spikes |

### Official External — Security

| Binary | ErrKey | Input | Detects |
|--------|--------|-------|---------|
| `plugin-security-hardening` | SEC0103–SEC0118 | ServerVariables, DatabaseUsers, ClusterContext | CIS benchmark hardening checks |
| `plugin-security-no-password-user` | SEC0100 | DatabaseUsers | Accounts with empty authentication_string |
| `plugin-security-weak-auth` | SEC0101 | DatabaseUsers | Deprecated auth plugins (mysql_native_password) |
| `plugin-security-local-infile` | SEC0102 | ServerVariables | local_infile enabled |
| `plugin-binlog-cleartext-password` | WARN0310 | BinlogEvents | Cleartext passwords in binlog |
| `plugin-binlog-creditcard-leak` | WARN0311 | BinlogEvents | Credit card PANs in binlog (Luhn-validated) |
| `plugin-off-hours-access` | WARN0309 | AuditLog | Off-hours database access |
| `plugin-privilege-escalation` | WARN0308 | AuditLog | Unauthorized privilege changes |

### Official External — Score

| Binary | ScoreCheck tags | Input | Evaluates |
|--------|----------------|-------|-----------|
| `plugin-score-auth` | HasStrongAuth, HasStrongPwd | ServerVariables, DatabaseUsers | Authentication + password validation |
| `plugin-score-audit` | HasAudit | ServerVariables | Audit plugin loaded |
| `plugin-score-encryption` | HasEncryption | ServerVariables | InnoDB tablespace encryption |
| `plugin-score-lts` | HasLastLTS | ServerVersion | Server on active LTS release |
| `plugin-score-network` | HasNetwork | ServerVariables | Network security settings |
| `plugin-score-passwords` | NoEmptyPassword | DatabaseUsers | No empty-password accounts |
| `plugin-score-proxy` | HasProxy | ClusterContext | Proxy layer configured |
| `plugin-score-ssl` | HasSSL | ServerVariables | SSL/TLS enabled |

---

## Workload Plugin Details

### plugin-workload-tags

Detects which MariaDB/MySQL features are actively used by the workload using
`SHOW GLOBAL STATUS` counters and `SHOW GLOBAL VARIABLES` analysis.

**Input**: `ServerStatus`, `ServerVariables`

**Findings produced**:

| ErrKey | Category | Detection |
|--------|----------|-----------|
| WTAG0001–WTAG0017 | Feature usage | Feature_cte, feature_window_functions, feature_json, feature_dynamic_columns, feature_fulltext, feature_gis, feature_locale, feature_subquery, feature_timezone, feature_trigger, feature_xml, feature_system_versioning, feature_application_time_periods, feature_insert_returning, feature_into_outfile, feature_custom_aggregate_functions, feature_invisible_columns |
| WTAG0020 | Handler ratio | Heavy full-scan workload (handler_read_rnd_next / handler_read_key > 100) |
| WTAG0021 | Handler ratio | Write-intensive workload (write ratio > 50%) |
| WTAG0030–WTAG0032 | Feature usage | Prepared statements, XA transactions, stored procedures |
| WTAG0033 | Feature usage | Event scheduler enabled |
| WTAG0100–WTAG0118 | Optimizer | Optimizer switch flags: derived_merge, semijoin, subquery_cache, index_merge, ICP, MRR, hash join, rowid_filter, table_elimination, condition_pushdown, split_materialized, exists_to_in, etc. |
| WTAG0150–WTAG0153 | Optimizer problems | Joins without indexes, range check per row, multi-pass sorts, high disk temp table ratio |

**No configurable parameters.** Detection is purely status-counter based.

### plugin-workload-pfs-digest

Analyses `performance_schema.events_statements_summary_by_digest` and cached
EXPLAIN plans to produce workload coverage and optimization tags.

**Input**: `PFSQueries`, `PFSExplainPlans`, `PFSExplainCount`, `PFSLastTruncate`,
`GraphiteAPIURL`, `GraphiteHostname`

**Prerequisites**: `monitoring-performance-schema-queries = true`

**Findings produced**:

| ErrKey | Detection |
|--------|-----------|
| WTAG0200 | PFS digest coverage — ratio of PFS exec_count sum to Graphite total queries since last truncate |
| WTAG0201 | EXPLAIN coverage — percentage of digests with cached EXPLAIN plans |
| WTAG0210 | Full table scan (type=ALL) row cost percentage across all explained digests |
| WTAG0211 | Range scan (type=range/index) row cost percentage |
| WTAG0212 | Index lookup (type=ref/eq_ref/const) row cost percentage |
| WTAG0220 | On-disk temporary table usage from PFS digest stats |
| WTAG0230 | ICP (Using index condition) row cost from EXPLAIN Extra |
| WTAG0231 | Filesort (Using filesort) row cost |
| WTAG0232 | Temporary table (Using temporary) row cost |
| WTAG0233 | Covering index (Using index) row cost |
| WTAG0240 | Sorted rows across all digests |

The plugin makes HTTP requests to the Graphite API to compute the digest covering
ratio. If Graphite is not configured, WTAG0200 is skipped and row-based findings
still work from PFS data alone.

**No configurable parameters.**

---

## Relevant Source Files

| File | Purpose |
|------|---------|
| `cluster/logplugin/logplugin.go` | `LogPlugin` interface, `LogSource`, `EvaluateResult`, `PluginManifest` types |
| `cluster/logplugin/external.go` | External binary loader, manifest/prerequisites loading, 5s timeout |
| `cluster/logplugin/plugin_manifests.go` | Built-in plugin `Manifest()` implementations |
| `cluster/logplugin/plugins/wire/wire.go` | Shared JSON wire types v2 (import or copy) |
| `cluster/logplugin/plugins/plugin-*/main.go` | Official plugin implementations |
| `cluster/logplugin/plugins/plugin-*/*.manifest.json` | Plugin manifest sidecar files |
| `cluster/srv_log_plugins.go` | Server integration: snapshot creation, tick loop, state injection |
| `server/api_cluster.go` | `/api/clusters/{name}/plugins` endpoint (serves manifest) |
| `share/dashboard_react/src/Pages/Settings/PluginsSettings.jsx` | Dynamic manifest-driven settings UI |
| `Makefile` (plugins target) | Build, sign, and distribution targets |
