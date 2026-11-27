// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/gtid"
)

// Bounds of the test, in seconds: every wait is bounded, a timeout is a FAIL with the state of the
// servers in the log.
const (
	electionSyncWait     = 90
	electionHealthWait   = 120
	electionRefusalWait  = 120
	electionNoPromoteFor = 45
	electionSwitchWait   = 120
)

// TestElectionGTIDPositionalFailover (#2451): on MySQL and Percona the election ranked a replica with a
// "sequence" that the MariaDB parser derives from the source UUID, not from the transactions. With a
// source UUID whose third group is decimal-only both replicas got the same positive value, the
// ranking tied, and the preferred but less advanced replica was elected and promoted without the
// last committed transactions. The ranking is now positional (binlog file and Read_Master_Log_Pos).
//
// The scenarios run the real election and the real promotion, never the helpers:
//
//	F  failover (testElectionGTIDPositionalFailover): the primary is stopped; preferred A has not received the last transactions, B has;
//	   B must be elected and promoted, replication must be healthy and no committed transaction lost.
//	R  refusal (testElectionGTIDPositionalRefusal): the most advanced replica B is ineligible (SQL thread stopped, I/O running), the
//	   preferred A is behind and eligible: no replica may be promoted (ERR00032, automatic failover
//	   blocked), and nothing is promoted while the primary is down.
//	E  errant (testElectionGTIDPositionalErrant): a replica carries an errant transaction; the automatic
//	   failover stays held and nothing is promoted (the existing protection, unchanged by the fix).
//	S  switchover (testElectionGTIDPositionalSwitchover) on the non-preferred ranking path: no preferred host, check-replication-state off so
//	   the lagging A (listed first, I/O stopped) stays eligible: B must be elected.
//
// Targets (regtest.go): topology-target master-slave, 3 servers, flavor mysql or percona (the runner
// also requires the mysqlgtid tag). Checked against the discovered cluster: GetTopology() is
// master-slave. A primary whose server_uuid has no decimal-only third group has nothing to exercise:
// SKIP before any change. Premises (checked and logged, never assumed): for each scenario both candidates must carry the SAME positive value
// the MariaDB parser derived (the primary's server_uuid has a decimal-only third group), and the
// candidates must differ in position as the scenario states. A scenario whose premise cannot be
// established is a FAIL when the cluster is not eligible for the defect (a premise the test builds
// itself); when only the decimal-UUID premise is missing (for instance MySQL or Percona below 8.4,
// where the failed-primary metric is empty) the scenario still runs, as a CONTROL, and the test is a
// SKIP, never a PASS. At the end the cluster is put back: the old primary restarted, the replicas
// started, the original primary promoted again and the temporary schema dropped.
//
// The four tests are independent: each starts from the healthy master-slave cluster and puts the
// original primary back. Run one by name:
// /api/clusters/<cluster>/tests/actions/run/testElectionGTIDPositionalFailover (Refusal, Switchover, Errant),
// or all of them with ALL.
func (regtest *RegTest) TestElectionGTIDPositionalFailover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionScenario(cl, test, "testElectionGTIDPositionalFailover", "F", (*electionRun).scenarioFailover)
}

// TestElectionGTIDPositionalRefusal: the most advanced replica is ineligible; nothing may be promoted.
func (regtest *RegTest) TestElectionGTIDPositionalRefusal(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionScenario(cl, test, "testElectionGTIDPositionalRefusal", "R", (*electionRun).scenarioRefusal)
}

// TestElectionGTIDPositionalSwitchover: switchover on the non-preferred ranking path.
func (regtest *RegTest) TestElectionGTIDPositionalSwitchover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionScenario(cl, test, "testElectionGTIDPositionalSwitchover", "S", (*electionRun).scenarioSwitchover)
}

// TestElectionGTIDPositionalErrant: an errant transaction on a replica still holds the automatic failover.
func (regtest *RegTest) TestElectionGTIDPositionalErrant(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionScenario(cl, test, "testElectionGTIDPositionalErrant", "E", (*electionRun).scenarioErrant)
}

func runElectionScenario(cl *cluster.Cluster, test *cluster.Test, name, step string, scenario func(*electionRun)) bool {
	r := newElectionRun(cl, test, name)
	r.step, r.scenario = step, scenario
	return r.finish(r.run())
}

// finish logs the SUMMARY and returns the verdict. The class says what the run established: REPRODUCED (the defect
// premise was established and the assertions ran), CONTROL (the premise is absent: the scenario ran as a control,
// a failure still fails, a pass is a SKIP, never evidence of the fix) or UNESTABLISHED (a premise of the scenario
// could not be established: SKIP, nothing was proven).
func (r *electionRun) finish(ok bool) bool {
	notExercised := append(append([]string{}, r.control...), r.skipped...)
	class := ""
	switch {
	case len(r.skipped) > 0:
		class = " class=UNESTABLISHED"
	case len(r.control) > 0:
		class = " class=CONTROL"
	case r.premiseUsed:
		class = " class=REPRODUCED"
	}
	switch electionVerdict(ok, r.failed, notExercised) {
	case "FAIL":
		r.logf("SUMMARY result=FAIL%s failed=%s passed=%s", class, strings.Join(r.failed, ","), strings.Join(r.passed, ","))
		return false
	case "SKIP":
		reason := strings.Join(notExercised, "; ")
		r.test.SkipReason = reason
		r.logf("SUMMARY result=SKIP%s reason=%q passed=%s", class, reason, strings.Join(r.passed, ","))
	default:
		r.logf("SUMMARY result=PASS%s checks=%s", class, strings.Join(r.passed, ","))
	}
	return true
}

