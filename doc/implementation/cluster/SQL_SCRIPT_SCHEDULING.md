# SQL Script Scheduling and Orchestration

## Overview

This document describes the SQL script scheduling and orchestration feature implemented for replication-manager. This feature allows users to schedule and execute SQL scripts on database clusters automatically via cron expressions or manually through API/CLI.

## Architecture

### Components

1. **Job Definition** (`cluster/cluster_job_sqlscript.go`)
   - `SQLScriptJob`: Defines scheduled SQL script execution jobs
   - `SQLScriptJobResult`: Contains execution results
   - Job persistence using JSON files

2. **Scheduler Integration** (`cluster/cluster_scheduler.go`)
   - Integrates with existing cron-based scheduler
   - Supports multiple scheduled SQL script executions
   - Automatic execution based on cron expressions

3. **Configuration** (`config/config.go`)
   - 14 new configuration parameters for SQL script scheduling
   - Safety validation options
   - Path and database targeting options

4. **REST API** (`server/api_sqlscript.go`)
   - 5 new API endpoints for SQL script management
   - Support for both file-based and inline SQL execution
   - Job CRUD operations

5. **CLI Commands** (`clients/client_sqlscript.go`)
   - 5 new CLI commands for SQL script operations
   - Execute, trigger, list, save, and delete jobs

## Configuration Parameters

### Core Settings

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `scheduler-sql-scripts` | bool | false | Enable SQL script scheduling |
| `scheduler-sql-scripts-cron` | string | "" | Cron expression for scheduled execution |
| `scheduler-sql-scripts-path` | string | "" | Directory containing SQL script files |
| `scheduler-sql-scripts-database` | string | "" | Target database for scripts |
| `scheduler-sql-scripts-target-server` | string | "master" | Target server: master, slave, or specific URL |
| `scheduler-sql-scripts-timeout` | int | 300 | Script execution timeout in seconds |

### Safety Validation Settings

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `scheduler-sql-scripts-validate` | bool | true | Enable safety validation |
| `scheduler-sql-scripts-allow-drop-database` | bool | false | Allow DROP DATABASE/SCHEMA |
| `scheduler-sql-scripts-allow-drop-table` | bool | false | Allow DROP TABLE |
| `scheduler-sql-scripts-allow-truncate` | bool | false | Allow TRUNCATE |
| `scheduler-sql-scripts-allow-delete-all` | bool | false | Allow DELETE without WHERE |
| `scheduler-sql-scripts-allow-update-all` | bool | false | Allow UPDATE without WHERE |
| `scheduler-sql-scripts-alert-on-error` | bool | true | Send alerts on script failures |

## Configuration Example

```toml
[cluster-name]
# Enable SQL script scheduling
scheduler-sql-scripts = true

# Run every day at 2 AM
scheduler-sql-scripts-cron = "0 2 * * *"

# Directory containing SQL scripts
scheduler-sql-scripts-path = "/var/lib/replication-manager/sql-scripts"

# Target database (empty = use database specified in script)
scheduler-sql-scripts-database = ""

# Execute on master server
scheduler-sql-scripts-target-server = "master"

# Timeout after 10 minutes
scheduler-sql-scripts-timeout = 600

# Enable safety validation
scheduler-sql-scripts-validate = true

# Disallow dangerous operations
scheduler-sql-scripts-allow-drop-database = false
scheduler-sql-scripts-allow-drop-table = false
scheduler-sql-scripts-allow-truncate = false
scheduler-sql-scripts-allow-delete-all = false
scheduler-sql-scripts-allow-update-all = false

# Send alerts on errors
scheduler-sql-scripts-alert-on-error = true
```

## Usage

### 1. Scheduled Execution

Place SQL script files (*.sql) in the configured directory:

```bash
mkdir -p /var/lib/replication-manager/sql-scripts
cat > /var/lib/replication-manager/sql-scripts/daily_cleanup.sql <<EOF
-- Daily cleanup script
DELETE FROM logs WHERE created_at < DATE_SUB(NOW(), INTERVAL 30 DAY);
OPTIMIZE TABLE logs;
EOF
```

