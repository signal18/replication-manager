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

// galeraGate checks that a cluster declared Galera (the test's topology target, MatchTarget)
// is discovered Galera. It used to log "Skipping" and return PASS: a Galera cluster discovered
// master-slave (the regression fixed by 06c9757c4) passed every Galera test without running
// one. It is a failure (#2350).
func galeraGate(cl *cluster.Cluster, name string) bool {
	if cl.GetTopology() != config.TopoMultiMasterWsrep {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL %s: the cluster is declared %s but discovered %s", name, config.TopoMultiMasterWsrep, cl.GetTopology())
		return false
	}
	return true
}

// galeraWaitWhole waits for want Synced nodes and starts what is still down once a minute, as an
// operator does: a node's start can abort for a reason of its own (the state transfer could not
// resolve the joiner's name just after its restart), and the orchestrator can take minutes to see
// the container gone, answering "already started" until then. It returns why the cluster is not
// whole, nil once it is.
func galeraWaitWhole(cl *cluster.Cluster, want int, timeout time.Duration) error {
	var err error
	lastStart := time.Now()
	waitFor(timeout, func() bool {
		if _, err = galeraHealthy(cl, want); err == nil {
			return true
		}
		if time.Since(lastStart) >= time.Minute {
			lastStart = time.Now()
			for _, s := range cl.Servers {
				if s.IsDown() {
					cl.StartDatabaseService(s)
				}
			}
		}
		return false
	})
	return err
}

// galeraProxyProbeTable holds the node a write made through a proxy landed on.
const galeraProxyProbeTable = "regtest_proxy_route"

// galeraProvisionProxies provisions the cluster's proxies that are down and waits for them: moving
// the virtual master exists so that the writes follow it, a test without a proxy proves nothing
// (SKIP, never PASS).
func galeraProvisionProxies(cl *cluster.Cluster, name string, test *cluster.Test) bool {
	if len(cl.Proxies) == 0 {
		test.SkipReason = "needs a proxy: the test checks that the proxies follow the virtual master"
		return false
	}
	for _, p := range cl.Proxies {
		if p.IsDown() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Provisioning proxy %s", p.GetURL())
			if err := cl.InitProxyService(p); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL %s: cannot provision proxy %s: %s", name, p.GetURL(), err)
				return false
			}
		}
	}
	var down string
	if !waitFor(300*time.Second, func() bool {
		down = ""
		for _, p := range cl.Proxies {
			if p.IsDown() {
				down = p.GetURL()
			}
		}
		return down == ""
	}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL %s: proxy %s still down 300s after its provision", name, down)
		return false
	}
	return true
}

// galeraProxiesFollow writes through each proxy's write port until the write lands on master. A
// read proves nothing here and any node accepts a Galera write: the row records the node that
// executed it, and the master's own connection reads it back.
func galeraProxiesFollow(cl *cluster.Cluster, master *cluster.ServerMonitor, timeout time.Duration) error {
	table := rollingProbeSchema + "." + galeraProxyProbeTable
	if _, err := master.Conn.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id INT PRIMARY KEY, node VARCHAR(255))"); err != nil {
		return fmt.Errorf("cannot create %s on %s: %s", table, master.URL, err)
	}
	defer func() {
		if m := cl.GetMaster(); m != nil && m.Conn != nil {
			m.Conn.Exec("DROP TABLE IF EXISTS " + table)
		}
	}()
	var want string
	if err := master.Conn.QueryRow("SELECT @@hostname").Scan(&want); err != nil {
		return fmt.Errorf("cannot read the hostname of %s: %s", master.URL, err)
	}
	for _, p := range cl.Proxies {
		var got string
		var lastErr error
		if !waitFor(timeout, func() bool {
			db, err := p.GetClusterConnection()
			if err != nil {
				lastErr = err
				return false
			}
			defer db.Close()
			if _, err = db.Exec("REPLACE INTO " + table + " VALUES (1, @@hostname)"); err != nil {
				lastErr = err
				return false
			}
			lastErr = nil
			if err = master.Conn.QueryRow("SELECT node FROM " + table + " WHERE id = 1").Scan(&got); err != nil {
				lastErr = err
				return false
			}
			return got == want
		}) {
			if lastErr != nil {
				return fmt.Errorf("proxy %s: no write reached master %s within %s: %s", p.GetURL(), master.URL, timeout, lastErr)
			}
			return fmt.Errorf("proxy %s: writes land on %s, not on the virtual master %s (%s), %s after the move", p.GetURL(), got, master.URL, want, timeout)
		}
	}
	return nil
}

