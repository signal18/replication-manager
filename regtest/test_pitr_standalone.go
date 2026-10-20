// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// TestPITRStandalone checks that a point-in-time restore runs in the background (#2117) and
// leaves its server standalone with a state saying so (#2115, law T27). A PITR restores a
// backup of the primary, then replays the primary's binary logs from the backup's position up
// to the restore time. Cases "no replay" restore a logical (mysqldump), a physical
// (mariabackup, xtrabackup) and an older logical backup (active replication statements, as
// written before v3.1.44) without replaying, to the backup's own time; cases "logical +
// binlog" and "physical + binlog" replay up to a restore time between two sets of writes:
//
//   - P1: the backup, then a PITR of the first replica from it: the request returns while the
//     restore runs (#2117);
//   - P2: once the restore has ended, the replica has no replication channel
//     replication-manager manages, and still none a few ticks later;
//   - P3: CINF0020 is open on the replica;
//   - P6 (with binary logs): the replica has the rows written before the restore time and none
//     written after it;
//   - P4: a reseed of the replica makes it a replica again, checked as in TestLogicalReseed
//     (after the older-dump case, from a new backup: the reseed refuses that dump);
//   - P5: CINF0020 ends.
//
// It needs the cluster's scheduler for the PITR (monitoring-scheduler, switched on for its run
// only, as the API does), dbjobs next to each database for the physical restore, and reseeds
// the first replica. Run it by name: /api/clusters/<cluster>/tests/actions/run/testPITRStandalone
func (regtest *RegTest) TestPITRStandalone(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e, tool, done := pitrRun(cl, "pitr standalone")
	if e == nil {
		return false
	}
	defer done()

	e.pitrStandalone("logical, no replay", false, func() (int64, int64, bool) {
		if _, ok := e.backup("P1 logical, no replay: logical backup", false); !ok {
			return 0, 0, false
		}
		return e.m.LastBackupMeta.Logical.Id, 0, true
	})
	e.pitrStandalone("physical, no replay", false, func() (int64, int64, bool) {
		id, ok := e.physicalBackup("P1 physical, no replay: physical backup", tool)
		return id, 0, ok
	})
	// an older backup (active CHANGE MASTER, STOP/START SLAVE): its replication statements
	// must not link the PITR server again
	e.pitrStandalone("older dump, no replay", true, func() (int64, int64, bool) {
		meta, ok := e.backup("P1 older dump, no replay: logical backup", false)
		if !ok || !e.writeOlderDump("P1 older dump, no replay: rewritten with active replication statements", meta.Dest) {
			return 0, 0, false
		}
		return e.m.LastBackupMeta.Logical.Id, 0, true
	})
	// with binary logs (#2117: replayed by the PITR's background step): rows written before the
	// restore time reach the replica, rows written after it do not
	e.pitrStandalone("logical + binlog", false, func() (int64, int64, bool) {
		if _, ok := e.backup("P1 logical + binlog: logical backup", false); !ok {
			return 0, 0, false
		}
		id := e.m.LastBackupMeta.Logical.Id
		restoreTime, ok := e.writesAroundRestoreTime("logical + binlog")
		return id, restoreTime, ok
	})
	// the same from a physical backup: the replay starts from the binary log position the
	// backup tool recorded
	e.pitrStandalone("physical + binlog", false, func() (int64, int64, bool) {
		id, ok := e.physicalBackup("P1 physical + binlog: physical backup", tool)
		if !ok {
			return 0, 0, false
		}
		restoreTime, ok := e.writesAroundRestoreTime("physical + binlog")
		return id, restoreTime, ok
	})
	return e.result()
}

// pitrRun prepares a PITR regtest on cl: the first replica must be one of cl's own, the
// scheduler on (set as the API does: it starts it), splitdump off, the physical backup type
// of the engine. done puts the settings back.
func pitrRun(cl *clusterpkg.Cluster, name string) (*edgeRun, string, func()) {
	e := newEdgeRun(cl)
	if e == nil {
		return nil, "", nil
	}
	e.name = name
	// the replica restored must be one of this cluster's, never a child cluster's server the
	// cluster monitors through its multi-source link
	if e.r.GetSourceClusterName() != cl.Name {
		e.logf(config.LvlErr, "the first replica %s belongs to cluster %s, not %s", e.r.URL, e.r.GetSourceClusterName(), cl.Name)
		return nil, "", nil
	}
	restoreConf := saveConf(cl)
	scheduler := cl.Conf.MonitorScheduler
	cl.SetMonitoringScheduler(true)
	cl.Conf.BackupMysqldumpSplitDump = false
	tool := config.ConstBackupPhysicalTypeXtrabackup
	if e.m.IsMariaDB() {
		tool = config.ConstBackupPhysicalTypeMariaBackup
	}
	physical := cl.Conf.BackupPhysicalType
	cl.SetBackupPhysicalType(tool)
	return e, tool, func() {
		cl.SetBackupPhysicalType(physical)
		cl.SetMonitoringScheduler(scheduler)
		restoreConf()
	}
}

