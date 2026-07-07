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

// Chaos isolation severs, for testing, the individual communication links a
// replication-manager instance depends on, so real split-brain scenarios can
// be reproduced without touching the network. Each link is cut independently:
//
//   - db         : this node's database connections for this cluster fail
//                  (GetNewDBConn) — the node loses sight of the master.
//   - arbitrator : this node's arbitrator calls fail (arbitratorElection,
//                  isActiveArbitration) — the node cannot confirm authority.
//   - peer       : server-level (ReplicationManager) — the peer heartbeat is
//                  severed both ways: /api/heartbeat stops answering and this
//                  node's HeartbeatPeerSplitBrain treats the peer as gone.
//
// Composed, they reproduce e.g. an active repman co-located with its master
// losing external network (cut arbitrator+peer, master left reachable): the
// isolated node cannot confirm authority, the standby sees it dark and takes
// over through the arbitrator — no dual master, because the isolated side
// can't self-confirm. Split brain is NOT forced; it emerges naturally from
// the cuts so the real detection and arbitration paths are exercised.
//
// Deliberately RUNTIME state, never a config key: a config key would
// replicate to the peer through the config event log and isolate both sides.

const (
	chaosDefault = 120 * time.Second
	chaosMax     = 15 * time.Minute
)

func chaosBoundedUntil(duration time.Duration) int64 {
	if duration <= 0 {
		duration = chaosDefault
	}
	if duration > chaosMax {
		duration = chaosMax
	}
	return time.Now().Add(duration).Unix()
}

func chaosActive(until int64) bool {
	return until > 0 && time.Now().Unix() < until
}

func chaosRemaining(until int64) int64 {
	if until == 0 {
		return 0
	}
	if left := until - time.Now().Unix(); left > 0 {
		return left
	}
	return 0
}

// ChaosCutDB severs this cluster's database link on this instance.
func (cluster *Cluster) ChaosCutDB(duration time.Duration) {
	atomic.StoreInt64(&cluster.chaosCutDBUntil, chaosBoundedUntil(duration))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "CHAOS: database link cut for cluster %s", cluster.Name)
}

// ChaosCutArbitrator severs this cluster's arbitrator link on this instance.
func (cluster *Cluster) ChaosCutArbitrator(duration time.Duration) {
	atomic.StoreInt64(&cluster.chaosCutArbitratorUntil, chaosBoundedUntil(duration))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "CHAOS: arbitrator link cut for cluster %s", cluster.Name)
}

// ChaosStop clears all chaos cuts on this cluster immediately.
func (cluster *Cluster) ChaosStop() {
	if cluster.IsChaosActive() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "CHAOS: cuts cleared for cluster %s", cluster.Name)
	}
	atomic.StoreInt64(&cluster.chaosCutDBUntil, 0)
	atomic.StoreInt64(&cluster.chaosCutArbitratorUntil, 0)
}

func (cluster *Cluster) IsChaosDBCut() bool {
	return chaosActive(atomic.LoadInt64(&cluster.chaosCutDBUntil))
}

func (cluster *Cluster) IsChaosArbitratorCut() bool {
	return chaosActive(atomic.LoadInt64(&cluster.chaosCutArbitratorUntil))
}

// IsChaosActive reports whether any chaos cut is live on this cluster.
func (cluster *Cluster) IsChaosActive() bool {
	return cluster.IsChaosDBCut() || cluster.IsChaosArbitratorCut()
}

// ChaosRemaining returns the seconds left before the cluster's cuts auto-restore.
func (cluster *Cluster) ChaosRemaining() int64 {
	d := chaosRemaining(atomic.LoadInt64(&cluster.chaosCutDBUntil))
	if a := chaosRemaining(atomic.LoadInt64(&cluster.chaosCutArbitratorUntil)); a > d {
		d = a
	}
	return d
}

// ChaosCutsDescription lists the active cuts on this cluster, for the state.
func (cluster *Cluster) ChaosCutsDescription() string {
	var cuts []string
	if cluster.IsChaosDBCut() {
		cuts = append(cuts, "db")
	}
	if cluster.IsChaosArbitratorCut() {
		cuts = append(cuts, "arbitrator")
	}
	return strings.Join(cuts, ",")
}

// AssertChaosState raises the standing chaos warning while any cut is live so
// the simulation is impossible to forget. Called per tick.
func (cluster *Cluster) AssertChaosState() {
	if cluster.IsChaosActive() {
		cluster.SetState("WARN0181", state.State{ErrType: "WARNING", ErrKey: "WARN0181", ErrDesc: fmt.Sprintf(clusterError["WARN0181"], cluster.ChaosCutsDescription(), cluster.ChaosRemaining()), ErrFrom: "TEST"})
	}
}
