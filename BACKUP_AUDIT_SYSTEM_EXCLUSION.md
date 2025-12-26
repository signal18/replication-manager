# Backup Scripts Audit: .system Directory Exclusion

**Date**: 2025-12-10
**Auditor**: Claude Code
**Scope**: Verify `.system` directory exclusion in all backup operations

## Executive Summary

Audit of replication-manager backup scripts reveals **MISSING** `.system` directory exclusion in several backup methods. Only mariabackup has the exclusion properly implemented.

## Findings

### ✅ COMPLIANT: Physical Backups with mariabackup

**Files**:
- `share/scripts/dbjobs_new.sh:1159`
- `share/dashboard/static/configurator/init/dbjobs_new:552`

**Implementation**:
```bash
$MARIADB_BACKUP --innobackupex --defaults-file=$MYSQL_CONF/my.cnf --databases-exclude=.system \
    --protocol=TCP $BINARY_CLIENT_PARAMETERS --stream=xbstream
```

**Status**: ✅ Correctly excludes `.system` directory

---

### ❌ NON-COMPLIANT: Physical Backups with xtrabackup

**File**: `share/scripts/dbjobs_new.sh:1155`

**Implementation**:
```bash
$XTRABACKUP --defaults-file=$MYSQL_CONF/my.cnf --backup -u$USER -H$MYSQL_SERVER \
    -p$PASSWORD -P$MYSQL_PORT --stream=xbstream --target-dir=$LOG_DIR/
```

**Issue**: Missing `--databases-exclude=.system` flag

**Impact**:
- Backs up unnecessary logs, binlogs, and the backup directory itself
- Wastes storage space
- Increases backup time
- Can cause confusion during restore

**Recommendation**:
```bash
$XTRABACKUP --defaults-file=$MYSQL_CONF/my.cnf --databases-exclude=.system --backup \
    -u$USER -H$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT --stream=xbstream --target-dir=$LOG_DIR/
```

---

### ❌ NON-COMPLIANT: Logical Backups with mysqldump

**File**: `cluster/cluster_get.go:44-60` (getDumpParameter function)

**Implementation**:
```go
func (cluster *Cluster) getDumpParameter() string {
    dump_param := cluster.Conf.BackupMysqldumpOptions
    // ... processes parameters but does NOT add .system exclusion
    return dump_param
}
```

**Default Options** (`server/server.go:850`):
```
--hex-blob --single-transaction --verbose --all-databases --routines=true --triggers=true --system=all
```

**Issue**: Uses `--all-databases` without excluding `.system`

**Impact**:
- Attempts to back up `.system` as a database (will fail or create invalid dumps)
- `.system` is a directory structure, not a MySQL database
- Can cause backup errors
- Wastes processing time

**Recommendation**:
```bash
--ignore-database=.system
```

OR modify `cluster/cluster_get.go:95` to add:
```go
dumpargs = append(dumpargs, "--ignore-database=.system")
```

---

### ❌ NON-COMPLIANT: Logical Backups with mydumper

**File**: `cluster/srv_job.go` (mydumper implementation)

**Current Regex** (`server/server.go:849`):
```go
BackupMyDumperRegex: `^(?!(sys\.|performance_schema\.|information_schema\.|replication_manager_schema\.jobs|mysql\.gtid_slave_pos$))`
```

**Issue**: Regex does NOT exclude `.system` database/directory

**Impact**: Same as mysqldump - attempts to process `.system` as a database

**Recommendation**:
Update regex to:
```go
BackupMyDumperRegex: `^(?!(sys\.|performance_schema\.|information_schema\.|replication_manager_schema\.jobs|mysql\.gtid_slave_pos$|\.system\.))`
```

---

### ❓ UNKNOWN: Logical Backups with dumpling

**File**: `cluster/srv_job.go:641`

**Implementation**: Uses dumpling from TiDB/PingCAP

**Issue**: No explicit `.system` exclusion found in code

**Status**: Requires further investigation - likely inherits the same issue as mysqldump

---

### ❌ NON-COMPLIANT: Old Script (Deprecated but Still Present)

**File**: `share/scripts/dbjobs.sh:103`

**Implementation**:
```bash
/usr/bin/innobackupex --defaults-file=/etc/mysql/my.cnf --socket='/var/run/mysqld/mysqld.sock' \
    --slave-info --no-version-check --user=$USER --password=$PASSWORD --stream=xbstream /tmp/
```

**Issue**: Missing `--databases-exclude=.system`

**Status**: This is the OLD script (noted in header as "sample of an old script"), but should still be fixed or removed to avoid confusion.

---

## Summary Table

| Backup Method | Tool | .system Excluded? | File Location | Priority |
|--------------|------|-------------------|---------------|----------|
| Physical | mariabackup | ✅ Yes | share/scripts/dbjobs_new.sh:1159 | N/A |
| Physical | xtrabackup | ❌ No | share/scripts/dbjobs_new.sh:1155 | HIGH |
| Physical | innobackupex (old) | ❌ No | share/scripts/dbjobs.sh:103 | LOW |
| Logical | mysqldump | ❌ No | cluster/cluster_get.go:95 | HIGH |
| Logical | mydumper | ❌ No | server/server.go:849 | MEDIUM |
| Logical | dumpling | ❓ Unknown | cluster/srv_job.go:641 | MEDIUM |

---

## Recommendations

### Priority 1: Fix Active Backup Methods

1. **xtrabackup** (share/scripts/dbjobs_new.sh:1155)
   ```bash
   $XTRABACKUP --defaults-file=$MYSQL_CONF/my.cnf --databases-exclude=.system --backup \
       -u$USER -H$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT --stream=xbstream --target-dir=$LOG_DIR/
   ```

2. **mysqldump** (cluster/cluster_get.go:95)
   ```go
   dumpargs = append(dumpargs, "--apply-slave-statements", "--ignore-table=replication_manager_schema.jobs", "--ignore-database=.system")
   ```

3. **mydumper** (server/server.go:849)
   ```go
   flags.StringVar(&conf.BackupMyDumperRegex, "backup-mydumper-regex",
       `^(?!(sys\.|performance_schema\.|information_schema\.|replication_manager_schema\.jobs|mysql\.gtid_slave_pos$|\.system\.))`,
       "Mydumper regex for backup")
   ```

### Priority 2: Clean Up Legacy Code

4. **Old dbjobs.sh**: Either fix or add prominent deprecation warning
5. **dumpling**: Investigate and add exclusion if needed

### Priority 3: Documentation

6. Update `CLAUDE.md` to reflect that `.system` exclusion is now consistent across all backup methods
7. Add configuration documentation for users who run external backups

---

## Testing Recommendations

1. Test each backup method (xtrabackup, mariabackup, mysqldump, mydumper) on a system with `.system` directory
2. Verify that `.system` contents are NOT included in backups
3. Verify that backups complete without errors related to `.system`
4. Test restore operations to ensure they work correctly

---

## Notes

- The `.system` directory structure includes:
  - `.system/logs/` - Error logs, slow query logs, audit logs
  - `.system/backup/` - Backup storage
  - `.system/innodb/` - InnoDB data files
  - `.system/binlog/` - Binary logs
  - `.system/relay/` - Relay logs

- These should never be backed up as they are:
  1. Already part of the filesystem structure
  2. Not actual MySQL databases
  3. May contain the backup files themselves (recursive backup issue)
  4. Logs that should be rotated/archived separately
