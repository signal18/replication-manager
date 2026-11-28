// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// failover-at-sync, #2438: the automatic failover must not promote a replica whose semi-sync is enabled but
// whose replica status is OFF. Five tests share one scenario and differ by the semi-sync state they set up and the
// two safety flags they keep or drop:
//
//	testFailoverSemisyncReplicasOutOfSyncBlocked   semi-sync, both IO threads stopped, failover-at-sync ON: no promotion (named-only)
//	testFailoverSemisyncInSyncAllowed              semi-sync healthy, failover-at-sync ON: promotion, no lost row
//	testFailoverSemisyncDisabledAllowed            semi-sync disabled, failover-at-sync ON: ordinary asynchronous failover
//	testFailoverSemisyncOutOfSyncAtSyncOff         replicas out of sync, failover-at-sync OFF: promotion (override)
//	testFailoverSemisyncOutOfSyncNoRplChecks       replicas out of sync, check-replication-state OFF: promotion (override)
//
// Each test validates the clean state it starts from (one master, every server up and healthy, every replica replicating,
// no schema left by an earlier run) BEFORE anything is changed: configuration, failover counter, automatic failover or
// database. It refuses to run otherwise (FAIL, with the reason). It restores what it changed (semi-sync settings, replication,
// the stopped servers, the master role) in a deferred step, so a failed assertion cleans up too, and it FAILS when the
// cluster could not be put back as found. The framework restores the cluster configuration (RestoreConf) after every test,
// pass or fail.
//
// The scenarios stop the master, like the existing failover regtests: run them on a disposable cluster. That is documented,
// not verified: the framework has no marker of a disposable cluster, and the tests do not enforce it.
//
// ReplicasOutOfSyncBlocked commits rows no replica has. On a build that promotes a replica (the bug it shows) it ends with a
// diverged former master that is not repaired: it is NAMED-ONLY, out of the list ALL runs (see regtest.go), and checks its
// own targets (semisyncBlockedTarget) since testTargets only holds tests of that list. The four others keep every row on the
// replicas, so a promotion loses nothing and the former master rejoins: they stay in ALL.

const (
	semisyncTestSchema = "regtest_semisync_2438"
	semisyncTestTable  = semisyncTestSchema + ".marker"
	// bounds, in seconds
	semisyncWaitState    = 90
	semisyncWaitFailover = 240
	semisyncWaitRestore  = 120
	semisyncWaitHold     = 30
)

// semisyncBlockedTarget is the setup the named-only test applies to; the framework matches testTargets for the tests of the
// list ALL runs only (TestTargetsDeclared), so the named-only test checks it itself.
var semisyncBlockedTarget = TestTarget{TopologyTarget: []string{config.TopoMasterSlave}, NumberNodesTarget: 2}

// semisyncSaved is what a test found on each server and puts back.
type semisyncSaved struct {
	masterEnabled map[string]bool
	slaveEnabled  map[string]bool
	master        string
}

// waitSemisync polls cond for up to timeout seconds and reports whether it became true.
func waitSemisync(timeout int, cond func() bool) bool {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Second)
	}
	return cond()
}

// semisyncVarOn reads a semi-sync variable of the server as the monitor last saw it: ON or 1.
func semisyncVarOn(s *cluster.ServerMonitor, names ...string) bool {
	if s.Variables == nil || s.Variables.Map == nil {
		return false
	}
	for _, n := range names {
		if v := s.Variables.Get(n); strings.EqualFold(v, "ON") || v == "1" {
			return true
		}
	}
	return false
}

func semisyncMasterEnabled(s *cluster.ServerMonitor) bool {
	return semisyncVarOn(s, "RPL_SEMI_SYNC_MASTER_ENABLED", "RPL_SEMI_SYNC_SOURCE_ENABLED")
}

