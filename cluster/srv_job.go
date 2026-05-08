// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/state"
)

type DBTask struct {
	task string
	ct   int
	id   int64
}

type jobCancelEntry struct {
	cancel        context.CancelFunc
	userRequested bool
}

/*
  - 0-2	Indicates Job still not done yet
  - 3		Indicate it's finished recently and check if there is post-job task
  - 4-6	Job completed
    either success or failed
*/
var (
	JobStateAvailable  int = 0
	JobStateRunning    int = 1
	JobStateHalted     int = 2
	JobStateFinished   int = 3
	JobStateSuccess    int = 4
	JobStateErrorExec  int = 5
	JobStateErrorAfter int = 6
)

// Timeout for getting job records
var JobTimeout time.Duration = time.Second

func (server *ServerMonitor) JobRun() {

}

func (server *ServerMonitor) JobsCheckSchedulerTable() error {
	cluster := server.ClusterGroup
	if cluster.Conf.SchedulerJobsMode == "api" {
		return nil
	}
	if server.IsDown() || cluster.IsInFailover() {
		return nil
	}

	//If no default connection no alert
	if server.Conn == nil {
		return nil
	}

	Conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Failed to create connection: %v", err)
	}
	defer Conn.Close()

	query := `SELECT
    COUNT(*) AS columns_present
FROM information_schema.columns
WHERE table_schema = 'replication_manager_schema'
  AND table_name = 'jobs'
  AND (
      (column_name='id'     AND column_type='int(11)'      AND is_nullable='NO' AND extra LIKE '%auto_increment%')
   OR (column_name='task'   AND column_type='varchar(20)'  AND is_nullable='YES')
   OR (column_name='port'   AND column_type='int(11)'      AND is_nullable='YES')
   OR (column_name='server' AND column_type='varchar(255)' AND is_nullable='YES')
   OR (column_name='done'   AND column_type='tinyint(4)'   AND is_nullable='NO' AND column_default='0')
   OR (column_name='state'  AND column_type='tinyint(4)'   AND is_nullable='NO' AND column_default='0')
   OR (column_name='result' AND column_type='mediumtext'   AND is_nullable='YES')
   OR (column_name='payload' AND column_type='mediumtext'  AND is_nullable='YES')
   OR (column_name='start'  AND column_type='datetime'     AND is_nullable='YES')
   OR (column_name='end'    AND column_type='datetime'     AND is_nullable='YES')
  );`

	var exist int
	err = server.ConnGetQueryWithTimeout(Conn, JobTimeout, &exist, query)
	if err != nil {
		return fmt.Errorf("Failed to check jobs table: %v", err)
	}

	if exist != 10 {
		err = server.jobsCreateTable()
		if err != nil {
			return fmt.Errorf("Failed to recreate jobs table: %v", err)
		}
	}

	return nil
}

func (server *ServerMonitor) JobsCreateTable() error {
	cluster := server.ClusterGroup
	// In API mode the jobs table is not used — but still ensure the
	// replication_manager_schema database exists (needed by checksum, benchmarks, etc.)
	if cluster.Conf.SchedulerJobsMode == "api" {
		server.ensureReplicationManagerSchema()
		server.jobsDropTable()
		return nil
	}
	if err := server.jobsCreateTable(); err != nil {
		cluster.SetState("WARN0153", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0153"], server.URL, err), ErrFrom: "JOB"})
	}

	return nil
}

// ensureReplicationManagerSchema creates the replication_manager_schema database
// only if it doesn't already exist. Needed even in API mode for checksum, benchmarks, etc.
func (server *ServerMonitor) ensureReplicationManagerSchema() {
	if server.IsDown() || server.Conn == nil {
		return
	}
	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return
	}
	defer conn.Close()
	var count int
	server.ConnGetQueryWithTimeout(conn, JobTimeout, &count,
		"/* replication-manager */ SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='replication_manager_schema'")
	if count == 0 {
		server.ConnExecQueryWithTimeout(conn, JobTimeout, "/* replication-manager */ CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	}
}

// jobsDropTable drops the jobs table when switching to API mode.
// Checks information_schema first to avoid unnecessary DDL.
// Uses sql_log_bin=0 to avoid replication.
func (server *ServerMonitor) jobsDropTable() {
	cluster := server.ClusterGroup
	if server.IsDown() || server.Conn == nil {
		return
	}
	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return
	}
	defer conn.Close()
	var count int
	err = server.ConnGetQueryWithTimeout(conn, JobTimeout, &count,
		"/* replication-manager */ SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='replication_manager_schema' AND table_name='jobs'")
	if err != nil || count == 0 {
		return
	}
	_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "/* replication-manager */ DROP TABLE replication_manager_schema.jobs")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to drop jobs table on %s: %s", server.URL, err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Dropped jobs table on %s (switched to API mode)", server.URL)
	}
}

