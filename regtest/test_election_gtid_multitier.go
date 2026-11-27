// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestElectionMultiTier (#2451), run by name only: it deliberately builds a state in which the election compares
// binlog coordinates of replicas that read DIFFERENT servers' binlogs, and it may expose an unresolved limitation.
// It is not in the ALL list and declares no TestTarget; it checks its own applicability (topology-target
// multi-tier-slave, three servers or more, MySQL or Percona) and starts only from a healthy cluster (SKIP otherwise).
//
// The relay reads the primary's binlog and the replicas below it read the relay's. File numbers and positions of two
// different servers are not a measure of replication progress, so a positional ranking across them can pick a replica
// that is behind. This is a limit of the ranking that existed before the patch and that the patch does not remove;
// what the patch changes is its operational impact: on a cluster whose parsed sequence was positive (decimal-only
// third UUID group) develop tied the candidates and kept the list order or the preferred host, and the corrected
// ranking now compares the coordinates across the two streams. The test therefore records which of three
// situations it ran, in the SUMMARY class:
//
//	REPRODUCED   the parsed sequence of the relay and of a replica below it is equal and positive (the case develop
//	             tied), the preferred hosts are cleared and the relay is the first candidate: the result of the two
//	             builds can be compared;
//	CONTROL      the parsed sequence is not equal and positive (for instance a UUID with a letter): the positional path
//	             applies on both builds; a failure fails, a pass is a PASS recorded as a control (the expected result
//	             on both builds), not evidence about the fix;
//	UNESTABLISHED a premise of the state could not be built (the replica below the relay is not ahead in file number,
//	             it already holds the markers, the candidate order is not the relay first): SKIP, nothing is proven.
//
// State built: the relay's own binary log is rotated, so the replicas below it read a higher file number than the relay
// reads from the primary; they stop receiving; markers are committed on the primary and reach the relay; the primary is
// stopped. A safe election promotes the relay, the most advanced server, and it holds every committed marker.
//
// Recovery (the product's own, nothing reset or re-created): the stopped primary is started and rejoins, the stopped
// receiver threads are started, and the end of the test requires every replica to run both threads and the primary to
// accept writes; a divergence created by a wrong promotion stays visible and fails the cleanup checks. The original
// primary is not promoted back.
func (regtest *RegTest) TestElectionMultiTier(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := newElectionRun(cl, test, "testElectionMultiTier")
	return r.finish(r.multiTier())
}

func (r *electionRun) sourceHost(s *cluster.ServerMonitor) string {
	if ss, err := s.GetSlaveStatus(s.ReplicationSourceName); err == nil {
		return ss.MasterHost.String
	}
	return ""
}

func (r *electionRun) unestablished(format string, args ...interface{}) bool {
	reason := "premise not established: " + fmt.Sprintf(format, args...)
	r.test.SkipReason = reason
	r.skipped = append(r.skipped, reason)
	r.logf("%s", reason)
	r.restorePrefered()
	return true
}

