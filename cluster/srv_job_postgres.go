// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

// PostgreSQL backups (#1850). The tools run in the jobs sidecar of the PostgreSQL service
// (share/scripts/postgres_job.sh), driven through the API jobs mode, never a jobs table:
//
//	request   JobBackupPostgresLogical arms the task (a cookie) and opens the metadata
//	sidecar   asks /needs/pgdump, then /actions/receive-task/pgdump for a receiver port,
//	          streams pg_dumpall to it, reports /actions/job-state/pgdump/done
//	receiver  at the end of the stream JobFinishReceiveFile closes the metadata
//
// The receiver compresses (gzip) on replication-manager's side, like a physical backup.

func postgresJobCookie(task string) string {
	return "cookie_wait" + task
}

// PostgresBackupDest is the artifact of a PostgreSQL backup task in the server's backup
// directory.
func (server *ServerMonitor) PostgresBackupDest(task string) string {
	switch config.TaskName(task) {
	case config.ConstTaskPgDump:
		return server.GetMyBackupDirectory() + "pg_dumpall.sql.gz"
	}
	return server.GetMyBackupDirectory() + task
}

// JobBackupPostgresLogical requests a logical backup of a PostgreSQL server from its jobs
// sidecar. It returns once the task is armed: the sidecar picks it at its next poll.
func (server *ServerMonitor) JobBackupPostgresLogical() error {
	cluster := server.ClusterGroup
	task := string(config.ConstTaskPgDump)
	if server.IsDown() {
		return errors.New("Can't backup when server down")
	}
	if cluster.IsInBackup() {
		return errors.New("A backup is already running on the cluster")
	}
	cluster.SetInLogicalBackupState(true)
	cluster.SetState("WARN0175", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0175"], "pg_dumpall", server.URL), ErrFrom: "JOB", ServerUrl: server.URL})

	now := time.Now()
	var prevID int64
	if prev := cluster.BackupMetaMap.GetPreviousBackup("pg_dumpall", server.URL); prev != nil {
		prevID = prev.Id
	}
	server.backupMetaMutex.Lock()
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		Id:             now.Unix(),
		StartTime:      now,
		BackupMethod:   backupmgr.BackupMethodLogical,
		BackupStrategy: backupmgr.BackupStrategyFull,
		BackupTool:     task, // the job name: WriteBackupMetadata reads the job state under it
		Source:         server.URL,
		Dest:           server.PostgresBackupDest(task),
		Compressed:     true,
		Previous:       prevID,
		BackupLine:     backupmgr.BackupLineDefault,
	}
	meta := server.LastBackupMeta.Logical
	server.backupMetaMutex.Unlock()
	server.ensureBackupSessionID(meta, backupmgr.BackupMethodLogical, now, backupmgr.BackupLineDefault)
	cluster.BackupMetaMap.Set(meta.Id, meta)

	// live progress: the receiver counts the stream into this state (cluster_backup_progress.go)
	cluster.StartBackupProgress(server, "logical", "pg_dumpall")
	server.JobsUpdateStateRuntimeOnly(task, "requested", JobStateAvailable, 0)
	if err := server.createCookie(postgresJobCookie(task)); err != nil {
		cluster.SetInLogicalBackupState(false)
		cluster.EndBackupProgress(cluster.backupProgressFor(server, "logical"))
		return fmt.Errorf("can not arm the %s task: %w", task, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup pg_dumpall requested from the jobs sidecar of %s", server.URL)
	return nil
}

// finishPostgresLogicalBackup closes a PostgreSQL logical backup when its stream ends:
// completed when something was received, then the metadata is written and the cluster
// leaves the backup state.
func (server *ServerMonitor) finishPostgresLogicalBackup() {
	cluster := server.ClusterGroup
	task := string(config.ConstTaskPgDump)
	defer cluster.SetInLogicalBackupState(false)
	progress := cluster.backupProgressFor(server, "logical")
	defer cluster.EndBackupProgress(progress)

	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Logical
	if meta != nil && progress != nil {
		// the bytes the receiver counted are the next run's progress denominator
		meta.StreamSize = progress.View().BytesDone
	}
	server.backupMetaMutex.Unlock()
	if meta == nil || meta.BackupTool != task {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "PostgreSQL dump received on %s without a pending backup request", server.URL)
		return
	}
	received := false
	if fi, err := os.Stat(meta.Dest); err == nil && fi.Size() > 20 { // an empty gzip stream is 20 bytes
		received = true
	}
	if received {
		// the stream ended: the job is over whatever the sidecar's own report says or when
		server.JobsUpdateStateRuntimeOnly(task, "received", JobStateSuccess, 1)
		// the mark HasValidBackup reads: the cluster has a logical backup of this server
		// (closes WARN0111)
		server.createCookie("cookie_logicalbackup")
	} else {
		server.JobsUpdateStateRuntimeOnly(task, "no data received from the dump", JobStateErrorExec, 1)
	}
	server.WriteBackupMetadata(backupmgr.BackupMethodLogical)
	elapsed := time.Since(meta.StartTime).Round(time.Second)
	if received {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup pg_dumpall completed in %s for: %s", elapsed, server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Logical backup pg_dumpall received no data after %s for: %s", elapsed, server.URL)
	}
}