// TestPITRFailure checks that a PITR whose restore fails once its request has returned is a
// state (#2117), from a physical restore cancelled as the GUI's reseed cancel does
// (actions/reseed-cancel) before the jobs agent takes it:
//
//   - P7: the cancel marks the restore job cancelled; the PITR ends; ERR00119 is open on the
//     replica and names the cause, CINF0020 is not;
//   - P8: a new (logical) PITR replaces ERR00119 with CINF0020;
//   - P9: a reseed makes the replica a replica again and CINF0020 ends.
//
// Same needs as TestPITRStandalone. Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testPITRFailure
func (regtest *RegTest) TestPITRFailure(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e, tool, done := pitrRun(cl, "pitr failure")
	if e == nil {
		return false
	}
	defer done()
	e.pitrFailed(tool)
	return e.result()
}

// pitrFailed runs the checks of TestPITRFailure.
func (e *edgeRun) pitrFailed(tool string) {
	cl := e.cl
	refresh := time.Duration(cl.Conf.MonitoringTicker) * time.Second * 10
	task := "reseed" + tool
	pitr := func(label string, id int64) bool {
		if err := e.r.ReseedPointInTime(backupmgr.PointInTimeMeta{IsInPITR: true, Backup: id}); err != nil {
			return e.check(label, false, err.Error())
		}
		return true
	}
	ended := func(label string) bool {
		return e.check(label, physReseedWait(30*time.Minute, func() bool { return !e.r.HasAnyReseedingState() && !e.r.PointInTimeMeta.IsInPITR }), "still reseeding or in PITR after 30 minutes")
	}

	e.seed()
	id, ok := e.physicalBackup("P7 cancelled: physical backup", tool)
	if !ok || !pitr("P7 cancelled: PITR", id) {
		return
	}
	// the restore job is written before the request returns: cancel it as the GUI's reseed
	// cancel does, before the jobs agent takes it
	if err := e.r.JobsCancelTasks(false, "reseedmariabackup", "reseedxtrabackup", "flashbackmariabackup", "flashbackxtrabackup"); err != nil {
		e.check("P7 cancelled: cancel", false, err.Error())
		return
	}
	var state int
	var result string
	err := e.r.Conn.QueryRow("SELECT state, COALESCE(result,'') FROM replication_manager_schema.jobs WHERE task=? ORDER BY id DESC LIMIT 1", task).Scan(&state, &result)
	if !e.check("P7 cancelled: restore job cancelled", err == nil && state == 5 && result == "cancelled by user", fmt.Sprintf("last %s job: state %d, result %q, err %v (want 5, \"cancelled by user\"): the jobs agent took the restore before the cancel", task, state, result, err)) {
		return
	}
	if !ended("P7 cancelled: PITR ended") {
		return
	}
	e.check("P7 cancelled: ERR00119 open", waitFor(refresh, func() bool { return cl.IsInErrorState("ERR00119", e.r.URL) }), "not open within "+refresh.String())
	// the open errors, as the API lists them (the open states are rebuilt every tick)
	desc := ""
	for _, st := range cl.GetStateMachine().GetOpenErrors() {
		if strings.HasPrefix(st.ErrNumber, "ERR00119") && strings.Contains(st.ErrDesc, e.r.URL) {
			desc = st.ErrDesc
		}
	}
	e.check("P7 cancelled: ERR00119 names the cause", strings.Contains(desc, fmt.Sprintf("backup %d", id)) && strings.Contains(desc, "cancelled by user"), "ERR00119: "+desc)
	e.check("P7 cancelled: no CINF0020", !cl.IsInErrorState("CINF0020", e.r.URL), "CINF0020 open after a failed PITR")

	// a new PITR replaces the failed one's state
	if _, ok := e.backup("P8 new PITR: logical backup", false); !ok {
		return
	}
	if !pitr("P8 new PITR", e.m.LastBackupMeta.Logical.Id) || !ended("P8 new PITR ended") {
		return
	}
	e.check("P8 new PITR: ERR00119 ended", waitFor(refresh, func() bool { return !cl.IsInErrorState("ERR00119", e.r.URL) }), "still open after "+refresh.String())
	e.check("P8 new PITR: CINF0020 open", waitFor(refresh, func() bool { return cl.IsInErrorState("CINF0020", e.r.URL) }), "not open within "+refresh.String())

	e.reseedAndCheck("P9: reseed makes it a replica again", e.r.HasGTIDReplication())
	e.check("P9 CINF0020 ended", waitFor(refresh, func() bool { return !cl.IsInErrorState("CINF0020", e.r.URL) }), "still open after "+refresh.String())
}

