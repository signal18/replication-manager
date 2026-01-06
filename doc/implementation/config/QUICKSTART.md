# Phase 1 Quick Start Guide

## Quick Overview

Phase 1 introduces a refactored configuration system with three key improvements:

1. **Domain-specific configs** - Organized into logical groups instead of one giant struct
2. **Validation** - Catch config errors at startup with clear error messages
3. **Merge tracking** - Know exactly where each config value came from

## Installation

All new code is in the `config/` directory and doesn't affect existing functionality:

```bash
cd config/
ls -1 *_v2.go *.md
# config_v2.go         # New refactored Config struct
# config_v2_test.go    # Tests
# database.go          # Database domain config
# failover.go          # Failover domain config
# merge.go             # Merge precedence tracking
# monitoring.go        # Monitoring domain config
# replication.go       # Replication domain config
# validation.go        # Validation framework
# REFACTORING.md       # Detailed docs
# PHASE1_SUMMARY.md    # Summary
# QUICKSTART.md        # This file
```

## 5-Minute Tutorial

### 1. Create and Validate Configuration

```go
package main

import (
    "fmt"
    "log"
    "github.com/signal18/replication-manager/config"
)

func main() {
    // Create new config
    cfg := config.NewConfigV2()

    // Set monitoring values
    cfg.Monitoring.Ticker = 2
    cfg.Monitoring.QueryTimeout = 2000
    cfg.Monitoring.Address = "localhost"

    // Set database values
    cfg.Database.ConnectTimeout = 5
    cfg.Database.Credential = "root:password"
    cfg.Database.Hosts = "192.168.1.10,192.168.1.11"

    // Set replication
    cfg.Replication.MasterConnectRetry = 10
    cfg.Replication.Credential = "repl:replpass"

    // Validate everything
    if err := cfg.Validate(); err != nil {
        log.Fatalf("Config validation failed: %v", err)
    }

    fmt.Println("✅ Configuration is valid!")
}
```

### 2. Handle Validation Errors

```go
cfg := config.NewConfigV2()

// Set invalid value
cfg.Monitoring.Ticker = 100 // Must be 1-60

// Validate
if err := cfg.Validate(); err != nil {
    fmt.Println(err)
    // Output: config validation failed for 'monitoring-ticker':
    // must be between 1 and 60 seconds (got: 100)
}
```

### 3. Track Configuration Sources

```go
cfg := config.NewConfigV2()

// Simulate config loading from different sources
cfg.Tracker.Set("monitoring-ticker", 2, config.MergeSourceDefault, "default")
cfg.Tracker.Set("monitoring-ticker", 5, config.MergeSourceFile, "/etc/replication-manager/config.toml")
cfg.Tracker.Set("monitoring-ticker", 10, config.MergeSourceCommandLine, "--monitoring-ticker")

// See which value won
val, _ := cfg.Tracker.Get("monitoring-ticker")
fmt.Printf("Final value: %v\n", val)
// Output: Final value: 10

// Explain why
fmt.Println(cfg.Explain("monitoring-ticker"))
// Output: monitoring-ticker = 10 (source: command-line from --monitoring-ticker)
```

### 4. Enforce Server-Scoped Fields

```go
cfg := config.NewConfigV2()

// Set server-level value (marked with scope:"server" tag)
cfg.Tracker.Set("monitoring-address", "server-host", config.MergeSourceFile, "/etc/config.toml")

// Try to override in cluster config
clusterConfig := map[string]interface{}{
    "monitoring-address": "cluster-host", // This will fail!
    "monitoring-ticker":  5,               // This is OK
}

// Validate cluster config
err := cfg.ValidateClusterConfig("production", clusterConfig)
if err != nil {
    fmt.Println(err)
    // Output: cluster configuration validation failed:
    //   cluster 'production' attempted to override server-scoped field
    //   'monitoring-address' (server value: server-host, cluster value: cluster-host)
}
```

### 5. Prevent Topology Conflicts

```go
cfg := config.NewConfigV2()

// Try to enable multiple topologies (ERROR!)
cfg.Replication.MultiMaster = true
cfg.Replication.MultiMasterWsrep = true

// Validate catches the conflict
if err := cfg.Validate(); err != nil {
    fmt.Println(err)
    // Output: multiple replication topologies selected -
    // only one topology can be active
}
```

## Common Validation Errors

### Invalid Range
```
Error: config validation failed for 'monitoring-ticker':
       must be between 1 and 60 seconds (got: 100)

Fix: Set monitoring-ticker between 1 and 60
```

### Invalid Enum
```
Error: config validation failed for 'failover-mode':
       must be one of: manual, auto, sync (got: invalid)

Fix: Use one of the allowed values: manual, auto, or sync
```

### Topology Conflict
```
Error: config validation failed for 'replication-topology':
       multiple replication topologies selected (got: 2)

Fix: Enable only ONE of: multi-master, multi-master-ring,
     multi-master-wsrep, multi-master-grouprep, etc.
```

