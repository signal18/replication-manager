// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// The split-brain simulator severs, for testing, the individual communication links a
// replication-manager instance depends on, so real split-brain scenarios can
// be reproduced without touching the network. Each link is cut independently:
//
//   - db         : this node's database connections for this cluster fail
//                  (GetNewDBConn) — the node loses sight of every server.
//   - master     : only this node's connection to the MASTER fails; slaves
//                  stay reachable so the majority side can still promote. Used
//                  when the master is colocated on the isolated/minority side.
//   - arbitrator : this node's arbitrator calls fail (arbitratorElection,
//                  isActiveArbitration) and it stops reporting, so its row goes
//                  stale — the node cannot confirm authority.
//   - heartbeat  : server-level (ReplicationManager), one-directional by
//                  design — this node's inbound /api/heartbeat stops answering,
//                  so the PEER's outbound request times out and detects it as
//                  gone. Each node darkens only itself; cutting one side never
//                  affects the other direction, exactly like a real cable cut
//                  where neither end can reach the other independently.
//
// Composed, they reproduce e.g. an active repman co-located with its master
// losing external network (cut arbitrator+heartbeat, master left reachable):
// the isolated node cannot confirm authority, the standby sees it dark and
// takes over through the arbitrator — no dual master, because the isolated
// side can't self-confirm. Split brain is NOT forced; it emerges naturally
// from the cuts so the real detection and arbitration paths are exercised.
//
// Deliberately RUNTIME state, never a config key: a config key would
// replicate to the peer through the config event log and isolate both sides.

const (
	sbDefault = 120 * time.Second
	sbMax     = 15 * time.Minute
)

func sbBoundedUntil(duration time.Duration) int64 {
	if duration <= 0 {
		duration = sbDefault
	}
	if duration > sbMax {
		duration = sbMax
	}
	return time.Now().Add(duration).Unix()
}

func sbActive(until int64) bool {
	return until > 0 && time.Now().Unix() < until
}

func sbRemaining(until int64) int64 {
	if until == 0 {
		return 0
	}
	if left := until - time.Now().Unix(); left > 0 {
		return left
	}
	return 0
}

// SimulateDatabaseFailure severs this cluster's database link on this instance.
func (cluster *Cluster) SimulateDatabaseFailure(duration time.Duration) {
	atomic.StoreInt64(&cluster.sbDatabaseFailUntil, sbBoundedUntil(duration))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: database link cut for cluster %s", cluster.Name)
}

// SimulateArbitratorFailure severs this cluster's arbitrator link on this instance.
func (cluster *Cluster) SimulateArbitratorFailure(duration time.Duration) {
	atomic.StoreInt64(&cluster.sbArbitratorFailUntil, sbBoundedUntil(duration))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: arbitrator link cut for cluster %s", cluster.Name)
}

// SimulateArbitratorFailureAll severs the arbitrator link on EVERY cluster this
// instance drives, not just the receiver. A real partition isolates the whole DC
// from the arbitrator (DC3) for ALL clusters at once; the per-cluster cut used by
// SimulateArbitratorFailure produces the impossible "one cluster alone loses the
// arbitrator" scenario, which is a test artifact, not a real failure mode. Minority
// tests use this so every cluster goes minority TOGETHER, matching reality — a
// uniform verdict, coherent server status, no per-cluster divergence.
func (cluster *Cluster) SimulateArbitratorFailureAll(duration time.Duration) {
	armed := 0
	for _, c := range cluster.clusterList {
		c.SimulateArbitratorFailure(duration)
		armed++
	}
	if armed == 0 { // single-cluster / unit-test context: at least arm the receiver
		cluster.SimulateArbitratorFailure(duration)
	}
}

