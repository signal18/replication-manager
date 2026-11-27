// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestElectionRing (#2451): in a multi-master ring the promotion does not rank candidates
// (VMasterFailover -> electVirtualCandidate takes the first live server that is not the old primary), but the
// monitor runs the shared FoundCandidateMaster gate every tick: isFoundCandidateMaster calls
// electFailoverCandidate on the ring's servers, the function whose MySQL and Percona ranking #2451 corrects.
// The test verifies, on a healthy ring of three servers declared by the topology target multi-master-ring and
// built by the supported bootstrap:
//
//	switchover  the virtual primary moves to another server, an independent session can write on it, a marker
//	            written there reaches the two other servers around the ring, and the ring edges are intact;
//	failover    the virtual primary is stopped, a survivor takes over and is writable for an independent
//	            session, the ring is closed around the stopped server (each survivor replicates, threads
//	            running), and a marker written on the new primary reaches the other survivor;
//	gate        during the whole run the monitor never raised ERR00032 ("no candidate"): the shared gate did
//	            not refuse the ring (sampled twice a second).
//
// The stopped server is started again and its rejoin is observed, not asserted (ring reintegration is not
// part of #2451). Name length: the runner formats "<cluster>/<test>" in 60 columns, so the name stays short.
func (regtest *RegTest) TestElectionRing(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := newElectionRun(cl, test, "testElectionRing")
	return r.finish(r.ring())
}

// ringEdges says whether each server replicates from its predecessor in the server list (the ring the
// bootstrap builds) with both threads running.
func (r *electionRun) ringEdges(servers []*cluster.ServerMonitor, live func(*cluster.ServerMonitor) bool) bool {
	n := len(servers)
	for i, s := range servers {
		if !live(s) {
			continue
		}
		p := servers[(i+n-1)%n]
		if !r.replicating(s, p) {
			return false
		}
	}
	return true
}