func semisyncSlaveEnabled(s *cluster.ServerMonitor) bool {
	return semisyncVarOn(s, "RPL_SEMI_SYNC_SLAVE_ENABLED", "RPL_SEMI_SYNC_REPLICA_ENABLED")
}

// semisyncMarkers counts the marker rows a server holds; -1 when it cannot be read.
func semisyncMarkers(s *cluster.ServerMonitor) int {
	var n int
	if s.Conn == nil {
		return -1
	}
	if err := s.Conn.QueryRow("SELECT COUNT(*) FROM " + semisyncTestTable).Scan(&n); err != nil {
		return -1
	}
	return n
}

// semisyncReadOnly reads read_only from the server itself: the monitor's copy lags one check behind a role change.
// -1 when it cannot be read.
func semisyncReadOnly(s *cluster.ServerMonitor) int {
	var v int
	if s.Conn == nil {
		return -1
	}
	if err := s.Conn.QueryRow("SELECT @@global.read_only").Scan(&v); err != nil {
		return -1
	}
	return v
}

func semisyncLog(cl *cluster.Cluster, level string, format string, args ...interface{}) {
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
}

func semisyncErr(cl *cluster.Cluster, format string, args ...interface{}) {
	semisyncLog(cl, config.LvlErr, format, args...)
}

// semisyncSetEnabled sets both sides of semi-sync on one server; the replica side takes effect at the next IO thread connect.
func semisyncSetEnabled(s *cluster.ServerMonitor, masterOn bool, slaveOn bool) error {
	if _, err := dbhelper.SetSemiSyncMasterEnabled(s.Conn, s.DBVersion, masterOn); err != nil {
		return err
	}
	_, err := dbhelper.SetSemiSyncSlaveEnabled(s.Conn, s.DBVersion, slaveOn)
	return err
}

// semisyncFind returns the monitored server of that URL, nil when unknown.
func semisyncFind(cl *cluster.Cluster, url string) *cluster.ServerMonitor {
	for _, s := range cl.GetServers() {
		if s.URL == url {
			return s
		}
	}
	return nil
}

// semisyncCleanReason says why the cluster is not in the clean state these tests start from: one master, every server up
// and healthy, every replica replicating, no leftover of an earlier run. Empty when it is clean.
func semisyncCleanReason(cl *cluster.Cluster) string {
	if cl.GetMaster() == nil {
		return "no master"
	}
	if cl.IsInFailover() {
		return "a failover is running"
	}
	masters := 0
	for _, s := range cl.GetServers() {
		if s.IsDown() || s.Conn == nil {
			return fmt.Sprintf("server %s is down", s.URL)
		}
		switch s.State {
		case "Master":
			masters++
		case "Slave":
			if s.IsReplicationBroken() {
				return fmt.Sprintf("replica %s does not replicate", s.URL)
			}
		default:
			return fmt.Sprintf("server %s is in state %s", s.URL, s.State)
		}
		if s.IsDataDiverge {
			return fmt.Sprintf("server %s has diverged data", s.URL)
		}
		var n int
		if err := s.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='" + semisyncTestSchema + "'").Scan(&n); err != nil {
			return fmt.Sprintf("server %s cannot be read: %s", s.URL, err)
		} else if n != 0 {
			return fmt.Sprintf("server %s still holds the schema of an earlier run", s.URL)
		}
	}
	if masters != 1 {
		return fmt.Sprintf("%d servers are masters", masters)
	}
	return ""
}

