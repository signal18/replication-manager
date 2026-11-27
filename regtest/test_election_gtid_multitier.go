// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestElectionMultiTier (#2451): in a multi-tier cluster the election compares binlog coordinates of replicas
// that read DIFFERENT servers' binlogs: the relay reads the primary's, the replicas below it read the relay's.
// File numbers and positions of two different servers are not a measure of replication progress. The test
// builds that state on a cluster declared multi-tier-slave and bootstrapped by the supported lifecycle
// (the relay replicates from the primary, the other servers replicate from the relay):
//
//   - the relay's own binary log is rotated several times, so the replicas below it read a higher file number
//     than the relay reads from the primary;
//   - the replicas below the relay stop receiving; markers are committed on the primary and reach the relay;
//   - the primary is stopped.
//
// Expected (what a safe election does): the promoted server is the relay, the most advanced one, and it holds every
// committed marker. A replica below the relay, behind it, must not be promoted.
//
// This is a characterisation of the comparability limit, not an acceptance criterion of #2451: both builds are
// run and the result is classified by comparing them (a pre-existing limitation fails on both; a decimal-UUID
// cluster, whose parsed sequence tied the candidates on develop, can differ). The premise (equal positive parsed
// sequence for the relay and a replica below it, and the replica below it ahead in file number) is recorded.
func (regtest *RegTest) TestElectionMultiTier(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := &electionRun{cl: cl, test: test, name: "testElectionMultiTier", schema: "regtest_election_2451", prefered: map[string]bool{}}
	ok := r.multiTier() && len(r.failed) == 0
	if ok {
		r.logf("SUMMARY result=PASS checks=%s", strings.Join(r.passed, ","))
	} else {
		r.logf("SUMMARY result=FAIL failed=%s passed=%s", strings.Join(r.failed, ","), strings.Join(r.passed, ","))
	}
	return ok
}

func (r *electionRun) sourceHost(s *cluster.ServerMonitor) string {
	if ss, err := s.GetSlaveStatus(s.ReplicationSourceName); err == nil {
		return ss.MasterHost.String
	}
	return ""
}

func (r *electionRun) multiTier() bool {
	cl := r.cl
	master := cl.GetMaster()
	if master == nil || len(cl.Servers) < 3 {
		return r.check("setup", false, "a multi-tier cluster needs a primary, a relay and a replica below it; found master=%s servers=%d", urlOf(master), len(cl.Servers))
	}
	if cl.GetTopology() != config.TopoMultiTierSlave {
		return r.check("setup", false, "topology-target %s but the discovered topology is %s", cl.GetTopologyTarget(), cl.GetTopology())
	}
	// upstream and downstream relationships, from the live servers
	var relay *cluster.ServerMonitor
	var below []*cluster.ServerMonitor
	for _, s := range cl.Servers {
		if s.URL == master.URL {
			continue
		}
		if r.sourceHost(s) == master.Host {
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
	d := below[0]
	r.orig = master
	for _, s := range cl.Servers {
		r.prefered[s.URL] = s.IsPrefered()
	}
	r.logf("multi-tier: primary %s, relay %s, replicas below the relay %d (first %s)", master.URL, relay.URL, len(below), d.URL)
	cl.SetInteractive(false)
	cl.SetCheckFalsePositiveHeartbeat(false)
	cl.SetFailSync(false)
	cl.SetRejoin(true)
	cl.SetRejoinFlashback(false)
	cl.SetRejoinDump(false)
	cl.SetRplChecks(true)
	defer r.cleanup()

	for _, q := range []string{
		"CREATE DATABASE IF NOT EXISTS " + r.schema,
		"CREATE TABLE IF NOT EXISTS " + r.schema + ".marker (tag VARCHAR(64) PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS " + r.schema + ".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB",
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
	ahead := electionWait(electionSyncWait, func() bool { v, _ := electionReceivedAhead(d, relay); return v })
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
	_, n := r.hasTags(d, tags)
	if !r.check("mt-premise", ahead && n < len(tags), "the replica %s below the relay reads a higher file number than the relay does from the primary (%v) and lacks the new markers (%d of %d)", d.URL, ahead, n, len(tags)) {
		r.dump("mt-premise")
		return false
	}
	r.premise("MT", relay, d, true)
	r.dump("mt-before-failover")

	ctr := cl.GetFailoverCtr()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitFailover(wg)
	cl.StopDatabaseService(master)
	wg.Wait()
	time.Sleep(5 * time.Second)
	nm := cl.GetMaster()
	r.dump("mt-after-failover")
	promoted := nm != nil && nm.URL != master.URL && cl.GetFailoverCtr() == ctr+1
	r.check("mt-promotion", promoted, "a new primary was promoted: %s (failover counter %d->%d)", urlOf(nm), ctr, cl.GetFailoverCtr())
	if !promoted {
		return false
	}
	r.check("mt-selection", nm.URL == relay.URL, "promoted %s, the most advanced is the relay %s (replicas below the relay: %s)", nm.URL, relay.URL, fmt.Sprint(len(below)))
	all, got := r.hasTags(nm, tags)
	r.check("mt-consistency", all, "the promoted server holds %d of the %d committed markers", got, len(tags))
	r.independentWrite("mt-writable", nm)
	return len(r.failed) == 0
}
