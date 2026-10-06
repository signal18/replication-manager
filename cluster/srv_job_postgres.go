// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
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

// physicalBackupType is the physical backup tool of this server: the cluster's
// backup-physical-type, or pg_basebackup in the jobs sidecar for a PostgreSQL server.
func (server *ServerMonitor) physicalBackupType() string {
	if server.DBVersion != nil && server.DBVersion.IsPostgreSQL() {
		return string(config.ConstTaskPgBaseBackup)
	}
	return server.ClusterGroup.Conf.BackupPhysicalType
}

// physicalBackupExtension is the extension of the physical artifact before compression:
// an xbstream for mariabackup and xtrabackup, a tar for pg_basebackup.
func (server *ServerMonitor) physicalBackupExtension(tool string) string {
	if tool == string(config.ConstTaskPgBaseBackup) {
		return ".tar"
	}
	return ".xbtream"
}

// openPhysicalBackupReceiver opens the receiver of a physical backup stream. For
// pg_basebackup nothing is opened here: the jobs sidecar asks for its receiver when it
// takes the task (receive-task), on the destination the running backup recorded.
func (server *ServerMonitor) openPhysicalBackupReceiver(gzip bool, dest, tool string) (string, error) {
	cluster := server.ClusterGroup
	if tool == string(config.ConstTaskPgBaseBackup) {
		return "0", nil
	}
	if gzip {
		return cluster.SSTRunReceiverToGZip(server, dest, ConstJobCreateFile, tool)
	}
	return cluster.SSTRunReceiverToFile(server, dest, ConstJobCreateFile, tool)
}

// BackupStagingSuffix is the suffix of a backup artifact still in staging (encryption).
func BackupStagingSuffix() string {
	return partialSuffixForCleanup
}

