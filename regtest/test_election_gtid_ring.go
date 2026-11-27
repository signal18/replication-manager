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

// TestElectionRingSwitch and TestElectionRingFailover (#2451): in a multi-master ring the promotion does not rank
// candidates (VMasterFailover -> electVirtualCandidate takes the first live server that is not the old primary), but the
// monitor runs the shared FoundCandidateMaster gate every tick: isFoundCandidateMaster calls electFailoverCandidate on the
// ring's servers, the function whose MySQL and Percona ranking #2451 corrects. Both tests start from a healthy ring of three
// servers declared by the topology target multi-master-ring (set through the settings API on a wired ring), each on its own
// fresh ring, run by name only.
//
// What the product does, read from VMasterFailover and CloseRing and confirmed in the logs of develop and of the fix:
// CloseRing(oldMaster) runs for the switchover AND the failover. It stops the replication of the child (the server that
// replicates from the old virtual primary) and repoints it to the old virtual primary's parent. A switchover also freezes
// the old virtual primary, which stops all its replication threads and leaves it bypassed (ERR00048, state SlaveErr).
// So a ring is NOT intact after either operation, and what the tests assert is the structure CloseRing is designed to give:
//
//	switchover  the election result (the virtual primary as the operation ends) is another server, an independent session can
//	            write on it, the child replicates from the old primary's parent with both threads running, and a marker
//	            written on the new primary reaches the ring members (every server but the bypassed old primary, whose
//	            replication is stopped: observed);
//	failover    the virtual primary is stopped, the election result is a live server, an independent session can write
//	            on it, the child replicates from the stopped server's parent, a marker reaches the other survivor.
//
// The failover test is independent of the switchover test: a ring that a switchover has left bypassed and unhealthy is not
// a valid start for a failover (CloseRing then finds the same server as child and parent). The FoundCandidateMaster gate is
// sampled twice a second and its refusals (ERR00032) are observed for comparison between the builds: a refusal comes from the
// eligibility of the candidate (for instance ERR00042), not from the ranking. The stopped server is started again and its
// rejoin is observed. Names stay short: the runner formats "<cluster>/<test>" in 60 columns.
func (regtest *RegTest) TestElectionRingSwitch(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := newElectionRun(cl, test, "testElectionRingSwitch")
	return r.finish(r.ring(false))
}

func (regtest *RegTest) TestElectionRingFailover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	r := newElectionRun(cl, test, "testElectionRingFailover")
	return r.finish(r.ring(true))
}

// ringEdges says whether each server replicates from its predecessor in the server list (the ring the bootstrap
// builds) with both threads running.
func (r *electionRun) ringEdges(servers []*cluster.ServerMonitor, live func(*cluster.ServerMonitor) bool) bool {
	n := len(servers)
	for i, s := range servers {
		if !live(s) {
			continue
		}
		if !r.replicating(s, servers[(i+n-1)%n]) {
			return false
		}
	}
	return true
}

// ringNeighbours returns the child (the server that replicates from srv) and the parent (the server srv replicates from).
func (r *electionRun) ringNeighbours(servers []*cluster.ServerMonitor, srv *cluster.ServerMonitor) (child, parent *cluster.ServerMonitor) {
	for _, s := range servers {
		if s.URL == srv.URL {
			continue
		}
		if r.sourceHost(s) == srv.Host {
			child = s
		}
		if r.sourceHost(srv) == s.Host {
			parent = s
		}
	}
	return child, parent
}

