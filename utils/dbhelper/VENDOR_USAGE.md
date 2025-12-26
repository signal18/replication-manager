# Database Vendor Abstraction - Usage Guide

## Overview

Phase 2 introduces the `DatabaseVendor` interface to eliminate vendor-specific conditionals scattered throughout the codebase. This makes code cleaner, more testable, and easier to extend.

## Before (Phase 1)

```go
func GetSlaveStatus(db *sqlx.DB, Channel string, myver *version.Version) (SlaveStatus, string, error) {
    var query string

    if myver.IsPostgreSQL() {
        if Channel == "" {
            Channel = "alltables"
        }
        query = `SELECT ss.subname as "Connection_name", ... complex PG query ...`
    } else if myver.IsMySQLOrPerconaGreater84() {
        if Channel != "" {
            query = "SHOW REPLICA STATUS FOR CHANNEL '" + Channel + "'"
        } else {
            query = "SHOW REPLICA STATUS"
        }
    } else {
        if Channel != "" {
            query = "SHOW SLAVE STATUS FOR CHANNEL '" + Channel + "'"
        } else {
            query = "SHOW SLAVE STATUS"
        }
    }

    // ... execute query ...
}
```

## After (Phase 2)

```go
func GetSlaveStatus(db *sqlx.DB, Channel string, myver *version.Version) (SlaveStatus, string, error) {
    vendor := NewDatabaseVendor(myver)
    query := vendor.GetReplicationStatusQuery(Channel)

    // ... execute query ...
}
```

## Usage Examples

### 1. Getting Variables

```go
// Old way
var query string
source := GetVariableSource(db, myver)
if vcase == "UPPER" {
    query = "SELECT UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_variables"
} else {
    query = "SELECT UPPER(Variable_name) AS variable_name, Variable_Value AS value FROM " + source + ".global_variables"
}

// New way
vendor := NewDatabaseVendor(myver)
query := vendor.BuildVariablesQuery(vcase)
```

### 2. Replication Commands

```go
// Old way
cmd := "STOP SLAVE"
if myver.IsMySQLOrPerconaGreater84() {
    cmd = "STOP REPLICA"
}
if myver.IsMariaDB() && Channel != "" {
    cmd += " '" + Channel + "'"
} else if myver.IsMySQLOrPercona() && Channel != "" {
    cmd += " FOR CHANNEL '" + Channel + "'"
}

// New way
vendor := NewDatabaseVendor(myver)
cmd := vendor.BuildStopReplicationCommand(Channel)
```

### 3. Binary Logs

```go
// Old way
if myver.IsPostgreSQL() {
    return errors.New("Binary logs not supported on PostgreSQL")
}
query := "SHOW BINARY LOGS"

// New way
vendor := NewDatabaseVendor(myver)
if !vendor.SupportsBinaryLogs() {
    return errors.New("Binary logs not supported on " + vendor.Name())
}
query := vendor.GetBinaryLogsQuery()
```

### 4. Terminology (for UI/logging)

```go
// Old way
var masterTerm string
if myver.IsMySQLOrPerconaGreater84() {
    masterTerm = "Source"
} else {
    masterTerm = "Master"
}
log.Printf("Connecting to %s...", masterTerm)

// New way
vendor := NewDatabaseVendor(myver)
log.Printf("Connecting to %s...", vendor.ReplicationTermMaster())
```

## Implementing New Vendors

To add support for a new database (e.g., Amazon Aurora):

```go
type AuroraVendor struct {
    version *version.Version
}

func (v *AuroraVendor) Name() string {
    return "Amazon Aurora"
}

func (v *AuroraVendor) SupportsGTID() bool {
    return true
}

// Implement all interface methods...
```

Then add to `NewDatabaseVendor()`:

```go
func NewDatabaseVendor(ver *version.Version) DatabaseVendor {
    if ver.IsAurora() {
        return &AuroraVendor{version: ver}
    }
    // ... existing checks
}
```

## Benefits

1. **Cleaner Code**: Eliminates deeply nested conditionals
2. **Single Responsibility**: Each vendor handles its own logic
3. **Testability**: Easy to mock vendor implementations
4. **Extensibility**: Adding new databases is straightforward
5. **Maintainability**: Vendor logic in one place
6. **Type Safety**: Interface ensures consistency

## Migration Strategy

Existing code continues to work unchanged. Migrate functions gradually:

1. Identify functions with vendor conditionals
2. Add vendor abstraction methods
3. Refactor functions to use vendor
4. Test thoroughly
5. Remove old conditional code

## Current Interface Methods

- `Name()` - Vendor name for display
- `SupportsGTID()` - GTID capability check
- `GetVariableSource()` - Schema for variables
- `BuildStatusQuery()` - Status query builder
- `BuildVariablesQuery()` - Variables query builder
- `GetReplicationStatusQuery()` - Replication status
- `BuildChangeMasterCommand()` - Setup replication
- `BuildStartReplicationCommand()` - Start replication
- `BuildStopReplicationCommand()` - Stop replication
- `BuildResetReplicationCommand()` - Reset replication
- `SupportsBinaryLogs()` - Binary log support
- `GetBinaryLogsQuery()` - List binary logs
- `BuildPurgeBinaryLogsCommand()` - Purge logs
- `ReplicationTermMaster()` - Display term
- `ReplicationTermSlave()` - Display term
- `ReplicationTermChannel()` - Display term

More methods can be added as needed.