func (server *ServerMonitor) jobsCreateTable() error {

	cluster := server.ClusterGroup
	if server.IsDown() || cluster.IsInFailover() {
		return nil
	}

	//If no default connection no alert
	if server.Conn == nil {
		return nil
	}

	Conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Failed to create connection: %v", err)
	}
	defer Conn.Close()

	master := cluster.GetMaster()
	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	// Check if schema exists before CREATE to avoid metadata lock on every tick
	var schemaExists int
	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &schemaExists,
		"/* replication-manager */ SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='replication_manager_schema'")
	if schemaExists == 0 {
		_, err = server.ConnExecQueryWithTimeout(Conn, JobTimeout, "/* replication-manager */ CREATE DATABASE IF NOT EXISTS replication_manager_schema")
		if err != nil {
			return fmt.Errorf("Failed to create replication_manager_schema: %v", err)
		}
	}

	createquery := `/* replication-manager */ CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, state tinyint not null default 0, result MEDIUMTEXT, payload MEDIUMTEXT, start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task), KEY idx3 (task, state), UNIQUE(task)) engine=innodb`

	// Check if jobs table exists before CREATE to avoid metadata lock on every tick
	var tableExists int
	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &tableExists,
		"/* replication-manager */ SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='replication_manager_schema' AND table_name='jobs'")
	if tableExists == 0 {
		_, err = server.ConnExecQueryWithTimeout(Conn, JobTimeout, createquery)
		if err != nil {
			return fmt.Errorf("Failed to create jobs table: %v", err)
		}
	}

	var exist int
	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &exist, "/* replication-manager */ SELECT COUNT(CASE WHEN COLUMN_KEY = 'UNI' THEN 1 END) AS num_task_unique FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' GROUP BY table_name")

	if exist == 0 {
		server.ConnExecQueryWithTimeout(Conn, JobTimeout, "DROP TABLE IF EXISTS replication_manager_schema.jobs")
		_, err := server.ConnExecQueryWithTimeout(Conn, JobTimeout, createquery)
		if err != nil {
			return fmt.Errorf("Failed to create jobs table: %v", err)
		}
	}

	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &exist, "SELECT COUNT(*) col_exists FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' AND COLUMN_NAME = 'state'")
	if exist == 0 {
		//Add column instead of changing create table for compatibility
		_, err := server.ConnExecQueryWithTimeout(Conn, JobTimeout, "ALTER TABLE replication_manager_schema.jobs ADD COLUMN state tinyint not null default 0 AFTER `done`")
		if err != nil {
			return fmt.Errorf("Failed to add column on jobs table: %v", err)
		}

		//Add index
		_, err = server.ConnExecQueryWithTimeout(Conn, JobTimeout, "ALTER TABLE replication_manager_schema.jobs ADD INDEX idx3 (task, state)")
		if err != nil {
			return fmt.Errorf("Failed to add index on jobs table: %v", err)
		}
	}

	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &exist, "SELECT COUNT(*) col_exists FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' AND COLUMN_NAME = 'result' AND COLUMN_TYPE not like '%MEDIUMTEXT%'")
	if exist == 1 {
		_, err := server.ConnExecQueryWithTimeout(Conn, JobTimeout, "ALTER TABLE replication_manager_schema.jobs MODIFY COLUMN result MEDIUMTEXT DEFAULT NULL;")
		if err != nil {
			return fmt.Errorf("Failed to modify column result to MEDIUMTEXT on jobs table: %v", err)
		}
	}
	server.ConnGetQueryWithTimeout(Conn, JobTimeout, &exist, "SELECT COUNT(*) col_exists FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' AND COLUMN_NAME = 'payload'")
	if exist == 0 {
		_, err := server.ConnExecQueryWithTimeout(Conn, JobTimeout, "ALTER TABLE replication_manager_schema.jobs ADD COLUMN payload MEDIUMTEXT DEFAULT NULL AFTER result")
		if err != nil {
			return fmt.Errorf("Failed to add column payload on jobs table: %v", err)
		}
	}

	return nil
}

func jobTaskFreshnessTimestamp(task *config.Task) int64 {
	if task == nil {
		return 0
	}

	if task.End > 0 {
		return task.End
	}

	return task.Start
}

func jobTaskHasMeaningfulChanges(cached *config.Task, dbTask *config.Task) bool {
	if cached == nil || dbTask == nil {
		return cached != dbTask
	}

	return cached.Id != dbTask.Id ||
		cached.Task != dbTask.Task ||
		cached.Port != dbTask.Port ||
		cached.Server != dbTask.Server ||
		cached.Done != dbTask.Done ||
		cached.State != dbTask.State ||
		cached.Result != dbTask.Result ||
		cached.Payload != dbTask.Payload ||
		cached.Start != dbTask.Start ||
		cached.End != dbTask.End
}

func shouldUpdateCachedJobTask(cached *config.Task, dbTask *config.Task) bool {
	if dbTask == nil {
		return false
	}

	if cached == nil {
		return true
	}

	dbFreshness := jobTaskFreshnessTimestamp(dbTask)
	cachedFreshness := jobTaskFreshnessTimestamp(cached)

	if dbFreshness > cachedFreshness {
		return true
	}

	if dbFreshness < cachedFreshness {
		return false
	}

	return jobTaskHasMeaningfulChanges(cached, dbTask)
}