type electionRun struct {
	name        string
	step        string // label of the scenario in the check names (F, R, S, E)
	scenario    func(*electionRun)
	recovered   bool                     // the scenario recovery ran
	stopped     []*cluster.ServerMonitor // servers this test stopped: started again at the end, whatever the verdict
	schemaOwned bool                     // the schema was created by this run: only then it is dropped
	cl          *cluster.Cluster
	test        *cluster.Test
	schema      string
	orig        *cluster.ServerMonitor
	prefered    map[string]bool
	passed      []string
	failed      []string
	control     []string // scenarios that ran without the defect premise
	skipped     []string // scenarios that could not run
	rows        int
	premiseUsed bool // the test established a defect premise (r.premise)
}

// electionSuffix is a random suffix, so that the temporary schema and the application account of a run can
// never collide with an object a user already has.
func electionSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newElectionRun(cl *cluster.Cluster, test *cluster.Test, name string) *electionRun {
	return &electionRun{cl: cl, test: test, name: name, schema: "regtest_election_2451_" + electionSuffix(), prefered: map[string]bool{}}
}

// createSchema creates the temporary schema; it fails when the name already exists (never IF NOT EXISTS), so that a
// run only ever drops what it created.
func (r *electionRun) createSchema(primary *cluster.ServerMonitor) error {
	if err := electionExec(primary, "CREATE DATABASE "+r.schema); err != nil {
		return err
	}
	r.schemaOwned = true
	return nil
}

// stop stops a database service and remembers it: the cleanup starts it again whatever the verdict.
func (r *electionRun) stop(srv *cluster.ServerMonitor) {
	r.stopped = append(r.stopped, srv)
	r.cl.StopDatabaseService(srv)
}

// requireTarget is the applicability gate of a test that is run by name only (not in the ALL list, so without
// a TestTarget): the declared topology-target, the number of servers and the engine. A miss is a SKIP with the
// reason, before anything is changed.
func (r *electionRun) requireTarget(target string, nodes int) bool {
	cl := r.cl
	reason := ""
	switch {
	case cl.GetTopologyTarget() != target:
		reason = fmt.Sprintf("topology-target %s, cluster is %s", target, cl.GetTopologyTarget())
	case len(cl.Servers) < nodes:
		reason = fmt.Sprintf("number-nodes-target %d, cluster has %d", nodes, len(cl.Servers))
	default:
		for _, s := range cl.Servers {
			if s.DBVersion == nil || !s.DBVersion.IsMySQLOrPercona() {
				reason = "flavor-target mysql, percona: " + s.URL + " is another engine"
				break
			}
		}
	}
	if reason != "" {
		r.test.SkipReason = reason
		r.skipped = append(r.skipped, reason)
		return false
	}
	return true
}

// requireHealthy is healthyStart as a SKIP: a destructive test does not start from a cluster that is not healthy.
func (r *electionRun) requireHealthy(primary *cluster.ServerMonitor) bool {
	if ok, why := r.healthyStart(primary); !ok {
		reason := "prerequisite not established, the cluster is not healthy at the start: " + why
		r.test.SkipReason = reason
		r.skipped = append(r.skipped, reason)
		return false
	}
	return true
}

// restoreStopped puts back what the test stopped, whatever the verdict: stopped servers are started (and rejoin by
// the product's own rejoin), replication threads that are not running are started. It resets and re-creates
// nothing: a divergence the scenario caused stays visible and fails the cleanup check.
func (r *electionRun) restoreStopped() {
	for _, s := range r.stopped {
		if s.IsFailed() {
			r.logf("cleanup: starting %s", s.URL)
			r.cl.StartDatabaseWaitRejoin(s)
		}
	}
	electionWait(electionHealthWait, func() bool {
		all := true
		primary := r.cl.GetMaster()
		for _, s := range r.cl.Servers {
			if primary != nil && s.URL == primary.URL {
				continue
			}
			if io, sqlThread, err := electionReplicaThreads(s); err != nil || io != "Yes" || sqlThread != "Yes" {
				all = false
				s.StartSlave()
			}
		}
		return all
	})
}