func (r *electionRun) multiTier() bool {
	cl := r.cl
	if !r.requireTarget(config.TopoMultiTierSlave, 3) {
		return true
	}
	master := cl.GetMaster()
	if master == nil {
		return r.check("setup", false, "no primary discovered")
	}
	if cl.GetTopology() != config.TopoMultiTierSlave {
		return r.check("setup", false, "topology-target %s but the discovered topology is %s", cl.GetTopologyTarget(), cl.GetTopology())
	}
	// upstream and downstream relationships, from the live servers
	var relay *cluster.ServerMonitor
	var below []*cluster.ServerMonitor
	for _, s := range cl.Servers {
		if s.URL != master.URL && r.sourceHost(s) == master.Host {
			relay = s
		}
	}
	if relay == nil {
		return r.check("setup", false, "no server replicates from the primary %s", master.URL)
	}
	for _, s := range cl.Servers {
		if s.URL != master.URL && s.URL != relay.URL && r.sourceHost(s) == relay.Host {
			below = append(below, s)
		}
	}
	if len(below) == 0 {
		return r.check("setup", false, "no server replicates from the relay %s", relay.URL)
	}
	if !r.requireHealthy(master) {
		return true
	}
	d := below[0]
	r.orig = master
	for _, s := range cl.Servers {
		r.prefered[s.URL] = s.IsPrefered()
	}
	// the comparison of the two builds needs controlled inputs: no preferred host, and the relay first in the candidate list
	cl.SetPrefMaster("")
	time.Sleep(2 * time.Second)
	order := ""
	for _, s := range cl.GetSlaves() {
		order += s.URL + " "
	}
	r.logf("multi-tier: primary %s, relay %s, replicas below the relay %d (first %s); preferred hosts cleared; candidate order: %s", master.URL, relay.URL, len(below), d.URL, order)
	if sl := cl.GetSlaves(); len(sl) == 0 || sl[0].URL != relay.URL {
		return r.unestablished("the candidate order is not controlled: the first candidate is not the relay %s (order: %s)", relay.URL, order)
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
	for _, q := range []string{
		"CREATE TABLE " + r.schema + ".marker (tag VARCHAR(64) PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE " + r.schema + ".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB",
		"INSERT INTO " + r.schema + ".marker(tag) VALUES('w0')",
	} {
		if err := electionExec(master, q); err != nil {
			return r.check("setup", false, "%s: %s", q, err)
		}
	}
	tags := []string{"w0"}
	for _, s := range cl.Servers {
		if !r.tagsOn("setup-propagation@"+s.URL, s, tags) {
			return false
		}
	}
	// the relay rotates its own binary log: the replicas below it now read a higher file number than the relay
	// reads from the primary
	for i := 0; i < 4; i++ {
		if err := electionExec(relay, "FLUSH BINARY LOGS"); err != nil {
			return r.check("mt-premise", false, "flush binary logs on the relay: %s", err)
		}
		time.Sleep(time.Second)
	}
	ahead := electionWait(electionSyncWait, func() bool { v, err := electionReceivedAhead(d, relay); return err == nil && v })
	if !ahead {
		return r.unestablished("after the relay's binary log was rotated the replica %s below it does not rank above the relay", d.URL)
	}
	rankRelayBefore, _ := electionServerRank(relay) // before the markers: used only to see the monitor catch up
	for _, s := range below {
		if _, err := s.StopSlaveIOThread(); err != nil {
			return r.check("mt-premise", false, "stop the I/O thread of %s: %s", s.URL, err)
		}
	}
	for _, tag := range []string{"t1", "t2", "t3"} {
		if err := electionExec(master, "INSERT INTO "+r.schema+".marker(tag) VALUES(?)", tag); err != nil {
			return r.check("mt-premise", false, "insert %s: %s", tag, err)
		}
		tags = append(tags, tag)
	}
	if !r.tagsOn("mt-premise-relay", relay, tags) {
		return false
	}
	// The election reads the replication status the monitor holds. The decisive inputs are the ranks as they stand right
	// before the primary is stopped, after the markers: wait for the monitor to show the relay's new position, then sample.
	electionWait(electionSyncWait, func() bool { v, err := electionServerRank(relay); return err == nil && v > rankRelayBefore })
	time.Sleep(3 * time.Second)
	rankD, errD := electionServerRank(d)
	rankRelay, errRelay := electionServerRank(relay)
	if errD != nil || errRelay != nil {
		return r.unestablished("the election's rank cannot be computed from the replication status (%v, %v)", errD, errRelay)
	}
	for _, s := range cl.Servers {
		if s.URL == master.URL {
			continue
		}
		if ss, err := s.GetSlaveStatus(s.ReplicationSourceName); err == nil {
			rk, _ := electionServerRank(s)
			r.logf("mt-final-inputs: %s source=%s file=%s pos=%s rank=%d io=%s sql=%s", s.URL, ss.MasterHost.String, ss.MasterLogFile.String, ss.ReadMasterLogPos.String, rk, ss.SlaveIORunning.String, ss.SlaveSQLRunning.String)
		}
	}
	r.logf("mt-premise: election rank of the relay %s = %d, of the replica below it %s = %d (sampled right before the primary is stopped)", relay.URL, rankRelay, d.URL, rankD)
	if _, n := r.hasTags(d, tags); rankD <= rankRelay || n >= len(tags) {
		return r.unestablished("the replica %s below the relay does not rank above the relay by the election's own number (%d vs %d) or already holds the new markers (%d of %d)", d.URL, rankD, rankRelay, n, len(tags))
	}
	if !r.premise("MT", relay, d, true) {
		return true
	}
	r.dump("mt-before-failover")

	ctr := cl.GetFailoverCtr()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitFailover(wg)
	r.stop(master)
	wg.Wait()
	time.Sleep(5 * time.Second)
	nm := cl.GetMaster()
	r.dump("mt-after-failover")
	promoted := nm != nil && nm.URL != master.URL && cl.GetFailoverCtr() == ctr+1
	r.check("mt-promotion", promoted, "a new primary was promoted: %s (failover counter %d->%d)", urlOf(nm), ctr, cl.GetFailoverCtr())
	if !promoted {
		return false
	}
	r.check("mt-selection", nm.URL == relay.URL, "promoted %s, the most advanced is the relay %s (replicas below the relay: %d)", nm.URL, relay.URL, len(below))
	all, got := r.hasTags(nm, tags)
	r.check("mt-consistency", all, "the promoted server holds %d of the %d committed markers", got, len(tags))
	r.independentWrite("mt-writable", nm)
	return len(r.failed) == 0
}
