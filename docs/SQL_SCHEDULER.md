# SQL Script Scheduler

## Overview

The replication-manager now includes a comprehensive SQL script scheduler that allows you to orchestrate and schedule SQL scripts across your database clusters. This feature is comparable to the MariaDB Operator's SQL scheduler functionality.

## Features

- **Cron-based scheduling**: Use standard cron expressions to schedule SQL scripts
- **Per-cluster scheduling**: Each cluster has its own independent job scheduler
- **Database targeting**: Specify which database to execute scripts against
- **Server targeting**: Target specific servers within a cluster
- **Job persistence**: Jobs are saved to disk and restored on restart
- **Status tracking**: Monitor job execution status, results, and next run times
- **Enable/disable jobs**: Control job execution without deleting them
- **REST API**: Full API for managing scheduled jobs

## Job Structure

Each scheduled job has the following properties:

```json
{
  "id": "unique-job-id",
  "name": "Human readable name",
  "description": "Description of what the job does",
  "schedule": "0 2 * * *",
  "sql": "DELETE FROM temp_table WHERE created_at < NOW() - INTERVAL 7 DAY",
  "database": "target_database",
  "server_id": "specific-server-id",
  "enabled": true,
  "timeout": 30000000000,
  "last_run": "2025-06-13T14:17:33Z",
  "next_run": "2025-06-14T02:00:00Z",
  "created_at": "2025-06-13T14:17:33Z",
  "updated_at": "2025-06-13T14:17:33Z",
  "results": "Rows affected: 42",
  "status": "success"
}
```

### Field Descriptions

- **id**: Unique identifier for the job (auto-generated if not provided)
- **name**: Human-readable name for the job
- **description**: Optional description
- **schedule**: Cron expression using standard 5-field format (minute hour day month weekday)
- **sql**: SQL statement to execute
- **database**: Target database (optional, uses default if not specified)
- **server_id**: Target specific server (optional, uses master if not specified)
- **enabled**: Whether the job should be executed
- **timeout**: Maximum execution time in nanoseconds (default: 30 seconds)
- **last_run**: Timestamp of last execution
- **next_run**: Timestamp of next scheduled execution
- **results**: Results from last execution
- **status**: Last execution status (success, error, running)

## API Endpoints

All endpoints require authentication and are prefixed with `/api/clusters/{clusterName}/scheduler/`

### List Jobs
```
GET /api/clusters/{clusterName}/scheduler/jobs
```

Returns all scheduled jobs for the cluster.

### Create Job
```
POST /api/clusters/{clusterName}/scheduler/jobs
Content-Type: application/json

{
  "name": "Daily Cleanup",
  "description": "Clean up old data",
  "schedule": "0 2 * * *",
  "sql": "DELETE FROM logs WHERE created_at < NOW() - INTERVAL 30 DAY",
  "database": "app_db",
  "enabled": true
}
```

### Get Job
```
GET /api/clusters/{clusterName}/scheduler/jobs/{jobId}
```

### Update Job
```
PUT /api/clusters/{clusterName}/scheduler/jobs/{jobId}
Content-Type: application/json

{
  "name": "Updated Job Name",
  "schedule": "0 3 * * *",
  "enabled": false
}
```

### Delete Job
```
DELETE /api/clusters/{clusterName}/scheduler/jobs/{jobId}
```

### Enable Job
```
POST /api/clusters/{clusterName}/scheduler/jobs/{jobId}/enable
```

### Disable Job
```
POST /api/clusters/{clusterName}/scheduler/jobs/{jobId}/disable
```

## Cron Expression Format

The scheduler uses standard 5-field cron expressions:

```
* * * * *
│ │ │ │ │
│ │ │ │ └─── day of week (0-7, both 0 and 7 represent Sunday)
│ │ │ └───── month (1-12)
│ │ └─────── day of month (1-31)
│ └───────── hour (0-23)
└─────────── minute (0-59)
```

### Common Examples

- `0 2 * * *` - Daily at 2:00 AM
- `0 */6 * * *` - Every 6 hours
- `30 1 * * 0` - Weekly on Sunday at 1:30 AM
- `0 0 1 * *` - Monthly on the 1st at midnight
- `0 9 * * 1-5` - Weekdays at 9:00 AM

## Error Handling

Jobs that fail will have their status set to "error" and the error message stored in the results field. The scheduler will continue to run failed jobs according to their schedule.

## Storage

Jobs are persisted to JSON files in the cluster's working directory as `scheduled_jobs.json`. This ensures jobs are restored when the cluster is restarted.

## Integration

The scheduler integrates with the existing replication-manager database connection pooling and uses the same connection patterns as other cluster operations. Jobs execute using the `ConnExecQueryWithTimeout` method for proper timeout handling.

## Example Usage

See `examples/sql_scheduler_demo.go` for a complete example of how to use the scheduler programmatically.

## Security Considerations

- All SQL jobs are executed with the same database credentials as the cluster
- Jobs can access any database the cluster user has permissions for
- Consider the principle of least privilege when configuring database users
- SQL injection prevention relies on careful job creation and validation

## Monitoring

Job execution is logged with detailed information including:
- Job start and completion times
- SQL execution results
- Error messages for failed jobs
- Server targeting information

Use the cluster logs to monitor job execution and troubleshoot issues.
