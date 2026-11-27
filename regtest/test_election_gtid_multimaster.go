// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestElectionMM{SwitchSingleWriter,SwitchConcurrent,Failover} (#2451):
// the election functions changed for MySQL and Percona also serve the asynchronous multi-master topology (two
// servers replicating from each other, one designated primary). With one candidate the ranking itself is not
// exercised (a two-node election proves availability and promotion, never ranking correctness among several
// candidates); the tests verify that the corrected metric does not turn a one-candidate election into a
// refusal and that the writer policy and the data are the same as on develop. Each test starts from a fresh,
// healthy pair (target multi-master, GetTopology checked):
//
//	switchover  the other server is promoted (the preferred master is moved to it, as an operator does), the
//	            new primary is writable for an independent session, the old primary is read-only (or writable
//	            under concurrent writes), and the transactions of the new primary reach the old one;
//	failover    the designated primary is stopped, the survivor is promoted and writable for an independent
//	            session, its transactions are kept, the stopped server is started, rejoins and holds them.
//
// Both replication channels and the multi-master topology are asserted after each operation. They hold when the
// topology is declared the supported way (the legacy flag Conf.MultiMaster is set by BootstrapTopology, reached
// through the settings API); a target written only in the TOML leaves the flag false and MasterFailover then
// takes its master-slave branch ("Resetting slave on new master"), on develop as on this fix. SUPER accounts are
// not bound by read_only, so the single-writer policy is observed through a non-privileged account. The concurrent-write
// setting follows the implementation: replication-multi-master-concurrent-write leaves the old primary writable
// after a switchover only when failover-readonly-state is false (MasterFailover tests ReadOnly first), so the
// concurrent variant sets both (the settings API case is named multi-master-concurrent-write, not the TOML key,
// so the runner's must-set cannot carry it). No proxy is part of the lab: routing is not asserted.
func (regtest *RegTest) TestElectionMMSwitchSingleWriter(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionMultiMaster(cl, test, "testElectionMMSwitchSingleWriter", false, "switchover")
}

func (regtest *RegTest) TestElectionMMSwitchConcurrent(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionMultiMaster(cl, test, "testElectionMMSwitchConcurrent", true, "switchover")
}

func (regtest *RegTest) TestElectionMMFailover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return runElectionMultiMaster(cl, test, "testElectionMMFailover", false, "failover")
}

func runElectionMultiMaster(cl *cluster.Cluster, test *cluster.Test, name string, concurrent bool, phase string) bool {
	r := newElectionRun(cl, test, name)
	return r.finish(r.multiMaster(concurrent, phase))
}

// appAccount is a non-privileged account created for the test (SELECT and INSERT on the test schema only):
// root and every SUPER account ignore read_only, so the writer policy is observed through this account.
type appAccount struct {
	user, pass string
}

func (r *electionRun) createAppAccount(primary *cluster.ServerMonitor, replicas ...*cluster.ServerMonitor) (*appAccount, bool) {
	a := &appAccount{user: "rt2451app_" + electionSuffix(), pass: electionSuffix() + electionSuffix() + electionSuffix()}
	for _, q := range []string{
		fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", a.user, a.pass),
		fmt.Sprintf("GRANT SELECT, INSERT ON %s.* TO '%s'@'%%'", r.schema, a.user),
	} {
		if err := electionExec(primary, q); err != nil {
			return nil, r.check("setup-app-account", false, "create the application account on %s: %s", primary.URL, err)
		}
	}
	for _, s := range append([]*cluster.ServerMonitor{primary}, replicas...) {
		s := s
		ok := electionWait(electionSyncWait, func() bool {
			v, err := electionQuery(s, "SELECT COUNT(*) FROM mysql.user WHERE user=?", a.user)
			return err == nil && v == "1"
		})
		if !r.check("setup-app-account@"+s.URL, ok, "the application account exists on %s", s.URL) {
			return nil, false
		}
	}
	return a, true
}

// appWrite inserts a marker through a session of the application account (never repman's pool, never root).
func (r *electionRun) appWrite(a *appAccount, srv *cluster.ServerMonitor, tag string) error {
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/?timeout=5s", a.user, a.pass, srv.Host, srv.Port))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	_, err = db.ExecContext(ctx, "INSERT INTO "+r.schema+".marker(tag) VALUES(?)", tag)
	return err
}

