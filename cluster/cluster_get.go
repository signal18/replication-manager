// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/state"
)

func (cluster *Cluster) GetMaster() *ServerMonitor {
	if cluster.master == nil {
		return cluster.vmaster
	} else {
		return cluster.master
	}
}

func (cluster *Cluster) GetTraffic() bool {
	return cluster.conf.TestInjectTraffic
}

func (cluster *Cluster) GetClusterName() string {
	return cluster.cfgGroup
}

func (cluster *Cluster) GetServers() serverList {
	return cluster.servers
}

func (cluster *Cluster) GetSlaves() serverList {
	return cluster.slaves
}

func (cluster *Cluster) GetProxies() proxyList {
	return cluster.proxies
}

func (cluster *Cluster) GetConf() config.Config {
	return cluster.conf
}

func (cluster *Cluster) GetWaitTrx() int64 {
	return cluster.conf.SwitchWaitTrx
}

func (cluster *Cluster) GetStateMachine() *state.StateMachine {
	return cluster.sme
}

func (cluster *Cluster) GetMasterFailCount() int {
	return cluster.master.FailCount
}

func (cluster *Cluster) GetFailoverCtr() int {
	return cluster.failoverCtr
}

func (cluster *Cluster) GetFailoverTs() int64 {
	return cluster.failoverTs
}

func (cluster *Cluster) GetRunStatus() string {
	return cluster.runStatus
}
func (cluster *Cluster) GetFailSync() bool {
	return cluster.conf.FailSync
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.conf.RplChecks
}

func (cluster *Cluster) GetMaxFail() int {
	return cluster.conf.MaxFail
}

func (cluster *Cluster) GetLogLevel() int {
	return cluster.conf.LogLevel
}
func (cluster *Cluster) GetSwitchSync() bool {
	return cluster.conf.SwitchSync
}

func (cluster *Cluster) GetRejoin() bool {
	return cluster.conf.Autorejoin
}

func (cluster *Cluster) GetRejoinDump() bool {
	return cluster.conf.AutorejoinMysqldump
}

func (cluster *Cluster) GetRejoinBackupBinlog() bool {
	return cluster.conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) GetRejoinSemisync() bool {
	return cluster.conf.AutorejoinSemisync
}

func (cluster *Cluster) GetRejoinFlashback() bool {
	return cluster.conf.AutorejoinFlashback
}

func (cluster *Cluster) GetName() string {
	return cluster.cfgGroup
}

func (cluster *Cluster) GetTestMode() bool {
	return cluster.conf.Test
}

func (cluster *Cluster) GetDbUser() string {
	return cluster.dbUser
}

func (cluster *Cluster) GetDbPass() string {
	return cluster.dbPass
}

func (cluster *Cluster) GetStatus() bool {
	return cluster.sme.IsFailable()
}

func (cluster *Cluster) GetGComm() string {
	var gcomms []string
	for _, server := range cluster.servers {
		gcomms = append(gcomms, server.Host+":4567")
	}
	return strings.Join(gcomms, ",")
}

func (cluster *Cluster) getPreferedMaster() *ServerMonitor {
	if cluster.conf.PrefMaster == "" {
		return nil
	}
	for _, server := range cluster.servers {
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG", "Lookup server %s if preferred master: %s", server.URL, cluster.conf.PrefMaster)
		}
		if server.URL == cluster.conf.PrefMaster {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetRelayServer() *ServerMonitor {
	for _, server := range cluster.servers {
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG", "Lookup server %s if maxscale binlog server: %s", server.URL, cluster.conf.PrefMaster)
		}
		if server.IsRelay {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetIndiceServerFromId(Id string) int {
	i := 0
	for _, server := range cluster.servers {

		if server.Id == Id {
			return i
		}
		i = i + 1
	}
	return 0
}

func (cluster *Cluster) GetServerFromId(serverid uint) *ServerMonitor {
	for _, server := range cluster.servers {
		if server.ServerID == serverid {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetServerFromName(name string) *ServerMonitor {
	for _, server := range cluster.servers {
		if server.Id == name {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetServerFromURL(url string) *ServerMonitor {
	for _, server := range cluster.servers {
		if server.Host+":"+server.Port == url {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetMasterFromReplication(s *ServerMonitor) (*ServerMonitor, error) {

	for _, server := range cluster.servers {

		if len(s.Replications) > 0 {

			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Rejoin replication master id %d was lookup if master %s is that one : %d", s.GetReplicationServerID(), server.DSN, server.ServerID)
			}
			if s.IsIOThreadRunning() && s.IsSQLThreadRunning() {
				if s.GetReplicationServerID() == server.ServerID {
					return server, nil
				}
			} else {
				if s.GetReplicationMasterHost() == server.Host && s.GetReplicationMasterPort() == server.Port {
					return server, nil
				}
			}
		}

	}
	// Possible that we can't found the master because the replication host and configurartion host missmatch:  hostname vs IP
	// Lookup for reverse DNS IP match
	if cluster.master != nil {
		is, err := dbhelper.IsSlaveof(s.Conn, s.Host, cluster.master.IP, cluster.master.Port)
		if err != nil {
			return nil, nil
		}
		if is {
			return cluster.master, nil
		}
	}
	return nil, nil
}

func (cluster *Cluster) GetFailedServer() *ServerMonitor {
	for _, server := range cluster.servers {
		if server.State == stateFailed {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetTopology() string {
	cluster.conf.Topology = topoUnknown
	if cluster.conf.MultiMaster {
		cluster.conf.Topology = topoMultiMaster
	} else if cluster.conf.MultiMasterRing {
		cluster.conf.Topology = topoMultiMasterRing
	} else if cluster.conf.MultiMasterWsrep {
		cluster.conf.Topology = topoMultiMasterWsrep
	} else if cluster.conf.MxsBinlogOn {
		cluster.conf.Topology = topoBinlogServer
	} else if cluster.conf.MultiTierSlave {
		cluster.conf.Topology = topoMultiTierSlave
	} else {
		relay := cluster.GetRelayServer()
		if relay != nil && cluster.conf.ReplicationNoRelay == false {
			cluster.conf.Topology = topoMultiTierSlave
		} else if cluster.master != nil {
			cluster.conf.Topology = topoMasterSlave
		}
	}
	return cluster.conf.Topology
}
