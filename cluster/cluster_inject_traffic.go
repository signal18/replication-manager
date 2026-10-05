// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

// The traffic marker (InjectProxiesTraffic, prx.go) used to run inside the tick's wait
// group: a marker blocked on the primary froze the whole cluster tick for as long as the
// block lasted. Live on belair (2026-10-04/05): a logical backup's single-transaction
// snapshot holds a shared metadata lock on replication_manager_schema.pseudo_gtid_v, the
// DDL marker waits for it, and the tick -- refresh, sensors, failover decisions -- stalled
// for the whole dump (GWARN001 then GERR001 "stalled cluster heartbeat"). A backup must
// never compromise monitoring.
//
// Stéphane's design: run the marker in the BACKGROUND and make re-entry impossible while
// one is still running. The tick starts it and moves on; while a previous injection is
// still in flight nothing new is started and a state says since when it is stuck.

// startInjectProxiesTraffic launches InjectProxiesTraffic in a tracked background
// goroutine unless one is still running, in which case it only opens WARN0229.
func (cluster *Cluster) startInjectProxiesTraffic() {
	// Stéphane 2026-10-05: say it every tick while the marker is the DDL view -- the
	// pseudo-GTID marker exists for tests and positional rejoin, and each one is a binlog
	// event flashback cannot reverse. inject-traffic-mode = dml is the safe traffic.
	if cluster.injectTrafficUsesDDL() {
		forced := ""
		if cluster.Conf.ForceSlaveNoGtid {
			forced = " (forced by force-slave-no-gtid-mode, positional rejoin greps the marker in the binlog)"
		}
		cluster.SetState("WARN0230", state.State{ErrType: "WARNING",
			ErrDesc: fmt.Sprintf(clusterError["WARN0230"], forced), ErrFrom: "TOPO"})
	}
	if !cluster.tryStartInjectTraffic(time.Now()) {
		since := time.Unix(cluster.injectTrafficSince.Load(), 0)
		cluster.SetState("WARN0229", state.State{ErrType: "WARNING",
			ErrDesc: fmt.Sprintf(clusterError["WARN0229"], time.Since(since).Round(time.Second), since.Format("15:04:05")),
			ErrFrom: "TOPO"})
		return
	}
	cluster.trackTickGoroutine(func() {
		defer cluster.endInjectTraffic()
		cluster.InjectProxiesTraffic()
	})
}

// tryStartInjectTraffic takes the single injection slot; false when one is in flight.
func (cluster *Cluster) tryStartInjectTraffic(now time.Time) bool {
	if !cluster.injectTrafficInFlight.CompareAndSwap(false, true) {
		return false
	}
	cluster.injectTrafficSince.Store(now.Unix())
	return true
}

func (cluster *Cluster) endInjectTraffic() {
	cluster.injectTrafficInFlight.Store(false)
}