// SimulateMinority marks THIS instance the minority side of the partition, on every
// cluster it drives (a real partition isolates the whole DC at once, like
// SimulateArbitratorFailureAll). While armed the arbitrator link reads as severed
// (IsArbitratorFailureSimulated), so the minority cannot confirm authority and yields to
// standby while the majority side fails over. Timed like every other cut — a real outage
// resolves too, it is just a question of time: the simulation ends when all cuts time
// out, or earlier on explicit restore.
func (cluster *Cluster) SimulateMinority(duration time.Duration) {
	until := sbBoundedUntil(duration)
	armed := 0
	for _, c := range cluster.clusterList {
		atomic.StoreInt64(&c.sbMinorityUntil, until)
		armed++
	}
	if armed == 0 { // single-cluster / unit-test context: at least arm the receiver
		atomic.StoreInt64(&cluster.sbMinorityUntil, until)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: this instance marked minority side, auto-restore in %ds", sbRemaining(until))
}

// SimulateMasterFailure severs only this cluster's connection to the MASTER on this
// instance (slaves stay reachable). Used when the master is colocated on the
// isolated/minority side: the majority instance loses the master but keeps
// its slave to promote.
func (cluster *Cluster) SimulateMasterFailure(duration time.Duration) {
	mst := ""
	if cluster.GetMaster() != nil {
		mst = cluster.GetMaster().URL
	}
	// Pin the frozen host ONCE, on the first arm. Re-arms (the freeze API re-fires
	// per tick during the split) only extend the timer — so the cut can NOT migrate
	// to the promoted replica when a failover moves GetMaster().
	if !cluster.IsMasterFailureSimulated() {
		cluster.sbMasterFailURL.Store(mst)
	}
	atomic.StoreInt64(&cluster.sbMasterFailUntil, sbBoundedUntil(duration))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: master connection cut for cluster %s (master %s)", cluster.Name, mst)
}

// SimulateServerFailure severs this instance's DB link to ONE server, pinned by URL —
// the data-plane counterpart of SimulateMasterFailure for NON-master hosts. The minority
// scenario uses it (op 5/5) to also lose the MAJORITY-side databases: a real partition
// cuts the wire in both directions, so the minority must see the remote slave genuinely
// Failed (firing the real Failed->up rejoin trigger at resolve), not merely untrusted.
// Timed like every other cut.
func (cluster *Cluster) SimulateServerFailure(url string, duration time.Duration) {
	cluster.sbServerFailMu.Lock()
	if cluster.sbServerFailURLs == nil {
		cluster.sbServerFailURLs = make(map[string]int64)
	}
	cluster.sbServerFailURLs[url] = sbBoundedUntil(duration)
	cluster.sbServerFailMu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: server connection cut for cluster %s (server %s)", cluster.Name, url)
}

func (cluster *Cluster) isServerFailureSimulated(host string) bool {
	cluster.sbServerFailMu.Lock()
	defer cluster.sbServerFailMu.Unlock()
	return sbActive(cluster.sbServerFailURLs[host])
}

// simulatedServerFailures returns the URLs of the per-host cuts currently live.
func (cluster *Cluster) simulatedServerFailures() []string {
	cluster.sbServerFailMu.Lock()
	defer cluster.sbServerFailMu.Unlock()
	var urls []string
	for url, until := range cluster.sbServerFailURLs {
		if sbActive(until) {
			urls = append(urls, url)
		}
	}
	sort.Strings(urls)
	return urls
}

// SimulatedOnFreezeDBAccess reports whether the split-brain sim should freeze DB
// access to host. The frozen host is PINNED at SimulateMasterFailure /
// SimulateServerFailure time and checked by URL, so it does NOT migrate with
// GetMaster(): a failover promoting a replica must not shift the cut onto the new
// master (that let the majority see the isolated old master reappear and revert its
// own election).
func (cluster *Cluster) SimulatedOnFreezeDBAccess(host string) bool {
	if cluster.isServerFailureSimulated(host) {
		return true
	}
	if !cluster.IsMasterFailureSimulated() {
		return false
	}
	pinned, _ := cluster.sbMasterFailURL.Load().(string)
	return pinned != "" && host == pinned
}

// RestoreSplitBrainSimulation clears all simulated cuts on this cluster immediately.
func (cluster *Cluster) RestoreSplitBrainSimulation() {
	if cluster.IsSplitBrainSimulationActive() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: cuts cleared for cluster %s", cluster.Name)
	}
	atomic.StoreInt64(&cluster.sbDatabaseFailUntil, 0)
	atomic.StoreInt64(&cluster.sbArbitratorFailUntil, 0)
	atomic.StoreInt64(&cluster.sbMasterFailUntil, 0)
	cluster.sbMasterFailURL.Store("")
	atomic.StoreInt64(&cluster.sbMinorityUntil, 0)
	cluster.sbServerFailMu.Lock()
	cluster.sbServerFailURLs = nil
	cluster.sbServerFailMu.Unlock()
}

