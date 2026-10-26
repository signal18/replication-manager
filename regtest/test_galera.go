// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// Galera (multi-master-wsrep, MariaDB): every node takes writes, replication-manager picks one
// of them as the write target (its virtual master, routed by the proxies), moves it on a
// switchover, and on the loss of that node moves it to a node still in the Primary component.
// These tests read each node's wsrep status (WSREP_CLUSTER_STATUS, WSREP_LOCAL_STATE,
// WSREP_CLUSTER_SIZE, refreshed by the monitor every tick) and check replication-manager's view
// against it (F5). FUNCTIONAL: they gate on the topology, never on the orchestrator.

// galeraSynced lists the nodes that are up, in the Primary component and Synced.
func galeraSynced(cl *cluster.Cluster) []*cluster.ServerMonitor {
	var synced []*cluster.ServerMonitor
	for _, s := range cl.Servers {
		if s.Conn != nil && !s.IsDown() && s.HasWsrepPrimary() && s.HasWsrepSync() {
			synced = append(synced, s)
		}
	}
	return synced
}

// galeraHealthy says whether want nodes are Synced in one Primary component of that size and
// replication-manager's master is one of them; else why not.
func galeraHealthy(cl *cluster.Cluster, want int) (*cluster.ServerMonitor, error) {
	synced := galeraSynced(cl)
	if len(synced) != want {
		return nil, fmt.Errorf("%d Synced nodes in the Primary component (want %d)", len(synced), want)
	}
	for _, s := range synced {
		if size := s.Status.Get("WSREP_CLUSTER_SIZE"); size != fmt.Sprint(want) {
			return nil, fmt.Errorf("%s sees a cluster of %s nodes (want %d)", s.URL, size, want)
		}
	}
	m := cl.GetMaster()
	if m == nil {
		return nil, fmt.Errorf("replication-manager has no master")
	}
	for _, s := range synced {
		if s.URL == m.URL {
			return m, nil
		}
	}
	return nil, fmt.Errorf("replication-manager's master %s is not a Synced node", m.URL)
}

// galeraProbeOn writes the probe table on the master and waits until each of the nodes has it
// (the write set is certified and applied on every node).
func galeraProbeOn(master *cluster.ServerMonitor, nodes []*cluster.ServerMonitor, timeout time.Duration) error {
	if err := createProbeTable(master); err != nil {
		return err
	}
	var missing []string
	if !waitFor(timeout, func() bool {
		missing = missing[:0]
		for _, s := range nodes {
			if ok, err := probeTablePresent(s); err != nil || !ok {
				missing = append(missing, s.URL)
			}
		}
		return len(missing) == 0
	}) {
		return fmt.Errorf("probe table %s.%s missing on %s after %s", rollingProbeSchema, rollingProbeTable, strings.Join(missing, ", "), timeout)
	}
	return nil
}

func galeraGate(cl *cluster.Cluster, name string) bool {
	if cl.GetTopology() != config.TopoMultiMasterWsrep {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping %s: requires topology %s, cluster is %s", name, config.TopoMultiMasterWsrep, cl.GetTopology())
		return false
	}
	return true
}

// TestGaleraBootstrap checks a bootstrapped Galera cluster: every server Synced in one Primary
// component of the cluster's size, replication-manager's master one of them, and a write on it
// reaching every node.
func (regtest *RegTest) TestGaleraBootstrap(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraBootstrap") {
		return true
	}
	var master *cluster.ServerMonitor
	var err error
	// the monitor needs a tick or two to read the nodes after a provision or a restart
	if !waitFor(30*time.Second, func() bool { master, err = galeraHealthy(cl, len(cl.Servers)); return err == nil }) {
		logf("FAIL GaleraBootstrap: %s", err)
		return false
	}
	defer dropProbeTable(cl)
	if err := galeraProbeOn(master, cl.Servers, 30*time.Second); err != nil {
		logf("FAIL GaleraBootstrap: %s", err)
		return false
	}
	logf("PASS GaleraBootstrap: %d Synced nodes in the Primary component, master %s, writes reach every node", len(cl.Servers), master.URL)
	return true
}

