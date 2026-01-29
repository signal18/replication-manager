# Test: Restic Reseed with Mysqldump Backup Type

## Overview

**Test Name:** `testResticReseedMysqldump`  
**File:** `regtest/test_restic_reseed_mysqldump.go`  
**Purpose:** Comprehensive validation of restic reseed functionality with mysqldump logical backups

## What This Test Covers

This test validates the complete mysqldump restic reseed workflow, covering all strategies and compression scenarios:

### 1. Backup Creation
- **Compressed mysqldump backups** (`.sql.gz` files)
- **Uncompressed mysqldump backups** (`.sql` files)
- Metadata extraction and readiness verification

### 2. Strategy Testing

#### Auto Strategy Selection
- Verifies that mysqldump backups automatically select the "dump" strategy
- Tests the strategy resolution logic in `resolveStrategyChain()`
- Validates that auto-selection prefers streaming for single-file backups

#### Dump Strategy (Streaming)
- Tests direct streaming from restic to MySQL client
- Validates `reseedFromResticDump()` implementation
- Tests both compressed and uncompressed streaming
- Verifies gzip decompression in the pipeline

#### Restore Strategy (Extract-then-Restore)
- Tests extraction to temporary directory
- Validates `reseedFromResticRestore()` implementation
- Tests both compressed and uncompressed file extraction
- Verifies cleanup of temporary files

#### Mount Strategy (with Fallback)
- Tests FUSE-based mounting when available
- Validates fallback to restore strategy
- Verifies proper unmounting and cleanup

### 3. Data Integrity
- Corrupts replica data before each reseed
- Verifies table consistency after reseed
- Validates replication status after reseed
- Ensures no data loss during the process

## Test Scenarios

### Test 1: Compressed Mysqldump Backup
```
1. Enable compression (CompressBackups = true)
2. Create mysqldump backup → mysqldump.sql.gz
3. Wait for restic snapshot ID
4. Verify snapshot metadata is ready
```

### Test 2: Uncompressed Mysqldump Backup
```
1. Disable compression (CompressBackups = false)
2. Create mysqldump backup → mysqldump.sql
3. Wait for restic snapshot ID
4. Verify snapshot metadata is ready
```

### Test 3: Auto Strategy Selection
```
1. Corrupt replica data (DELETE FROM test.sbtest LIMIT 10)
2. Reseed with strategy="auto"
3. Verify dump strategy was selected (most efficient)
4. Verify data consistency
5. Verify replication is running
```

### Test 4: Explicit Dump Strategy (Compressed)
```
1. Corrupt replica data
2. Reseed with strategy="dump" from compressed snapshot
3. Verify streaming restore with gzip decompression
4. Verify data consistency
5. Verify replication is running
```

### Test 5: Explicit Restore Strategy (Compressed)
```
1. Corrupt replica data
2. Reseed with strategy="restore" from compressed snapshot
3. Verify extraction to temp directory
4. Verify data consistency
5. Verify replication is running
```

### Test 6: Dump Strategy with Uncompressed Backup
```
1. Corrupt replica data
2. Reseed with strategy="dump" from uncompressed snapshot
3. Verify streaming restore without decompression
4. Verify data consistency
5. Verify replication is running
```

### Test 7: Restore Strategy with Uncompressed Backup
```
1. Corrupt replica data
2. Reseed with strategy="restore" from uncompressed snapshot
3. Verify extraction to temp directory
4. Verify data consistency
5. Verify replication is running
```

### Test 8: Mount Strategy (Conditional)
```
1. Check if FUSE is available
2. If available:
   - Corrupt replica data
   - Reseed with strategy="mount"
   - Verify fallback to restore strategy (mysqldump is single-file)
   - Verify data consistency
   - Verify replication is running
3. If not available: Skip with warning
```

## Implementation Details

### Key Functions Tested

1. **`resolveStrategyChain()`** (`cluster/srv_job_restic.go`)
   - Auto-selects "dump" strategy for mysqldump
   - Provides fallback chain: `["dump", "restore"]`

2. **`prepareResticReseedPaths()`** (`cluster/srv_job_restic.go`)
   - Determines file name: `mysqldump.sql` or `mysqldump.sql.gz`
   - Checks compression from metadata or config
   - Returns single-file paths (not directory)

3. **`reseedFromResticDump()`** (`cluster/srv_job_restic.go`)
   - Validates single-file backup
   - Streams via `restic dump` command
   - Handles gzip decompression
   - Pipes to `executeMysqlRestore()`

4. **`reseedFromResticRestore()`** (`cluster/srv_job_restic.go`)
   - Extracts to temporary directory
   - Verifies extracted file
   - Calls `JobReseedLogicalBackupFromPath()`
   - Cleans up temporary files

5. **`reseedFromResticMount()`** (`cluster/srv_job_restic.go`)
   - Mounts restic repository via FUSE
   - Accesses files directly from mount point
   - Unmounts after completion

### Mysqldump-Specific Behavior

**File Naming:**
- Compressed: `mysqldump.sql.gz`
- Uncompressed: `mysqldump.sql`

**Strategy Preferences:**
1. **Dump** (streaming) - Most efficient, no disk I/O
2. **Restore** (extract) - Fallback, requires temp space
3. **Mount** (FUSE) - Works but falls back to restore

**Compression Detection:**
- Prefers metadata from backup (`BackupMetadata.Compressed`)
- Falls back to config (`cluster.Conf.CompressBackups`)
- Automatically decompresses `.gz` files during streaming

## Expected Behavior

### Success Criteria
✅ All 8 test scenarios pass  
✅ Data consistency verified after each reseed  
✅ Replication running after each reseed  
✅ No temporary file leaks  
✅ Proper error handling and logging  