func (cluster *Cluster) IsDatabaseFailureSimulated() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbDatabaseFailUntil))
}

func (cluster *Cluster) IsArbitratorFailureSimulated() bool {
	// The minority marker implies a severed arbitrator link: the minority cannot
	// confirm authority for as long as the simulated outage holds.
	return sbActive(atomic.LoadInt64(&cluster.sbArbitratorFailUntil)) || cluster.IsSplitBrainSimulatorMinoritySide()
}

func (cluster *Cluster) IsMasterFailureSimulated() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbMasterFailUntil))
}

// IsSplitBrainSimulationActive reports whether any simulated cut is live on this cluster.
func (cluster *Cluster) IsSplitBrainSimulationActive() bool {
	return cluster.IsDatabaseFailureSimulated() || cluster.IsArbitratorFailureSimulated() || cluster.IsMasterFailureSimulated() || len(cluster.simulatedServerFailures()) > 0
}

// IsSplitBrainSimulatorRunning reports whether any simulated cut is live.
func (cluster *Cluster) IsSplitBrainSimulatorRunning() bool {
	return cluster.IsSplitBrainSimulationActive()
}

// IsSplitBrainSimulatorActiveSide reports whether THIS repman is the side the sim chose
// to block from the master (the active/majority side that will fail over). No identity
// check is needed: simulator state is process-local and never replicates, so the master
// cut being armed here means this instance is the one that received the freeze API. The
// minority forms on the OTHER partition, which keeps the master.
func (cluster *Cluster) IsSplitBrainSimulatorActiveSide() bool {
	return cluster.IsMasterFailureSimulated()
}

// IsSplitBrainSimulatorMinoritySide reports whether the test (SimulateMinority) marked
// THIS repman minority — a local call is all it takes to pick the side, since simulator
// state never replicates. Consequence: it cuts its own arbitrator link and yields to
// standby, while the majority (master-frozen side) fails over.
func (cluster *Cluster) IsSplitBrainSimulatorMinoritySide() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbMinorityUntil))
}

// IsSplitBrainSimulatorEnabledSlaveCanReachMaster reports whether a master/db cut is
// simulated in the mode where the slave->master DB wire stays LIVE (this app-level sim
// only darkens repman's VIEW of the master). While true, the slave keeps replicating
// and receiving heartbeats — which would otherwise look like a false positive and veto
// the failover forever — so the false-positive detection MUST be disabled while it
// holds. It is surfaced as VSPLIT0005 (AssertSplitBrainSimulationState) so the disable is
// visible opening/closing in the state timeline.
func (cluster *Cluster) IsSplitBrainSimulatorEnabledSlaveCanReachMaster() bool {
	return cluster.IsMasterFailureSimulated() || cluster.IsDatabaseFailureSimulated()
}

// SplitBrainSimulationRemaining returns the seconds left before the cluster's cuts auto-restore.
func (cluster *Cluster) SplitBrainSimulationRemaining() int64 {
	d := sbRemaining(atomic.LoadInt64(&cluster.sbDatabaseFailUntil))
	if a := sbRemaining(atomic.LoadInt64(&cluster.sbArbitratorFailUntil)); a > d {
		d = a
	}
	if m := sbRemaining(atomic.LoadInt64(&cluster.sbMasterFailUntil)); m > d {
		d = m
	}
	if n := sbRemaining(atomic.LoadInt64(&cluster.sbMinorityUntil)); n > d {
		d = n
	}
	cluster.sbServerFailMu.Lock()
	for _, until := range cluster.sbServerFailURLs {
		if s := sbRemaining(until); s > d {
			d = s
		}
	}
	cluster.sbServerFailMu.Unlock()
	return d
}