// appReadable proves that the account, its password and the connection are valid on a server: a session of the
// account reads the test table. Without it a refused write could be an authentication or connection failure.
func (r *electionRun) appReadable(a *appAccount, srv *cluster.ServerMonitor) error {
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/?timeout=5s", a.user, a.pass, srv.Host, srv.Port))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), electionSQLTimeout)
	defer cancel()
	var n int
	return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+r.schema+".marker").Scan(&n)
}

// isReadOnlyRejection: the server refused the statement because of read_only or super_read_only
// (ER_OPTION_PREVENTS_STATEMENT, 1290); an authentication, connection, timeout or any other SQL error is not one.
func isReadOnlyRejection(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1290
}

func (r *electionRun) dropAppAccount(a *appAccount, primary *cluster.ServerMonitor) {
	if a == nil {
		return
	}
	err := electionExec(primary, fmt.Sprintf("DROP USER '%s'@'%%'", a.user))
	r.check("cleanup-app-account", err == nil, "dropped the application account on %s (err=%v)", urlOf(primary), err)
}

// observe records a fact that is not an acceptance criterion of the test (never a PASS or FAIL).
func (r *electionRun) observe(format string, args ...interface{}) {
	r.logf("OBSERVE "+format, args...)
}

// hasTags says whether a server holds every given marker.
func (r *electionRun) hasTags(srv *cluster.ServerMonitor, tags []string) (bool, int) {
	n := 0
	for _, tag := range tags {
		v, err := electionQuery(srv, "SELECT COUNT(*) FROM "+r.schema+".marker WHERE tag=?", tag)
		if err == nil && v == "1" {
			n++
		}
	}
	return n == len(tags), n
}

// tagsOn waits until a server holds every given marker.
func (r *electionRun) tagsOn(step string, srv *cluster.ServerMonitor, tags []string) bool {
	var n int
	ok := electionWait(electionSyncWait, func() bool { var all bool; all, n = r.hasTags(srv, tags); return all })
	return r.check(step, ok, "%s holds %d of the %d markers %v", srv.URL, n, len(tags), tags)
}

func (r *electionRun) mmBoth(step string, a, b *cluster.ServerMonitor, want int) bool {
	var na, nb int
	ok := electionWait(electionSyncWait, func() bool {
		na, _ = r.count(a)
		nb, _ = r.count(b)
		return na == want && nb == want
	})
	return r.check(step, ok, "rows on %s=%d, on %s=%d, committed=%d", a.URL, na, b.URL, nb, want)
}

// mmChannels says whether each server replicates from the other (the channel of a multi-master pair).
func (r *electionRun) mmChannels(step string, a, b *cluster.ServerMonitor) bool {
	ok := electionWait(electionHealthWait, func() bool { return r.replicating(a, b) && r.replicating(b, a) })
	if !ok {
		r.dump(step)
	}
	return r.check(step, ok, "%s replicates from %s and %s from %s (both threads running)", a.URL, b.URL, b.URL, a.URL)
}