// physicalBackup takes a physical backup of the primary with tool and waits for it to complete.
func (e *edgeRun) physicalBackup(label, tool string) (int64, bool) {
	started := time.Now()
	if err := e.m.JobBackupPhysical(); err != nil {
		return 0, e.check(label, false, err.Error())
	}
	ok := physReseedWait(10*time.Minute, func() bool {
		m := e.m.LastBackupMeta.Physical
		return m != nil && m.Completed && m.EndTime.After(started) && m.BackupTool == tool
	})
	if !e.check(label, ok, "no completed "+tool+" backup within 10 minutes") {
		return 0, false
	}
	return e.m.LastBackupMeta.Physical.Id, true
}

// writesAroundRestoreTime writes rows 20001-20005 on the primary, takes the restore time, then
// writes rows 20006-20010: a PITR to that time has the first five rows and none of the others.
func (e *edgeRun) writesAroundRestoreTime(kind string) (int64, bool) {
	if _, err := e.m.Conn.Exec("INSERT INTO " + edgeTable + " VALUES (20001,'pitr-before'),(20002,'pitr-before'),(20003,'pitr-before'),(20004,'pitr-before'),(20005,'pitr-before')"); err != nil {
		return 0, e.check("P1 "+kind+": rows before the restore time", false, err.Error())
	}
	time.Sleep(2 * time.Second)
	restoreTime := time.Now().Unix()
	time.Sleep(2 * time.Second)
	if _, err := e.m.Conn.Exec("INSERT INTO " + edgeTable + " VALUES (20006,'pitr-after'),(20007,'pitr-after'),(20008,'pitr-after'),(20009,'pitr-after'),(20010,'pitr-after')"); err != nil {
		return 0, e.check("P1 "+kind+": rows after the restore time", false, err.Error())
	}
	return restoreTime, true
}

// pitrStandalone runs P1 to P5 of TestPITRStandalone for one kind of backup, taken by backup
// with the restore time of a PITR with binary logs (0: without); fresh: a new logical backup
// before the P4 reseed.
func (e *edgeRun) pitrStandalone(kind string, fresh bool, backup func() (int64, int64, bool)) {
	cl := e.cl
	gtid := e.r.HasGTIDReplication()
	refresh := time.Duration(cl.Conf.MonitoringTicker) * time.Second * 10
	managed := func() bool {
		for _, r := range e.r.Replications {
			if r.ConnectionName.String == cl.Conf.MasterConn {
				return true
			}
		}
		return false
	}

	e.seed()
	id, restoreTime, ok := backup()
	if !ok {
		return
	}
	if err := e.r.ReseedPointInTime(backupmgr.PointInTimeMeta{IsInPITR: true, Backup: id, UseBinlog: restoreTime > 0, RestoreTime: restoreTime}); err != nil {
		e.check("P1 "+kind+" PITR", false, err.Error())
		return
	}
	// the request returns once the restore is started, not when it ends (#2117)
	e.check("P1 "+kind+" PITR returns while the restore runs", e.r.HasAnyReseedingState(), "the restore had already ended when the request returned")
	// the restore and the PITR's own end run in the background: wait for both, then a few ticks
	restored := physReseedWait(30*time.Minute, func() bool { return !e.r.HasAnyReseedingState() && !e.r.PointInTimeMeta.IsInPITR })
	e.check("P2 "+kind+" restore ended", restored, "still reseeding or in PITR after 30 minutes")
	time.Sleep(time.Duration(cl.Conf.MonitoringTicker) * time.Second * 3)
	e.check("P2 "+kind+" link removed", waitFor(refresh, func() bool { return !managed() }), "the managed channel is in the replica's status")
	e.check("P3 "+kind+" CINF0020 open", waitFor(refresh, func() bool { return cl.IsInErrorState("CINF0020", e.r.URL) }), "not open within "+refresh.String())
	// the restore succeeded: a failed one opens ERR00119 instead (#2117)
	e.check("P2 "+kind+" restore succeeded", !cl.IsInErrorState("ERR00119", e.r.URL), "ERR00119 open: the PITR failed")
	if restoreTime > 0 {
		// the binary logs replayed up to the restore time: the rows written before it, none after
		var n, max int
		err := e.r.Conn.QueryRow("SELECT COUNT(*), COALESCE(MAX(id),0) FROM "+edgeTable+" WHERE id BETWEEN 20001 AND 20010").Scan(&n, &max)
		e.check("P6 "+kind+": the replica stops at the restore time", err == nil && n == 5 && max == 20005, fmt.Sprintf("rows 20001-20010 on the replica: %d, max %d, err %v (want 5, max 20005)", n, max, err))
	}

	// the reseed restores the primary's last logical backup, and refuses an older dump that
	// starts replication itself: after the older-dump case, a new backup first
	if fresh {
		if _, ok := e.backup("P4 "+kind+": new backup", false); !ok {
			return
		}
	}
	e.reseedAndCheck("P4 "+kind+": reseed makes it a replica again", gtid)
	e.check("P5 "+kind+" CINF0020 ended", waitFor(refresh, func() bool { return !cl.IsInErrorState("CINF0020", e.r.URL) }), "still open after "+refresh.String())
}
