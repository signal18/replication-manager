// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// On Galera the cluster fails over by itself: as long as a majority is up, its nodes keep a
// Primary component and every one of them takes writes. replication-manager's virtual master is
// only the node it routes the writes to, so moving it is a switchover, never a promotion: no
// data is at risk, there is nothing for an operator to decide. It is moved automatically in any
// failover mode as soon as the Primary component has a synced node, and never without one
// (#2350). The master-slave gates (manual mode, failover-limit, failover-time-limit) do not
// apply: a Galera cluster losing a node every day would otherwise stop routing after five.

// isGaleraTopology: the cluster runs as Galera (the discovered topology).
func (cluster *Cluster) isGaleraTopology() bool {
	return cluster.GetTopology() == config.TopoMultiMasterWsrep
}

// galeraElectable lists the nodes the virtual master can move to: synced members of the
// Primary component, reachable, not failed, not ignored, not in maintenance, of this cluster.
// A node of the Primary component is part of a majority: an empty list means no majority.
func (cluster *Cluster) galeraElectable() []*ServerMonitor {
	var l []*ServerMonitor
	for _, s := range cluster.Servers {
		if s == nil || s.IsIgnored() || s.IsDown() || s.IsFailed() || s.IsMaintenance || s.SourceClusterName != cluster.Name {
			continue
		}
		if s.HasWsrepPrimary() && s.HasWsrepSync() {
			l = append(l, s)
		}
	}
	return l
}

// galeraHasMajority: the Primary component has a node the virtual master can move to.
func (cluster *Cluster) galeraHasMajority() bool {
	return len(cluster.galeraElectable()) > 0
}

// galeraElectVirtualMaster elects a virtual master when there is none: after a restart of
// replication-manager whose last master is the failed node, or a cluster never routed. The
// preferred master when it is electable, else the first electable node in declared order. With
// no majority, nothing is elected. It returns the node elected, nil when none.
func (cluster *Cluster) galeraElectVirtualMaster() *ServerMonitor {
	if !cluster.isGaleraTopology() || cluster.GetMaster() != nil {
		return nil
	}
	electable := cluster.galeraElectable()
	if len(electable) == 0 {
		return nil
	}
	elected := electable[0]
	if p := cluster.getOnePreferedMaster(); p != nil {
		for _, s := range electable {
			if s.URL == p.URL {
				elected = s
				break
			}
		}
	}
	cluster.vmaster = elected
	cluster.master = elected
	elected.SetMaster()
	cluster.SetState("WARN0006", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Wsrep cluster need a leader electing one: %s, of %d synced node(s) in the Primary component", elected.URL, len(electable)), ErrFrom: "TOPO"})
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo,
		"Galera: no virtual master, %s elected among %d synced node(s) of the Primary component (#2350)", elected.URL, len(electable))
	return elected
}