// semisyncPrepare sets up the cluster for one test and returns what to restore. withSemisync: semi-sync enabled on the
// master (source side) and on every replica (replica side), the replicas' source side as found; otherwise both sides
// OFF everywhere. It then waits for the monitor to see the replicas' operating status match.
func semisyncPrepare(cl *cluster.Cluster, withSemisync bool, failSync bool, rplChecks bool) (*semisyncSaved, bool) {
	// The start state is validated FIRST, before the configuration, the failover counter, the failover mode or the databases
	// are touched: a cluster an earlier destructive test left behind, or one that is failing now, is refused as it is. The
	// refusal changes nothing and cannot enable or trigger a failover.
	if !waitSemisync(semisyncWaitState, func() bool { return semisyncCleanReason(cl) == "" }) {
		semisyncErr(cl, "The cluster is not in a clean state (%s): these tests stop the master and need a disposable cluster, re-provision it", semisyncCleanReason(cl))
		return nil, false
	}
	cl.SetFailoverCtr(0)
	cl.SetInteractive(false) // automatic failover
	cl.SetFailSync(failSync)
	cl.SetRplChecks(rplChecks)
	cl.SetCheckFalsePositiveHeartbeat(false)
	cl.SetFailTime(0)
	cl.SetFailRestartUnsafe(false)
	cl.SetRejoin(true)

	master := cl.GetMaster()
	saved := &semisyncSaved{masterEnabled: map[string]bool{}, slaveEnabled: map[string]bool{}, master: master.URL}
	for _, s := range cl.GetServers() {
		saved.masterEnabled[s.URL] = semisyncMasterEnabled(s)
		saved.slaveEnabled[s.URL] = semisyncSlaveEnabled(s)
	}
	for _, s := range cl.GetSlaves() {
		var err error
		if withSemisync {
			_, err = dbhelper.SetSemiSyncSlaveEnabled(s.Conn, s.DBVersion, true)
		} else {
			err = semisyncSetEnabled(s, false, false)
		}
		if err != nil {
			semisyncErr(cl, "Set semi-sync on replica %s: %s", s.URL, err)
			return saved, false
		}
		// the replica registers as a semi-sync client only when its IO thread connects
		s.ReplicationLink().Stop(cluster.LinkByTest)
		s.ReplicationLink().Start(cluster.LinkByTest)
	}
	var err error
	if withSemisync {
		_, err = dbhelper.SetSemiSyncMasterEnabled(master.Conn, master.DBVersion, true)
	} else {
		err = semisyncSetEnabled(master, false, false)
	}
	if err != nil {
		semisyncErr(cl, "Set semi-sync on master %s: %s", master.URL, err)
		return saved, false
	}
	ok := waitSemisync(semisyncWaitState, func() bool {
		for _, s := range cl.GetSlaves() {
			if s.SemiSyncSlaveStatus != withSemisync || s.IsReplicationBroken() {
				return false
			}
		}
		return len(cl.GetSlaves()) > 0
	})
	if !ok {
		semisyncErr(cl, "Replicas never reached semi-sync status %t with replication running: precondition not met", withSemisync)
		return saved, false
	}
	if _, err := master.Conn.Exec("CREATE DATABASE IF NOT EXISTS " + semisyncTestSchema); err != nil {
		semisyncErr(cl, "Create schema: %s", err)
		return saved, false
	}
	if _, err := master.Conn.Exec("CREATE TABLE IF NOT EXISTS " + semisyncTestTable + " (id INT AUTO_INCREMENT PRIMARY KEY, tag VARCHAR(64))"); err != nil {
		semisyncErr(cl, "Create marker table: %s", err)
		return saved, false
	}
	return saved, true
}

// semisyncInsert commits n identifiable rows on the master, one transaction each.
func semisyncInsert(cl *cluster.Cluster, master *cluster.ServerMonitor, prefix string, n int) bool {
	for i := 1; i <= n; i++ {
		if _, err := master.Conn.Exec(fmt.Sprintf("INSERT INTO %s(tag) VALUES('%s-%d')", semisyncTestTable, prefix, i)); err != nil {
			semisyncErr(cl, "Insert %s-%d on %s: %s", prefix, i, master.URL, err)
			return false
		}
	}
	return true
}