// SplitBrainSimulationDescription lists the active cuts on this cluster, for the state.
func (cluster *Cluster) SplitBrainSimulationDescription() string {
	var cuts []string
	if cluster.IsDatabaseFailureSimulated() {
		cuts = append(cuts, "db")
	}
	if cluster.IsArbitratorFailureSimulated() {
		cuts = append(cuts, "arbitrator")
	}
	if cluster.IsMasterFailureSimulated() {
		cuts = append(cuts, "master")
	}
	if cluster.IsSplitBrainSimulatorMinoritySide() {
		cuts = append(cuts, "minority")
	}
	for _, url := range cluster.simulatedServerFailures() {
		cuts = append(cuts, "server:"+url)
	}
	return strings.Join(cuts, ",")
}

// AssertSplitBrainSimulationState raises the standing split-brain warning while any cut is live so
// the simulation is impossible to forget. Called per tick.
func (cluster *Cluster) AssertSplitBrainSimulationState() {
	if cluster.IsSplitBrainSimulatorRunning() {
		cluster.SetState("VSPLIT0001", state.State{ErrType: "WARNING", ErrKey: "VSPLIT0001", ErrDesc: fmt.Sprintf(clusterError["VSPLIT0001"], cluster.SplitBrainSimulationDescription(), cluster.SplitBrainSimulationRemaining()), ErrFrom: "TEST"})
	}
	// VSPLIT0003 — the DB-host-blocked state prints on the side the sim chose to block
	// (the active/majority side), one per frozen host, so the timeline shows exactly
	// which host is cut and from which side.
	if cluster.IsSplitBrainSimulatorActiveSide() {
		for _, sv := range cluster.Servers {
			if sv != nil && cluster.SimulatedOnFreezeDBAccess(sv.URL) {
				cluster.SetState("VSPLIT0003", state.State{ErrType: "WARNING", ErrKey: "VSPLIT0003", ErrDesc: fmt.Sprintf(clusterError["VSPLIT0003"], cluster.Conf.ArbitrationSasUniqueId, sv.URL), ErrFrom: "TEST", ServerUrl: sv.URL})
			}
		}
	}
	// VSPLIT0006 — per-host data-plane cut (op 5/5): this side is blocked from a
	// majority-side database, one state per frozen host.
	for _, url := range cluster.simulatedServerFailures() {
		cluster.SetState("VSPLIT0006", state.State{ErrType: "WARNING", ErrKey: "VSPLIT0006", ErrDesc: fmt.Sprintf(clusterError["VSPLIT0006"], url), ErrFrom: "TEST", ServerUrl: url})
	}
	// VSPLIT0004 — the arbitrator link reads as severed on this side (timed cut or
	// latched minority): it cannot confirm authority and yields to standby while the
	// other partition fails over.
	if cluster.IsArbitratorFailureSimulated() {
		cluster.SetState("VSPLIT0004", state.State{ErrType: "WARNING", ErrKey: "VSPLIT0004", ErrDesc: clusterError["VSPLIT0004"], ErrFrom: "TEST"})
	}
	// VSPLIT0005 — slave still reaches master, so the ERR00028 false-positive guard is
	// disabled; track it open/close so the suppression is visible.
	if cluster.IsSplitBrainSimulatorEnabledSlaveCanReachMaster() {
		cluster.SetState("VSPLIT0005", state.State{ErrType: "WARNING", ErrKey: "VSPLIT0005", ErrDesc: clusterError["VSPLIT0005"], ErrFrom: "TEST"})
	}
}