// TestGaleraBootstrap checks a bootstrapped Galera cluster: every server Synced in one Primary
// component of the cluster's size, replication-manager's master one of them, and a write on it
// reaching every node.
func (regtest *RegTest) TestGaleraBootstrap(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraBootstrap") || !galeraProvisionProxies(cl, "GaleraBootstrap", test) {
		return false
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
	if err := galeraProxiesFollow(cl, master, 60*time.Second); err != nil {
		logf("FAIL GaleraBootstrap: %s", err)
		return false
	}
	logf("PASS GaleraBootstrap: %d Synced nodes in the Primary component, master %s, writes reach every node, proxies route to it", len(cl.Servers), master.URL)
	return true
}

// TestGaleraSwitchover moves replication-manager's write target to another node through its
// switchover and checks every node stays Synced and writes on the new master reach them all.
func (regtest *RegTest) TestGaleraSwitchover(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraSwitchover") || !galeraProvisionProxies(cl, "GaleraSwitchover", test) {
		return false
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
	if err := galeraProxiesFollow(cl, after, 60*time.Second); err != nil {
		logf("FAIL GaleraSwitchover: %s", err)
		return false
	}
	logf("PASS GaleraSwitchover: master %s -> %s, every node Synced, writes reach every node, proxies follow", before.URL, after.URL)
	return true
}