// semisyncRestore puts back what a test changed. It runs deferred, after assertion failures too: servers that are
// down are started, the semi-sync settings and the replication are put back, the first master is the master again,
// the test schema is dropped. A step that fails is logged; the following steps still run. It returns whether the cluster is
// back as found: the first master is the master, the schema is gone, every server is up and every replica replicating.
func semisyncRestore(cl *cluster.Cluster, saved *semisyncSaved) bool {
	if saved == nil {
		return true
	}
	semisyncLog(cl, "TEST", "Restoring the cluster after the semi-sync failover test")
	// 1. every server up
	for _, s := range cl.GetServers() {
		if s.IsDown() {
			cl.StartDatabaseService(s) // the onpremise ssh start may report an error: the start script does the work
			if !waitSemisync(semisyncWaitFailover, func() bool { return !s.IsDown() && !s.IsFailed() }) {
				semisyncErr(cl, "Server %s did not come back", s.URL)
			}
		}
	}
	// 2. semi-sync settings as found, then replication running again
	for _, s := range cl.GetServers() {
		if s.Conn == nil {
			continue
		}
		if err := semisyncSetEnabled(s, saved.masterEnabled[s.URL], saved.slaveEnabled[s.URL]); err != nil {
			semisyncErr(cl, "Restore semi-sync on %s: %s", s.URL, err)
		}
	}
	for _, s := range cl.GetServers() {
		if s.IsMaster() || s.Conn == nil {
			continue
		}
		s.ReplicationLink().Stop(cluster.LinkByTest)
		s.ReplicationLink().Start(cluster.LinkByTest)
	}
	waitSemisync(semisyncWaitState, func() bool {
		for _, s := range cl.GetSlaves() {
			if s.IsReplicationBroken() {
				return false
			}
		}
		return true
	})
	// 3. the first master is the master again: once it rejoined as a healthy replica AND caught up with the current master
	// (a switchover to a replica that is behind leaves the others ahead of their new master, their IO thread cannot
	// start), switch back to it, retried and bounded. A former master that holds rows the promoted master lacks has
	// diverged (the unsafe failover this suite looks for): it cannot rejoin and a switchover cannot undo that, it is reported.
	if m := cl.GetMaster(); m != nil && m.URL != saved.master {
		first := semisyncFind(cl, saved.master)
		deadline := time.Now().Add(semisyncWaitRestore * time.Second)
		attempt := time.Time{}
		diverged := false
		for time.Now().Before(deadline) {
			cur := cl.GetMaster()
			if cur != nil && cur.URL == saved.master && !cl.IsInFailover() {
				break
			}
			if cur != nil && first != nil && !first.IsDown() {
				have, want := semisyncMarkers(first), semisyncMarkers(cur)
				if have > want && want >= 0 {
					diverged = true
					break
				}
				if first.State == "Slave" && !first.IsReplicationBroken() && have == want && want >= 0 && !cl.IsInFailover() && time.Since(attempt) > 20*time.Second {
					attempt = time.Now()
					if err := cl.Switchover(saved.master, false); err != nil {
						semisyncLog(cl, config.LvlWarn, "Switch back to %s: %s", saved.master, err)
					}
				}
			}
			time.Sleep(2 * time.Second)
		}
		if cur := cl.GetMaster(); cur == nil || cur.URL != saved.master {
			current := "none"
			if cur != nil {
				current = cur.URL
			}
			state := "unknown"
			if first != nil {
				state = fmt.Sprintf("state %s, replication broken %t, marker rows %d, current master %d", first.State, first.IsReplicationBroken(), semisyncMarkers(first), func() int {
					if cur != nil {
						return semisyncMarkers(cur)
					}
					return -1
				}())
			}
			semisyncErr(cl, "The master is %s, the first master %s was not restored (diverged %t; %s)", current, saved.master, diverged, state)
		}
	}
	// 4. the test schema
	if m := cl.GetMaster(); m != nil && m.Conn != nil {
		if _, err := m.Conn.Exec("DROP DATABASE IF EXISTS " + semisyncTestSchema); err != nil {
			semisyncErr(cl, "Drop test schema: %s", err)
		}
	}
	// 5. the cluster is left as found: every server up, the schema gone everywhere, every replica replicating.
	// A replica whose IO thread stayed stopped after the switchover is started again, at most every 10 seconds.
	lastStart := time.Time{}
	clean := waitSemisync(semisyncWaitRestore, func() bool {
		clean := true
		for _, s := range cl.GetServers() {
			if s.IsDown() || s.Conn == nil {
				clean = false
				continue
			}
			var n int
			if err := s.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='" + semisyncTestSchema + "'").Scan(&n); err != nil || n != 0 {
				clean = false
			}
			if !s.IsMaster() && s.IsReplicationBroken() {
				clean = false
				if time.Since(lastStart) > 10*time.Second {
					s.ReplicationLink().Start(cluster.LinkByTest)
				}
			}
		}
		if !clean && time.Since(lastStart) > 10*time.Second {
			lastStart = time.Now()
		}
		return clean && !cl.IsInFailover()
	})
	if !clean {
		semisyncErr(cl, "The cluster was not left as found: a server is down, the test schema remains or a replica is not replicating")
	}
	m := cl.GetMaster()
	return clean && m != nil && m.URL == saved.master
}