func (r *electionRun) logf(format string, args ...interface{}) {
	r.cl.LogModulePrintf(r.cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", r.name+": "+format, args...)
}

func (r *electionRun) errf(format string, args ...interface{}) {
	r.cl.LogModulePrintf(r.cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, r.name+": "+format, args...)
}

// check records one assertion.
func (r *electionRun) check(step string, ok bool, format string, args ...interface{}) bool {
	if ok {
		r.passed = append(r.passed, step)
		r.logf("CHECK %s PASS: %s", step, fmt.Sprintf(format, args...))
		return true
	}
	r.failed = append(r.failed, step)
	r.errf("CHECK %s FAIL: %s", step, fmt.Sprintf(format, args...))
	return false
}

// electionSQLTimeout bounds every statement of the test: a server that never answers (a global read lock
// held by another session, a hung connection) is a FAIL or a logged cleanup error, never a hang.
var electionSQLTimeout = 30 * time.Second

func electionQuery(srv *cluster.ServerMonitor, query string, args ...interface{}) (string, error) {
	if srv == nil || srv.Conn == nil {
		return "", errors.New("no connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	var v sql.NullString
	if err := srv.Conn.QueryRowContext(ctx, query, args...).Scan(&v); err != nil {
		return "", err
	}
	return v.String, nil
}

func electionExec(srv *cluster.ServerMonitor, query string, args ...interface{}) error {
	if srv == nil || srv.Conn == nil {
		return errors.New("no connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	_, err := srv.Conn.ExecContext(ctx, query, args...)
	return err
}

func electionGTIDExecuted(srv *cluster.ServerMonitor) (string, error) {
	return electionQuery(srv, "SELECT REPLACE(REPLACE(@@global.gtid_executed,'\n',''),' ','')")
}

func electionWait(seconds int, cond func() bool) bool {
	for i := 0; i < seconds; i++ {
		if cond() {
			return true
		}
		time.Sleep(time.Second)
	}
	return cond()
}

// dump logs what an operator needs to understand a failure: state, read-only, executed GTIDs and
// replication coordinates of every server.
func (r *electionRun) dump(label string) {
	for _, s := range r.cl.Servers {
		ro, _ := electionQuery(s, "SELECT @@global.read_only")
		ge, _ := electionGTIDExecuted(s)
		line := fmt.Sprintf("%s: %s state=%s read_only=%s gtid_executed=%s", label, s.URL, s.State, ro, ge)
		if ss, err := s.GetSlaveStatus(s.ReplicationSourceName); err == nil {
			line += fmt.Sprintf(" source=%s file=%s pos=%s io=%s sql=%s retrieved=%s", ss.MasterHost.String, ss.MasterLogFile.String, ss.ReadMasterLogPos.String, ss.SlaveIORunning.String, ss.SlaveSQLRunning.String, ss.GtidIOPos.String)
		}
		r.logf("%s", line)
	}
}

// parsedSeq is the value the MariaDB GTID parser derives from the candidate's replication
// coordinates: the ranking value the election used before the fix. failedPath selects the
// coordinate the election reads when the primary is down (the received set).
func (r *electionRun) parsedSeq(srv *cluster.ServerMonitor, failedPath bool) (uint64, error) {
	master := r.cl.GetMaster()
	if master == nil {
		return 0, errors.New("no master")
	}
	var l *gtid.List
	if failedPath {
		ss, err := srv.GetSlaveStatus(srv.ReplicationSourceName)
		if err != nil {
			return 0, err
		}
		l = gtid.NewList(ss.GtidIOPos.String)
	} else {
		l = srv.SlaveGtid
	}
	if l == nil {
		return 0, nil
	}
	var sum uint64
	for _, v := range l.GetSeqDomainIdNos(master.DomainID) {
		sum += v
	}
	return sum, nil
}

// receivedAhead tells whether a has received more of the primary's binlog than b (same file name
// width: file name then position).
func electionReceivedAhead(a, b *cluster.ServerMonitor) (bool, error) {
	sa, err := a.GetSlaveStatus(a.ReplicationSourceName)
	if err != nil {
		return false, err
	}
	sb, err := b.GetSlaveStatus(b.ReplicationSourceName)
	if err != nil {
		return false, err
	}
	if sa.MasterLogFile.String != sb.MasterLogFile.String {
		return sa.MasterLogFile.String > sb.MasterLogFile.String, nil
	}
	pa, _ := strconv.ParseUint(sa.ReadMasterLogPos.String, 10, 64)
	pb, _ := strconv.ParseUint(sb.ReadMasterLogPos.String, 10, 64)
	return pa > pb, nil
}

func (r *electionRun) insert(master *cluster.ServerMonitor, tag string) bool {
	if err := electionExec(master, "INSERT INTO "+r.schema+".marker(tag) VALUES(?)", tag); err != nil {
		r.errf("insert %s on %s: %s", tag, master.URL, err)
		return false
	}
	r.rows++
	return true
}

func (r *electionRun) count(srv *cluster.ServerMonitor) (int, error) {
	v, err := electionQuery(srv, "SELECT COUNT(*) FROM "+r.schema+".marker")
	if err != nil {
		return -1, err
	}
	n, _ := strconv.Atoi(v)
	return n, nil
}

// subset is GTID_SUBSET(executed, the server's gtid_executed): every transaction of the set is on the server.
func electionSubset(srv *cluster.ServerMonitor, executed string) bool {
	v, err := electionQuery(srv, "SELECT GTID_SUBSET(?, @@global.gtid_executed)", executed)
	return err == nil && v == "1"
}

// electionDecimalThirdGroup: the third dash-separated group of the UUID is made of decimal digits only,
// the shape the MariaDB parser reads as a sequence number.
func electionDecimalThirdGroup(uuid string) bool {
	parts := strings.Split(uuid, "-")
	if len(parts) != 5 || parts[2] == "" {
		return false
	}
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (r *electionRun) run() bool {
	cl := r.cl
	master := cl.GetMaster()
	// The declared topology-target, the node count and the flavor are the TestTarget of the test
	// (regtest.go). What the target cannot say is checked here against the discovered cluster.
	if master == nil || len(cl.GetSlaves()) < 2 {
		return r.check("setup", false, "a master-slave cluster of 3 servers needs a master and two replicas, found master=%s replicas=%d", urlOf(master), len(cl.GetSlaves()))
	}
	if cl.GetTopology() != config.TopoMasterSlave {
		return r.check("setup", false, "topology-target %s but the discovered topology is %s", cl.GetTopologyTarget(), cl.GetTopology())
	}
	if master.DBVersion == nil || !master.DBVersion.IsMySQLOrPercona() {
		return r.check("setup", false, "the master %s is not MySQL or Percona", master.URL)
	}
	// Nothing to exercise without the data premise of the defect: SKIP before any change.
	if uuid, err := electionQuery(master, "SELECT @@server_uuid"); err != nil || !electionDecimalThirdGroup(uuid) {
		r.test.SkipReason = fmt.Sprintf("the primary's server_uuid %q has no decimal-only third group (err=%v): the MariaDB parser derives 0 from it, the defect cannot show", uuid, err)
		r.skipped = append(r.skipped, r.test.SkipReason)
		return true
	}
	r.orig = master
	for _, s := range cl.Servers {
		r.prefered[s.URL] = s.IsPrefered()
	}
	uuid, _ := electionQuery(master, "SELECT @@server_uuid")
	r.logf("cluster %s: primary %s version %s server_uuid=%s binlog=%s", cl.Name, master.URL, master.DBVersion.ToString(), uuid, master.BinaryLogFile)

	if ok, why := r.healthyStart(master); !ok {
		r.test.SkipReason = "prerequisite not established, the cluster is not healthy at the start: " + why
		r.skipped = append(r.skipped, r.test.SkipReason)
		return true
	}
	cl.SetInteractive(false)
	cl.SetCheckFalsePositiveHeartbeat(false)
	cl.SetFailSync(false)
	cl.SetSwitchSync(false)
	cl.SetRejoin(true)
	cl.SetRejoinFlashback(false)
	cl.SetRejoinDump(false)

	defer r.cleanup()
	if err := r.createSchema(master); err != nil {
		return r.check("setup", false, "create schema %s: %s", r.schema, err)
	}
	if err := electionExec(master, "CREATE TABLE "+r.schema+".marker (id INT AUTO_INCREMENT PRIMARY KEY, tag VARCHAR(64)) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create table: %s", err)
	}
	if err := electionExec(master, "CREATE TABLE "+r.schema+".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create probe table: %s", err)
	}
	if !r.syncAll(master, "setup") {
		return false
	}
	r.scenario(r)
	// the failure evidence is kept before any recovery, and a recovery never changes the verdict
	if len(r.failed) > 0 {
		r.dump("failure-state")
	}
	if !r.recovered {
		r.recover(r.step)
	}
	return len(r.failed) == 0
}

// syncAll waits until every replica has executed the primary's transactions.
func (r *electionRun) syncAll(master *cluster.ServerMonitor, step string) bool {
	want, err := electionGTIDExecuted(master)
	if err != nil {
		return r.check(step+"-sync", false, "gtid_executed of %s: %s", master.URL, err)
	}
	ok := electionWait(electionSyncWait, func() bool {
		for _, s := range r.cl.GetSlaves() {
			if !electionSubset(s, want) {
				return false
			}
		}
		return true
	})
	if !ok {
		r.dump(step + "-sync")
	}
	return r.check(step+"-sync", ok, "every replica executed the primary's %s", want)
}

// candidates returns the preferred, lagging replica A (first in the replica list) and B.
func (r *electionRun) candidates() (a, b *cluster.ServerMonitor) {
	s := r.cl.GetSlaves()
	return s[0], s[1]
}

// premise records the decimal-UUID premise of a scenario: the parsed value of both candidates is the
// same and positive. Without it the scenario is a CONTROL.
func (r *electionRun) premise(name string, a, b *cluster.ServerMonitor, failedPath bool) {
	r.premiseUsed = true
	sa, ea := r.parsedSeq(a, failedPath)
	sb, eb := r.parsedSeq(b, failedPath)
	r.logf("%s premise: parsed sequence A(%s)=%d(%v) B(%s)=%d(%v) failedPath=%v", name, a.URL, sa, ea, b.URL, sb, eb, failedPath)
	if ea != nil || eb != nil || sa != sb || sa == 0 {
		r.control = append(r.control, name+": defect premise not established (parsed sequence of A and B is not equal and positive), run as control")
	}
}

// stopped says whether a server's replica threads are what the scenario needs.
func (r *electionRun) ioStopSQLRunning(srv *cluster.ServerMonitor) bool {
	ss, err := srv.GetSlaveStatus(srv.ReplicationSourceName)
	return err == nil && ss.SlaveIORunning.String == "No" && ss.SlaveSQLRunning.String == "Yes"
}

func (r *electionRun) scenarioFailover() {
	cl := r.cl
	master := cl.GetMaster()
	a, b := r.candidates()
	r.logf("F: primary %s, preferred and lagging A %s, advanced B %s", master.URL, a.URL, b.URL)
	cl.SetRplChecks(true)
	cl.SetPrefMaster(a.URL)
	time.Sleep(2 * time.Second)
	if _, err := a.StopSlaveIOThread(); err != nil {
		r.check("F-premise", false, "stop I/O thread of A: %s", err)
		return
	}
	for _, tag := range []string{"f1", "f2", "f3", "f4", "f5"} {
		if !r.insert(master, tag) {
			r.check("F-premise", false, "insert %s", tag)
			return
		}
	}
	want, _ := electionGTIDExecuted(master)
	bOK := electionWait(electionSyncWait, func() bool { return electionSubset(b, want) })
	nA, _ := r.count(a)
	nB, _ := r.count(b)
	// the monitor reads the status on its tick: wait until its view shows B ahead of A
	ahead := electionWait(electionSyncWait, func() bool { v, _ := electionReceivedAhead(b, a); return v })
	if !r.check("F-premise", bOK && nA < r.rows && nB == r.rows && ahead && r.ioStopSQLRunning(a), "A behind (%d of %d rows, I/O stopped), B current (%d rows), B received ahead of A=%v", nA, r.rows, nB, ahead) {
		r.dump("F-premise")
		return
	}
	r.premise("F", a, b, true)
	ctr := cl.GetFailoverCtr()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitFailover(wg)
	r.stop(master)
	wg.Wait()
	time.Sleep(5 * time.Second)

	nm := cl.GetMaster()
	r.dump("F-after")
	if !r.check("F-selection", nm != nil && nm.URL == b.URL, "elected primary is %s, want the most advanced replica %s (preferred A %s is behind)", urlOf(nm), b.URL, a.URL) {
		return
	}
	ro, _ := electionQuery(nm, "SELECT @@global.read_only")
	r.check("F-promotion", cl.GetFailoverCtr() == ctr+1 && ro == "0" && master.IsFailed(), "failover counter %d->%d, read_only=%s on %s, old primary failed=%v", ctr, cl.GetFailoverCtr(), ro, nm.URL, master.IsFailed())
	r.independentWrite("F-writable", nm)
	r.check("F-consistency", electionSubset(nm, want), "every transaction of the old primary (%s) is on the new primary %s", want, nm.URL)
	healthy := electionWait(electionHealthWait, func() bool { return r.replicating(a, nm) })
	r.check("F-replication", healthy, "A %s replicates from the new primary %s", a.URL, nm.URL)
	nA2 := -1
	electionWait(electionHealthWait, func() bool { nA2, _ = r.count(a); return nA2 == r.rows })
	nNew, _ := r.count(nm)
	r.check("F-rows", nNew == r.rows && nA2 == r.rows, "rows on the new primary=%d, on A=%d, committed=%d", nNew, nA2, r.rows)
	r.recover("F")
}

func urlOf(s *cluster.ServerMonitor) string {
	if s == nil {
		return "<none>"
	}
	return s.URL
}

// replicating says whether srv replicates from source with both threads running.
func (r *electionRun) replicating(srv, source *cluster.ServerMonitor) bool {
	ss, err := srv.GetSlaveStatus(srv.ReplicationSourceName)
	if err != nil {
		return false
	}
	return ss.SlaveIORunning.String == "Yes" && ss.SlaveSQLRunning.String == "Yes" && (ss.MasterHost.String == source.Host || ss.MasterHost.String+":"+ss.MasterPort.String == source.URL)
}

func (r *electionRun) scenarioRefusal() {
	cl := r.cl
	master := cl.GetMaster()
	a, b := r.candidates()
	r.logf("R: primary %s, preferred, behind and eligible A %s, most advanced and ineligible B %s", master.URL, a.URL, b.URL)
	cl.SetRplChecks(true)
	cl.SetPrefMaster(a.URL)
	time.Sleep(2 * time.Second)
	// A must have received from the primary before it stops receiving: a replica whose received
	// set is empty (fresh channel after the previous scenario) has no GTID term to tie with.
	if !r.insert(master, "r0") || !r.syncAll(master, "R-baseline") {
		r.check("R-premise", false, "A and B did not receive and execute r0")
		return
	}
	if _, err := a.StopSlaveIOThread(); err != nil {
		r.check("R-premise", false, "stop I/O thread of A: %s", err)
		return
	}
	if _, err := b.StopSlaveSQLThread(); err != nil {
		r.check("R-premise", false, "stop SQL thread of B: %s", err)
		return
	}
	for _, tag := range []string{"r1", "r2", "r3"} {
		if !r.insert(master, tag) {
			r.check("R-premise", false, "insert %s", tag)
			return
		}
	}
	want, _ := electionGTIDExecuted(master)
	var ahead bool
	received := electionWait(electionSyncWait, func() bool {
		ahead, _ = electionReceivedAhead(b, a)
		ss, err := b.GetSlaveStatus(b.ReplicationSourceName)
		return err == nil && ahead && ss.SlaveIORunning.String == "Yes" && ss.SlaveSQLRunning.String == "No"
	})
	if !r.check("R-premise", received, "B received ahead of A with its SQL thread stopped and its I/O thread running (A I/O stopped)") {
		r.dump("R-premise")
		return
	}
	r.premise("R", a, b, true)
	ctr := cl.GetFailoverCtr()
	r.stop(master)

	refused := electionWait(electionRefusalWait, func() bool { return cl.GetStateMachine().IsInState("ERR00032") })
	r.check("R-refusal-state", refused, "ERR00032 (no candidate, automatic failover blocked) raised while the primary %s is down", master.URL)
	// bounded window: nothing may be promoted while the most advanced replica is ineligible
	promoted := false
	for i := 0; i < electionNoPromoteFor && !promoted; i++ {
		if m := cl.GetMaster(); m == nil || m.URL != master.URL || cl.GetFailoverCtr() != ctr {
			promoted = true
		}
		for _, s := range []*cluster.ServerMonitor{a, b} {
			if ro, err := electionQuery(s, "SELECT @@global.read_only"); err == nil && ro == "0" {
				promoted = true
			}
		}
		time.Sleep(time.Second)
	}
	r.dump("R-after")
	r.check("R-no-promotion", !promoted && master.IsFailed(), "no replica promoted for %ds, failover counter %d->%d, old primary failed=%v, master now %s", electionNoPromoteFor, ctr, cl.GetFailoverCtr(), master.IsFailed(), urlOf(cl.GetMaster()))
	_ = want
	r.recover("R")
}

func (r *electionRun) scenarioSwitchover() {
	cl := r.cl
	master := cl.GetMaster()
	a, b := r.candidates()
	r.logf("S: switchover from %s, no preferred host, lagging A %s listed first, advanced B %s", master.URL, a.URL, b.URL)
	cl.SetPrefMaster("")
	cl.SetRplChecks(false)
	time.Sleep(2 * time.Second)
	if _, err := a.StopSlaveIOThread(); err != nil {
		r.check("S-premise", false, "stop I/O thread of A: %s", err)
		return
	}
	for _, tag := range []string{"s1", "s2", "s3"} {
		if !r.insert(master, tag) {
			r.check("S-premise", false, "insert %s", tag)
			return
		}
	}
	want, _ := electionGTIDExecuted(master)
	bOK := electionWait(electionSyncWait, func() bool { return electionSubset(b, want) })
	nA, _ := r.count(a)
	nB, _ := r.count(b)
	// the monitor reads the status on its tick: wait until its view shows B ahead of A
	ahead := electionWait(electionSyncWait, func() bool { v, _ := electionReceivedAhead(b, a); return v })
	first := cl.GetSlaves()[0].URL == a.URL
	if !r.check("S-premise", bOK && nA < r.rows && nB == r.rows && ahead && first && !a.IsPrefered() && !b.IsPrefered(), "A first in the list=%v, A behind (%d of %d rows), B current (%d), B received ahead=%v, no preferred host", first, nA, r.rows, nB, ahead) {
		r.dump("S-premise")
		return
	}
	r.premise("S", a, b, false)
	ctr := cl.GetFailoverCtr()
	cl.SwitchoverWaitTest()
	electionWait(electionSwitchWait, func() bool { m := cl.GetMaster(); return m != nil && m.URL != master.URL })
	time.Sleep(5 * time.Second)
	nm := cl.GetMaster()
	r.dump("S-after")
	if !r.check("S-selection", nm != nil && nm.URL == b.URL, "elected primary is %s, want the most advanced replica %s (lagging A %s is listed first)", urlOf(nm), b.URL, a.URL) {
		r.recover("S")
		return
	}
	ro, _ := electionQuery(nm, "SELECT @@global.read_only")
	oro, _ := electionQuery(master, "SELECT @@global.read_only")
	r.check("S-promotion", cl.GetFailoverCtr() >= ctr && ro == "0" && oro == "1", "read_only=%s on the new primary %s and %s on the old primary", ro, nm.URL, oro)
	r.independentWrite("S-writable", nm)
	r.check("S-consistency", electionSubset(nm, want), "every transaction of the old primary (%s) is on the new primary %s", want, nm.URL)
	healthy := electionWait(electionHealthWait, func() bool { return r.replicating(a, nm) && r.replicating(master, nm) })
	r.check("S-replication", healthy, "A %s and the old primary %s replicate from the new primary %s", a.URL, master.URL, nm.URL)
	nA2, nOld := -1, -1
	electionWait(electionHealthWait, func() bool { nA2, _ = r.count(a); nOld, _ = r.count(master); return nA2 == r.rows && nOld == r.rows })
	nNew, _ := r.count(nm)
	r.check("S-rows", nNew == r.rows && nA2 == r.rows && nOld == r.rows, "rows on the new primary=%d, on A=%d, on the old primary=%d, committed=%d", nNew, nA2, nOld, r.rows)
	r.recover("S")
}

// recover puts the servers back after a scenario: failed servers started, replicas started, the
// original primary promoted again.
func (r *electionRun) recover(step string) {
	r.recovered = true
	cl := r.cl
	for _, s := range cl.Servers {
		if s.IsFailed() {
			r.logf("%s cleanup: starting %s", step, s.URL)
			cl.StartDatabaseWaitRejoin(s)
		}
	}
	for _, s := range cl.GetSlaves() {
		if ss, err := s.GetSlaveStatus(s.ReplicationSourceName); err == nil && (ss.SlaveIORunning.String != "Yes" || ss.SlaveSQLRunning.String != "Yes") {
			if _, err := s.StartSlave(); err != nil {
				r.errf("%s cleanup: start replication on %s: %s", step, s.URL, err)
			}
		}
	}
	if m := cl.GetMaster(); m != nil && m.URL != r.orig.URL {
		cl.SetRplChecks(true)
		cl.SetPrefMaster(r.orig.URL)
		time.Sleep(2 * time.Second)
		cl.SwitchoverWaitTest()
		electionWait(electionSwitchWait, func() bool { m := cl.GetMaster(); return m != nil && m.URL == r.orig.URL })
	}
	healthy := electionWait(electionHealthWait, func() bool {
		all := true
		for _, s := range cl.Servers {
			if m := cl.GetMaster(); m != nil && s.URL == m.URL {
				continue
			}
			if io, sqlThread, err := electionReplicaThreads(s); err != nil || io != "Yes" || sqlThread != "Yes" {
				all = false
				s.StartSlave() // start the threads the scenario stopped
			}
		}
		return all
	})
	r.check(step+"-cleanup-replication", healthy, "every replica runs both replication threads (live status) after the scenario")
	if !healthy {
		r.dump(step + "-cleanup")
	}
	if m := cl.GetMaster(); m == nil || m.URL != r.orig.URL {
		r.check(step+"-cleanup", false, "the original primary %s is not primary again (now %s)", r.orig.URL, urlOf(m))
	}
}

// electionOpenIndependent opens a session of its own on a server (not repman's connection pool): what a
// client sees. A variable so that the unit test can answer with a mock.
var electionOpenIndependent = func(srv *cluster.ServerMonitor) (*sql.DB, error) {
	if srv == nil || srv.DSN == "" {
		return nil, errors.New("no DSN")
	}
	db, err := sql.Open("mysql", srv.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// electionIndependentWrite commits a row through a session of its own, the way a client would write:
// a read-only server, a global read lock or a hung server is an error, bounded by the deadline.
func (r *electionRun) electionIndependentWrite(srv *cluster.ServerMonitor) error {
	db, err := electionOpenIndependent(srv)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET SESSION lock_wait_timeout=10"); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO "+r.schema+".probe(who) VALUES(?)", "independent-"+srv.URL)
	return err
}

// electionGlobalLockHolders counts the idle sessions of a server that hold a granted global shared metadata
// lock: an idle session holds none, except under FLUSH TABLES WITH READ LOCK. Read through a session of its own.
func electionGlobalLockHolders(srv *cluster.ServerMonitor) (int, error) {
	db, err := electionOpenIndependent(srv)
	if err != nil {
		return -1, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	var n int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM performance_schema.metadata_locks m JOIN performance_schema.threads t ON t.THREAD_ID=m.OWNER_THREAD_ID WHERE m.OBJECT_TYPE='GLOBAL' AND m.LOCK_TYPE='SHARED' AND m.LOCK_STATUS='GRANTED' AND t.PROCESSLIST_COMMAND='Sleep'").Scan(&n)
	return n, err
}

// electionReplicaThreads reads the live state of a replica's two threads through a session of its own
// (never repman's cached status): the values of Replica_IO_Running and Replica_SQL_Running.
func electionReplicaThreads(srv *cluster.ServerMonitor) (io, sqlThread string, err error) {
	db, err := electionOpenIndependent(srv)
	if err != nil {
		return "", "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	for _, q := range []string{"SHOW REPLICA STATUS", "SHOW SLAVE STATUS"} {
		rows, qerr := db.QueryContext(ctx, q)
		if qerr != nil {
			err = qerr
			continue
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		if !rows.Next() {
			return "", "", errors.New("no replication channel")
		}
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if serr := rows.Scan(ptrs...); serr != nil {
			return "", "", serr
		}
		for i, c := range cols {
			switch c {
			case "Replica_IO_Running", "Slave_IO_Running":
				io = string(vals[i])
			case "Replica_SQL_Running", "Slave_SQL_Running":
				sqlThread = string(vals[i])
			}
		}
		return io, sqlThread, nil
	}
	return "", "", err
}

// healthyStart: a destructive scenario starts only from a verified healthy cluster: every replica runs both
// threads (live status) and no server holds a global read lock. Otherwise the test does not run (SKIP with
// the reason): the previous operation left the cluster unhealthy, which is not a result of this test.
func (r *electionRun) healthyStart(primary *cluster.ServerMonitor) (bool, string) {
	for _, s := range r.cl.Servers {
		if s.URL != primary.URL {
			io, sqlThread, err := electionReplicaThreads(s)
			if err != nil || io != "Yes" || sqlThread != "Yes" {
				return false, fmt.Sprintf("replica %s: I/O=%q SQL=%q (err=%v)", s.URL, io, sqlThread, err)
			}
		}
		if n, err := electionGlobalLockHolders(s); err != nil || n != 0 {
			return false, fmt.Sprintf("%s holds %d global read lock(s) (err=%v)", s.URL, n, err)
		}
	}
	return true, ""
}

// independentWrite records the write availability of a server seen by an independent session.
func (r *electionRun) independentWrite(step string, srv *cluster.ServerMonitor) bool {
	err := r.electionIndependentWrite(srv)
	return r.check(step, err == nil, "an independent session writes on %s (err=%v)", urlOf(srv), err)
}

// finalChecks closes the test: the primary accepts writes from an independent session, no server holds a
// global read lock, and the temporary schema is dropped. Any failure is a FAIL of the test, reported apart
// from the election assertions; nothing is released or killed here, so a lock left by the product stays
// visible and is reported, never hidden by a PASS.
func (r *electionRun) finalChecks(primary *cluster.ServerMonitor, all []*cluster.ServerMonitor) {
	if primary == nil {
		r.check("cleanup-primary", false, "no primary at the end of the test")
		return
	}
	if r.schemaOwned {
		r.independentWrite("cleanup-writable", primary)
	}
	for _, s := range all {
		n, err := electionGlobalLockHolders(s)
		r.check("cleanup-global-lock@"+s.URL, err == nil && n == 0, "idle sessions holding a global read lock on %s: %d (err=%v)", s.URL, n, err)
	}
	for _, s := range all {
		if s.URL == primary.URL {
			continue
		}
		io, sqlThread, err := electionReplicaThreads(s)
		r.check("cleanup-replication@"+s.URL, err == nil && io == "Yes" && sqlThread == "Yes", "%s replicates with both threads running (I/O=%q SQL=%q err=%v)", s.URL, io, sqlThread, err)
	}
	if !r.schemaOwned {
		return
	}
	if err := electionExec(primary, "DROP DATABASE "+r.schema); err != nil {
		r.check("cleanup-drop", false, "drop %s on %s: %s", r.schema, primary.URL, err)
	} else {
		r.check("cleanup-drop", true, "dropped %s on %s", r.schema, primary.URL)
	}
}

// cleanup runs the final checks and restores the preferred flags; the configuration itself is restored by
// the runner (RestoreConf).
func (r *electionRun) cleanup() {
	if len(r.failed) > 0 {
		r.dump("failure-state-at-cleanup")
	}
	r.restoreStopped()
	primary := r.cl.GetMaster()
	if primary == nil {
		primary = r.orig
	}
	r.finalChecks(primary, r.cl.Servers)
	r.restorePrefered()
}

// restorePrefered puts the preferred flags back as the test found them.
func (r *electionRun) restorePrefered() {
	for _, s := range r.cl.Servers {
		if was, ok := r.prefered[s.URL]; ok {
			s.SetPrefered(was)
		}
	}
}

// electionVerdict: the result of a scenario. An election assertion that passed never hides a failed check
// (including the cleanup ones); a premise that was not established is a SKIP, never a PASS.
func electionVerdict(ok bool, failed, notExercised []string) string {
	switch {
	case !ok || len(failed) > 0:
		return "FAIL"
	case len(notExercised) > 0:
		return "SKIP"
	}
	return "PASS"
}

// scenarioErrant: the correction must not bypass the errant-transaction check. A replica gets a
// transaction that the primary does not have (an empty transaction with a foreign GTID, binary log
// on); replication-manager raises WARN0091 and, with check-replication-errant-trx, holds the automatic
// failover when the primary is lost: no replica is promoted. The cleanup commits the same empty
// transaction on the primary, which makes it a normal transaction again.
func (r *electionRun) scenarioErrant() {
	cl := r.cl
	master := cl.GetMaster()
	a, b := r.candidates()
	r.logf("E: primary %s, errant transaction on %s, other replica %s", master.URL, a.URL, b.URL)
	cl.SetRplChecks(true)
	cl.SetPrefMaster(a.URL)
	time.Sleep(2 * time.Second)
	if !r.insert(master, "e1") || !r.syncAll(master, "E-baseline") {
		r.check("E-premise", false, "replicas did not execute e1")
		return
	}
	const errant = "bbbbbbbb-2451-2451-2451-bbbbbbbbbbbb:1"
	if err := electionEmptyTransaction(a, errant); err != nil {
		r.check("E-premise", false, "errant transaction on %s: %s", a.URL, err)
		return
	}
	detected := electionWait(electionRefusalWait, func() bool { return cl.GetStateMachine().IsInState("WARN0091@" + a.URL) })
	if !r.check("E-detected", detected, "WARN0091 raised for %s (errant GTID %s)", a.URL, errant) {
		r.dump("E-detected")
		_ = electionEmptyTransaction(master, errant)
		return
	}
	ctr := cl.GetFailoverCtr()
	r.stop(master)
	promoted := false
	for i := 0; i < electionNoPromoteFor+30 && !promoted; i++ {
		if m := cl.GetMaster(); m == nil || m.URL != master.URL || cl.GetFailoverCtr() != ctr {
			promoted = true
		}
		time.Sleep(time.Second)
	}
	r.dump("E-after")
	r.check("E-held", !promoted && master.IsFailed(), "automatic failover held for %ds: failover counter %d->%d, old primary failed=%v, master now %s", electionNoPromoteFor+30, ctr, cl.GetFailoverCtr(), master.IsFailed(), urlOf(cl.GetMaster()))
	r.recover("E")
	// the same transaction on the primary: no longer errant
	if err := electionEmptyTransaction(cl.GetMaster(), errant); err != nil {
		r.errf("E cleanup: commit %s on the primary %s: %s", errant, urlOf(cl.GetMaster()), err)
	}
	cleared := electionWait(electionHealthWait, func() bool { return !cl.GetStateMachine().IsInState("WARN0091@" + a.URL) })
	r.check("E-cleanup", cleared, "WARN0091 for %s cleared once the primary has the transaction", a.URL)
}

// electionEmptyTransaction commits an empty transaction with the given GTID on a server (binary log on).
func electionEmptyTransaction(srv *cluster.ServerMonitor, gtidNext string) error {
	if srv == nil || srv.Conn == nil {
		return errors.New("no connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := srv.Conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, q := range []string{"SET gtid_next='" + gtidNext + "'", "BEGIN", "COMMIT", "SET gtid_next='AUTOMATIC'"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