// TestGaleraMasterLoss stops the database service of replication-manager's master: the other
// nodes keep a Primary component and the virtual master moves to one of them by itself, in any
// failover mode (a switchover: no operator, no ERR00002, #2350). Once started again, the lost
// node rejoins Synced with the writes made while it was down: on an OpenSVC-provisioned cluster
// that is the node the provision bootstrapped, whose start used to bootstrap a new cluster.
func (regtest *RegTest) TestGaleraMasterLoss(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraMasterLoss") || !galeraProvisionProxies(cl, "GaleraMasterLoss", test) {
		return false
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
	// no operator in any mode: the master moves by itself (the GUI has no failover for Galera)
	var elected *cluster.ServerMonitor
	if !waitFor(120*time.Second, func() bool { elected, err = galeraHealthy(cl, n-1); return err == nil }) {
		logf("FAIL GaleraMasterLoss: the master did not move off %s by itself (interactive=%v): %s", lost.URL, cl.Conf.Interactive, err)
		cl.StartDatabaseService(lost)
		return false
	}
	if cl.StateMachine.IsInState("ERR00002") {
		logf("FAIL GaleraMasterLoss: ERR00002 open: Galera never waits for an operator failover")
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

	if err := galeraProxiesFollow(cl, elected, 60*time.Second); err != nil {
		logf("FAIL GaleraMasterLoss: %s", err)
		cl.StartDatabaseService(lost)
		return false
	}

	logf("Starting %s again", lost.URL)
	if err := cl.StartDatabaseService(lost); err != nil {
		logf("FAIL GaleraMasterLoss: cannot start %s: %s", lost.URL, err)
		return false
	}
	if err = galeraWaitWhole(cl, n, 600*time.Second); err != nil {
		logf("FAIL GaleraMasterLoss: %s did not rejoin Synced 600s after its start: %s", lost.URL, err)
		return false
	}
	if ok, err := probeTablePresent(lost); err != nil || !ok {
		logf("FAIL GaleraMasterLoss: %s rejoined without the write made while it was down (%v)", lost.URL, err)
		return false
	}
	logf("PASS GaleraMasterLoss: %s lost, master moved to %s, proxies followed, %s rejoined Synced with the writes", lost.URL, elected.URL, lost.URL)
	return true
}

// TestGaleraFullRestartNotLastFirst stops the whole cluster, the master first, so the last node
// stopped (the only one Galera lets bootstrap, safe_to_bootstrap: 1) is another node. It then
// starts a node that was NOT the last one stopped, alone: that start must not create a new cluster
// (no Primary component while the others are down). Then it starts the rest: the cluster must come
// back whole (every node Synced in one Primary component) with the SAME cluster state UUID as
// before, never a new cluster (#2350, asked after the master-loss fix).
func (regtest *RegTest) TestGaleraFullRestartNotLastFirst(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}
	if !galeraGate(cl, "GaleraFullRestartNotLastFirst") || !galeraProvisionProxies(cl, "GaleraFullRestartNotLastFirst", test) {
		return false
	}
	n := len(cl.Servers)
	master, err := galeraHealthy(cl, n)
	if err != nil {
		logf("FAIL GaleraFullRestartNotLastFirst: the cluster is not healthy before the test: %s", err)
		return false
	}
	uuid := master.Status.Get("WSREP_CLUSTER_STATE_UUID")
	if uuid == "" {
		logf("FAIL GaleraFullRestartNotLastFirst: no cluster state UUID on %s", master.URL)
		return false
	}
	restartAll := func() {
		for _, s := range cl.Servers {
			if s.IsDown() {
				cl.StartDatabaseService(s)
			}
		}
	}

	// the master first, then the others in declared order: the last one stopped is not the master
	order := []*cluster.ServerMonitor{master}
	for _, s := range cl.Servers {
		if s.URL != master.URL {
			order = append(order, s)
		}
	}
	for _, s := range order {
		logf("Stopping %s", s.URL)
		if err := cl.StopDatabaseService(s); err != nil {
			logf("FAIL GaleraFullRestartNotLastFirst: cannot stop %s: %s", s.URL, err)
			restartAll()
			return false
		}
		if !waitFor(120*time.Second, func() bool { return s.IsDown() }) {
			logf("FAIL GaleraFullRestartNotLastFirst: %s not down 120s after its stop", s.URL)
			restartAll()
			return false
		}
	}
	first, last := order[0], order[len(order)-1]
	logf("Cluster down; the last node stopped is %s; starting %s first, alone", last.URL, first.URL)

	// the node that was not the last to leave, alone: never a Primary component (a new cluster)
	if err := cl.StartDatabaseService(first); err != nil {
		logf("Start of %s refused: %s (no new cluster: acceptable)", first.URL, err)
	}
	newCluster := ""
	waitFor(90*time.Second, func() bool {
		if first.Conn != nil && !first.IsDown() && first.HasWsrepPrimary() {
			newCluster = first.Status.Get("WSREP_CLUSTER_STATE_UUID")
			return true
		}
		return false
	})
	if newCluster != "" {
		logf("FAIL GaleraFullRestartNotLastFirst: %s, not the last node stopped, formed a Primary component alone (state UUID %s, was %s): a new cluster", first.URL, newCluster, uuid)
		restartAll()
		return false
	}

	// the rest, the last node stopped first: the cluster comes back whole, the same cluster
	starts := []*cluster.ServerMonitor{last}
	for _, s := range order[:len(order)-1] {
		starts = append(starts, s)
	}
	for _, s := range starts {
		if s.IsDown() {
			logf("Starting %s", s.URL)
			if err := cl.StartDatabaseService(s); err != nil {
				logf("Start of %s: %s", s.URL, err)
			}
		}
	}
	if err = galeraWaitWhole(cl, n, 600*time.Second); err != nil {
		logf("FAIL GaleraFullRestartNotLastFirst: the cluster did not come back whole 600s after the starts: %s", err)
		restartAll()
		return false
	}
	for _, s := range galeraSynced(cl) {
		if got := s.Status.Get("WSREP_CLUSTER_STATE_UUID"); got != uuid {
			logf("FAIL GaleraFullRestartNotLastFirst: %s is in cluster %s, was %s: a new cluster was bootstrapped", s.URL, got, uuid)
			return false
		}
	}
	m := cl.GetMaster()
	if m == nil {
		logf("FAIL GaleraFullRestartNotLastFirst: no master after the restart")
		return false
	}
	if err := galeraProxiesFollow(cl, m, 60*time.Second); err != nil {
		logf("FAIL GaleraFullRestartNotLastFirst: %s", err)
		return false
	}
	logf("PASS GaleraFullRestartNotLastFirst: %s started first formed no cluster; the cluster came back whole, state UUID %s kept, proxies follow %s", first.URL, uuid, m.URL)
	return true
}