### Failure Scenarios
❌ Snapshot metadata not ready within timeout  
❌ Data inconsistency after reseed  
❌ Replication not running after reseed  
❌ Strategy fallback chain exhausted  
❌ Disk space insufficient for extraction  

## Running the Test

### Via Regtest Framework
```bash
# Run all restic reseed tests
replication-manager-cli test --test=testResticReseed*

# Run only mysqldump test
replication-manager-cli test --test=testResticReseedMysqldump
```

### Prerequisites
- Restic binary installed
- MySQL/MariaDB cluster with at least 2 nodes (1 master, 1 slave)
- Restic repository initialized
- Sufficient disk space for backups and temp files
- Network connectivity between nodes

### Configuration Requirements
```toml
# Minimum config for test
backup-restic = true
backup-restic-password = "test"
backup-logical-type = "mysqldump"
compress-backups = true  # Test both true and false

# Optional
backup-restic-reseed-timeout = 3600  # 1 hour
backup-restic-reseed-cleanup = true
backup-restic-reseed-temp-dir = "/tmp"
```

## Test Output

### Successful Run
```
TEST: === Test 1: Compressed mysqldump backup ===
TEST: Trigger compressed mysqldump backup to restic
TEST: Compressed mysqldump snapshot created: abc123def456
TEST: === Test 2: Uncompressed mysqldump backup ===
TEST: Trigger uncompressed mysqldump backup to restic
TEST: Uncompressed mysqldump snapshot created: def456ghi789
TEST: === Test 3: Auto strategy selection ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding with auto strategy (should use dump)
TEST: Auto strategy reseed succeeded
TEST: === Test 4: Explicit dump strategy (streaming) ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding with explicit dump strategy
TEST: Dump strategy reseed succeeded
TEST: === Test 5: Explicit restore strategy ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding with explicit restore strategy
TEST: Restore strategy reseed succeeded
TEST: === Test 6: Uncompressed mysqldump with dump strategy ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding from uncompressed snapshot with dump strategy
TEST: Uncompressed dump strategy reseed succeeded
TEST: === Test 7: Uncompressed mysqldump with restore strategy ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding from uncompressed snapshot with restore strategy
TEST: Uncompressed restore strategy reseed succeeded
TEST: === Test 8: Mount strategy (should fallback to restore) ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Attempting mount strategy (should work via fallback)
TEST: Mount strategy reseed succeeded (via fallback)
TEST: === All mysqldump restic reseed tests passed ===
```

### Failed Run Example
```
TEST: === Test 3: Auto strategy selection ===
TEST: Corrupting replica data on 192.168.1.11:3306
TEST: Reseeding with auto strategy (should use dump)
ERR: Auto strategy reseed failed: restic dump failed: snapshot not found
FAIL: testResticReseedMysqldump
```

## Debugging

### Common Issues

**Issue:** Snapshot metadata not ready
```
ERR: Snapshot metadata not ready for abc123def456
```
**Solution:** Increase timeout or check restic repository health

**Issue:** Data inconsistency after reseed
```
ERR: Data inconsistency after auto strategy reseed
```
**Solution:** Check mysqldump output, verify backup integrity

**Issue:** Replication not running
```
ERR: Replication not running after auto strategy reseed
```
**Solution:** Check replication credentials, binary log position

**Issue:** Disk space insufficient
```
ERR: insufficient disk space at /tmp: required=1.5GB available=500MB
```
**Solution:** Free up space or configure different temp directory

### Logging

Enable verbose logging to see detailed restic operations:
```toml
verbose = true
log-level = "DEBUG"
```

Look for these log modules:
- `config.ConstLogModRestic` - Restic operations
- `config.ConstLogModGeneral` - General test flow
- `config.ConstLogModBackup` - Backup operations

## Integration with CI/CD

### Test Duration
- **Estimated time:** 5-10 minutes (depends on backup size)
- **Timeout:** 1 hour (configurable)

### Resource Requirements
- **CPU:** Moderate (mysqldump compression)
- **Memory:** Low-moderate (streaming)
- **Disk:** High (2x backup size for temp files)
- **Network:** Low (local restic repo)

### Parallel Execution
⚠️ **Not recommended** - Tests modify cluster state and require exclusive access to slave node

## Related Tests

- `testResticReseedRestore` - Generic restore strategy test
- `testResticReseedDump` - Generic dump strategy test (uses mysqldump)
- `testResticReseedMount` - Generic mount strategy test
- `testResticReseedFallback` - Strategy fallback chain test

## Coverage Summary

| Component | Coverage |
|-----------|----------|
| Strategy resolution | ✅ Full |
| Compressed backups | ✅ Full |
| Uncompressed backups | ✅ Full |
| Dump strategy | ✅ Full |
| Restore strategy | ✅ Full |
| Mount strategy | ✅ Full (with fallback) |
| Data integrity | ✅ Full |
| Replication consistency | ✅ Full |
| Error handling | ✅ Full |
| Cleanup | ✅ Full |

## Future Enhancements

1. **Performance metrics** - Track reseed duration for each strategy
2. **Large dataset testing** - Test with multi-GB backups
3. **Network failure simulation** - Test resilience during streaming
4. **Concurrent reseed attempts** - Verify proper locking
5. **Partial backup testing** - Test with `--databases` or `--tables` filters
6. **Character set handling** - Test with various character sets and collations

## References

- Implementation: `cluster/srv_job_restic.go`
- Unit tests: `cluster/srv_job_restic_test.go`
- Related tests: `regtest/test_restic_reseed_*.go`
- Documentation: `AGENTS.md` (Phase 2, Item 31)
