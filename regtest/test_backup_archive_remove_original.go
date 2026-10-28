// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

const archiveOriginalSchema = "regtest_archorig"

// TestBackupArchiveRemoveOriginal (#1987, #2091): with backup-archive-remove-original, the
// logical (mysqldump, then mydumper) and the physical backups of the master are removed from the
// backup directory once the attached archive's snapshot holds them, and a replica that missed
// rows is reseeded from each: the reseed fetches the original back from the archive. The
// attached archive is used as it is, restic or an archive plugin's (plakar, pbs); with none, a
// restic archive is set up. Each backup and reseed runs under sysbench; at the end the replicas
// hold the master's sysbench table and replicate.
//
// Needs a jobs container next to each database, a physical backup tool matching the servers
// and sysbench; it reseeds a replica. Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testBackupArchiveRemoveOriginal
func (regtest *RegTest) TestBackupArchiveRemoveOriginal(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testBackupArchiveRemoveOriginal: "+format, args...)
		return false
	}
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testBackupArchiveRemoveOriginal: "+format, args...)
	}

	master := cl.GetMaster()
	if master == nil || master.Conn == nil {
		return fail("no master with a connection")
	}
	slaves := cl.GetSlaves()
	if len(slaves) == 0 || slaves[0] == nil || slaves[0].Conn == nil {
		return fail("no replica with a connection")
	}
	slave := slaves[0]

	// Reuse the cluster's attached archive, restic or a plugin's; with none, set up restic.
	attached := cl.Conf.AttachedArchive()
	if attached == "" && (!cl.Conf.BackupRestic || cl.ResticManager == nil) {
		if err := cl.SetBackupRestic(true); err != nil {
			return fail("enable restic: %s", err)
		}
		if cl.Conf.BackupResticPassword == "" {
			cl.Conf.BackupResticPassword = "test"
		}
		if err := cl.StartResticManager(); err != nil {
			return fail("StartResticManager: %s", err)
		}
		if err := cl.ResticInitRepo(false); err != nil && !strings.Contains(err.Error(), "already exists") {
			return fail("ResticInitRepo: %s", err)
		}
		attached = cl.Conf.AttachedArchive()
	}
	step("archive %s", attached)
	original := cl.Conf.BackupArchiveRemoveOriginal
	cl.Conf.BackupArchiveRemoveOriginal = true
	defer func() { cl.Conf.BackupArchiveRemoveOriginal = original }()

	mdb, err := master.GetNewDBConn()
	if err != nil {
		return fail("cannot open a connection to the master: %s", err)
	}
	defer mdb.Close()
	defer func() {
		if err := dbhelper.ExecStatements(mdb, "DROP DATABASE IF EXISTS "+archiveOriginalSchema); err != nil {
			step("cleanup failed: %s", err)
		}
	}()

	table := archiveOriginalSchema + ".t"
	digest := func(s *clusterpkg.ServerMonitor) (string, error) {
		return dbhelper.TableContentDigest(s.Conn, table, "CONCAT_WS('|', id, v)")
	}
	sameData := func() (bool, string) {
		dm, err := digest(master)
		if err != nil {
			return false, fmt.Sprintf("master: %s", err)
		}
		ds, err := digest(slave)
		if err != nil {
			return false, fmt.Sprintf("replica: %s", err)
		}
		return dm == ds, fmt.Sprintf("master %s, replica %s", dm, ds)
	}
	insert := func(from, to int) error {
		var rows []string
		for i := from; i <= to; i++ {
			rows = append(rows, fmt.Sprintf("INSERT INTO %s VALUES (%d, 'row %d')", table, i, i))
		}
		return dbhelper.ExecStatements(mdb, rows...)
	}

	step("create the fixture on the master %s", master.URL)
	if err := dbhelper.ExecStatements(mdb,
		"DROP DATABASE IF EXISTS "+archiveOriginalSchema,
		"CREATE DATABASE "+archiveOriginalSchema,
		"CREATE TABLE "+table+" (id INT PRIMARY KEY, v VARCHAR(40)) ENGINE=InnoDB",
	); err != nil {
		return fail("fixture: %s", err)
	}
	if err := insert(1, 50); err != nil {
		return fail("fixture rows: %s", err)
	}
	lastDiff := ""
	if !physReseedWait(2*time.Minute, func() bool { ok, d := sameData(); lastDiff = d; return ok }) {
		return fail("the replica never received the fixture: %s", lastDiff)
	}

	// diverge stops replication on the replica and commits rows it misses.
	diverge := func(from, to int) bool {
		if _, err := slave.StopSlave(); err != nil {
			return fail("cannot stop replication: %s", err)
		}
		if err := insert(from, to); err != nil {
			return fail("rows committed while the replica was stopped: %s", err)
		}
		if same, _ := sameData(); same {
			return fail("the replica already has the master's data: the test would prove nothing")
		}
		return true
	}
	// archived waits for the backup started after started to be archived and its original removed;
	// a backup that ended without completing fails at once (its error is in the log).
	archived := func(kind string, started time.Time, meta func() *backupmgr.BackupMetadata) (string, bool) {
		var dest string
		var failedAt time.Time
		ok := physReseedWait(15*time.Minute, func() bool {
			m := meta()
			if m != nil && !m.Completed && m.EndTime.After(started) {
				failedAt = m.EndTime
				return true
			}
			if m == nil || !m.Completed || !m.EndTime.After(started) || !m.OriginalRemoved {
				return false
			}
			if m.ArchiveSnapshot(attached) == "" && m.ResticSnapshotID == "" {
				return false
			}
			dest = m.Dest
			return true
		})
		if !failedAt.IsZero() {
			return "", fail("the %s backup ended at %s without completing: its error is in the log", kind, failedAt.Format(time.RFC3339))
		}
		if !ok {
			return "", fail("the %s backup was not archived with its original removed within 15 minutes (WARN0238 open: %v)", kind, cl.StateMachine.IsInState("WARN0238"))
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			return "", fail("the %s original %s is still on disk", kind, dest)
		}
		return dest, true
	}
	// reseeded waits for the replica to finish the reseed, hold the master's data and replicate.
	reseeded := func(kind, dest string, next int) bool {
		if !physReseedWait(30*time.Minute, func() bool { return !slave.HasAnyReseedingState() && !slave.HasReplicationIssue() }) {
			return fail("the replica is still reseeding or not replicating after the %s reseed", kind)
		}
		if !physReseedWait(2*time.Minute, func() bool { ok, d := sameData(); lastDiff = d; return ok }) {
			return fail("the replica differs from the master after the %s reseed: %s", kind, lastDiff)
		}
		if _, err := os.Stat(dest); err != nil {
			return fail("the %s original %s was not fetched back from the archive: %s", kind, dest, err)
		}
		if err := insert(next, next); err != nil {
			return fail("insert after the %s reseed: %s", kind, err)
		}
		if !physReseedWait(2*time.Minute, func() bool { ok, d := sameData(); lastDiff = d; return ok }) {
			return fail("a transaction after the %s reseed did not reach the replica: %s", kind, lastDiff)
		}
		return true
	}

	// load runs sysbench for the phase, its end waited by the returned function: 120s and at
	// most 16 threads, enough to cover the backup and the reseed and small enough for the
	// binlogs and relay logs of three phases to fit a small cluster's data disks (128
	// threads for 300s filled dev3's 2G volumes).
	originalSysbenchTime, originalSysbenchThreads := cl.Conf.SysbenchTime, cl.Conf.SysbenchThreads
	defer func() { cl.Conf.SysbenchTime, cl.Conf.SysbenchThreads = originalSysbenchTime, originalSysbenchThreads }()
	cl.Conf.SysbenchTime = 120
	if cl.Conf.SysbenchThreads > 16 {
		cl.Conf.SysbenchThreads = 16
	}
	load := func(kind string) func() {
		step("%s: sysbench on the master for %ds", kind, cl.Conf.SysbenchTime)
		done := make(chan error, 1)
		go func() { done <- cl.RunSysbench() }()
		return func() {
			select {
			case err := <-done:
				if err != nil {
					step("%s: sysbench ended: %s", kind, err)
				}
			case <-time.After(time.Duration(cl.Conf.SysbenchTime)*time.Second + 10*time.Minute):
				step("%s: sysbench still running", kind)
			}
		}
	}
	// consistent: once sysbench ended, the replicas hold the master's sysbench table and
	// replicate.
	consistent := func(kind string) bool {
		// the last attempt's results, not a second checksum that could disagree with them
		var running, same bool
		if !physReseedWait(5*time.Minute, func() bool {
			running = cl.CheckSlavesRunning()
			same = running && cl.CheckTableConsistency(cl.BenchTable())
			return same
		}) {
			return fail("after the %s reseed under sysbench: replicas running %v, %s consistent %v", kind, running, cl.BenchTable(), same)
		}
		return true
	}

	originalLogical := cl.Conf.BackupLogicalType
	defer cl.SetBackupLogicalType(originalLogical)
	for i, tool := range []string{config.ConstBackupLogicalTypeMysqldump, config.ConstBackupLogicalTypeMydumper} {
		base := 100 + i*100
		step("logical: %s backup of the master, archived, original removed", tool)
		cl.SetBackupLogicalType(tool)
		ended := load(tool)
		if !diverge(base+1, base+10) {
			return false
		}
		started := time.Now()
		if err := master.JobBackupLogical(context.Background()); err != nil {
			return fail("JobBackupLogical %s: %s", tool, err)
		}
		dest, ok := archived(tool, started, func() *backupmgr.BackupMetadata { return master.LastBackupMeta.Logical })
		if !ok {
			return false
		}
		step("logical reseed of %s from the archived %s backup", slave.URL, tool)
		if err := slave.JobReseedLogicalBackup(context.Background(), tool); err != nil {
			return fail("JobReseedLogicalBackup %s: %s", tool, err)
		}
		if !reseeded(tool, dest, base+50) {
			return false
		}
		ended()
		if !consistent(tool) {
			return false
		}
	}

	tool := config.ConstBackupPhysicalTypeXtrabackup
	if master.IsMariaDB() {
		tool = config.ConstBackupPhysicalTypeMariaBackup
	}
	step("physical: %s backup of the master, archived, original removed", tool)
	originalPhysical := cl.Conf.BackupPhysicalType
	cl.SetBackupPhysicalType(tool)
	defer cl.SetBackupPhysicalType(originalPhysical)
	ended := load("physical")
	if !diverge(301, 310) {
		return false
	}
	started := time.Now()
	if err := master.JobBackupPhysical(); err != nil {
		return fail("JobBackupPhysical: %s", err)
	}
	dest, ok := archived("physical", started, func() *backupmgr.BackupMetadata { return master.LastBackupMeta.Physical })
	if !ok {
		return false
	}
	step("physical reseed of %s from the archived backup", slave.URL)
	if err := slave.JobReseedPhysicalBackup(tool); err != nil {
		return fail("JobReseedPhysicalBackup: %s", err)
	}
	if !reseeded("physical", dest, 400) {
		return false
	}
	ended()
	if !consistent("physical") {
		return false
	}
	// The physical reseed restores replication_manager_schema but its jobs table: the
	// traffic marker table the master holds is on the replica (created once on the
	// master, then only updated), and the replica keeps its own job queue.
	hasTable := func(s *clusterpkg.ServerMonitor, table string) (bool, error) {
		var n int
		err := s.Conn.QueryRowx("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='replication_manager_schema' AND table_name=?", table).Scan(&n)
		return n > 0, err
	}
	if onMaster, err := hasTable(master, "pseudo_gtid_hist"); err != nil {
		return fail("read the traffic marker table on the master: %s", err)
	} else if onMaster {
		if onSlave, err := hasTable(slave, "pseudo_gtid_hist"); err != nil || !onSlave {
			return fail("the physical reseed did not restore the traffic marker table replication_manager_schema.pseudo_gtid_hist on %s (%v)", slave.URL, err)
		}
	}
	if jobs, err := hasTable(slave, "jobs"); err != nil || !jobs {
		return fail("the physical reseed took the jobs table of %s away (%v)", slave.URL, err)
	}

	step("passed: the mysqldump, mydumper and %s backups were removed after their push to archive %s and restored from it, under sysbench", tool, attached)
	return true
}
