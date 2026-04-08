# Log Plugin Developer Guide

This document describes how to write, build, and deploy external log plugins for
replication-manager. It covers the wire protocol, the `LogPlugin` interface,
configuration, signing, and distribution.

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

The fastest way to write a plugin is to copy the example:

```
cluster/logplugin/example/plugin-innodb-corruption/main.go
```

It demonstrates the complete lifecycle: decode the JSON request from stdin,
evaluate log entries, and write the JSON response to stdout.

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

### Request

Defined in `cluster/logplugin/plugins/wire/wire.go`:

```go
type Request struct {
    ServerURL        string     `json:"server_url"`
    GraphiteAPIURL   string     `json:"graphite_api_url"`
    GraphiteHostname string     `json:"graphite_hostname"`

    ErrorLog      []Msg      `json:"error_log"`
    SqlErrorLog   []Msg      `json:"sql_error_log"`
    SlowLog       []SlowMsg  `json:"slow_log"`
    AuditLog      []Msg      `json:"audit_log"`

    PFSQueries    []PFSQuery `json:"pfs_queries"`
    ProcessList   []Process  `json:"process_list"`
    MetaDataLocks []MDL      `json:"metadata_locks"`
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
| `process_list` | `monitoring-processlist = true` | Current `INFORMATION_SCHEMA.PROCESSLIST` |
| `metadata_locks` | `METADATA_LOCK_INFO` plugin installed | MDL wait snapshot |

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
    PlanFullScan  string  `json:"plan_full_scan"` // "YES" or "NO"
    PlanTmpDisk   int64   `json:"plan_tmp_disk"`
    PlanTmpMem    int64   `json:"plan_tmp_mem"`
    LastSeen      string  `json:"last_seen"`
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

### Response

```go
type Response struct {
    Findings []Finding `json:"findings"`
}

type Finding struct {
    ErrKey      string `json:"err_key"`   // e.g. "WARN0300"
    Severity    string `json:"severity"`  // "WARNING" or "ERROR"
    Description string `json:"description"`
}
```

Return an empty `findings` array (or omit the field entirely) when no issue is
detected. Return a non-zero exit code only for fatal plugin errors — not for
"no findings".

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

    // --- analysis ---
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

See `cluster/logplugin/plugins/plugin-innodb-corruption/main.go` for a complete
example.

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

Copy the binary to the per-cluster plugins directory and make it executable:

```bash
install -m 0755 plugin-mycheck /var/lib/replication-manager/<cluster-name>/plugins/
```

Replication-manager hot-reloads plugins at runtime via `ReloadLogPlugins()`.
No restart is required. If a plugin with the same name is already registered it
is replaced in place.

The canonical path is:

```
<Conf.WorkingDir>/<cluster-name>/plugins/plugin-<name>
```

---

## Configuration

Per-plugin configuration is read from the cluster config file under a section
named `[<cluster>.plugin-config.<plugin-name>]`:

```toml
[mycluster.plugin-config.plugin-mycheck]
enabled        = true
threshold      = 5
time-window-h  = 24
```

The configuration map is passed to **built-in** plugins via `LogSource.Config`.
For **external** plugins the map is currently not forwarded via the wire
protocol. External plugins should read their settings from environment variables
or embed sensible defaults.

The `enabled` key is honoured for all built-in plugins. Setting it to `false`,
`0`, or `no` prevents the plugin from being called on each tick. External
plugins do not currently receive `enabled` via the wire protocol but the loader
will skip calling them if the key resolves to false in the server's config
lookup (future work).

---

## Error Key Assignment

Error keys follow the pattern `WARN<NNNN>`. The ranges in use are:

| Range | Owner |
|-------|-------|
| WARN0201–WARN0209 | Built-in log plugins |
| WARN0203 | Reserved — plugin execution error (timeout, bad JSON, non-zero exit) |
| WARN0300–WARN0399 | Official external plugins (Signal18) |
| WARN0400+ | Custom / third-party plugins |

Choose a key in the WARN0400+ range for custom plugins to avoid collisions.

---

## Signing and Distribution (CI)

Official plugins are signed and distributed via the signer repository:

```
https://github.com/signal18/replication-manager-plugin-signer
```

The repository layout maps repman releases to wire protocol versions:

```
replication-manager-plugin-signer/
├── plugin-signing.key          (private — CI only)
├── plugin-signing.pub          (public key deployed to repman servers)
├── wire1/                      (binaries for wire protocol v1)
│   ├── plugin-connection-storm
│   ├── plugin-connection-storm.sig
│   └── ...
└── 3.2.1 -> wire1              (symlink: repman version → wire dir)
```

The wire version is read directly from source:

```go
// cluster/logplugin/plugins/wire/wire.go
WireVersion = 1
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

### Official External

| Binary | ErrKey | Input | Detects |
|--------|--------|-------|---------|
| `plugin-innodb-corruption` | WARN0300 | ErrorLog | InnoDB corruption keywords (last 24h) |
| `plugin-slow-query-regression` | WARN0301 | SlowLog + PFSQueries | Query regressions vs baseline |
| `plugin-connection-storm` | WARN0307 | ProcessList | Connection pool saturation |
| `plugin-error-storm` | — | ErrorLog | Error rate bursts |
| `plugin-full-table-scan-spike` | — | PFSQueries | Full table scan spike |
| `plugin-metadata-lock-contention` | WARN0305 | MetaDataLocks | MDL wait accumulation |
| `plugin-off-hours-access` | — | AuditLog | Off-hours database access |
| `plugin-privilege-escalation` | — | AuditLog | Privilege escalation attempts |
| `plugin-replication-lag-predictor` | — | ProcessList + SlowLog | Replication lag prediction |
| `plugin-tmp-table-storm` | — | PFSQueries | Temporary table creation spikes |

---

## Relevant Source Files

| File | Purpose |
|------|---------|
| `cluster/logplugin/logplugin.go` | `LogPlugin` interface, `LogSource`, `EvaluateResult`, spike detection |
| `cluster/logplugin/external.go` | External binary loader, wire type definitions, 5s timeout |
| `cluster/logplugin/plugins/wire/wire.go` | Shared JSON wire types (import or copy) |
| `cluster/logplugin/example/plugin-innodb-corruption/main.go` | Minimal self-contained example |
| `cluster/logplugin/plugins/plugin-*/main.go` | Official plugin implementations |
| `cluster/srv_log_plugins.go` | Server integration: snapshot creation, tick loop, state injection |
| `Makefile` (lines 74–212) | Build, sign, and distribution targets |