The script will automatically execute according to the cron schedule.

### 2. Manual Execution via CLI

```bash
# Execute a single SQL script
replication-manager-cli sql-script execute \
  --cluster=mycluster \
  --script-path=/path/to/script.sql \
  --target-database=mydb \
  --target-server=master \
  --timeout=300

# Execute inline SQL
replication-manager-cli sql-script execute \
  --cluster=mycluster \
  --script-content="SELECT COUNT(*) FROM users;" \
  --target-database=mydb

# Trigger all scheduled scripts manually
replication-manager-cli sql-script trigger-scheduled \
  --cluster=mycluster
```

### 3. Job Management via CLI

```bash
# Save a job definition
replication-manager-cli sql-script save-job \
  --cluster=mycluster \
  --name=hourly-stats \
  --script-path=/scripts/update_stats.sql \
  --target-database=analytics \
  --cron-schedule="0 * * * *" \
  --enabled=true \
  --timeout=600

# List all jobs
replication-manager-cli sql-script list-jobs \
  --cluster=mycluster

# Delete a job
replication-manager-cli sql-script delete-job \
  --cluster=mycluster \
  --name=hourly-stats
```

### 4. REST API Usage

#### Execute SQL Script

```bash
curl -X POST https://localhost:10001/api/clusters/mycluster/actions/execute-sql-script \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "scriptPath": "/path/to/script.sql",
    "targetDatabase": "mydb",
    "targetServer": "master",
    "timeout": 300
  }'
```

Response:
```json
{
  "jobId": 123,
  "jobName": "",
  "startTime": "2026-01-15T10:30:00Z",
  "endTime": "2026-01-15T10:30:05Z",
  "duration": 5.2,
  "status": "success",
  "rowsAffected": 1500,
  "errorMessage": "",
  "serverUrl": "192.168.1.10:3306",
  "scriptPath": "/path/to/script.sql",
  "targetDatabase": "mydb"
}
```

#### Execute Inline SQL

```bash
curl -X POST https://localhost:10001/api/clusters/mycluster/actions/execute-sql-script \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "scriptContent": "SELECT VERSION(); SHOW DATABASES;",
    "targetDatabase": "",
    "targetServer": "master",
    "timeout": 60
  }'
```

#### Trigger Scheduled Scripts

```bash
curl -X POST https://localhost:10001/api/clusters/mycluster/actions/trigger-scheduled-sql-scripts \
  -H "Authorization: Bearer $TOKEN"
```

#### List Jobs

```bash
curl -X GET https://localhost:10001/api/clusters/mycluster/sql-jobs \
  -H "Authorization: Bearer $TOKEN"
```

#### Save Job

```bash
curl -X POST https://localhost:10001/api/clusters/mycluster/sql-jobs/save \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "name": "nightly-backup-verification",
    "scriptPath": "/scripts/verify_backup.sql",
    "targetDatabase": "mysql",
    "targetServer": "master",
    "cronSchedule": "0 3 * * *",
    "enabled": true,
    "runOnce": false,
    "maxRetries": 3,
    "timeout": 1800
  }'
```

#### Delete Job

```bash
curl -X DELETE https://localhost:10001/api/clusters/mycluster/sql-jobs/nightly-backup-verification \
  -H "Authorization: Bearer $TOKEN"
```

## Security Features

### 1. Safety Validation

The system includes configurable validation to prevent dangerous operations:

- **DROP DATABASE/SCHEMA**: Controlled by `scheduler-sql-scripts-allow-drop-database`
- **DROP TABLE**: Controlled by `scheduler-sql-scripts-allow-drop-table`
- **TRUNCATE**: Controlled by `scheduler-sql-scripts-allow-truncate`
- **DELETE without WHERE**: Controlled by `scheduler-sql-scripts-allow-delete-all`
- **UPDATE without WHERE**: Controlled by `scheduler-sql-scripts-allow-update-all`

