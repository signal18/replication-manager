// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestFailoverRejoinLogicalBackupMydumper rejoins a failed primary from a mydumper backup
// (JobFlashbackLogicalBackup, the "logical-backup" rejoin method), #2365: a mydumper backup of
// the primary, a failover with writes running, the old primary started again with the
// automatic rejoin off, then an operator rejoin by logical backup. The old primary must
// finish its flashbackmydumper job and replicate from the new primary with the same data:
// its binary logs, which hold transactions newer than the backup, are reset before its
// gtid_slave_pos (with gtid_strict_mode they stopped replication on an out-of-order GTID).
func (regtest *RegTest) TestFailoverRejoinLogicalBackupMydumper(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	saved := *cluster.Conf
	defer func() {
		cluster.SetBackupLogicalType(saved.BackupLogicalType)
		cluster.Conf.Autorejoin = saved.Autorejoin
		cluster.Conf.AutorejoinFlashback = saved.AutorejoinFlashback
		cluster.Conf.AutorejoinMysqldump = saved.AutorejoinMysqldump
		cluster.Conf.AutorejoinLogicalBackup = saved.AutorejoinLogicalBackup
		cluster.Conf.Interactive = saved.Interactive
		cluster.Conf.FailSync = saved.FailSync
		cluster.Conf.RplChecks = saved.RplChecks
	}()
	fail := func(format string, args ...interface{}) bool {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "rejoin by logical backup: "+format, args...)
		return false
	}

	master := cluster.GetMaster()
	if master == nil {
		return fail("no primary")
	}
	if !master.IsMariaDB() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "rejoin by logical backup: MariaDB only (the mydumper GTID position is gtid_slave_pos), skipped on %s", master.URL)
		return true
	}
	oldURL := master.URL

	cluster.SetBackupLogicalType(config.ConstBackupLogicalTypeMydumper)
	if err := cluster.ResetBench(); err != nil {
		return fail("benchmark prepare failed: %s", err)
	}
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("mydumper backup of %s: %s", oldURL, err)
	}

	// the automatic rejoin stays off: the old primary is rejoined by the operator method only
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(false)
	cluster.DisableSemisync()

	// writes after the backup, so the old primary holds GTIDs newer than it
	go cluster.RunBench()
	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(master)
	wg.Wait()
	if m := cluster.GetMaster(); m == nil || m.URL == oldURL {
		return fail("no failover away from %s", oldURL)
	}

	cluster.StartDatabaseService(master)
	deadline := time.Now().Add(3 * time.Minute)
	for master.IsDown() {
		if time.Now().After(deadline) {
			return fail("%s did not come back", oldURL)
		}
		time.Sleep(2 * time.Second)
	}

	task := "flashback" + config.ConstBackupLogicalTypeMydumper
	requested := time.Now().Unix()
	if !cluster.RearmRejoin(oldURL, "logical-backup") {
		return fail("no crash of %s to rejoin", oldURL)
	}
	deadline = time.Now().Add(15 * time.Minute)
	for {
		t := master.JobResults.Get(task)
		if t != nil && t.Done == 1 && t.End >= requested {
			if t.State != 3 {
				return fail("%s on %s ended with %+v", task, oldURL, t)
			}
			break
		}
		if time.Now().After(deadline) {
			return fail("%s on %s not finished after 15 minutes: %+v", task, oldURL, t)
		}
		time.Sleep(2 * time.Second)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "rejoin by logical backup: %s restored from the mydumper backup: PASS", oldURL)

	if err := cluster.WaitDatabaseReplica(master); err != nil {
		return fail("%s does not replicate after its rejoin: %s", oldURL, err)
	}
	time.Sleep(2 * time.Second)
	if !cluster.CheckTableConsistency(cluster.BenchTable()) {
		return fail("inconsistent replica")
	}
	if !cluster.CheckSlavesRunning() {
		return fail("replication issue")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "rejoin by logical backup: %s replicates from the new primary with the same data: PASS", oldURL)
	return true
}