// PostgresBackupDest is the default artifact of a PostgreSQL backup task in the server's
// backup directory.
func (server *ServerMonitor) PostgresBackupDest(task string) string {
	switch config.TaskName(task) {
	case config.ConstTaskPgDump:
		return server.GetMyBackupDirectory() + "pg_dumpall.sql.gz"
	case config.ConstTaskPgBaseBackup:
		return server.GetMyBackupDirectory() + task + ".tar.gz"
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
	if m := server.LastBackupMeta.Physical; m != nil && m.BackupTool == task && m.Dest != "" && !m.Completed {
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

// postgresNextStartTargetKey is where the primary a role-change task works against is kept
// while the task is armed (streamTasks holds the per-task runtime of the sidecar tasks).
func postgresNextStartTargetKey(task string) string {
	return "target:" + task
}

// PostgresNextStartTarget answers the jobs sidecar's question for a role-change task
// (pgstandby, pgreseed): the "host:port" of the primary its next start must follow. Empty
// when no such task is armed for this server.
func (server *ServerMonitor) PostgresNextStartTarget(task string) string {
	if v, ok := server.streamTasks.Load(postgresNextStartTargetKey(task)); ok {
		return v.(string)
	}
	return ""
}

// armPostgresNextStart asks the jobs sidecar of this server to arm its next start --
// as a standby of primary (pgstandby) or re-seeded from it (pgreseed) -- and waits for the
// sidecar to report it done. PostgreSQL cannot change role while it runs: the caller then
// stops or restarts the service, whose start script applies what was armed.
func (server *ServerMonitor) armPostgresNextStart(task config.TaskName, primary *ServerMonitor) error {
	cluster := server.ClusterGroup
	name := string(task)
	key := postgresNextStartTargetKey(name)
	server.streamTasks.Store(key, primary.Host+":"+primary.Port)
	defer server.streamTasks.Delete(key)

	armed := time.Now().Unix()
	if err := server.setTaskCookie(name); err != nil {
		return fmt.Errorf("can not arm the %s task: %w", name, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Task %s requested from the jobs sidecar of %s (primary %s)", name, server.URL, primary.URL)
	cookie := postgresJobCookie(name)
	deadline := time.Now().Add(postgresJobPickupTimeout)
	for time.Now().Before(deadline) {
		if server.JobResults != nil {
			if t := server.JobResults.Get(name); t != nil && t.Done == 1 && t.End >= armed {
				if t.State != JobStateSuccess {
					return fmt.Errorf("the jobs sidecar of %s failed the %s task: %s", server.URL, name, t.Result)
				}
				return nil
			}
		}
		if cluster.exit.Load() {
			break
		}
		time.Sleep(time.Second)
	}
	server.delCookie(cookie)
	return fmt.Errorf("the jobs sidecar of %s did not complete the %s task within %s", server.URL, name, postgresJobPickupTimeout)
}

// checkPostgresJobsVersion is CheckJobsVersion for a PostgreSQL server: its jobs sidecar
// runs the script delivered as the config key APP_JOBS_SCRIPT of its service. The key is
// compared with the embedded postgres_job.sh; a difference raises WARN0147 like a MariaDB
// dbjobs mismatch, the key is refreshed and the sidecar alone restarted (container#jobs,
// the restart-container cookie, as the MariaDB jobs upgrade does), nothing else touched.
func (server *ServerMonitor) checkPostgresJobsVersion() error {
	cluster := server.ClusterGroup
	if !server.HasProvisionCookie() || cluster.IsInFailover() || cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return nil
	}
	app := cluster.engineAppOfServer(server)
	if app == nil {
		return nil
	}
	embedded := appJobsScript(app)
	if embedded == "" {
		return nil
	}
	newsum := fmt.Sprintf("%x", sha256.Sum256([]byte(embedded)))
	if server.pgJobsScriptSum == "" {
		// read once from the orchestrator; kept in memory until a refresh changes it
		svc := cluster.OpenSVCConnect()
		delivered, err := svc.GetConfigKeyValueV3(cluster.Name, app.Name, appJobsScriptKey)
		if err != nil {
			return err
		}
		server.pgJobsScriptSum = fmt.Sprintf("%x", sha256.Sum256(delivered))
	}
	if server.pgJobsScriptSum == newsum {
		return nil
	}
	cluster.SetState("WARN0147", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0147"], server.URL, newsum, server.pgJobsScriptSum, "jobs script changed"), ErrFrom: "JOB", ServerUrl: server.URL})
	svc := cluster.OpenSVCConnect()
	if err := svc.CreateConfigKeyValue(cluster.Name, app.Name, appJobsScriptKey, embedded); err != nil { // create updates an existing key (409)
		return fmt.Errorf("jobs script refresh on %s: %w", server.URL, err)
	}
	server.pgJobsScriptSum = newsum
	server.RestartNode = ""
	server.RestartRid = RestartRidJobsContainer
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Jobs script of %s refreshed, its sidecar restarts", server.URL)
	return server.SetRestartContainerCookie()
}

// postgresReseedFromBackup restores a PostgreSQL server from the cluster's stored backup,
// the way a MariaDB server is reseeded from one: the server's jobs sidecar is asked to
// receive the backup (task pgrestore for the physical pg_basebackup tar, pgrestorelogical for
// the pg_dumpall), it listens on the server's SST port and reports processing, and
// WaitAndSendSST streams the file to it as it does to a dbjobs listener.
//
// Physical: the sidecar stores the tar on the data volume and arms the next start to
// restore it (the data directory is replaced, the server comes back as a standby of the
// current primary when it is not the primary itself); the service restarts when the sidecar
// reports done. Logical: the dump is replayed on the running server (pg_dumpall --clean).
func (server *ServerMonitor) postgresReseedFromBackup(task config.TaskName) error {
	cluster := server.ClusterGroup
	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed")
	}
	master.backupMetaMutex.Lock()
	meta := master.LastBackupMeta.Physical
	if task == config.ConstTaskPgRestoreLogical {
		meta = master.LastBackupMeta.Logical
	}
	master.backupMetaMutex.Unlock()
	if meta == nil || !meta.Completed || meta.Dest == "" {
		return fmt.Errorf("no completed %s backup of the primary to restore from", map[bool]string{true: "logical", false: "physical"}[task == config.ConstTaskPgRestoreLogical])
	}
	if _, err := os.Stat(meta.Dest); err != nil {
		return fmt.Errorf("backup file %s: %w", meta.Dest, err)
	}
	if server.HasAnyReseedingState() {
		return errors.New("a reseed is already in progress on " + server.URL)
	}
	if task == config.ConstTaskPgRestoreLogical && master.URL != server.URL && cluster.isPostgresLogical() {
		// the dump would drop and recreate the subscribed tables under the subscription
		return errors.New("a logical replication subscriber is reseeded from the primary (initial copy), not from the dump")
	}
	if task == config.ConstTaskPgRestoreLogical && master.URL != server.URL && !cluster.isPostgresLogical() {
		return errors.New("a WAL streaming standby is read-only: the dump can only be replayed on the primary")
	}
	name := string(task)
	// the primary the restored server follows: none when it is the primary itself
	target := ""
	if master.URL != server.URL {
		target = master.Host + ":" + master.Port
	}
	server.streamTasks.Store(postgresNextStartTargetKey(name), target)
	server.SetInReseedBackup(name)
	if err := server.setTaskCookie(name); err != nil {
		server.SetInReseedBackup("")
		return fmt.Errorf("can not arm the %s task: %w", name, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restore of %s from %s (%s) requested from its jobs sidecar", server.URL, meta.Dest, meta.BackupTool)
	go func() {
		if err := server.WaitAndSendSST(name, meta.Dest, false, 0); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restore of %s: %s", server.URL, err)
			server.delCookie(postgresJobCookie(name))
			server.SetInReseedBackup("")
		}
	}()
	return nil
}

// armPostgresLogicalReseed asks the jobs sidecar of this server (a former publisher, or any
// subscriber to re-copy) for the task pgreseedlogical: on the primary it creates the slot of
// the subscription with an exported snapshot, dumps the primary at that snapshot and replays
// the dump here; PostgresLogicalReseedFinish then subscribes on that slot (job-state done).
func (server *ServerMonitor) armPostgresLogicalReseed(primary *ServerMonitor) error {
	cluster := server.ClusterGroup
	name := string(config.ConstTaskPgReseedLogical)
	server.streamTasks.Store(postgresNextStartTargetKey(name), primary.Host+":"+primary.Port)
	server.SetInReseedBackup(name)
	if err := server.setTaskCookie(name); err != nil {
		server.SetInReseedBackup("")
		return fmt.Errorf("can not arm the %s task: %w", name, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Re-copy of %s from %s at the snapshot of a new slot requested from its jobs sidecar", server.URL, primary.URL)
	return nil
}

// PostgresLogicalReseedFinish is the end of pgreseedlogical, on the sidecar's done: the data
// is in place at the snapshot of the slot the sidecar created on the primary, the subscription
// takes that slot, the server keeps the read-only default of a replica.
func (server *ServerMonitor) PostgresLogicalReseedFinish() {
	cluster := server.ClusterGroup
	defer server.SetInReseedBackup("")
	primary := cluster.GetMaster()
	if primary == nil || primary.URL == server.URL || server.Conn == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Re-copy of %s done but no primary to subscribe to", server.URL)
		return
	}
	cluster.postgresInstallDDLReplication(server)
	opt := cluster.GetChangeMasterBaseOptForSlave(server, primary, false)
	opt.PostgresExistingSlot = true
	logs, err := dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Re-copy of %s done but it could not subscribe on its slot: %s", server.URL, err)
	if err != nil {
		return
	}
	if _, err := server.StartSlave(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Re-copy of %s done but its subscription could not be enabled: %s", server.URL, err)
		return
	}
	logs, err = server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Could not set the read-only default on %s: %s", server.URL, err)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "%s re-copied from %s and subscribed on the slot of its snapshot", server.URL, primary.URL)
}