func (r *electionRun) multiMaster(concurrent bool, phase string) bool {
	cl := r.cl
	if !r.requireTarget(config.TopoMultiMaster, 2) {
		return true
	}
	master := cl.GetMaster()
	if master == nil || len(cl.Servers) != 2 {
		return r.check("setup", false, "a multi-master pair needs a master and one other server, found master=%s servers=%d", urlOf(master), len(cl.Servers))
	}
	if cl.GetTopology() != config.TopoMultiMaster {
		return r.check("setup", false, "topology-target %s but the discovered topology is %s", cl.GetTopologyTarget(), cl.GetTopology())
	}
	// Not settable through the settings API under its key (the API case is named differently), so the
	// test sets them like the other tests set their runtime behaviour; the runner restores the
	// configuration (RestoreConf).
	if cl.Conf.MultiMasterConcurrentWrite != concurrent {
		cl.SwitchMultiMasterConcurrentWrite()
	}
	cl.SetReadOnly(!concurrent) // failover-readonly-state: concurrent writes need the old primary left writable
	var other *cluster.ServerMonitor
	for _, s := range cl.Servers {
		if s.URL != master.URL {
			other = s
		}
	}
	r.orig = master
	for _, s := range cl.Servers {
		r.prefered[s.URL] = s.IsPrefered()
	}
	r.logf("pair: primary %s, other %s, concurrent-write=%v failover-readonly-state=%v", master.URL, other.URL, cl.Conf.MultiMasterConcurrentWrite, cl.Conf.ReadOnly)
	if !r.requireHealthy(master) {
		return true
	}
	cl.SetInteractive(false)
	cl.SetCheckFalsePositiveHeartbeat(false)
	cl.SetFailSync(false)
	cl.SetRejoin(true)
	cl.SetRejoinFlashback(false)
	cl.SetRejoinDump(false)
	cl.SetRplChecks(true)
	defer r.cleanup()

	if err := r.createSchema(master); err != nil {
		return r.check("setup", false, "create schema %s: %s", r.schema, err)
	}
	if err := electionExec(master, "CREATE TABLE "+r.schema+".marker (tag VARCHAR(64) PRIMARY KEY) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create table: %s", err)
	}
	if err := electionExec(master, "CREATE TABLE "+r.schema+".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create probe table: %s", err)
	}
	if !electionWait(electionSyncWait, func() bool { _, err := r.count(other); return err == nil }) {
		return r.check("setup", false, "the table did not reach %s", other.URL)
	}
	if !r.insert(master, "w0") || !r.mmBoth("setup-sync", master, other, r.rows) || !r.mmChannels("setup-channels", master, other) {
		return false
	}

	app, ok := r.createAppAccount(master, other)
	if !ok {
		return false
	}
	defer func() { r.dropAppAccount(app, cl.GetMaster()) }()
	tags := []string{"w0"}
	if phase == "switchover" {
		// in a multi-master pair the designated primary is the preferred master (the election is rigged to
		// it), so the operator moves it by changing the preferred master
		cl.SetPrefMaster(other.URL)
		time.Sleep(2 * time.Second)
		ctr := cl.GetFailoverCtr()
		cl.SwitchoverWaitTest()
		// The election result of the operation is the designated primary as the switchover ends, before the monitor's next
		// topology pass; the timeline then shows what the rediscovery does with it.
		elected := cl.GetMaster()
		timeline := []string{urlOf(elected)}
		for i := 0; i < 40; i++ {
			time.Sleep(100 * time.Millisecond)
			if u := urlOf(cl.GetMaster()); u != timeline[len(timeline)-1] {
				timeline = append(timeline, u)
			}
		}
		time.Sleep(3 * time.Second)
		nm := cl.GetMaster()
		r.dump("switchover-after")
		r.observe("designated primary: at the end of the switchover, then each change over the next 4 seconds %v, after 7 seconds %s (target %s)", timeline, urlOf(nm), other.URL)
		if !r.check("switchover-election", elected != nil && elected.URL == other.URL, "the switchover elected %s as designated primary (target %s)", urlOf(elected), other.URL) {
			r.recoverPair(other, master)
			return false
		}
		if concurrent {
			// with both servers writable the topology pass that follows designates the writable server it finds first
			// (fallback of the discovery, ERR00011 / WARN0003): the designation can move back, the election result above cannot
			r.observe("concurrent writes: designated primary after the rediscovery is %s", urlOf(nm))
		} else if !r.check("switchover-selection", nm != nil && nm.URL == other.URL, "designated primary after the rediscovery is %s, want the other server %s", urlOf(nm), other.URL) {
			return false
		}
		roNew, _ := electionQuery(other, "SELECT @@global.read_only")
		roOld, _ := electionQuery(master, "SELECT @@global.read_only")
		wantOld := "1"
		if concurrent {
			wantOld = "0"
		}
		r.check("switchover-writers", roNew == "0" && roOld == wantOld, "read_only=%s on the new primary, %s on the old primary (want 0 and %s, concurrent-write=%v)", roNew, roOld, wantOld, concurrent)
		r.independentWrite("switchover-writable", other)
		if !r.insert(other, "s-new") {
			return r.check("switchover-propagation", false, "insert on the new primary")
		}
		tags = append(tags, "s-new")
		// what a client sees: the transactions of the new primary reach the old one
		r.tagsOn("switchover-propagation", master, tags)
		r.tagsOn("switchover-new-primary-complete", other, tags)
		// writer policy, observed as a client does (a non-privileged account whose validity is proven on both servers)
		okNew := r.check("switchover-app-session-new", r.appReadable(app, other) == nil, "the application account reads on the new primary %s", other.URL)
		okOld := r.check("switchover-app-session-old", r.appReadable(app, master) == nil, "the application account reads on the old primary %s", master.URL)
		if okNew {
			if err := r.appWrite(app, other, "app-new"); err != nil {
				r.check("switchover-app-write-new", false, "application write on the new primary %s: %s", other.URL, err)
			} else {
				tags = append(tags, "app-new")
				r.check("switchover-app-write-new", true, "application write on the new primary %s committed", other.URL)
			}
		}
		if okOld {
			err := r.appWrite(app, master, "app-old")
			if concurrent {
				if err == nil {
					tags = append(tags, "app-old")
				}
				r.check("switchover-app-write-old", err == nil, "application write on the old primary %s under concurrent-write (err=%v)", master.URL, err)
			} else {
				r.check("switchover-app-write-old", isReadOnlyRejection(err), "application write on the old primary %s is refused by read_only, error 1290 (err=%v)", master.URL, err)
			}
		}
		// both replication channels of the pair survive the switchover and the topology stays multi-master
		bothWays := electionWait(electionHealthWait, func() bool { return r.replicating(master, other) && r.replicating(other, master) })
		r.check("switchover-channels", bothWays, "%s replicates from %s and %s from %s with both threads running", master.URL, other.URL, other.URL, master.URL)
		r.check("switchover-topology", cl.GetTopology() == config.TopoMultiMaster, "discovered topology after the switchover is %s", cl.GetTopology())
		r.tagsOn("switchover-markers-old", master, tags)
		r.tagsOn("switchover-markers-new", other, tags)
		r.check("switchover-counter", cl.GetFailoverCtr() >= ctr, "failover counter %d->%d", ctr, cl.GetFailoverCtr())
		r.recoverPair(other, master)
		return len(r.failed) == 0
	}

	// ---- failover of the designated primary
	failed := master
	survivor := other
	ctr := cl.GetFailoverCtr()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitFailover(wg)
	r.stop(failed)
	wg.Wait()
	time.Sleep(5 * time.Second)
	nm := cl.GetMaster()
	r.dump("failover-after")
	if !r.check("failover-selection", nm != nil && nm.URL == survivor.URL, "designated primary is %s, want the survivor %s", urlOf(nm), survivor.URL) {
		r.recoverPair(failed, survivor)
		return false
	}
	ro, _ := electionQuery(survivor, "SELECT @@global.read_only")
	r.check("failover-promotion", ro == "0" && cl.GetFailoverCtr() == ctr+1 && failed.IsFailed(), "read_only=%s on %s, failover counter %d->%d, old primary failed=%v", ro, survivor.URL, ctr, cl.GetFailoverCtr(), failed.IsFailed())
	r.independentWrite("failover-writable", survivor)
	if err := r.appWrite(app, survivor, "app-fo"); err != nil {
		r.check("failover-app-write", false, "application write on the new primary %s: %s", survivor.URL, err)
	} else {
		tags = append(tags, "app-fo")
		r.check("failover-app-write", true, "application write on the new primary %s committed", survivor.URL)
	}
	if !r.insert(survivor, "f-new") {
		r.check("failover-write", false, "insert on the new primary")
	}
	tags = append(tags, "f-new")
	r.tagsOn("failover-new-primary-complete", survivor, tags)
	r.recoverPair(failed, survivor)
	r.tagsOn("failover-rejoined-complete", failed, tags)
	bothWays := electionWait(electionHealthWait, func() bool { return r.replicating(failed, survivor) && r.replicating(survivor, failed) })
	r.check("failover-channels", bothWays, "after the rejoin %s replicates from %s and %s from %s with both threads running", failed.URL, survivor.URL, survivor.URL, failed.URL)
	r.check("failover-topology", cl.GetTopology() == config.TopoMultiMaster, "discovered topology after the failover and the rejoin is %s", cl.GetTopology())
	return len(r.failed) == 0
}

// recoverPair starts the stopped server and waits for its rejoin, then puts the original primary back.
func (r *electionRun) recoverPair(failed, survivor *cluster.ServerMonitor) {
	cl := r.cl
	if failed.IsFailed() {
		r.logf("cleanup: starting %s", failed.URL)
		cl.StartDatabaseWaitRejoin(failed)
	}
	electionWait(electionHealthWait, func() bool { return !failed.IsFailed() && r.replicating(failed, survivor) })
	if m := cl.GetMaster(); m != nil && r.orig != nil && m.URL != r.orig.URL {
		cl.SetPrefMaster(r.orig.URL)
		time.Sleep(2 * time.Second)
		cl.SwitchoverWaitTest()
		electionWait(electionSwitchWait, func() bool { m := cl.GetMaster(); return m != nil && m.URL == r.orig.URL })
	}
}