func (server *ServerMonitor) JobsUpdateEntries(Conn *sqlx.Conn) error {
	query := "SELECT id, task, port, server, done, state, result, payload, floor(UNIX_TIMESTAMP(start)) start, floor(UNIX_TIMESTAMP(end)) end FROM replication_manager_schema.jobs"

	ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
	rows, err := Conn.QueryContext(ctx, query)
	if err != nil {
		cancel()
		err2 := server.JobsCreateTable()
		if err2 != nil {
			return fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}

		ctx, cancel = context.WithTimeout(context.Background(), JobTimeout)
		rows, err = Conn.QueryContext(ctx, query)
		if err != nil {
			cancel()
			return fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}
	}
	defer rows.Close()
	defer cancel()

	for rows.Next() {
		var t config.Task
		var res sql.NullString
		var payload sql.NullString
		var end sql.NullInt64
		err := rows.Scan(&t.Id, &t.Task, &t.Port, &t.Server, &t.Done, &t.State, &res, &payload, &t.Start, &end)
		if err != nil {
			return fmt.Errorf("Failed to scan row values on jobs table: %v. Row: %v", err, t)
		}
		t.Result = res.String
		t.Payload = payload.String
		t.End = end.Int64
		if v, exists := server.JobResults.LoadOrStore(t.Task, &t); !exists {
			continue
		} else if shouldUpdateCachedJobTask(v, &t) {
			v.Set(t)
		}
	}

	server.SetNeedRefreshJobs(false)

	return nil
}

func (server *ServerMonitor) JobsHasEntries() bool {
	hasEntries := false
	server.JobResults.Range(func(k, v any) bool {
		hasEntries = true
		return false
	})
	return hasEntries
}

func (server *ServerMonitor) JobsRefreshEntries() error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return nil
	}
	if cluster.IsInFailover() || cluster.InRollingRestart {
		return nil
	}
	if server.IsDown() {
		return nil
	}
	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}
	if !server.TryStartLoadingJobList() {
		return errors.New("Waiting for previous update")
	}
	defer server.SetLoadingJobList(false)
	server.MarkJobsRefreshAttempt(time.Now())

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer conn.Close()

	return server.JobsUpdateEntries(conn)
}

func (server *ServerMonitor) JobInsertTask(task string, port string, repmanhost string) (int64, error) {
	return server.jobInsertTask(task, port, repmanhost, nil)
}

func (server *ServerMonitor) JobInsertTaskWithPayload(task string, port string, repmanhost string, payload string) (int64, error) {
	return server.jobInsertTask(task, port, repmanhost, &payload)
}

func (server *ServerMonitor) jobInsertTask(task string, port string, repmanhost string, payload *string) (int64, error) {
	cluster := server.ClusterGroup
	if cluster.InRollingRestart {
		return 0, errors.New("In rolling restart")
	}

	if cluster.IsInFailover() {
		return 0, errors.New("In failover")
	}

	master := cluster.GetMaster()
	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return 0, errors.New("In super read-only")
	}

	// In API mode, dispatch depends on the task's execution mode.
	// Remote tasks: set a cookie so the dbjobs script discovers them via the needs API.
	// Local tasks (mysqldump, mydumper): only track state in memory — repman runs them directly.
	if cluster.Conf.SchedulerJobsMode == "api" {
		if cluster.Conf.SchedulerJobsExecOverrides == nil {
			cluster.Conf.ParseJobsExecOverrides()
		}
		if config.IsRemoteTask(config.TaskName(task), cluster.Conf.SchedulerJobsExecOverrides) {
			if err := server.setTaskCookie(task); err != nil {
				return 0, fmt.Errorf("Failed to set cookie for remote task %s: %v", task, err)
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "API mode: set cookie for task %s on %s", task, server.URL)
		} else {
			// Local task — track in memory only, no cookie, no dbjobs dispatch
			server.JobsUpdateState(task, "", 0, 0)
		}
		return 0, nil
	}

	if server.Conn == nil {
		return 0, fmt.Errorf("No pool connection")
	}

	// Create jobs table if not exists yet
	server.JobsCreateTable()

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return 0, fmt.Errorf("Job can't connect: %v", err)
	}
	defer conn.Close()

	if task == "" {
		return 0, errors.New("Job can't insert empty task")
	}

	query := "SELECT id, task, done, state FROM replication_manager_schema.jobs WHERE id = (SELECT max(id) FROM replication_manager_schema.jobs WHERE task = '" + task + "')"
	ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		cancel()
		return 0, fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
	}
	defer rows.Close()
	defer cancel()

	t, _ := server.JobResults.LoadOrStore(task, &config.Task{Task: task, Start: time.Now().Unix()})
	nr := 0
	for rows.Next() {
		nr = 1
		rows.Scan(&t.Id, &t.Task, &t.Done, &t.State)

		if t.State <= 3 && t.Done == 0 {
			return 0, fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}
	}
	rows.Close()
	cancel()

	//delete row to reset all values
	if nr > 0 {
		_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, fmt.Sprintf("DELETE FROM replication_manager_schema.jobs WHERE ID = %d", t.Id))
		if err != nil {
			return 0, fmt.Errorf("Failed to delete row on jobs table for %s: %v", t.Task, err)
		}
	}

	//Reuse the same id
	var res sql.Result
	if payload == nil {
		res, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "INSERT INTO replication_manager_schema.jobs(id, task, port, server, start) VALUES(?, ?, ?, ?, NOW())", t.Id, task, port, repmanhost)
	} else {
		res, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "INSERT INTO replication_manager_schema.jobs(id, task, port, server, payload, start) VALUES(?, ?, ?, ?, ?, NOW())", t.Id, task, port, repmanhost, *payload)
	}
	if err != nil {
		return 0, fmt.Errorf("Failed to insert row on jobs table for %s: %v", t.Task, err)
	}

	if payload != nil {
		t.Payload = *payload
	}

	server.SetNeedRefreshJobs(true)
	return res.LastInsertId()
}

