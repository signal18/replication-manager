// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
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

// SimulateMasterFailure severs only this cluster's connection to the MASTER on this
// instance (slaves stay reachable). Used when the master is colocated on the
// isolated/minority side: the majority instance loses the master but keeps
// its slave to promote.
func (cluster *Cluster) SimulateMasterFailure(duration time.Duration) {
	atomic.StoreInt64(&cluster.sbMasterFailUntil, sbBoundedUntil(duration))
	mst := ""
	if cluster.GetMaster() != nil {
		mst = cluster.GetMaster().URL
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: master connection cut for cluster %s (master %s)", cluster.Name, mst)
}

// RestoreSplitBrainSimulation clears all simulated cuts on this cluster immediately.
func (cluster *Cluster) RestoreSplitBrainSimulation() {
	if cluster.IsSplitBrainSimulationActive() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SPLITBRAIN SIMULATION: cuts cleared for cluster %s", cluster.Name)
	}
	atomic.StoreInt64(&cluster.sbDatabaseFailUntil, 0)
	atomic.StoreInt64(&cluster.sbArbitratorFailUntil, 0)
	atomic.StoreInt64(&cluster.sbMasterFailUntil, 0)
}

func (cluster *Cluster) IsDatabaseFailureSimulated() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbDatabaseFailUntil))
}

func (cluster *Cluster) IsArbitratorFailureSimulated() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbArbitratorFailUntil))
}

func (cluster *Cluster) IsMasterFailureSimulated() bool {
	return sbActive(atomic.LoadInt64(&cluster.sbMasterFailUntil))
}

// IsSplitBrainSimulationActive reports whether any simulated cut is live on this cluster.
func (cluster *Cluster) IsSplitBrainSimulationActive() bool {
	return cluster.IsDatabaseFailureSimulated() || cluster.IsArbitratorFailureSimulated() || cluster.IsMasterFailureSimulated()
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
	return strings.Join(cuts, ",")
}

// AssertSplitBrainSimulationState raises the standing split-brain warning while any cut is live so
// the simulation is impossible to forget. Called per tick.
func (cluster *Cluster) AssertSplitBrainSimulationState() {
	if cluster.IsSplitBrainSimulationActive() {
		cluster.SetState("WARN0181", state.State{ErrType: "WARNING", ErrKey: "WARN0181", ErrDesc: fmt.Sprintf(clusterError["WARN0181"], cluster.SplitBrainSimulationDescription(), cluster.SplitBrainSimulationRemaining()), ErrFrom: "TEST"})
	}
}
