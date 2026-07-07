// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
)

// Chaos isolation simulates, for ONE cluster on THIS instance only, the
// scenario arbitration exists to protect against: the monitor loses the data
// plane (every database connection fails) and peer visibility (forced split
// brain) while the arbitrator stays reachable. The isolated side must lose
// the election, go standby and refuse to fail over; the peer must keep the
// real master untouched.
//
// This is deliberately RUNTIME state, never a config setting: a config key
// would replicate to the peer through the config event log and isolate both
// sides, sabotaging the very test.

const (
	chaosIsolationDefault = 120 * time.Second
	chaosIsolationMax     = 15 * time.Minute
)

// ChaosIsolateStart arms the simulated isolation for the given duration
// (bounded; zero selects the default). Always auto-restores: a forgotten
// test cannot strand the cluster passive.
func (cluster *Cluster) ChaosIsolateStart(duration time.Duration) time.Duration {
	if duration <= 0 {
		duration = chaosIsolationDefault
	}
	if duration > chaosIsolationMax {
		duration = chaosIsolationMax
	}
	atomic.StoreInt64(&cluster.chaosIsolatedUntil, time.Now().Add(duration).Unix())
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "CHAOS: simulated isolation armed for %s — database connections and peer visibility cut on this instance", duration)
	return duration
}

// ChaosIsolateStop disarms the simulation immediately.
func (cluster *Cluster) ChaosIsolateStop() {
	if cluster.IsChaosIsolated() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "CHAOS: simulated isolation disarmed")
	}
	atomic.StoreInt64(&cluster.chaosIsolatedUntil, 0)
}

// IsChaosIsolated reports whether the simulation is armed and not expired.
func (cluster *Cluster) IsChaosIsolated() bool {
	until := atomic.LoadInt64(&cluster.chaosIsolatedUntil)
	return until > 0 && time.Now().Unix() < until
}

// ChaosIsolatedRemaining returns the seconds left before auto-restore.
func (cluster *Cluster) ChaosIsolatedRemaining() int64 {
	until := atomic.LoadInt64(&cluster.chaosIsolatedUntil)
	if until == 0 {
		return 0
	}
	left := until - time.Now().Unix()
	if left < 0 {
		return 0
	}
	return left
}