// setTaskCookie maps a remote task name to the corresponding wait cookie for
// API mode. Only remote tasks (executed by the dbjobs script on the database
// host) are listed here. Logical tasks (mysqldump, mydumper, analyze, etc.)
// are executed directly by replication-manager and must NOT have cookies set,
// otherwise the dbjobs script would also attempt to run them.
func (server *ServerMonitor) setTaskCookie(task string) error {
	switch config.TaskName(task) {
	// Physical backup — dbjobs runs xtrabackup/mariabackup on DB host
	case config.ConstTaskXB, config.ConstTaskMB:
		return server.SetWaitPhysicalBackupCookie()
	// Optimize — dbjobs runs mysqlcheck on DB host
	case config.ConstTaskOptimize:
		return server.SetWaitOptimizeCookie()
	// Service control — dbjobs runs systemctl on DB host
	case config.ConstTaskRestart:
		return server.SetWaitRestartCookie()
	case config.ConstTaskStop:
		return server.SetWaitStopCookie()
	case config.ConstTaskStart:
		return server.SetWaitStartCookie()
	// Reseed/flashback — dbjobs receives stream and prepares on DB host
	case config.ConstTaskReseedXB:
		return server.SetWaitReseedXtrabackupCookie()
	case config.ConstTaskReseedMB:
		return server.SetWaitReseedMariabackupCookie()
	case config.ConstTaskFlashXB:
		return server.SetWaitFlashbackXtrabackupCookie()
	case config.ConstTaskFlashMB:
		return server.SetWaitFlashbackMariabackupCookie()
	// Log collection — dbjobs reads local log files on DB host
	case config.ConstTaskError:
		return server.SetWaitErrorlogCookie()
	case config.ConstTaskSlowQuery:
		return server.SetWaitSlowqueryCookie()
	case config.ConstTaskAuditLog:
		return server.SetWaitAuditlogCookie()
	case config.ConstTaskSqlError:
		return server.SetWaitSqlErrorlogCookie()
	// ZFS — dbjobs runs zfs rollback on DB host
	case config.ConstTaskZFS:
		return server.createCookie("cookie_waitzfssnapback")
	default:
		return fmt.Errorf("no cookie mapping for task %s", task)
	}
}

func (server *ServerMonitor) HasRunningDBJobs() (bool, error) {
	if server.ClusterGroup.Conf.SchedulerJobsMode == "api" {
		return false, nil
	}
	if server.Conn == nil {
		return false, errors.New("no pool connection")
	}

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return false, err
	}
	if conn == nil {
		return false, errors.New("no connection established")
	}
	defer conn.Close()

	query := "SELECT COUNT(*) FROM replication_manager_schema.jobs WHERE done=0 AND state IN (?, ?)"
	var count int
	err = server.ConnGetQueryWithTimeout(conn, JobTimeout, &count, query, JobStateRunning, JobStateHalted)
	if err != nil {
		if isTableMissingError(err) {
			return false, nil
		}
		return false, err
	}

	return count > 0, nil
}

