// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

const aheadSchema = "rejoin_ahead"

// TestFailoverRejoinLogicalBackupAheadOfFailover: the operator rejoin by logical backup must
// never restore on a failed primary a backup holding transactions the new primary never
// received (#2365). The replicas stop receiving (STOP SLAVE IO_THREAD), 100 rows are written
// on the primary, the primary is backed up with mydumper (the backup holds the 100 rows), the
// primary fails over (the new primary has none of them), then is rejoined by "logical-backup".
// PASS only when the old primary ends without the 100 rows and replicating; every observed
// fact is logged either way: the backup picked, the rows on each side, the replication state.
func (regtest *RegTest) TestFailoverRejoinLogicalBackupAheadOfFailover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	saved := *cl.Conf
	defer func() {
		cl.SetBackupLogicalType(saved.BackupLogicalType)
		cl.Conf.Autorejoin = saved.Autorejoin
		cl.Conf.Interactive = saved.Interactive
		cl.Conf.FailSync = saved.FailSync
		cl.Conf.RplChecks = saved.RplChecks
	}()
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "rejoin backup ahead: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "rejoin backup ahead: "+format, args...)
		return false
	}
	count := func(s *cluster.ServerMonitor) string {
		var n int
		if err := s.Conn.QueryRowx("SELECT COUNT(*) FROM " + aheadSchema + ".t").Scan(&n); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("%d", n)
	}

	master := cl.GetMaster()
	if master == nil {
		return fail("no primary")
	}
	if !master.IsMariaDB() {
		logf("MariaDB only, skipped on %s", master.URL)
		return true
	}
	oldURL := master.URL
	// a test cluster: start from no pending crash, so the rejoin below runs on this test's crash
	logf("pending crash records cleared before the test: %d", len(cl.GetCrashes()))
	cl.ResetCrashes()
	cl.SetBackupLogicalType(config.ConstBackupLogicalTypeMydumper)

	for _, q := range []string{"DROP DATABASE IF EXISTS " + aheadSchema, "CREATE DATABASE " + aheadSchema, "CREATE TABLE " + aheadSchema + ".t (id INT PRIMARY KEY)"} {
		if _, err := master.Conn.Exec(q); err != nil {
			return fail("%s on %s: %s", q, oldURL, err)
		}
	}
	time.Sleep(5 * time.Second) // the table reaches the replicas
	replicas := cl.GetSlaves()
	for _, sl := range replicas {
		logs, err := sl.StopSlaveIOThread()
		cl.LogSQL(logs, err, sl.URL, "Rejoin", config.LvlErr, "STOP SLAVE IO_THREAD on %s: %s", sl.URL, err)
		if err != nil {
			return fail("STOP SLAVE IO_THREAD on %s: %s", sl.URL, err)
		}
	}
	for i := 1; i <= 100; i++ {
		if _, err := master.Conn.Exec(fmt.Sprintf("INSERT INTO %s.t VALUES (%d)", aheadSchema, i)); err != nil {
			return fail("insert on %s: %s", oldURL, err)
		}
	}
	logf("100 rows written on %s with the replicas' IO threads stopped", oldURL)
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("mydumper backup of %s: %s", oldURL, err)
	}
	logf("mydumper backup of %s taken: it holds the 100 rows", oldURL)

	cl.SetFailSync(false)
	cl.SetInteractive(false)
	cl.SetRplChecks(false)
	cl.SetRejoin(false)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitFailover(wg)
	cl.StopDatabaseService(master)
	wg.Wait()
	newMaster := cl.GetMaster()
	if newMaster == nil || newMaster.URL == oldURL {
		return fail("no failover away from %s", oldURL)
	}
	logf("failover: %s is the new primary, rows there: %s", newMaster.URL, count(newMaster))
	for _, sl := range cl.GetSlaves() {
		sl.StartSlave()
	}

	cl.StartDatabaseService(master)
	deadline := time.Now().Add(3 * time.Minute)
	for master.IsDown() {
		if time.Now().After(deadline) {
			return fail("%s did not come back", oldURL)
		}
		time.Sleep(2 * time.Second)
	}
	logf("%s back, rows there before the rejoin: %s", oldURL, count(master))

	task := "flashback" + config.ConstBackupLogicalTypeMydumper
	requested := time.Now().Unix()
	if !cl.RearmRejoin(oldURL, "logical-backup") {
		return fail("no crash of %s to rejoin", oldURL)
	}
	deadline = time.Now().Add(15 * time.Minute)
	for {
		t := master.JobResults.Get(task)
		if t != nil && t.Done == 1 && t.End >= requested {
			logf("%s on %s ended: state %d, result %q", task, oldURL, t.State, t.Result)
			break
		}
		if time.Now().After(deadline) {
			logf("%s on %s not finished after 15 minutes: %+v", task, oldURL, t)
			break
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(10 * time.Second)

	onOld, onNew := count(master), count(newMaster)
	rpl := "no replication status"
	if ss, err := master.GetSlaveStatus(master.ReplicationSourceName); err == nil && ss != nil {
		rpl = fmt.Sprintf("IO %s SQL %s, Gtid_IO_Pos %s, last IO error %q, last SQL error %q", ss.SlaveIORunning.String, ss.SlaveSQLRunning.String, ss.GtidIOPos.String, ss.LastIOError.String, ss.LastSQLError.String)
	}
	logf("after the rejoin: rows on %s (old primary) %s, on %s (new primary) %s; %s replication: %s", oldURL, onOld, newMaster.URL, onNew, oldURL, rpl)
	if onOld != onNew {
		return fail("DIVERGED: %s has %s rows of %s.t, the new primary %s has %s", oldURL, onOld, aheadSchema, newMaster.URL, onNew)
	}
	if !cl.CheckSlavesRunning() {
		return fail("replication issue after the rejoin")
	}
	logf("PASS: %s has the new primary's data and replicates", oldURL)
	return true
}