### 2. Execution Isolation

- Scripts execute with `sql_log_bin=0` to avoid replication conflicts
- Separate connection for each script execution
- Timeout protection to prevent runaway queries

### 3. Authentication & Authorization

- All API endpoints require JWT token authentication
- Cluster-level access control through existing authentication system

### 4. SQL Injection Protection

- Uses parameterized queries where applicable
- Script content validation before execution
- Statement parsing to handle multi-statement scripts safely

## Job Execution Flow

1. **Job Creation**
   - Job record inserted into `replication_manager_schema.jobs` table
   - Job state set to `JobStateRunning` (1)

2. **Script Processing**
   - Read script from file or use inline content
   - Validate script against safety rules
   - Parse into individual SQL statements

3. **Execution**
   - Get isolated database connection (no binlog)
   - Switch to target database if specified
   - Execute each statement sequentially with timeout
   - Track rows affected

4. **Result Recording**
   - Update job state to `JobStateSuccess` (4) or `JobStateErrorExec` (5)
   - Store result message with execution details
   - Log execution to cluster logs

5. **Alerting**
   - Send alert if configured and execution failed
   - Include error details and script information

## Job States

| State | Value | Description |
|-------|-------|-------------|
| JobStateAvailable | 0 | Ready to run |
| JobStateRunning | 1 | Currently executing |
| JobStateHalted | 2 | Paused |
| JobStateFinished | 3 | Just completed |
| JobStateSuccess | 4 | Completed successfully |
| JobStateErrorExec | 5 | Failed during execution |
| JobStateErrorAfter | 6 | Failed during post-processing |

## Monitoring & Logging

### Job History

All job executions are tracked in the `replication_manager_schema.jobs` table:

```sql
SELECT 
  id, task, server, state, result, 
  start, end, 
  TIMESTAMPDIFF(SECOND, start, end) as duration_seconds
FROM replication_manager_schema.jobs
WHERE task LIKE 'sqlscript%'
ORDER BY start DESC
LIMIT 10;
```

### Cluster Logs

Script execution is logged with module `ConstLogModTask`:

```
[INFO] SQL script /scripts/cleanup.sql executed successfully on 192.168.1.10:3306: 1500 rows affected in 5.20s
[ERR] SQL script execution failed on 192.168.1.10:3306: Error 1064: Syntax error
```

### API Response

Execution results include comprehensive information:
- Job ID for tracking
- Start/end timestamps
- Duration in seconds
- Status (success/error/timeout)
- Rows affected
- Error message (if applicable)
- Server URL
- Script path/content

## Script Parsing

The system includes intelligent SQL statement parsing:

1. **Multi-statement Support**: Scripts with multiple statements separated by `;` are split and executed sequentially
2. **Comment Handling**: Single-line (`--`, `#`) and multi-line (`/* */`) comments are ignored
3. **String Detection**: Semicolons inside strings are not treated as delimiters
4. **Whitespace Normalization**: Leading/trailing whitespace is removed

## Best Practices

### 1. Script Organization

```
/var/lib/replication-manager/sql-scripts/
├── 01_cleanup_logs.sql
├── 02_update_statistics.sql
├── 03_optimize_tables.sql
└── 99_housekeeping.sql
```

Scripts execute in alphabetical order.

### 2. Script Naming

Use prefixes to control execution order:
- `01_`, `02_`, etc. for sequential execution
- Descriptive names: `cleanup_`, `update_`, `optimize_`

### 3. Error Handling in Scripts

```sql
-- Use conditional logic
DELETE FROM logs WHERE created_at < DATE_SUB(NOW(), INTERVAL 30 DAY) AND id > 0;

-- Use IGNORE for optional operations
CREATE TABLE IF NOT EXISTS temp_results (id INT);

-- Check before destructive operations
SELECT COUNT(*) FROM table_to_truncate INTO @cnt;
-- (Requires multiple scripts or stored procedure)
```

