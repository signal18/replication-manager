# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**replication-manager** is a high-availability orchestrator for MariaDB, MySQL, and Percona Server replication topologies. It handles monitoring, failover, switchover, proxy integration, provisioning, backups, and alerting for database clusters.

## Build System

The project uses Go build tags to create multiple binaries from the same codebase:

### Build Commands

```bash
# Build all binaries (CLI, server variants, tarball versions, arbitrator)
make all

# Build individual components
make cli          # CLI client: replication-manager-cli
make osc          # Open Source Community server (no provisioning)
make tst          # Testing server (with all features)
make pro          # Professional server (with OpenSVC)
make arb          # Arbitrator service
make emb          # Embedded server

# Build with React dashboard (required for pro/osc/emb)
make react        # Builds dashboard from share/dashboard_react/

# Create packages
make package      # Runs package_linux.sh (creates RPM/DEB packages)

# Clean build artifacts
make clean
```

### Build Tags

Three main build tags control which binary is produced:
- `server` → Main monitoring/orchestration server
- `clients` → CLI client for interacting with server
- `arbitrator` → Split-brain arbitrator service

### Feature Flags (Compile-Time)

Build-time flags control which features are compiled in (set via `-X` linker flags):
```
WithProvisioning, WithArbitration, WithProxysql, WithHaproxy, WithMaxscale,
WithMonitoring, WithMail, WithHttp, WithOpenSVC, WithTarball, WithEmbed, etc.
```

## Architecture

### Core Package Responsibilities

