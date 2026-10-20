// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// TestPITRStandalone checks that a point-in-time restore leaves its server standalone with a
// state saying so (#2115, law T27), from a logical (mysqldump), a physical (mariabackup,
// xtrabackup) and an older logical backup (active replication statements, as written before
// v3.1.44) of the primary, each without binary logs:
//
//   - P1: the backup, then a PITR of the first replica from it;
//   - P2: once the restore has ended, the replica has no replication channel
//     replication-manager manages, and still none a few ticks later;
//   - P3: CINF0020 is open on the replica;
//   - P4: a reseed of the replica makes it a replica again, checked as in TestLogicalReseed
//     (after the older-dump case, from a new backup: the reseed refuses that dump);
//   - P5: CINF0020 ends.
//
// It needs the cluster's scheduler for the PITR (monitoring-scheduler, set for its run only),
// dbjobs next to each database for the physical restore, and reseeds the first replica. Run
// it by name: /api/clusters/<cluster>/tests/actions/run/testPITRStandalone
func (regtest *RegTest) TestPITRStandalone(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	e.name = "pitr standalone"
	defer saveConf(cl)()
	scheduler := cl.Conf.MonitorScheduler
	defer func() { cl.Conf.MonitorScheduler = scheduler }()
	cl.Conf.MonitorScheduler = true
	cl.Conf.BackupMysqldumpSplitDump = false
	tool := config.ConstBackupPhysicalTypeXtrabackup
	if e.m.IsMariaDB() {
		tool = config.ConstBackupPhysicalTypeMariaBackup
	}
	physical := cl.Conf.BackupPhysicalType
	cl.SetBackupPhysicalType(tool)
	defer cl.SetBackupPhysicalType(physical)

	e.pitrStandalone("logical", false, func() (int64, bool) {
		if _, ok := e.backup("P1 logical backup", false); !ok {
			return 0, false
		}
		return e.m.LastBackupMeta.Logical.Id, true
	})
	e.pitrStandalone("physical", false, func() (int64, bool) {
		started := time.Now()
		if err := e.m.JobBackupPhysical(); err != nil {
			return 0, e.check("P1 physical backup", false, err.Error())
		}
		ok := physReseedWait(10*time.Minute, func() bool {
			m := e.m.LastBackupMeta.Physical
			return m != nil && m.Completed && m.EndTime.After(started) && m.BackupTool == tool
		})
		if !e.check("P1 physical backup", ok, "no completed "+tool+" backup within 10 minutes") {
			return 0, false
		}
		return e.m.LastBackupMeta.Physical.Id, true
	})
	// an older backup (active CHANGE MASTER, STOP/START SLAVE): its replication statements
	// must not link the PITR server again
	e.pitrStandalone("older dump", true, func() (int64, bool) {
		meta, ok := e.backup("P1 older dump: logical backup", false)
		if !ok || !e.writeOlderDump("P1 older dump: rewritten with active replication statements", meta.Dest) {
			return 0, false
		}
		return e.m.LastBackupMeta.Logical.Id, true
	})
	return e.result()
}

// pitrStandalone runs P1 to P5 of TestPITRStandalone for one kind of backup, taken by backup;
// fresh: a new logical backup before the P4 reseed.
func (e *edgeRun) pitrStandalone(kind string, fresh bool, backup func() (int64, bool)) {
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
	id, ok := backup()
	if !ok {
		return
	}
	if err := e.r.ReseedPointInTime(backupmgr.PointInTimeMeta{IsInPITR: true, Backup: id}); err != nil {
		e.check("P1 "+kind+" PITR", false, err.Error())
		return
	}
	e.check("P1 "+kind+" PITR", true, "")
	// the restore ends asynchronously (dbjobs for a physical one): wait for it, then a few ticks
	restored := physReseedWait(30*time.Minute, func() bool { return !e.r.HasAnyReseedingState() })
	e.check("P2 "+kind+" restore ended", restored, "still reseeding after 30 minutes")
	time.Sleep(time.Duration(cl.Conf.MonitoringTicker) * time.Second * 3)
	e.check("P2 "+kind+" link removed", waitFor(refresh, func() bool { return !managed() }), "the managed channel is in the replica's status")
	e.check("P3 "+kind+" CINF0020 open", waitFor(refresh, func() bool { return cl.IsInErrorState("CINF0020", e.r.URL) }), "not open within "+refresh.String())

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