func (server *ServerMonitor) JobServerStop() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTask("stop", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Stop server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerRestart() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTask("restart", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restart server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobOptimize() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTask("optimize", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobZFSSnapBack() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTask("zfssnapback", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobsCheckRunning() error {
	cluster := server.ClusterGroup
	if cluster.Conf.SchedulerJobsMode == "api" {
		return server.jobsCheckRunningFromMemory()
	}
	if server.IsDown() || cluster.InRollingRestart {
		return nil
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	Conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer Conn.Close()

	tasks, err := server.GetTasksByState(Conn, JobStateAvailable)
	if err != nil {
		return fmt.Errorf("Error retrieving jobs on %s: %s", server.URL, err)
	}

	for _, task := range tasks {
		if task.ct > 10 {
			cluster.SetState("ERR00060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["ERR00060"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			purge := "DELETE from replication_manager_schema.jobs WHERE task='" + task.task + "' AND done=0 AND result IS NULL order by start asc limit  " + strconv.Itoa(task.ct-1)
			_, err := server.ConnExecQueryWithTimeout(Conn, JobTimeout, purge)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error purging replication_manager_schema.jobs %s", err)
			}
		} else {
			if task.task == "optimized" {
				cluster.SetState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "restart" {
				cluster.SetState("WARN0096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0096"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "stop" {
				cluster.SetState("WARN0097", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0097"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "xtrabackup" {
				cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "mariabackup" {
				cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "reseedxtrabackup" {
				cluster.SetState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "reseedmariabackup" {
				cluster.SetState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "reseedmysqldump" {
				cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "reseedmydumper" {
				cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "flashbackxtrabackup" {
				cluster.SetState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "flashbackmariabackup" {
				cluster.SetState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "flashbackmydumper" {
				cluster.SetState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			} else if task.task == "flashbackmysqldump" {
				cluster.SetState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			}
		}
	}

	return nil
}

// jobsCheckRunningFromMemory scans JobResults for pending tasks in API mode
// and sets the same WARN states as the SQL-based JobsCheckRunning.
func (server *ServerMonitor) jobsCheckRunningFromMemory() error {
	cluster := server.ClusterGroup
	server.JobResults.Range(func(k, v any) bool {
		t := v.(*config.Task)
		if t.Done == 1 {
			return true // skip completed
		}
		switch config.TaskName(t.Task) {
		case config.ConstTaskOptimize:
			cluster.SetState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskRestart:
			cluster.SetState("WARN0096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0096"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskStop:
			cluster.SetState("WARN0097", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0097"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskXB, config.ConstTaskMB:
			cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], t.Task, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskReseedXB, config.ConstTaskReseedMB:
			cluster.SetState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], t.Task, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskReseedDump:
			cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], t.Task, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskFlashXB, config.ConstTaskFlashMB:
			cluster.SetState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], t.Task, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		case config.ConstTaskFlashDump:
			cluster.SetState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], t.Task, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		}
		return true
	})
	return nil
}

func (server *ServerMonitor) JobsCheckPending(Conn *sqlx.Conn) error {
	if server.ClusterGroup.Conf.SchedulerJobsMode == "api" {
		return nil
	}
	var err error
	// Prevent interrupting current reseed
	if inReseed, task := server.GetReseedingState(); inReseed {
		return fmt.Errorf("Server is in reseeding state by %s", task)
	}
	// Set timeout for old task
	server.ConnExecQueryWithTimeout(Conn, JobTimeout, "UPDATE replication_manager_schema.jobs SET state=5, result='Timeout waiting for job to start', done=1, end=now() where state=0 and start <= DATE_SUB(NOW(), interval 1 hour)")

	tasks, err := server.GetTasksByState(Conn, JobStateHalted)
	if err != nil {
		return fmt.Errorf("Error retrieving pending tasks on %s: %s", server.URL, err)
	}

	for _, task := range tasks {
		if strings.HasPrefix(task.task, "reseed") || strings.HasPrefix(task.task, "flashback") {
			res := "Replication-manager is down while preparing task, cancelling operation for data safety."
			query := "UPDATE replication_manager_schema.jobs SET state=5, done=1, end=now(), result='%s' where task = '%s'"
			server.ConnExecQueryWithTimeout(Conn, JobTimeout, fmt.Sprintf(query, res, task.task))
			server.SetNeedRefreshJobs(true)
		}
	}

	return nil
}

func (server *ServerMonitor) JobsCheckErrors(Conn *sqlx.Conn) error {
	var err error
	cluster := server.ClusterGroup

	query := "SELECT task, result FROM replication_manager_schema.jobs WHERE done=0 AND state=5"
	ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
	rows, err := Conn.QueryContext(ctx, query)
	if err != nil {
		cancel()
		err2 := server.JobsCreateTable()
		if err2 != nil {
			return fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}

		ctx, cancel = context.WithTimeout(context.Background(), JobTimeout)
		rows, err = Conn.QueryContext(ctx, query)
		if err != nil {
			cancel()
			return fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}
	}
	defer rows.Close()
	defer cancel()

	ct := 0
	p := make([]string, 0)
	for rows.Next() {
		ct++
		var task, result sql.NullString
		rows.Scan(&task, &result)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job %s ended with ERROR: %s", task.String, result.String)
		p = append(p, "'"+task.String+"'")
		switch task.String {
		case "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariabackup":
			if server.HasWaitResticReseedCookie() {
				if err := server.DelWaitResticReseedCookie(); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
						"Failed to clear restic reseed cookie after job error for %s: %s", task.String, err)
				}
			}
			server.cleanupResticReseedForTask(task.String, "job-error")
			if server.HasReseedingState(task.String) {
				defer server.SetInReseedBackup("")
			}
		case "xtrabackup", "mariabackup":
			cluster.SetState("WARN0115", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0115"], ErrFrom: "JOB", ServerUrl: server.URL})
		}
	}

	if ct > 0 {
		query := "UPDATE replication_manager_schema.jobs SET done=1 WHERE done=0 AND state=5 and task in (%s)"
		server.ExecQueryNoBinLog(fmt.Sprintf(query, strings.Join(p, ",")), JobTimeout)
		server.SetNeedRefreshJobs(true)
	}

	return err
}

func (server *ServerMonitor) registerJobCancel(task string, cancel context.CancelFunc) {
	if strings.TrimSpace(task) == "" || cancel == nil {
		return
	}
	server.jobCancelMutex.Lock()
	if server.jobCancelEntries == nil {
		server.jobCancelEntries = make(map[string]*jobCancelEntry)
	}
	server.jobCancelEntries[task] = &jobCancelEntry{cancel: cancel}
	server.jobCancelMutex.Unlock()
}

func (server *ServerMonitor) clearJobCancel(task string) {
	if strings.TrimSpace(task) == "" {
		return
	}
	server.jobCancelMutex.Lock()
	if server.jobCancelEntries != nil {
		if server.jobCancelEntries[task] != nil {
			delete(server.jobCancelEntries, task)
		}
	}
	server.jobCancelMutex.Unlock()
}

func (server *ServerMonitor) requestJobCancel(task string) bool {
	task = strings.TrimSpace(task)
	if task == "" {
		return false
	}
	var cancel context.CancelFunc
	server.jobCancelMutex.Lock()
	entry := server.jobCancelEntries[task]
	if entry != nil {
		entry.userRequested = true
		cancel = entry.cancel
	}
	server.jobCancelMutex.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return entry != nil
}

func (server *ServerMonitor) isJobCancelRequested(task string) bool {
	task = strings.TrimSpace(task)
	if task == "" {
		return false
	}
	server.jobCancelMutex.Lock()
	entry := server.jobCancelEntries[task]
	requested := entry != nil && entry.userRequested
	server.jobCancelMutex.Unlock()
	return requested
}

func (server *ServerMonitor) JobsCancelTasks(force bool, tasks ...string) error {
	var err error
	var canCancel bool = true
	cluster := server.ClusterGroup
	cleanupRequested := false
	cleanupCanceled := func() {
		for _, task := range tasks {
			server.cleanupResticReseedForTask(task, "job-canceled")
		}
	}
	defer func() {
		if cleanupRequested {
			cleanupCanceled()
		}
	}()

	master := cluster.GetMaster()
	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	//Check for already running task
	server.JobResults.Range(func(k, v any) bool {
		key := k.(string)
		val := v.(*config.Task)
		if slices.Contains(tasks, key) {
			if server.HasReseedingState(key) && val.State > 0 {
				canCancel = false
			}
		}
		return true
	})

	if !(canCancel || force) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to cancel tasks. No rows found or tasks already started", server.URL)
	}

	cancelRequested := false
	for _, task := range tasks {
		if server.requestJobCancel(task) {
			cancelRequested = true
		}
	}
	if cancelRequested {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Cancel requested for running tasks on %s", server.URL)
	}

	if !cluster.Conf.MonitorScheduler {
		for _, task := range tasks {
			server.JobsUpdateState(task, "cancelled by user", 5, 1)
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
		}
		cleanupRequested = true
	}

	if server.IsDown() {
		if canCancel || force {
			server.SetInReseedBackup("")
			server.SetNeedRefreshJobs(true)
			cleanupRequested = true
		}
		return nil
	}

	if server.Conn == nil {
		return nil
	}

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer conn.Close()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Cancelling tasks on %s as requested", server.URL)
	//Using lock to prevent wrong reads
	_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "LOCK TABLES replication_manager_schema.jobs WRITE;")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't lock table jobs for cancel task")
		return err
	}
	defer server.ConnExecQueryWithTimeout(conn, JobTimeout, "UNLOCK TABLES;")

	query := "UPDATE replication_manager_schema.jobs SET done=1, state=5, result='cancelled by user' WHERE done=0 AND state=0 and task in (?);"
	if force {
		query = "UPDATE replication_manager_schema.jobs SET done=1, state=5, result='cancelled by user' WHERE task in (?);"
	}

	query, args, err := sqlx.In(query, tasks)
	if err != nil {
		return fmt.Errorf("Error processing args %s: %s", server.URL, err)
	}

	// Rebind the query to match the database's bind type
	query = conn.Rebind(query)

	var res sql.Result
	res, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, query, args...)
	if err != nil {
		return fmt.Errorf("Error exec query for cancel tasks on %s: %s", server.URL, err)
	}

	aff, err := res.RowsAffected()
	if err == nil {
		if aff > 0 {
			for _, task := range tasks {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
			}
			cleanupRequested = true
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Task cancelled successfully on %s", server.URL)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to cancel task on %s. No rows found or task already started", server.URL)
		}
	}

	server.SetNeedRefreshJobs(true)

	return err
}

func (server *ServerMonitor) JobsCheckStates() error {
	var err error
	cluster := server.ClusterGroup

	if cluster.IsInFailover() {
		return nil
	}

	if cluster.InRollingRestart {
		return nil
	}

	if server.IsDown() {
		return nil
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	if !server.TryStartLoadingJobList() {
		return errors.New("Waiting for previous update")
	}
	defer server.SetLoadingJobList(false)

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer conn.Close()

	master := cluster.GetMaster()
	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	server.JobsCheckFinished(conn)
	server.JobsCheckErrors(conn)
	server.JobsCheckPending(conn)

	if server.NeedRefreshJobsPending() {
		err = server.JobsUpdateEntries(conn)
		return err
	}

	return nil
}

func (server *ServerMonitor) JobsCheckFinished(conn *sqlx.Conn) error {
	var err error
	cluster := server.ClusterGroup

	master := cluster.GetMaster()
	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	var logs [][]string = make([][]string, 0)
	tasks, err := server.GetTasksByState(conn, JobStateFinished, 1)
	for _, task := range tasks {
		var logrow []string
		if err := server.AfterJobProcess(conn, task); err != nil {
			logrow = []string{config.LvlErr, "[ERROR] Scheduler error fetching finished replication_manager_schema.jobs %s", err.Error()}
			logs = append(logs, logrow)
		} else {
			if !slices.Contains([]string{"errorlog", "slowquery", "sqlerrorlog", "auditlog"}, task.task) {
				logrow = []string{config.LvlInfo, "[SUCCESS] Finished %s successfully", task.task}
				logs = append(logs, logrow)
			}
		}

		server.SetNeedRefreshJobs(true)
	}

	//Wait for debug sent via API
	time.Sleep(3 * time.Second)
	for _, logrow := range logs {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, logrow[0], logrow[1], logrow[2])
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, logrow[0], logrow[1], logrow[2])
	}

	return err
}

func (server *ServerMonitor) JobRunViaSSH() error {
	cluster := server.ClusterGroup

	// Atomically check and acquire the job lock
	if !server.TryAcquireJobLock() {
		loglvl := config.LvlErr

		if cluster.Conf.SchedulerJobsSSH {
			loglvl = config.LvlDbg
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, loglvl, "Cancel dbjob via ssh since another job is running")
		return errors.New("Cancel dbjob via ssh since another job is running")
	}

	defer server.ReleaseJobLock()

	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Cancel dbjob via ssh during failover")
		return errors.New("Cancel dbjob via ssh during failover")
	}

	client, err := server.GetCluster().OnPremiseConnect(server)
	if err != nil {
		if !server.HaveSSHError {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "OnPremise run job on %s: %s", server.URL, err)
			server.HaveSSHError = true
		}
		return err
	} else {
		server.HaveSSHError = false
	}
	defer client.Close()

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	scriptpath := server.Datadir + "/init/init/dbjobs_new"

	if _, err := os.Stat(scriptpath); os.IsNotExist(err) {
		server.GetDatabaseConfig()
	}

	if server.GetCluster().GetConf().OnPremiseSSHDbJobScript != "" {
		scriptpath = server.GetCluster().GetConf().OnPremiseSSHDbJobScript
	}

	filerc, err2 := os.Open(scriptpath)
	if err2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "JobRunViaSSH %s, scriptpath : %s", err2, scriptpath)
		return errors.New("Cancel dbjob can't open script")

	}
	defer filerc.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(filerc)

	buf2 := strings.NewReader(server.GetSshEnv())
	r := io.MultiReader(buf2, buf)

	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Running database jobs via SSH script: %s with env: %v", scriptpath, server.GetSshEnv())

	if err = client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Database jobs run via SSH: %s", stderr.String())
	}
	out := stdout.String()

	//Log Task - Debug Level
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Job run via ssh script: %s ,out: %s ,err: %s", scriptpath, out, stderr.String())
	return nil
}

// Job state always updated in replication-manager runtime.
func (server *ServerMonitor) JobsUpdateState(task, result string, state, done int) error {
	var err error
	cluster := server.ClusterGroup

	if t, exists := server.JobResults.LoadOrStore(task, &config.Task{
		Task:   task,
		State:  state,
		Result: result,
		Done:   done,
	}); exists {
		t.State = state
		t.Done = done
		t.Result = result
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Job state updated in runtime. Continue to update state in jobs table.")

	// In API mode, state is tracked in memory only — no jobs table
	if cluster.Conf.SchedulerJobsMode == "api" {
		return nil
	}

	if !cluster.Conf.MonitorScheduler {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Monitoring scheduler is inactive, task only updated in runtime")
		return nil
	}

	if server.Conn == nil {
		return errors.New("No connection pool")
	}

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer conn.Close()

	if done == 1 {
		_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "UPDATE replication_manager_schema.jobs SET done=?, state=?, result=?, end=NOW() WHERE task =?;", done, state, result, task)
	} else {
		_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "UPDATE replication_manager_schema.jobs SET done=?, state=?, result=? WHERE task =?;", done, state, result, task)
	}
	if err != nil {
		return err
	}

	server.SetNeedRefreshJobs(true)
	return err
}

func (server *ServerMonitor) JobsUpdatePayload(task, payload string) error {
	cluster := server.ClusterGroup
	if t, exists := server.JobResults.LoadOrStore(task, &config.Task{
		Task: task,
	}); exists {
		t.Payload = payload
	} else {
		t.Payload = payload
	}

	if cluster.Conf.SchedulerJobsMode == "api" {
		return nil
	}

	if !cluster.Conf.MonitorScheduler {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Monitoring scheduler is inactive, payload only updated in runtime")
		return nil
	}

	if server.Conn == nil {
		return errors.New("No connection pool")
	}

	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
	}
	defer conn.Close()

	_, err = server.ConnExecQueryWithTimeout(conn, JobTimeout, "UPDATE replication_manager_schema.jobs SET payload=? WHERE task =?;", payload, task)
	if err != nil {
		return err
	}

	server.SetNeedRefreshJobs(true)
	return nil
}