### Invalid Port
```
Error: config validation failed for 'replication-multi-master-wsrep-port':
       must be between 1024 and 65535 (got: 100)

Fix: Use a valid port number (1024-65535)
```

## Migration from Old Config

The old `Config` struct still works:

```go
// Old way (still works)
oldCfg := &config.Config{}
oldCfg.MonitoringTicker = 2

// New way (recommended)
newCfg := config.NewConfigV2()
newCfg.Monitoring.Ticker = 2
```

## Domain Organization

Configuration is now organized by domain:

```go
cfg := config.NewConfigV2()

// Monitoring domain
cfg.Monitoring.Ticker = 2
cfg.Monitoring.QueryTimeout = 2000
cfg.Monitoring.Capture = true

// Database domain
cfg.Database.ConnectTimeout = 5
cfg.Database.TLSSSLMode = "REQUIRED"

// Replication domain
cfg.Replication.MasterConnectRetry = 10
cfg.Replication.UseSSL = true

// Failover domain
cfg.Failover.Mode = "auto"
cfg.Failover.Limit = 5
```

## Debugging Configuration

### See all config sources
```go
for _, explanation := range cfg.ExplainAll() {
    fmt.Println(explanation)
}

// Output:
// monitoring-ticker = 10 (source: command-line from --monitoring-ticker) [scope:server]
// monitoring-address = localhost (source: file from /etc/config.toml) [scope:server]
// db-servers-connect-timeout = 5 (source: default from default)
// ...
```

### Understand merge precedence
```go
fmt.Println(config.MergePrecedenceDoc())

// Output:
// Configuration Merge Precedence (lowest to highest priority):
//
// 0. default - Default values from code
// 1. file - Main config file (config.toml [DEFAULT] section)
// 2. include - Include directory files (cluster.d/*.toml)
// 3. saved - Saved dynamic configuration (working-dir/cluster/cluster.toml)
// 4. git - Git repository pulled configuration
// 5. environment - Environment variables (REPMGR_*)
// 6. command-line - Command-line flags (--flag=value)
//
// Higher priority sources override lower priority sources.
// Fields marked with scope:"server" cannot be overridden by cluster configs.
```

## Testing Your Configuration

```go
func TestMyConfig(t *testing.T) {
    cfg := config.NewConfigV2()

    // Set your values
    cfg.Monitoring.Ticker = 2
    cfg.Database.ConnectTimeout = 5

    // Validate
    if err := cfg.Validate(); err != nil {
        t.Errorf("config validation failed: %v", err)
    }
}
```

## What's Validated?

### Monitoring
- ✅ Ticker: 1-60 seconds
- ✅ Query timeout: 100ms-300s
- ✅ Disk usage percentage: 0-100%
- ✅ Log lengths: 0-10000 entries

### Database
- ✅ Connect timeout: 1-300 seconds
- ✅ Exec timeout: 1-3600 seconds
- ✅ Read timeout: 1-86400 seconds
- ✅ SSL mode: DISABLED, PREFERRED, REQUIRED, VERIFY_CA, VERIFY_IDENTITY

### Replication
- ✅ Master connect retry: ≥1 second
- ✅ Wsrep port: 1024-65535
- ✅ Group replication port: 1024-65535
- ✅ SST method: mariabackup, xtrabackup-v2, rsync, mysqldump
- ✅ Only one topology enabled
- ✅ PostgreSQL topologies mutually exclusive

### Failover
- ✅ Mode: manual, auto, or sync
- ✅ Limits: non-negative
- ✅ False positive ping counter: 0-100
- ✅ External port: 1-65535

## Next Steps

1. Try the examples above
2. Read `REFACTORING.md` for detailed implementation info
3. Read `PHASE1_SUMMARY.md` for complete feature list
4. Wait for Phase 2 for server integration

## Questions?

- **Q: Can I use ConfigV2 now?**
  A: Yes! It's fully functional and tested. The server code still uses the old Config, but you can use ConfigV2 in your own code.

- **Q: Will my existing config files work?**
  A: Yes! ConfigV2 uses the same struct tags (mapstructure, toml, json) so it's compatible with existing TOML files.

- **Q: What if validation fails?**
  A: You'll get a clear error message telling you exactly what's wrong and how to fix it.

- **Q: How do I add more domains?**
  A: Follow the pattern in `monitoring.go`, `database.go`, etc. Create a new struct, add `Validate()` method, embed it in ConfigV2.

- **Q: Is this a breaking change?**
  A: No! The old Config struct is untouched. ConfigV2 is a parallel implementation.

## Summary

Phase 1 gives you:
- ✅ **Organized config** - No more 1000-field struct
- ✅ **Validation** - Catch errors early with clear messages
- ✅ **Debugging** - Know where each value came from
- ✅ **Safety** - Server-scoped fields enforced
- ✅ **Testing** - 25+ test cases included

All while maintaining 100% backward compatibility!
