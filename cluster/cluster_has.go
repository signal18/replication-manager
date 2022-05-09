// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) HasServer(srv *ServerMonitor) bool {
	for _, sv := range cluster.Servers {
		//	cluster.LogPrintf(LvlInfo, "HasServer:%s %s, %s %s", sv.Id, srv.Id, sv.URL, srv.URL)
		// id can not be used for checking equality because  same srv in different clusters
		// cluster.LogPrintf(LvlInfo, "HasServer check  %s  vs  %s  ", sv.URL, srv.URL)
		// When server has no port URL has no port then discovery use port
		if sv.URL == srv.URL || sv.URL == srv.URL+":3306" {
			return true
		}
	}
	return false
}

func (cluster *Cluster) HasValidBackup() bool {
	//	if cluster.Conf.MonitorScheduler && (cluster.Conf.SchedulerBackupLogical || cluster.Conf.SchedulerBackupPhysical) {
	sv := cluster.GetBackupServer()
	if sv != nil {
		if sv.HasBackupLogicalCookie() || sv.HasBackupPhysicalCookie() {
			return true
		}
	}
	//	}
	cluster.SetState("WARN0101", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0101"]), ErrFrom: "TOPO"})
	return false

}

func (cluster *Cluster) HasSchedulerEntry(myname string) bool {
	if _, ok := cluster.Schedule[myname]; ok {
		return true
	}

	return false
}

func (cluster *Cluster) HasNoValidSlave() bool {
	//All slave stopped
	if cluster.sme.IsInState("ERR00010") {
		return true
	}
	// Any issues on all slaves expeting delay and network
	if cluster.sme.IsInState("ERR00085") {
		return true
	}
	return false
}

func (cluster *Cluster) IsProvisioned() bool {
	if cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return true
	}
	if cluster.Conf.Hosts == "" {
		return false
	}
	for _, db := range cluster.Servers {
		if !db.HasProvisionCookie() {
			if db.IsRunning() {
				db.SetProvisionCookie()
				cluster.LogPrintf(LvlInfo, "Can DB Connect creating cookie state:%s", db.State)
			} else {
				return false
			}
		}
	}
	for _, px := range cluster.Proxies {
		if !px.HasProvisionCookie() {
			if px.IsRunning() {
				px.SetProvisionCookie()
				cluster.LogPrintf(LvlInfo, "Can Proxy Connect creating cookie state:%s", px.GetState())
			} else {
				return false
			}
		}
	}
	return true
}

func (cluster *Cluster) IsInIgnoredHosts(server *ServerMonitor) bool {
	ihosts := strings.Split(cluster.Conf.IgnoreSrv, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInPreferedBackupHosts(server *ServerMonitor) bool {
	ihosts := strings.Split(cluster.Conf.BackupServers, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInIgnoredReadonly(server *ServerMonitor) bool {
	ihosts := strings.Split(cluster.Conf.IgnoreSrvRO, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInPreferedHosts(server *ServerMonitor) bool {
	ihosts := strings.Split(cluster.Conf.PrefMaster, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInCaptureMode() bool {
	if !cluster.Conf.MonitorCapture || cluster.IsNotMonitoring || len(cluster.Servers) > 0 {
		return false
	}
	for _, server := range cluster.Servers {
		if server.InCaptureMode {
			return true
		}
	}
	return false
}

func (cluster *Cluster) HasAllDbUp() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.State == stateFailed /*&& misc.Contains(cluster.ignoreList, s.URL) == false*/ {
				return false
			}
			if s.State == stateSuspect && cluster.GetTopology() != topoUnknown {
				//supect is used to reload config and avoid backend state change to failed that would disable servers in proxies and cause glinch in cluster traffic
				// at the same time to enbale bootstrap replication we need to know when server are up
				return false
			}
			if s.Conn == nil {
				return false
			}
		}
	}

	return true
}

func (cluster *Cluster) HasRequestDBRestart() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.HasRestartCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestDBRollingRestart() bool {
	ret := true
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if !s.HasRestartCookie() {
				return false
			}
		}
	}
	return ret
}

func (cluster *Cluster) HasRequestDBRollingReprov() bool {
	ret := true
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if !s.HasReprovCookie() {
				return false
			}
		}
	}

	return ret
}

func (cluster *Cluster) HasRequestDBReprov() bool {
	for _, s := range cluster.Servers {
		if s != nil {
			if s.HasReprovCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestProxiesRestart() bool {
	for _, p := range cluster.Proxies {
		if p != nil {
			if p.HasRestartCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestProxiesReprov() bool {
	for _, p := range cluster.Proxies {
		if p != nil {
			if p.HasReprovCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) IsInHostList(host string) bool {
	for _, v := range cluster.hostList {
		if v == host {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsMasterFailed() bool {
	// get real master or the virtual master
	mymaster := cluster.GetMaster()
	if mymaster == nil {
		return true
	}
	if mymaster.State == stateFailed {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsActive() bool {
	if cluster.Status == ConstMonitorActif {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsVerbose() bool {
	if cluster.Conf.Verbose {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsInFailover() bool {
	return cluster.sme.IsInFailover()
}

func (cluster *Cluster) IsDiscovered() bool {
	return cluster.sme.IsDiscovered()
}
