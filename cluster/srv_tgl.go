// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

func (server *ServerMonitor) SwitchMaintenance() error {
	if server.ClusterGroup.GetTopology() == topoMultiMasterWsrep || server.ClusterGroup.GetTopology() == topoMultiMasterRing {
		if server.IsVirtualMaster && server.IsMaintenance == false {
			server.ClusterGroup.SwitchOver()
		}
	}
	if server.ClusterGroup.GetTopology() == topoMultiMasterRing {
		if server.IsMaintenance {
			server.ClusterGroup.CloseRing(server)
		} else {
			server.RejoinLoop()
		}
	}
	server.IsMaintenance = !server.IsMaintenance
	server.ClusterGroup.failoverProxies()

	return nil
}
