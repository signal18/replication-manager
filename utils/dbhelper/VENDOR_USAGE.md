# Database Vendor Abstraction - Usage Guide

## Overview

Phase 2 introduces the `DatabaseVendor` interface to eliminate vendor-specific conditionals scattered throughout the codebase. This makes code cleaner, more testable, and easier to extend.

## ⚠️ Safety First

**This abstraction layer is completely additive and does NOT modify existing code.**

- ✅ All existing functions work unchanged
- ✅ No behavior changes or risk to production
- ✅ Vendor layer exists alongside current code
- ✅ Migration is optional and gradual

**Do not rush to refactor existing functions.** Use the vendor layer for:
1. New features being developed
2. Functions you're already modifying
3. Code with comprehensive test coverage

The vendor implementations were created to match existing behavior, but **validation is essential** before migrating any production code.

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

**IMPORTANT: Migration is optional.** Existing code continues to work unchanged.

### Safe Migration Approach

When you do decide to migrate a function:

1. **Write tests first** - Capture current behavior
2. **Create new function** - Side-by-side with original
3. **Validate thoroughly** - Compare outputs across all vendors
4. **Use feature flags** - Enable new code gradually
5. **Monitor in production** - Watch for differences
6. **Remove old code** - Only after proven stability

### Example: Safe Migration Pattern

```go
// Step 1: Keep existing function (temporarily rename)
func getSlaveStatusLegacy(db *sqlx.DB, channel string, ver *version.Version) (SlaveStatus, string, error) {
    // Original implementation with version checks
    if ver.IsPostgreSQL() {
        // ... existing code
    }
    // ...
}

// Step 2: Create new function using vendor
func GetSlaveStatus(db *sqlx.DB, channel string, ver *version.Version) (SlaveStatus, string, error) {
    vendor := NewDatabaseVendor(ver)
    query := vendor.GetReplicationStatusQuery(channel)

    // Execute query (rest of implementation)
    // ...
}

// Step 3: Test both, compare results
func TestMigrationParity(t *testing.T) {
    oldResult, _, _ := getSlaveStatusLegacy(db, "", ver)
    newResult, _, _ := GetSlaveStatus(db, "", ver)

    assert.Equal(t, oldResult, newResult, "Migration broke behavior!")
}

// Step 4: After validation, remove legacy function
```

### Low-Risk Migration Targets

Start with these types of functions:
- Non-critical utility functions
- Functions with good test coverage
- Simple query builders
- Display/formatting logic

### High-Risk - Avoid Migrating

Do NOT migrate without extensive testing:
- Core replication control (START/STOP SLAVE)
- Failover logic
- GTID position tracking
- Critical production queries

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