type ConfigReceiverResponse struct {
	MonitorAddress    string `json:"monitor_address"`
	DummyConfigPort   string `json:"dummy_config_port"`
	CurrentConfigPort string `json:"current_config_port"`
	CurrentPIDFile    string `json:"current_pid_file"`
	DefaultConfigPath string `json:"default_config_dir"`
}

func (server *ServerMonitor) JobReceiveConfigFiles() (*ConfigReceiverResponse, error) {
	cluster := server.ClusterGroup
	var rcv_port_pid, rcv_port, pid_file string

	pid_file = server.SensitiveVariables.Get("PID_FILE")
	// prepare the receiver for current.cnf
	rcv_port_pid, err := cluster.SSTRunReceiverToFile(server, filepath.Join(server.Datadir, "current.cnf.tmp"), ConstJobCreateFile, "printdefault-current")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "OnPremise print default config database via ssh failed : %s", err)
		return nil, err
	}

	rcv_port_pid_int, _ := strconv.Atoi(rcv_port_pid)

	// prepare the receiver for dummy.cnf
	rcv_port, err = cluster.SSTRunReceiverToFile(server, filepath.Join(server.Datadir, "dummy.cnf.tmp"), ConstJobCreateFile, "printdefault-dummy")
	if err != nil {
		cluster.SSTCloseReceiver(rcv_port_pid_int)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "OnPremise print default config database via ssh failed : %s", err)
		return nil, err
	}

	return &ConfigReceiverResponse{
		MonitorAddress:    cluster.Conf.MonitorAddress,
		DummyConfigPort:   rcv_port,
		CurrentConfigPort: rcv_port_pid,
		CurrentPIDFile:    pid_file,
		DefaultConfigPath: filepath.Join(server.GetDatabaseConfdir(), "my.cnf"),
	}, nil
}