// semisyncRun prepares the cluster, runs the scenario and always restores. The test fails when the cluster could not be put
// back as found: a scenario that stops the master must never leave a reusable test cluster in a state the next test would
// trust. Run these tests on a disposable cluster: a former master that holds rows the promoted master lacks cannot be
// rejoined by a switchover and is not repaired here (that would hide the failure the test exists to show).
func semisyncRun(cl *cluster.Cluster, withSemisync bool, failSync bool, rplChecks bool, scenario func() bool) (result bool) {
	saved, ok := semisyncPrepare(cl, withSemisync, failSync, rplChecks)
	defer func() {
		if saved == nil {
			return
		}
		if !semisyncRestore(cl, saved) {
			semisyncErr(cl, "The cluster is NOT restored: discard and re-provision it before another test runs on it")
			result = false
		}
	}()
	if !ok {
		return false
	}
	return scenario()
}

// semisyncOutOfSyncFailover is the out-of-sync scenario: semi-sync healthy, every replica loses its IO thread (replica status
// OFF) and the master dies. unreplicatedRows: the master first falls back to asynchronous replication and commits rows no
// replica has (the data-loss case of #2438); without it the replicas hold every row, so a promotion loses nothing and the
// former master rejoins cleanly (the override tests, which must leave a restorable cluster). expectPromotion tells what the
// flags under test must produce.
func semisyncOutOfSyncFailover(cl *cluster.Cluster, failSync bool, rplChecks bool, expectPromotion bool, unreplicatedRows bool) bool {
	return semisyncRun(cl, true, failSync, rplChecks, func() bool {
		master := cl.GetMaster()
		slaves := append([]*cluster.ServerMonitor{}, cl.GetSlaves()...)
		if !semisyncInsert(cl, master, "in-sync", 2) {
			return false
		}
		want := semisyncMarkers(master)
		if !waitSemisync(semisyncWaitState, func() bool {
			for _, s := range slaves {
				if semisyncMarkers(s) != want {
					return false
				}
			}
			return true
		}) {
			semisyncErr(cl, "The replicas did not receive the %d in-sync rows", want)
			return false
		}
		for _, s := range slaves {
			if _, err := s.ReplicationLink().StopCopyEvents(cluster.LinkByTest); err != nil {
				semisyncErr(cl, "Stop the IO thread on %s: %s", s.URL, err)
				return false
			}
		}
		if !waitSemisync(semisyncWaitState, func() bool {
			for _, s := range slaves {
				if s.SemiSyncSlaveStatus {
					return false
				}
			}
			return true
		}) {
			semisyncErr(cl, "The monitor still sees a replica status ON after the IO thread stopped")
			return false
		}
		if unreplicatedRows {
			// the first commit waits for the semi-sync timeout, then the master runs asynchronously
			if !semisyncInsert(cl, master, "async", 3) {
				return false
			}
			if !waitSemisync(semisyncWaitState, func() bool { return !master.SemiSyncMasterStatus }) {
				semisyncErr(cl, "The monitor never saw the master fall back to asynchronous replication")
				return false
			}
			want = semisyncMarkers(master)
			for _, s := range slaves {
				if got := semisyncMarkers(s); got < 0 || got >= want {
					semisyncErr(cl, "Replica %s holds %d marker rows, expected fewer than the master's %d", s.URL, got, want)
					return false
				}
			}
		}
		semisyncLog(cl, "TEST", "Master %s holds %d marker rows, every replica has its IO thread stopped: stopping the master (failover-at-sync=%t check-replication-state=%t)", master.URL, want, failSync, rplChecks)

		cl.StopDatabaseService(master)

		if expectPromotion {
			if !waitSemisync(semisyncWaitFailover, func() bool {
				m := cl.GetMaster()
				return m != nil && m.URL != master.URL && !m.IsDown() && !cl.IsInFailover()
			}) {
				semisyncErr(cl, "No replica was promoted although the safety flag under test is off")
				return false
			}
			newMaster := cl.GetMaster()
			ok := true
			if !waitSemisync(semisyncWaitState, func() bool { return semisyncReadOnly(newMaster) == 0 }) {
				semisyncErr(cl, "The promoted master %s is read only (read_only %d)", newMaster.URL, semisyncReadOnly(newMaster))
				ok = false
			}
			if !unreplicatedRows {
				if got := semisyncMarkers(newMaster); got != want {
					semisyncErr(cl, "The promoted master holds %d marker rows, expected %d", got, want)
					ok = false
				}
			}
			semisyncLog(cl, "TEST", "Promoted %s with %d of %d marker rows (safety flag off: the former behaviour)", newMaster.URL, semisyncMarkers(newMaster), want)
			return ok
		}

		// The failover conditions are met once the master failed for more than max-fail checks: wait for that, then
		// hold a few more checks, so an absent promotion means the election refused every candidate.
		if !waitSemisync(semisyncWaitFailover, func() bool { return master.IsFailed() && master.FailCount >= cl.GetMaxFail() }) {
			semisyncErr(cl, "The master never reached the failover conditions (state %s, fail count %d)", master.State, master.FailCount)
			return false
		}
		time.Sleep(time.Duration(semisyncWaitHold) * time.Second)
		ok := true
		for _, s := range slaves {
			if !cl.GetStateMachine().IsInState("ERR00043@" + s.URL) {
				semisyncErr(cl, "ERR00043 (candidate semi-sync out of sync) is not raised for %s", s.URL)
				ok = false
			}
			if ro := semisyncReadOnly(s); s.State == "Master" || ro != 1 {
				semisyncErr(cl, "Replica %s was promoted or is writable (state %s, read_only %d)", s.URL, s.State, ro)
				ok = false
			}
		}
		if m := cl.GetMaster(); m != nil && m.URL != master.URL {
			semisyncErr(cl, "A new master was elected: %s", m.URL)
			ok = false
		}
		if cl.GetFailoverCtr() != 0 {
			semisyncErr(cl, "A failover was counted: %d", cl.GetFailoverCtr())
			ok = false
		}
		if !master.IsFailed() {
			semisyncErr(cl, "The failed master is not in the failed state (%s): the cluster left the expected failure state", master.State)
			ok = false
		}
		return ok
	})
}