func (r *electionRun) ring() bool {
	cl := r.cl
	if !r.requireTarget(config.TopoMultiMasterRing, 3) {
		return true
	}
	servers := append([]*cluster.ServerMonitor{}, cl.Servers...)
	vm := cl.GetMaster()
	if len(servers) != 3 || vm == nil {
		return r.check("setup", false, "a ring of three servers with a virtual primary is needed, found %d servers, primary %s", len(servers), urlOf(vm))
	}
	if cl.GetTopology() != config.TopoMultiMasterRing {
		return r.check("setup", false, "topology-target %s but the discovered topology is %s", cl.GetTopologyTarget(), cl.GetTopology())
	}
	if !r.requireHealthy(vm) {
		return true
	}
	r.orig = vm
	for _, s := range servers {
		r.prefered[s.URL] = s.IsPrefered()
	}
	all := func(*cluster.ServerMonitor) bool { return true }
	r.logf("ring: virtual primary %s, servers %s %s %s", vm.URL, servers[0].URL, servers[1].URL, servers[2].URL)
	cl.SetInteractive(false)
	cl.SetCheckFalsePositiveHeartbeat(false)
	cl.SetFailSync(false)
	cl.SetRejoin(true)
	cl.SetRejoinFlashback(false)
	cl.SetRejoinDump(false)
	cl.SetRplChecks(true)
	defer r.cleanup()

	if err := r.createSchema(vm); err != nil {
		return r.check("setup", false, "create schema %s: %s", r.schema, err)
	}
	if err := electionExec(vm, "CREATE TABLE "+r.schema+".marker (tag VARCHAR(64) PRIMARY KEY) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create table: %s", err)
	}
	if err := electionExec(vm, "CREATE TABLE "+r.schema+".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB"); err != nil {
		return r.check("setup", false, "create probe table: %s", err)
	}
	tags := []string{"w0"}
	if err := electionExec(vm, "INSERT INTO "+r.schema+".marker(tag) VALUES('w0')"); err != nil {
		return r.check("setup", false, "insert w0: %s", err)
	}
	for _, s := range servers {
		if !r.tagsOn("setup-ring-propagation@"+s.URL, s, tags) {
			return false
		}
	}
	if !r.check("setup-ring-edges", electionWait(electionHealthWait, func() bool { return r.ringEdges(servers, all) }), "every server replicates from its predecessor with both threads running") {
		r.dump("setup-ring")
		return false
	}

	// the shared gate is sampled for the whole run
	var gate int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(500 * time.Millisecond):
				if cl.GetStateMachine().IsInState("ERR00032") {
					atomic.AddInt32(&gate, 1)
				}
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	// ---- switchover of the virtual primary
	old := cl.GetMaster()
	cl.SwitchoverWaitTest()
	electionWait(electionSwitchWait, func() bool { m := cl.GetMaster(); return m != nil && m.URL != old.URL })
	time.Sleep(5 * time.Second)
	nm := cl.GetMaster()
	r.dump("ring-switch-after")
	if !r.check("ring-switch-selection", nm != nil && nm.URL != old.URL, "virtual primary moved from %s to %s", old.URL, urlOf(nm)) {
		return false
	}
	r.independentWrite("ring-switch-writable", nm)
	if err := electionExec(nm, "INSERT INTO "+r.schema+".marker(tag) VALUES('s-new')"); err != nil {
		r.check("ring-switch-propagation", false, "insert on the new primary: %s", err)
	} else {
		tags = append(tags, "s-new")
		for _, s := range servers {
			r.tagsOn("ring-switch-propagation@"+s.URL, s, tags)
		}
	}
	r.check("ring-switch-edges", electionWait(electionHealthWait, func() bool { return r.ringEdges(servers, all) }), "the ring edges are intact after the switchover")

	// ---- failover of the virtual primary
	failed := cl.GetMaster()
	ctr := cl.GetFailoverCtr()
	fw := new(sync.WaitGroup)
	fw.Add(1)
	go cl.WaitFailover(fw)
	r.stop(failed)
	fw.Wait()
	time.Sleep(5 * time.Second)
	np := cl.GetMaster()
	r.dump("ring-failover-after")
	if !r.check("ring-failover-selection", np != nil && np.URL != failed.URL && !np.IsFailed(), "virtual primary is %s after the loss of %s", urlOf(np), failed.URL) {
		r.recoverRing(failed)
		return false
	}
	r.check("ring-failover-counter", cl.GetFailoverCtr() == ctr+1, "failover counter %d->%d", ctr, cl.GetFailoverCtr())
	r.independentWrite("ring-failover-writable", np)
	var other *cluster.ServerMonitor
	for _, s := range servers {
		if s.URL != failed.URL && s.URL != np.URL {
			other = s
		}
	}
	if err := electionExec(np, "INSERT INTO "+r.schema+".marker(tag) VALUES('f-new')"); err != nil {
		r.check("ring-failover-propagation", false, "insert on the new primary: %s", err)
	} else {
		tags = append(tags, "f-new")
		r.tagsOn("ring-failover-propagation", other, tags)
	}
	survivorsRun := electionWait(electionHealthWait, func() bool {
		for _, s := range []*cluster.ServerMonitor{np, other} {
			if io, sqlThread, err := electionReplicaThreads(s); err != nil || io != "Yes" || sqlThread != "Yes" {
				return false
			}
		}
		return true
	})
	r.check("ring-failover-survivors", survivorsRun, "both survivors (%s, %s) replicate with both threads running: the ring is closed around %s", np.URL, other.URL, failed.URL)
	r.recoverRing(failed)
	r.observe("ring after the rejoin: edges intact=%v, discovered topology=%s", r.ringEdges(servers, all), cl.GetTopology())
	r.check("ring-found-candidate-gate", atomic.LoadInt32(&gate) == 0, "ERR00032 (no candidate) raised in %d of the samples taken during the run", atomic.LoadInt32(&gate))
	return len(r.failed) == 0
}

// recoverRing starts the stopped server and waits for its rejoin.
func (r *electionRun) recoverRing(failed *cluster.ServerMonitor) {
	if failed.IsFailed() {
		r.logf("cleanup: starting %s", failed.URL)
		r.cl.StartDatabaseWaitRejoin(failed)
	}
	electionWait(electionHealthWait, func() bool { return !failed.IsFailed() })
}
