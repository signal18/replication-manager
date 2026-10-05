// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/signal18/replication-manager/config"
)

// PostgreSQL backups (#1850). The tools run in the jobs sidecar of the PostgreSQL service
// (share/scripts/postgres_job.sh), driven through the API jobs mode, never a jobs table.
// The backup itself goes through the common path (JobBackupLogicalWithOptions: slot,
// states, metadata, encryption, archive); pg_dumpall is one more tool of its switch:
//
//	common path  arms the task (a cookie the sidecar asks for through /needs/pgdump)
//	sidecar      asks /actions/receive-task/pgdump for a receiver port, streams
//	             pg_dumpall to it, reports /actions/job-state/pgdump/done
//	receiver     at the end of the stream JobFinishReceiveFile signals the common path,
//	             which was waiting in runPostgresStreamTask and goes on
//
// The receiver compresses (gzip) on replication-manager's side, like a physical backup.

const (
	postgresJobPickupTimeout = 2 * time.Minute // the sidecar polls every 10 s
	postgresJobStreamTimeout = 12 * time.Hour
)

func postgresJobCookie(task string) string {
	return "cookie_wait" + task
}

// logicalBackupType is the logical backup tool of this server: the cluster's
// backup-logical-type, or pg_dumpall in the jobs sidecar for a PostgreSQL server.
func (server *ServerMonitor) logicalBackupType() string {
	if server.DBVersion != nil && server.DBVersion.IsPostgreSQL() {
		return string(config.ConstTaskPgDump)
	}
	return server.ClusterGroup.Conf.BackupLogicalType
}

// PostgresBackupDest is the default artifact of a PostgreSQL backup task in the server's
// backup directory.
func (server *ServerMonitor) PostgresBackupDest(task string) string {
	switch config.TaskName(task) {
	case config.ConstTaskPgDump:
		return server.GetMyBackupDirectory() + "pg_dumpall.sql.gz"
	}
	return server.GetMyBackupDirectory() + task
}

// PostgresStreamDest is where the receiver of a sidecar task writes: the destination the
// running backup recorded (a staging name when encryption is on, a unique one when ad
// hoc), else the default artifact.
func (server *ServerMonitor) PostgresStreamDest(task string) string {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	if m := server.LastBackupMeta.Logical; m != nil && m.BackupTool == task && m.Dest != "" && !m.Completed {
		return m.Dest
	}
	return server.PostgresBackupDest(task)
}

// armStreamTask registers the wait for the end of a sidecar task's stream.
func (server *ServerMonitor) armStreamTask(task string) chan struct{} {
	done := make(chan struct{})
	server.streamTasks.Store(task, done)
	return done
}

// signalStreamTaskDone is called when the receiver of a sidecar task reaches the end of
// its stream. Without a waiter it is a no-op.
func (server *ServerMonitor) signalStreamTaskDone(task string) {
	if v, ok := server.streamTasks.LoadAndDelete(task); ok {
		close(v.(chan struct{}))
	}
}

// runPostgresStreamTask makes the jobs sidecar run a task and waits for the end of its
// stream into dest. It fails when the sidecar does not take the task (no sidecar, not
// reachable), when nothing was received, or when the caller is cancelled.
func (server *ServerMonitor) runPostgresStreamTask(ctx context.Context, task, dest string) error {
	cluster := server.ClusterGroup
	done := server.armStreamTask(task)
	defer server.streamTasks.Delete(task)

	cookie := postgresJobCookie(task)
	if !server.hasCookie(cookie) {
		if err := server.setTaskCookie(task); err != nil {
			return fmt.Errorf("can not arm the %s task: %w", task, err)
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Task %s requested from the jobs sidecar of %s", task, server.URL)

	pickup := time.NewTimer(postgresJobPickupTimeout)
	defer pickup.Stop()
	limit := time.NewTimer(postgresJobStreamTimeout)
	defer limit.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for waiting := true; waiting; {
		select {
		case <-done:
			waiting = false
		case <-ctx.Done():
			server.delCookie(cookie)
			return fmt.Errorf("%s canceled: %w", task, ctx.Err())
		case <-pickup.C:
			if server.hasCookie(cookie) {
				server.delCookie(cookie)
				return fmt.Errorf("the jobs sidecar of %s did not take the %s task within %s", server.URL, task, postgresJobPickupTimeout)
			}
		case <-limit.C:
			return fmt.Errorf("%s still streaming after %s", task, postgresJobStreamTimeout)
		case <-tick.C:
			if cluster.exit.Load() {
				server.delCookie(cookie)
				return errors.New("backup canceled: cluster shutting down")
			}
		}
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() <= 20 { // an empty gzip stream is 20 bytes
		return fmt.Errorf("no data received from %s", task)
	}
	return nil
}