// TestFailoverSemisyncReplicasOutOfSyncBlocked is #2438: nothing is promoted while every replica is out of sync.
// It does not prove that no transaction is lost for good: the stopped master keeps the rows. On a build without the fix it
// ends with a promoted master and a diverged former master: it fails, and the cluster is not repaired. Named-only.
func (regtest *RegTest) TestFailoverSemisyncReplicasOutOfSyncBlocked(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if miss := semisyncBlockedTarget.missReason(cl); miss != "" {
		test.SkipReason = miss // SKIP, before anything is changed
		return false
	}
	return semisyncOutOfSyncFailover(cl, true, true, false, true)
}

// TestFailoverSemisyncOutOfSyncAtSyncOff: failover-at-sync is the override, the failover proceeds as before.
func (regtest *RegTest) TestFailoverSemisyncOutOfSyncAtSyncOff(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return semisyncOutOfSyncFailover(cl, false, true, true, false)
}

// TestFailoverSemisyncOutOfSyncNoRplChecks: check-replication-state is the override of every election check.
func (regtest *RegTest) TestFailoverSemisyncOutOfSyncNoRplChecks(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return semisyncOutOfSyncFailover(cl, true, false, true, false)
}

// semisyncAllowedFailover runs the master-stop scenario where the failover must go through: the rows committed before
// are on the promoted master, it is writable and the other replicas replicate from it.
func semisyncAllowedFailover(cl *cluster.Cluster, withSemisync bool) bool {
	return semisyncRun(cl, withSemisync, true, true, func() bool {
		master := cl.GetMaster()
		slaves := append([]*cluster.ServerMonitor{}, cl.GetSlaves()...)
		if !semisyncInsert(cl, master, "acked", 5) {
			return false
		}
		want := semisyncMarkers(master)
		if !waitSemisync(semisyncWaitState, func() bool {
			for _, s := range slaves {
				if semisyncMarkers(s) != want {
					return false
				}
			}
			return true
		}) {
			semisyncErr(cl, "The replicas did not receive the %d marker rows before the master stopped", want)
			return false
		}
		cl.StopDatabaseService(master)
		if !waitSemisync(semisyncWaitFailover, func() bool {
			m := cl.GetMaster()
			return m != nil && m.URL != master.URL && !m.IsDown() && !cl.IsInFailover()
		}) {
			semisyncErr(cl, "No replica was promoted: failover-at-sync must not stop a failover of an in-sync cluster")
			return false
		}
		newMaster := cl.GetMaster()
		ok := true
		if !waitSemisync(semisyncWaitState, func() bool { return semisyncReadOnly(newMaster) == 0 }) {
			semisyncErr(cl, "The new master %s is read only (read_only %d)", newMaster.URL, semisyncReadOnly(newMaster))
			ok = false
		}
		if got := semisyncMarkers(newMaster); got != want {
			semisyncErr(cl, "The new master holds %d marker rows, expected %d", got, want)
			ok = false
		}
		if _, err := newMaster.Conn.Exec("INSERT INTO " + semisyncTestTable + "(tag) VALUES('after-failover')"); err != nil {
			semisyncErr(cl, "Write on the new master: %s", err)
			return false
		}
		for _, s := range slaves {
			if s.URL == newMaster.URL {
				continue
			}
			if !waitSemisync(semisyncWaitState, func() bool { return !s.IsReplicationBroken() && semisyncMarkers(s) == want+1 }) {
				semisyncErr(cl, "Replica %s did not recover replication from the new master (%d rows, expected %d)", s.URL, semisyncMarkers(s), want+1)
				ok = false
			}
		}
		return ok
	})
}

// TestFailoverSemisyncInSyncAllowed: healthy semi-sync, failover-at-sync ON: the failover goes through.
func (regtest *RegTest) TestFailoverSemisyncInSyncAllowed(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return semisyncAllowedFailover(cl, true)
}

// TestFailoverSemisyncDisabledAllowed: semi-sync disabled, failover-at-sync ON: ordinary asynchronous failover.
func (regtest *RegTest) TestFailoverSemisyncDisabledAllowed(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return semisyncAllowedFailover(cl, false)
}