### 4. Testing

Test scripts manually before scheduling:

```bash
# Test with dry run (if applicable)
mysql -u user -p database < script.sql

# Test via CLI without scheduling
replication-manager-cli sql-script execute \
  --cluster=test-cluster \
  --script-path=/path/to/script.sql \
  --target-database=test_db
```

### 5. Monitoring

- Enable `scheduler-sql-scripts-alert-on-error`
- Review job history regularly
- Monitor execution duration for performance issues

## Troubleshooting

### Script Not Executing

1. Check scheduler is enabled: `monitoring-scheduler = true`
2. Verify SQL script scheduler is enabled: `scheduler-sql-scripts = true`
3. Check cron expression syntax: `scheduler-sql-scripts-cron`
4. Verify script directory exists and contains `.sql` files
5. Review cluster logs for errors

### Permission Errors

1. Ensure database user has required privileges
2. Check `db-servers-credential` configuration
3. Verify target server is reachable

### Safety Validation Errors

If scripts fail with "not allowed" errors:
1. Review safety configuration parameters
2. Consider if operation is truly necessary
3. Adjust `scheduler-sql-scripts-allow-*` settings if appropriate

### Timeout Issues

1. Increase `scheduler-sql-scripts-timeout` value
2. Optimize SQL queries
3. Split large scripts into smaller parts
4. Consider running during low-traffic periods

## API Endpoints Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/clusters/{clusterName}/actions/execute-sql-script` | POST | Execute SQL script immediately |
| `/api/clusters/{clusterName}/actions/trigger-scheduled-sql-scripts` | POST | Trigger scheduled scripts manually |
| `/api/clusters/{clusterName}/sql-jobs` | GET | List all SQL script jobs |
| `/api/clusters/{clusterName}/sql-jobs/save` | POST | Save job definition |
| `/api/clusters/{clusterName}/sql-jobs/{jobName}` | DELETE | Delete job definition |

## CLI Commands Reference

| Command | Description |
|---------|-------------|
| `sql-script execute` | Execute an SQL script |
| `sql-script trigger-scheduled` | Trigger scheduled scripts manually |
| `sql-script list-jobs` | List all SQL script jobs |
| `sql-script save-job` | Save job definition |
| `sql-script delete-job` | Delete job definition |

## File Locations

| Component | File Path |
|-----------|-----------|
| Job implementation | `cluster/cluster_job_sqlscript.go` |
| Scheduler integration | `cluster/cluster_scheduler.go` |
| Configuration | `config/config.go` |
| REST API handlers | `server/api_sqlscript.go` |
| CLI commands | `clients/client_sqlscript.go` |
| Job definitions storage | `{working-dir}/{cluster-name}/sql_jobs/*.json` |
| SQL scripts directory | Configured via `scheduler-sql-scripts-path` |

## Future Enhancements

Potential improvements for future versions:

1. **Job Templates**: Pre-defined job templates for common tasks
2. **Dependency Management**: Execute jobs in specific order based on dependencies
3. **Conditional Execution**: Execute based on cluster state or custom conditions
4. **Result Storage**: Store script output for review
5. **Rollback Support**: Automatic rollback on failure
6. **Variable Substitution**: Support for runtime variable replacement in scripts
7. **Parallel Execution**: Execute scripts on multiple servers simultaneously
8. **Web UI Integration**: Graphical interface for job management
9. **Audit Trail**: Enhanced audit logging for compliance
10. **Notification Channels**: Multiple notification methods beyond alerts

## Contributing

When modifying the SQL script scheduling feature:

1. Follow existing code patterns in the cluster package
2. Maintain backward compatibility with configuration
3. Add tests for new functionality
4. Update this documentation
5. Test with various SQL dialects (MariaDB, MySQL, Percona)

## Related Documentation

- Main scheduler documentation: `doc/scheduler.md`
- Job system documentation: `doc/jobs.md`
- API documentation: `doc/api.md`
- Configuration reference: `doc/configuration.md`
