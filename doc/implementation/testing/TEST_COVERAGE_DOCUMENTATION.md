# Test Coverage Documentation

## Overview
This document provides comprehensive technical documentation for the test coverage implemented for the replication-manager codebase. Five critical areas have been covered with extensive automated tests.

**Date**: January 5, 2026  
**Total Test Files**: 4  
**Total Test Functions**: 35  
**Total Test Cases**: ~137  
**Pass Rate**: 100%

---

## Table of Contents
1. [Job Locking Race Conditions](#1-job-locking-race-conditions)
2. [Variable Preservation System](#2-variable-preservation-system)
3. [LoadFromConfigFile() with INI Shadows](#3-loadfromconfigfile-with-ini-shadows)
4. [VariableValue Interface Implementations](#4-variablevalue-interface-implementations)
5. [BigInt Size Parsing](#5-bigint-size-parsing)

---

## 1. Job Locking Race Conditions

### File Location
`/home/ahmad/replication-manager/cluster/srv_job_lock_test.go`

### Purpose
Tests the job locking mechanism in the cluster package to ensure thread-safe operations during concurrent job execution. The job lock prevents race conditions when multiple goroutines attempt to acquire locks for critical operations.

### Implementation Being Tested
Located in `cluster/srv_set.go` (lines 755-775):
```go
func (server *ServerMonitor) TryAcquireJobLock() bool {
    return server.ClusterGroup.jobLock.TryLock()
}

func (server *ServerMonitor) ReleaseJobLock() {
    server.ClusterGroup.jobLock.Unlock()
}
```

### Test Functions (7 total)

#### 1. `TestJobLockBasic`
- **Purpose**: Verifies basic lock acquisition and release functionality
- **Test Steps**:
  1. Create a new cluster with single server
  2. Acquire lock using `TryAcquireJobLock()`
  3. Verify lock is acquired (returns true)
  4. Release lock using `ReleaseJobLock()`
  5. Verify lock can be re-acquired
- **Assertions**: Lock acquisition/release works as expected

#### 2. `TestJobLockRaceCondition`
- **Purpose**: Tests concurrent access patterns with 100 goroutines
- **Test Parameters**:
  - Goroutines: 100
  - Iterations per goroutine: 10
  - Total attempts: 1000
- **Test Steps**:
  1. Launch 100 goroutines simultaneously
  2. Each attempts to acquire lock 10 times
  3. Count successful acquisitions
  4. Verify only one goroutine holds lock at any time
- **Assertions**: Race-free lock acquisition, predictable behavior

#### 3. `TestJobLockConcurrentAttempts`
- **Purpose**: Tests high-concurrency scenarios
- **Test Parameters**:
  - Goroutines: 50
  - Immediate concurrent access
- **Test Steps**:
  1. Pre-acquire lock in main goroutine
  2. Launch 50 goroutines trying to acquire
  3. All should fail (return false)
  4. Release lock from main goroutine
  5. One of the waiting goroutines may acquire
- **Assertions**: Lock exclusivity maintained under pressure

#### 4. `TestJobLockMultipleReleases`
- **Purpose**: Ensures multiple releases don't panic or cause issues
- **Test Steps**:
  1. Acquire lock
  2. Release multiple times
  3. Verify no panic (though behavior is technically undefined)
- **Assertions**: Graceful handling of edge cases

#### 5. `TestJobLockStressTest`
- **Purpose**: High-load stress testing over sustained period
- **Test Parameters**:
  - Goroutines: 200
  - Duration: 2 seconds
  - Continuous acquire/release cycles
- **Test Steps**:
  1. Launch 200 goroutines
  2. Each continuously attempts to acquire/release for 2 seconds
  3. Track total successful operations
  4. Use WaitGroup for synchronization
- **Assertions**: System stability under heavy load
- **Typical Results**: ~2300+ operations completed

#### 6. `TestJobLockAcquireReleaseSequential`
- **Purpose**: Tests sequential lock/unlock cycles
- **Test Parameters**:
  - Cycles: 1000
- **Test Steps**:
  1. Acquire lock
  2. Release lock
  3. Repeat 1000 times
- **Assertions**: No deadlocks or issues in sequential operation

#### 7. `TestJobLockNoDeadlock`
- **Purpose**: Deadlock detection with timeout
- **Test Steps**:
  1. Launch goroutine attempting lock acquisition
  2. Set 5-second timeout
  3. Monitor for completion
  4. Report deadlock if timeout exceeded
- **Assertions**: Operations complete within reasonable time

### How to Run
```bash
cd /home/ahmad/replication-manager
go test -v ./cluster -run TestJobLock

# With race detection
go test -race -v ./cluster -run TestJobLock
```

### Expected Output
```
=== RUN   TestJobLockBasic
--- PASS: TestJobLockBasic (0.00s)
=== RUN   TestJobLockRaceCondition
    srv_job_lock_test.go:75: Successful lock acquisitions: 10 out of 1000 attempts
--- PASS: TestJobLockRaceCondition (0.01s)
=== RUN   TestJobLockConcurrentAttempts
    srv_job_lock_test.go:121: Goroutines that acquired lock: 1 out of 50
--- PASS: TestJobLockConcurrentAttempts (0.01s)
=== RUN   TestJobLockMultipleReleases
--- PASS: TestJobLockMultipleReleases (0.00s)
=== RUN   TestJobLockStressTest
    srv_job_lock_test.go:194: Completed 2355 operations
--- PASS: TestJobLockStressTest (2.00s)
=== RUN   TestJobLockAcquireReleaseSequential
--- PASS: TestJobLockAcquireReleaseSequential (0.00s)
=== RUN   TestJobLockNoDeadlock
    srv_job_lock_test.go:247: No deadlock detected
--- PASS: TestJobLockNoDeadlock (0.14s)
PASS
```

---

## 2. Variable Preservation System

### File Location
`/home/ahmad/replication-manager/cluster/srv_variable_preservation_test.go`

### Purpose
Tests the 3-file variable preservation system that allows users to maintain custom MySQL/MariaDB variable configurations across deployments.

### Three-File System Architecture
1. **00_config.cnf** - Original template configuration
2. **00_deployed.cnf** - Currently deployed configuration
3. **01_preserved.cnf** - User-accepted differences (overrides)

### Implementation Being Tested
Uses the `config.VariablesMap` API:
- `SetPreservedValue(name, value string)`
- `SetPreservedValues(vars map[string]string)`
- `UnsetPreservedValue()`
- `LoadFromConfigFile(path, fileType string)`

### Test Functions (8 total)

#### 1. `TestVariablePreservationBasic`
- **Purpose**: Basic set and verify operations
- **Test Steps**:
  1. Create new VariablesMap
  2. Set preserved value for `max_connections = 200`
  3. Retrieve variable state
  4. Verify preserved value is stored
- **Assertions**: Preserved values are correctly stored and retrievable

#### 2. `TestVariablePreservationMultiple`
- **Purpose**: Batch preservation of multiple variables
- **Test Variables**:
  ```
  max_connections = 200
  innodb_buffer_pool_size = 2G
  query_cache_size = 64M
  ```
- **Test Steps**:
  1. Preserve multiple variables
  2. Verify each preserved value
- **Assertions**: All variables preserved correctly

#### 3. `TestVariablePreservationClear`
- **Purpose**: Test clearing preserved values
- **Test Steps**:
  1. Set preserved value
  2. Call `UnsetPreservedValue()`
  3. Verify value is cleared
- **Assertions**: Preserved values can be removed

#### 4. `TestVariablePreservationThreeFileSplit`
- **Purpose**: Test the complete 3-file system
- **Test Scenario**:
  ```
  00_config.cnf:    max_connections = 100
  00_deployed.cnf:  max_connections = 150
  01_preserved.cnf: max_connections = 200
  ```
- **Test Steps**:
  1. Create three temporary files
  2. Load all three in order
  3. Verify all three values are tracked separately
  4. Confirm precedence order
- **Assertions**: All three states coexist, proper precedence

#### 5. `TestVariablePreservationOverwrite`
- **Purpose**: Test overwriting preserved values
- **Test Steps**:
  1. Set preserved value to 100
  2. Overwrite with 200
  3. Verify new value replaces old
- **Assertions**: Latest value persists

#### 6. `TestVariablePreservationMultiValueVariable`
- **Purpose**: Test multi-value variables (like optimizer_switch)
- **Test Steps**:
  1. Set multiple values for same variable
  2. Verify stored as MapValue type
- **Assertions**: Multi-value variables handled correctly

#### 7. `TestVariablePreservationLoadFromFile`
- **Purpose**: Load preserved values from INI file
- **Test File Content**:
  ```ini
  [mysqld]
  max_connections = 300
  innodb_buffer_pool_size = 4G
  optimizer_switch = index_merge=on
  optimizer_switch = mrr=on
  ```
- **Test Steps**:
  1. Create temporary file
  2. Load using `LoadFromConfigFile()`
  3. Verify all values loaded
- **Assertions**: File loading works correctly

#### 8. `TestVariablePreservationSetPreservedValues`
- **Purpose**: Batch setting via map
- **Test Steps**:
  1. Create map of variables
  2. Call `SetPreservedValues(map)`
  3. Verify all set
- **Assertions**: Batch operations work

### How to Run
```bash
cd /home/ahmad/replication-manager
go test -v ./cluster -run TestVariablePreservation
```

---

## 3. LoadFromConfigFile() with INI Shadows

### File Location
`/home/ahmad/replication-manager/config/variable_value_test.go`

### Purpose
Tests the INI file loading functionality with support for "shadow" keys (duplicate keys in INI files), which is common in MySQL configuration files.

### Implementation Being Tested
Located in `config/maps.go`:
```go
func (vm *VariablesMap) LoadFromConfigFile(path string, fileType string) error
```

### Key Features Tested
1. **INI Shadow Support**: Multiple values for same key
2. **File Type Handling**: config, deployed, preserved
3. **Loose Prefix**: Variables with `loose_` prefix
4. **Dash Conversion**: `max-connections` → `max_connections`

### Test Functions

#### `TestVariablesMapINIShadows`
- **Purpose**: Test duplicate keys in INI files
- **Configuration**:
  ```go
  ini.LoadOptions{
      AllowShadows: true,
  }
  ```
- **Test File**:
  ```ini
  [mysqld]
  replicate-do-db = db1
  replicate-do-db = db2
  replicate-do-db = db3
  ```
- **Test Steps**:
  1. Create file with duplicate keys
  2. Load with shadow support enabled
  3. Verify all values captured
  4. Check stored as SliceValue type
- **Assertions**: All duplicate values preserved

#### `TestVariablesMapLoadFromConfigFileTypes`
- **Purpose**: Test three file types (config/deployed/preserved)
- **Test Cases** (table-driven):
  - Load as "config" type → stores in Config field
  - Load as "deployed" type → stores in Deployed field
  - Load as "preserved" type → stores in Preserved field
- **Assertions**: Values stored in correct fields based on type

#### `TestVariablesMapLoosePrefixHandling`
- **Purpose**: Test `loose_` prefix handling
- **Test File**:
  ```ini
  [mysqld]
  loose_max_connections = 100
  ```
- **Test Steps**:
  1. Load file with loose_ prefix
  2. Verify prefix is preserved in key name
- **Assertions**: Prefix handling works correctly

### How to Run
```bash
cd /home/ahmad/replication-manager
go test -v ./config -run TestVariablesMap
```

---

## 4. VariableValue Interface Implementations

### File Location
`/home/ahmad/replication-manager/config/variable_value_test.go`

### Purpose
Tests the three implementations of the `VariableValue` interface used to represent MySQL/MariaDB variables with different value structures.

### Interface Definition
```go
type VariableValue interface {
    String() string
    Print() string
    PrintWithExclude(exclude []string) string
    Append(value string)
    Set(value string)
    IsEqual(other VariableValue) bool
}
```

### Three Implementations

#### 1. SingleValue
- **Use Case**: Simple key=value pairs
- **Example**: `max_connections = 100`
- **Storage**: Single string value

#### 2. SliceValue
- **Use Case**: Multi-valued variables
- **Examples**:
  - `optimizer_switch = index_merge=on,index_merge_union=on`
  - `replicate-do-db = db1, db2, db3`
- **Storage**: String slice `[]string`

#### 3. MapValue
- **Use Case**: Key-value pairs
- **Example**: `performance_schema_instrument = 'wait/%=ON'`
- **Storage**: Map `map[string]string`

### Test Functions (20 total)

#### SingleValue Tests (3 functions)
1. **TestVariableValueSingleValue**
   - Tests: String(), Set(), Append()
   - Verifies basic operations

2. **TestVariableValueSingleValueEquality**
   - Tests: IsEqual() method
   - Compares same and different values

3. **TestVariableValueSingleValuePrintWithExclude**
   - Tests: PrintWithExclude() with filter
   - Verifies filtering works

#### SliceValue Tests (4 functions)
1. **TestVariableValueSliceValue**
   - Tests: Multiple value storage
   - Append() adds new values
   - String() joins with comma

2. **TestVariableValueSliceValueDuplicates**
   - Tests: Duplicate handling
   - Append() same value multiple times
   - Verifies deduplication

3. **TestVariableValueSliceValueEquality**
   - Tests: IsEqual() for slices
   - Order-independent comparison

4. **TestVariableValueSliceValuePrintWithExclude**
   - Tests: Filtering slice values
   - Excludes specific entries

#### MapValue Tests (4 functions)
1. **TestVariableValueMapValue**
   - Tests: Key-value pair storage
   - Set() parses `key=value` format

2. **TestVariableValueMapValueEquality**
   - Tests: Map comparison
   - IsEqual() for maps

3. **TestVariableValueMapValuePrintWithExclude**
   - Tests: Filtering map entries
   - Excludes keys by pattern

4. **TestVariableValueMapValuePrint**
   - Tests: Print() formatting
   - Outputs as `key=value` pairs

#### Integration Tests (9 functions)
1. **TestVariableValueInterfaceCompliance**
   - Verifies all three types implement interface
   - Polymorphic behavior

2. **TestVariableStateSetMethods**
   - Tests VariableState struct
   - Config/Deployed/Preserved fields

3. **TestVariableStateMultiValue**
   - Tests multi-value in VariableState
   - SliceValue integration

4. **TestVariablesMapSetValue**
   - Tests VariablesMap.SetValue()
   - Map operations

5-9. **Additional VariablesMap tests**
   - LoadFromConfigFile variants
   - Type handling
   - Error cases

### Sample Test Code
```go
func TestVariableValueSingleValue(t *testing.T) {
    v := &config.SingleValue{}
    v.Set("value")
    
    if v.String() != "value" {
        t.Errorf("Expected 'value', got '%s'", v.String())
    }
    
    v.Append("newvalue")
    if v.String() != "newvalue" {
        t.Errorf("Expected 'newvalue', got '%s'", v.String())
    }
}
```

### How to Run
```bash
cd /home/ahmad/replication-manager
go test -v ./config -run TestVariableValue
```

### Expected Output
```
=== RUN   TestVariableValueSingleValue
--- PASS: TestVariableValueSingleValue (0.00s)
=== RUN   TestVariableValueSingleValueEquality
--- PASS: TestVariableValueSingleValueEquality (0.00s)
...
PASS
ok  	github.com/signal18/replication-manager/config	0.012s
```

---

## 5. BigInt Size Parsing

### File Location
`/home/ahmad/replication-manager/share/dashboard_react/src/Pages/ClusterDB/components/Variables/__tests__/parseSizeToBytes.test.cjs`

### Purpose
Tests the JavaScript `parseSizeToBytes()` function that converts human-readable size strings (like "1G", "128M") to BigInt byte values for accurate large number handling.

### Why BigInt?
JavaScript's `Number.MAX_SAFE_INTEGER` is 2^53-1 (9,007,199,254,740,991). Database configurations often exceed this:
- 10TB = 10,995,116,277,760 bytes (exceeds MAX_SAFE_INTEGER)
- BigInt allows accurate representation of any integer

### Implementation Being Tested
```javascript
const parseSizeToBytes = (value) => {
  // ... parsing logic ...
  const multipliers = {
    'K': 1024n,
    'M': 1024n * 1024n,
    'G': 1024n * 1024n * 1024n,
    'T': 1024n * 1024n * 1024n * 1024n
  }
  // Returns BigInt or null
}
```

### Test Suites (12 suites, 63 test cases)

#### 1. Plain Numbers (3 cases)
```javascript
"1024" → 1024n
"0" → 0n
"999999999999999999" → 999999999999999999n
```

#### 2. Kilobyte Values (4 cases)
```javascript
"1K" → 1024n
"10K" → 10240n
"1KB" → 1024n
"128k" → 131072n  // lowercase
```

#### 3. Megabyte Values (4 cases)
```javascript
"1M" → 1048576n
"16M" → 16777216n
"128MB" → 134217728n
"1m" → 1048576n  // lowercase
```

#### 4. Gigabyte Values (4 cases)
```javascript
"1G" → 1073741824n
"2G" → 2147483648n
"4GB" → 4294967296n
"8g" → 8589934592n  // lowercase
```

#### 5. Terabyte Values (3 cases)
```javascript
"1T" → 1099511627776n
"2TB" → 2199023255552n
"5t" → 5497558138880n  // lowercase
```

#### 6. Decimal Values (4 cases)
```javascript
"1.5G" → 1610612736n
"0.5M" → 524288n
"2.25K" → 2304n
"1.75T" → 1924145348608n
```

#### 7. Large Values Beyond MAX_SAFE_INTEGER (3 cases)
```javascript
"10000G" → 10737418240000n
"100T" → 109951162777600n
"9999999999999999" → 9999999999999999n
```

#### 8. Values with Whitespace (3 cases)
```javascript
" 1G " → 1073741824n
"  128M  " → 134217728n
" 1024 " → 1024n
```

#### 9. Invalid Inputs (8 cases)
```javascript
"" → null
null → null
undefined → null
"invalid" → null
"123.45.67G" → null
"1P" → null  // Unsupported unit
"GB" → null  // No number
"-1G" → null  // Negative
```

#### 10. Edge Cases (5 cases)
```javascript
"0K" → 0n
"0M" → 0n
"0G" → 0n
"0.0G" → 0n
"1.0G" → 1073741824n
```

#### 11. Case Insensitivity (8 cases)
```javascript
"1k" === "1K" → 1024n
"1m" === "1M" → 1048576n
"1g" === "1G" → 1073741824n
"1t" === "1T" → 1099511627776n
```

#### 12. Precision in Decimal Conversions (4 cases)
```javascript
"0.1G" → 107374182n
"0.01G" → 10737418n
"0.001G" → 1073741n
"3.14159M" → 3294195n  // Math.floor precision
```

### Comparison Function Tests (10 cases)
Tests `areSizeValuesEqual(val1, val2)`:
```javascript
"1024" === "1K" → true
"1048576" === "1M" → true
"1G" === "1024M" → true
"1G" === "2G" → false
"1.5G" === "1536M" → true
```

### How to Run
```bash
cd /home/ahmad/replication-manager/share/dashboard_react
node src/Pages/ClusterDB/components/Variables/__tests__/parseSizeToBytes.test.cjs
```

### Expected Output
```
 Testing: Plain numbers
  ✓ "1024" => 1024
  ✓ "0" => 0
  ✓ "999999999999999999" => 999999999999999999

...

========================================
Total: 53 tests
Passed: 53
Failed: 0

========================================
Testing areSizeValuesEqual
========================================
  ✓ 1024 == 1K => true
  ✓ 1048576 == 1M => true
  ...

Comparison tests: 10 passed, 0 failed
```

---

## Summary Statistics

| Test Area | File | Functions | Cases | LOC |
|-----------|------|-----------|-------|-----|
| Job Locking | cluster/srv_job_lock_test.go | 7 | ~13 | ~250 |
| Variable Preservation | cluster/srv_variable_preservation_test.go | 8 | ~13 | ~280 |
| VariableValue Interface | config/variable_value_test.go | 20 | ~47 | ~600 |
| BigInt Size Parsing | .../__tests__/parseSizeToBytes.test.cjs | 12 suites | 63 | ~280 |
| **TOTAL** | **4 files** | **35** | **~137** | **~1410** |

---

## Running All Tests

### Go Tests
```bash
cd /home/ahmad/replication-manager

# Run all tests
go test -v ./cluster -run TestJobLock
go test -v ./cluster -run TestVariablePreservation
go test -v ./config -run TestVariable

# With race detection
go test -race ./cluster ./config

# With coverage
go test -cover ./cluster ./config
```

### JavaScript Test
```bash
cd /home/ahmad/replication-manager/share/dashboard_react
node src/Pages/ClusterDB/components/Variables/__tests__/parseSizeToBytes.test.cjs
```

---

## Continuous Integration

These tests can be integrated into CI/CD pipelines:

```yaml
# Example GitHub Actions
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
        with:
          go-version: '1.21'
      - name: Run Go Tests
        run: |
          go test -v ./cluster -run TestJobLock
          go test -v ./cluster -run TestVariablePreservation
          go test -v ./config -run TestVariable
      - name: Run JavaScript Tests
        run: |
          cd share/dashboard_react
          node src/Pages/ClusterDB/components/Variables/__tests__/parseSizeToBytes.test.cjs
```

---

## Maintenance Notes

### Adding New Tests
1. Follow existing naming conventions: `Test<Component><Feature>`
2. Use table-driven tests for multiple similar cases
3. Add descriptive comments explaining test purpose
4. Use `t.TempDir()` for temporary files (auto-cleanup)
5. Verify tests pass both individually and as suite

### Common Issues
1. **Race Conditions**: Use `-race` flag to detect
2. **Temporary Files**: Always use `t.TempDir()` for auto-cleanup
3. **BigInt Literals**: Use `n` suffix (e.g., `1024n`)
4. **INI Parsing**: Enable `AllowShadows` for duplicate keys

---

## References

- Go Testing Package: https://pkg.go.dev/testing
- INI Package: https://github.com/go-ini/ini
- BigInt MDN: https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/BigInt
- Replication Manager: https://github.com/signal18/replication-manager