**server/** - Main server application
- `ReplicationManager` struct: Top-level orchestrator
- HTTP REST API (Gorilla mux + Negroni middleware)
- gRPC API (v3 protocol)
- Cobra command setup and flag parsing
- Multi-cluster management

**cluster/** - Core cluster monitoring and management
- `Cluster` struct: Represents a monitored database cluster
- `ServerMonitor` struct: Individual database server monitoring
- State machine for failure detection and remediation
- Monitoring loop (runs every tick, configurable via `monitoring-ticker`)
- Failover/switchover logic
- Proxy coordination

**clients/** - CLI client
- Remote API calls to server's REST endpoints
- Authentication and session management
- Cobra command definitions for user operations

**arbitrator/** - Arbitrator service
- Simple HTTP server for split-brain resolution
- SQLite or MySQL backend for state persistence
- Receives heartbeats and makes arbitration decisions

**config/** - Configuration management
- `Config` struct with 500+ configuration fields
- Viper-based TOML file parsing
- Support for encrypted values in config
- Per-cluster and global configuration scopes

**router/** - Proxy integrations
- `DatabaseProxy` interface (40+ methods)
- Implementations: MaxScale, ProxySQL, HAProxy, Spider, MyProxy
- Automatic backend updates on topology changes

**utils/** - Supporting utilities
- `dbhelper/`: Database operations (GTID, replication control)
- `backupmgr/`: Backup orchestration
- `alert/`: Email, Slack, Pushover, Teams notifications
- `s18log/`: Module-based logging system
- `crypto/`: Encryption for sensitive config values

**regtest/** - Regression testing
- 60+ test scenarios for failover, switchover, replication modes
- Test definitions in separate files (`test_*.go`)
- Framework for automated cluster testing

### Key Architectural Patterns

**Build Tag Separation**: Main entry points use build tags:
- `main_server.go` (`//go:build server`)
- `main_client.go` (`//go:build clients`)
- `main_arbitrator.go` (`//go:build arbitrator`)

**Cobra Command Structure**: Each package has its own `rootCmd` and subcommands:
- Server: `monitor`, `version`, `config-merge`
- Client: Various operational commands (status, switchover, failover, etc.)
- Arbitrator: `arbitrator`, `version`

**Configuration Scoping**:
- `scope:"server"` tag = immutable, server-wide settings
- Other settings = per-cluster, dynamically reloadable
- TOML sections: `[DEFAULT]` for globals, `[clustername]` for per-cluster

**State Machine Pattern**:
- `cluster.StateMachine` tracks cluster state
- Server states: `suspect`, `running`, `failed`, `maintenance`, etc.
- State changes trigger alerts and remediation

**Module-Based Logging**:
```go
cluster.LogModulePrintf(verbose, module, level, format, args...)
// module: config.ConstLogModProxy, config.ConstLogModGeneral, etc.
// level: "INFO", "WARN", "ERR", "DBG"
```

**Proxy Pattern**: `DatabaseProxy` interface with multiple implementations, allowing pluggable proxy backends

**Non-Blocking Channels**: Used for failover/switchover coordination:
```go
failoverCond   *nbc.NonBlockingChan
switchoverCond *nbc.NonBlockingChan
```

## Configuration Files

Configuration is loaded via Viper from TOML files in these locations:
1. `/etc/replication-manager/config.toml` (system-wide)
2. `/usr/local/replication-manager/etc/config.toml` (tarball installs)
3. `./config.toml` (current directory)
4. Custom path via `--config` flag

Structure:
```toml
[DEFAULT]
# Global settings

[cluster-name]
# Per-cluster settings
db-servers-hosts = "192.168.1.10,192.168.1.11,192.168.1.12"
```

## Testing

### Regression Tests

Run tests via the server with test mode enabled:
```bash
# The regtest package contains 60+ test scenarios
# Tests are defined in regtest/test_*.go files
# Each test modifies cluster state and verifies behavior
```

Tests cover:
- Failover scenarios (various replication modes)
- Switchover operations
- Proxy integration
- Backup/restore operations
- Replication lag handling
- Split-brain detection

### Unit Tests

```bash
# Standard Go tests (limited coverage)
go test ./cluster/...
go test ./utils/...
```

## Development Workflow

### Working with Server Code

The server initialization flow:
1. `main_server.go` → `server.Execute()`
2. Cobra parses flags and routes to commands
3. `server.InitConfig()` loads TOML via Viper
4. `StartCluster()` creates `cluster.Cluster` instances
5. `httpserver()` starts REST API
6. Each cluster runs `cluster.Monitor()` in a loop

### Adding New API Endpoints

1. Define handler in `server/api_*.go`:
   ```go
   func (repman *ReplicationManager) handlerNewEndpoint(w http.ResponseWriter, r *http.Request) {
       // Implementation
   }
   ```

2. Register route in `server/http.go`:
   ```go
   router.HandleFunc("/api/new-endpoint", repman.handlerNewEndpoint)
   ```

3. Add client command in `clients/client_cmd.go`:
   ```go
   var newCmd = &cobra.Command{
       Use: "new-command",
       Run: func(cmd *cobra.Command, args []string) {
           cliInit()
           // HTTP request to server
       },
   }
   ```

### Adding New Configuration Options

1. Add field to `config.Config` struct in `config/config.go`:
   ```go
   NewOption string `mapstructure:"new-option" valid:"required"`
   ```

2. Add flag in `server/server.go` `AddFlags()` method:
   ```go
   flags.StringVar(&conf.NewOption, "new-option", "default", "Description")
   ```

3. Use in cluster code:
   ```go
   cluster.Conf.NewOption
   ```

### Working with Proxies

To add a new proxy type:
1. Create package under `router/newproxy/`
2. Implement `DatabaseProxy` interface
3. Register in `cluster/prx.go` `newProxyList()`

### Important Code Locations

**Flag Parsing Issues**: The `AddFlags()` method in `server/server.go` is called during `init()`. Do not use Go's standard `flag` package here, as it interferes with Cobra's command handling. Only use `pflag.FlagSet` passed as parameter.

**Monitoring Loop**: `cluster/cluster_monitor.go` contains the main `Monitor()` method that runs continuously for each cluster.

**Failover Logic**: `cluster/cluster_fail.go` contains core failover orchestration.

**Proxy Backend Updates**: When topology changes, `cluster.RefreshProxies()` calls `BackendsStateChange()` on all proxies.

## Protocol Buffers

gRPC service definitions are in `signal18/replication-manager/v3/*.proto`:
```bash
# Regenerate gRPC code (requires protoc and plugins)
make proto
```

Generated files are in `repmanv3/` directory.

## React Dashboard

The web UI is a React application:
```bash
cd share/dashboard_react
npm install
npm run build      # Builds to share/dashboard_react/dist/
                   # Copied to share/dashboard/ by make react
```

The server embeds the dashboard and serves it via HTTP.

## Implementation Documentation

Implementation-specific documentation created by Claude Code agents is stored in `doc/implementation/`. This directory mirrors the project structure to keep implementation docs organized alongside their corresponding code modules.

**Structure**: `doc/implementation/{package_path}/{DOC_NAME}.md`

**Examples**:
- `doc/implementation/utils/dbhelper/MIGRATION_STATUS.md` - Migration tracking for dbhelper package
- `doc/implementation/utils/dbhelper/SECURITY_AUDIT.md` - Security audit findings
- `doc/implementation/utils/dbhelper/VENDOR_USAGE.md` - Third-party dependency analysis

When creating new implementation documentation, place it under `doc/implementation/` following the package structure, not alongside the source code.

## Common Issues

**CGO Dependencies**: Some builds require CGO (osc-cgo variant). Most builds use `CGO_ENABLED=0` for static binaries.

**Module Path**: The module is `github.com/signal18/replication-manager`. Import paths must use this prefix.

**Viper Binding**: Flags must be bound to Viper using `viper.BindPFlags(cmd.Flags())` for environment variable overrides to work.
