// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestStandbyNeverFailsOverWithLiveActive (#2040, #2397) — the master really dies while
// both replication-managers are connected: no link cut, no split brain. Both run the
// cluster's own failover-falsepositive-ping-counter, so the standby can detect the failure
// first and be the first to ask the arbitrator (the race of the incident). The standby
// must not fail over while its active peer is alive: the active
// runs the failover, keeps the crash record and rejoins the old master when it comes back.
//
// The other tests of the arbitration (test_split_brain.go) all cut a link. This one covers
// the plain master loss of a healthy pair, where the standby won the arbitration only
// because it asked first and the active never reports to the arbitrator in normal operation.
// It is RED until #2040 is fixed.
func (regtest *RegTest) TestStandbyNeverFailsOverWithLiveActive(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return standbyNeverFailsOverWithLiveActive(cl, "testStandbyNeverFailsOverWithLiveActive")
}

func standbyNeverFailsOverWithLiveActive(cl *cluster.Cluster, label string) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", label+": "+format, args...)
	}
	if GetPeerIsActive == nil {
		logf("no peer-status hook: this test needs the arbitration peer")
		return false
	}
	if !cl.IsActive() {
		logf("this node must be the active replication-manager at start (status %s)", cl.Status)
		return false
	}
	if cl.IsSplitBrain {
		logf("split brain already in progress: the pair must be connected at start")
		return false
	}
	if peerActive, err := GetPeerIsActive(cl.Name); err != nil || peerActive {
		logf("the peer must be reachable and standby at start (active=%t, err=%v)", peerActive, err)
		return false
	}
	oldMaster := cl.GetMaster()
	if oldMaster == nil {
		logf("no master")
		return false
	}
	oldMasterURL := oldMaster.URL

	cl.SetInteractive(false)
	cl.SetRejoin(true)
	failoverCtr := cl.FailoverCtr
	logf("both replication-managers connected, this node active, peer standby; stopping master %s (local failcount %d)", oldMasterURL, cl.Conf.MaxFail)

	cl.StopDatabaseService(oldMaster)
	restarted := false
	defer func() {
		if !restarted {
			cl.StartDatabaseService(oldMaster)
		}
	}()

	// The whole failover window: the peer must never become active, and the failover must be
	// this node's own (its FailoverCtr moves and it elects a new master).
	deadline := time.Now().Add(time.Duration(cl.Conf.MaxFail+cl.Conf.MaxFail/2+10)*time.Duration(cl.Conf.MonitoringTicker)*time.Second + 60*time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if peerActive, err := GetPeerIsActive(cl.Name); err == nil && peerActive {
			logf("the STANDBY became active and took the failover while this active node was alive (#2040)")
			return false
		}
		if cl.FailoverCtr != failoverCtr {
			break
		}
	}
	if cl.FailoverCtr == failoverCtr {
		logf("this active node never failed over (status %s)", cl.Status)
		return false
	}
	newMaster := cl.GetMaster()
	if newMaster == nil || newMaster.URL == oldMasterURL {
		logf("no new master after the failover")
		return false
	}
	if cr := cl.GetCrashes(); len(cr) == 0 || cr[len(cr)-1].URL != oldMasterURL || cr[len(cr)-1].ElectedMasterURL != newMaster.URL {
		logf("this node holds no crash record of %s elected %s (#2397)", oldMasterURL, newMaster.URL)
		return false
	}
	logf("failover run by the active: %s -> %s, crash recorded", oldMasterURL, newMaster.URL)

	// The old master comes back: the active rejoins it under the new master.
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cl.WaitRejoin(wg)
	cl.StartDatabaseService(oldMaster)
	restarted = true
	wg.Wait()
	time.Sleep(2 * time.Second)
	if peerActive, err := GetPeerIsActive(cl.Name); err == nil && peerActive {
		logf("the standby became active during the rejoin")
		return false
	}
	if !oldMaster.IsSlave {
		logf("old master %s was not rejoined: state %s (#2397)", oldMasterURL, oldMaster.State)
		return false
	}
	if !cl.CheckSlavesRunning() {
		logf("replication is not running on every replica after the rejoin")
		return false
	}
	logf("old master %s rejoined under %s; the standby stayed standby", oldMasterURL, newMaster.URL)
	return true
}