// TestGaleraSwitchover moves replication-manager's write target to another node through its
// switchover and checks every node stays Synced and writes on the new master reach them all.
func (regtest *RegTest) TestGaleraSwitchover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraSwitchover") {
		return true
	}
	before, err := galeraHealthy(cl, len(cl.Servers))
	if err != nil {
		logf("FAIL GaleraSwitchover: the cluster is not healthy before the switchover: %s", err)
		return false
	}
	cl.SwitchoverWaitTest()

	var after *cluster.ServerMonitor
	if !waitFor(60*time.Second, func() bool {
		after, err = galeraHealthy(cl, len(cl.Servers))
		return err == nil && after.URL != before.URL
	}) {
		if err == nil {
			err = fmt.Errorf("the master is still %s", before.URL)
		}
		logf("FAIL GaleraSwitchover: %s", err)
		return false
	}
	defer dropProbeTable(cl)
	if err := galeraProbeOn(after, cl.Servers, 30*time.Second); err != nil {
		logf("FAIL GaleraSwitchover: %s", err)
		return false
	}
	logf("PASS GaleraSwitchover: master %s -> %s, every node Synced, writes reach every node", before.URL, after.URL)
	return true
}

// TestGaleraMasterLoss stops the database service of replication-manager's master: the other
// nodes keep a Primary component, the master moves to one of them (in manual mode the cluster
// first waits for the operator, ERR00002, then the operator's failover moves it), and once
// started again the lost node rejoins Synced with the writes made while it was down.
func (regtest *RegTest) TestGaleraMasterLoss(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraMasterLoss") {
		return true
	}
	n := len(cl.Servers)
	lost, err := galeraHealthy(cl, n)
	if err != nil {
		logf("FAIL GaleraMasterLoss: the cluster is not healthy before the test: %s", err)
		return false
	}
	logf("Stopping the master %s", lost.URL)
	if err := cl.StopDatabaseService(lost); err != nil {
		logf("FAIL GaleraMasterLoss: cannot stop %s: %s", lost.URL, err)
		return false
	}

	// the remaining nodes keep the Primary component; replication-manager declares the master failed
	if !waitFor(120*time.Second, func() bool {
		return len(galeraSynced(cl)) == n-1 && lost.IsFailed()
	}) {
		logf("FAIL GaleraMasterLoss: 120s after stopping %s: %d Synced nodes (want %d), %s state %s", lost.URL, len(galeraSynced(cl)), n-1, lost.URL, lost.State)
		cl.StartDatabaseService(lost)
		return false
	}
	if cl.Conf.Interactive {
		// manual mode: the cluster waits for the operator, who fails over
		if !waitFor(30*time.Second, func() bool { return cl.StateMachine.IsInState("ERR00002") }) {
			logf("FAIL GaleraMasterLoss: manual mode, master %s failed, but no ERR00002 (waiting for a manual failover)", lost.URL)
			cl.StartDatabaseService(lost)
			return false
		}
		logf("Manual mode: ERR00002 open, failing over as the operator")
		cl.FailoverNow()
	}
	var elected *cluster.ServerMonitor
	if !waitFor(120*time.Second, func() bool { elected, err = galeraHealthy(cl, n-1); return err == nil }) {
		logf("FAIL GaleraMasterLoss: the master did not move off %s: %s", lost.URL, err)
		cl.StartDatabaseService(lost)
		return false
	}
	defer dropProbeTable(cl)
	// a write while the old master is down, which it must get when it comes back
	if err := galeraProbeOn(elected, galeraSynced(cl), 30*time.Second); err != nil {
		logf("FAIL GaleraMasterLoss: %s", err)
		cl.StartDatabaseService(lost)
		return false
	}

	logf("Starting %s again", lost.URL)
	if err := cl.StartDatabaseService(lost); err != nil {
		logf("FAIL GaleraMasterLoss: cannot start %s: %s", lost.URL, err)
		return false
	}
	if !waitFor(300*time.Second, func() bool { _, err = galeraHealthy(cl, n); return err == nil }) {
		logf("FAIL GaleraMasterLoss: %s did not rejoin Synced 300s after its start: %s", lost.URL, err)
		return false
	}
	if ok, err := probeTablePresent(lost); err != nil || !ok {
		logf("FAIL GaleraMasterLoss: %s rejoined without the write made while it was down (%v)", lost.URL, err)
		return false
	}
	logf("PASS GaleraMasterLoss: %s lost, master moved to %s, %s rejoined Synced with the writes", lost.URL, elected.URL, lost.URL)
	return true
}