func (server *ServerMonitor) CheckJobsVersion() error {
	var sum, newsum string
	var checkerr error
	cluster := server.ClusterGroup

	if server.IsIgnored() {
		return nil
	}

	if cluster.IsInFailover() {
		return nil
	}

	scriptPath := filepath.Join(server.Datadir, "init/init", "dbjobs_new")
	currentScriptPath := filepath.Join(server.Datadir, "dbjobs_new.current")

	// Check if the script exists
	finfo, err := os.Stat(currentScriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			server.SetWaitJobsCheckCookie()
		}
		checkerr = err
	} else {
		sum, checkerr = crypto.GenerateChecksum(currentScriptPath)
	}

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		server.GetDatabaseConfig()
	}

	newsum, err = crypto.GenerateChecksum(scriptPath)
	if err != nil {
		checkerr = err
	}

	if sum != newsum || checkerr != nil {
		if checkerr == nil {
			checkerr = fmt.Errorf("Checksum mismatch")
		}
		cluster.SetState("WARN0147", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0147"], server.URL, newsum, sum, checkerr), ErrFrom: "JOB", ServerUrl: server.URL})
		// Set cookie for next check
		if finfo != nil && !finfo.ModTime().IsZero() && finfo.ModTime().Add(10*time.Minute).Before(time.Now()) {
			server.SetWaitJobsCheckCookie()
		}
	}

	if finfo != nil && !finfo.ModTime().IsZero() && finfo.ModTime().Add(time.Hour).Before(time.Now()) {
		server.SetWaitJobsCheckCookie()
	}

	return checkerr
}

func (server *ServerMonitor) UpgradeJobsScript() error {
	cluster := server.ClusterGroup
	defer cluster.LogPanicToFile("jobs-upgrade")

	err := cluster.SSTRunSender(filepath.Join(server.Datadir, "init/init", "dbjobs_new"), server, true)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "dbjobs_new file does not exist on %s, retrying later", server.Name)
			return nil
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error sending dbjobs_new file to %s: %s", server.Name, err.Error())
		return err
	}

	if server.HasRollingJobsUpgradeCookie() {
		server.DelRollingJobsUpgradeCookie()
	}

	server.SetWaitJobsCheckCookie()
	return nil
}