func (r *electionRun) ring(failover bool) bool {
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
	child, parent := r.ringNeighbours(servers, vm)
	if child == nil || parent == nil || child.URL == parent.URL {
		return r.check("setup", false, "the neighbours of the virtual primary %s in the ring are child=%s parent=%s", vm.URL, urlOf(child), urlOf(parent))
	}
	all := func(*cluster.ServerMonitor) bool { return true }
	r.logf("ring: virtual primary %s, child %s, parent %s", vm.URL, child.URL, parent.URL)
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
	for _, q := range []string{
		"CREATE TABLE " + r.schema + ".marker (tag VARCHAR(64) PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE " + r.schema + ".probe (id INT AUTO_INCREMENT PRIMARY KEY, who VARCHAR(80)) ENGINE=InnoDB",
		"INSERT INTO " + r.schema + ".marker(tag) VALUES('w0')",
	} {
		if err := electionExec(vm, q); err != nil {
			return r.check("setup", false, "%s: %s", q, err)
		}
	}
	tags := []string{"w0"}
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

	if !failover {
		// ---- switchover of the virtual primary. The election result is the virtual primary as the operation ends,
		// before the next topology pass.
		cl.SwitchoverWaitTest()
		nm := cl.GetMaster()
		time.Sleep(5 * time.Second)
		r.dump("ring-switch-after")
		if !r.check("ring-switch-election", nm != nil && nm.URL != vm.URL, "the switchover elected %s as virtual primary (old %s)", urlOf(nm), vm.URL) {
			return false
		}
		r.observe("designated primary at the end of the switchover %s, after 5 seconds %s", nm.URL, urlOf(cl.GetMaster()))
		r.independentWrite("ring-switch-writable", nm)
		// CloseRing: the child of the old primary replicates from the old primary's parent
		r.check("ring-switch-closed", electionWait(electionHealthWait, func() bool { return r.replicating(child, parent) }), "after CloseRing the child %s replicates from the old primary's parent %s with both threads running", child.URL, parent.URL)
		if err := electionExec(nm, "INSERT INTO "+r.schema+".marker(tag) VALUES('s-new')"); err != nil {
			r.check("ring-switch-propagation", false, "insert on the new primary: %s", err)
		} else {
			tags = append(tags, "s-new")
			for _, s := range servers {
				if s.URL == vm.URL {
					continue
				}
				r.tagsOn("ring-switch-propagation@"+s.URL, s, tags)
			}
			ok, n := r.hasTags(vm, tags)
			io, sqlThread, err := electionReplicaThreads(vm)
			r.observe("the bypassed old virtual primary %s holds %d of the %d markers (all=%v); its replication threads I/O=%q SQL=%q (err=%v): the switchover froze it", vm.URL, n, len(tags), ok, io, sqlThread, err)
		}
	} else {
		// ---- failover of the virtual primary
		ctr := cl.GetFailoverCtr()
		fw := new(sync.WaitGroup)
		fw.Add(1)
		go cl.WaitFailover(fw)
		r.stop(vm)
		fw.Wait()
		np := cl.GetMaster() // the election result as the failover ends
		time.Sleep(5 * time.Second)
		r.dump("ring-failover-after")
		r.observe("designated primary at the end of the failover %s, after 5 seconds %s (lost %s)", urlOf(np), urlOf(cl.GetMaster()), vm.URL)
		if !r.check("ring-failover-election", np != nil && np.URL != vm.URL && !np.IsFailed(), "the failover elected %s as virtual primary after the loss of %s", urlOf(np), vm.URL) {
			return false
		}
		r.check("ring-failover-counter", cl.GetFailoverCtr() == ctr+1, "failover counter %d->%d", ctr, cl.GetFailoverCtr())
		r.independentWrite("ring-failover-writable", np)
		// CloseRing: the child of the stopped server replicates from the stopped server's parent
		r.check("ring-failover-closed", electionWait(electionHealthWait, func() bool { return r.replicating(child, parent) }), "after CloseRing the child %s replicates from the stopped server's parent %s with both threads running", child.URL, parent.URL)
		var other *cluster.ServerMonitor
		for _, s := range servers {
			if s.URL != vm.URL && s.URL != np.URL {
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
		r.check("ring-failover-survivors", survivorsRun, "both survivors (%s, %s) replicate with both threads running", np.URL, other.URL)
		r.recoverRing(vm)
		r.observe("ring after the rejoin: edges intact=%v, discovered topology=%s", r.ringEdges(servers, all), cl.GetTopology())
	}
	// the shared FoundCandidateMaster gate: its refusals come from the eligibility of the candidate, not from the ranking
	r.observe("FoundCandidateMaster gate: ERR00032 (no candidate) present in %d samples taken twice a second during the run", atomic.LoadInt32(&gate))
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
