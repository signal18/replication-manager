// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"sync"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
)

type topologyError struct {
	Code int
	Msg  string
}

const (
	topoMasterSlave      string = "master-slave"
	topoUnknown          string = "unknown"
	topoBinlogServer     string = "binlog-server"
	topoMultiTierSlave   string = "multi-tier-slave"
	topoMultiMaster      string = "multi-master"
	topoMultiMasterRing  string = "multi-master-ring"
	topoMultiMasterWsrep string = "multi-master-wsrep"
)

func (cluster *Cluster) newServerList() error {
	//sva issue to monitor server should not be fatal

	var err error
	err = cluster.repmgrFlagCheck()
	if err != nil {
		cluster.LogPrintf("ERROR", "Failed to validate config: %s", err)
		return err
	}
	cluster.LogPrintf("INFO", "hostlist: %s %s", cluster.conf.Hosts, cluster.hostList)
	cluster.servers = make([]*ServerMonitor, len(cluster.hostList))
	for k, url := range cluster.hostList {

		cluster.servers[k], err = cluster.newServerMonitor(url, cluster.dbUser, cluster.dbPass, "semisync.cnf")
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not open connection to server %s : %s", cluster.servers[k].URL, err)
		}
		if cluster.conf.Verbose {
			cluster.LogPrintf("INFO", "New server monitored: %v", cluster.servers[k].URL)
		}
	}

	return nil
}

func (cluster *Cluster) pingServerList() {

	wg := new(sync.WaitGroup)
	for _, sv := range cluster.servers {
		wg.Add(1)
		go func(sv *ServerMonitor) {
			defer wg.Done()
			//	tcpAddr, err := net.ResolveTCPAddr("tcp4", sv.)
			if sv.Conn != nil {
				conn, err := sqlx.Connect("mysql", sv.DSN)
				defer conn.Close()
				if err != nil {
					if driverErr, ok := err.(*mysql.MySQLError); ok {
						if driverErr.Number == 1045 {
							sv.State = stateUnconn
							cluster.sme.AddState("ERR00004", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00004"], sv.URL, err.Error()), ErrFrom: "TOPO"})
						}
					} else {
						cluster.sme.AddState("INF00001", state.State{ErrType: "INFO", ErrDesc: fmt.Sprintf("Server %s is down", sv.URL), ErrFrom: "TOPO"})
						// We can set the failed state at this point if we're in the initial loop
						// Otherwise, let the monitor check function handle failures
						if sv.State == "" {
							if cluster.conf.LogLevel > 2 {
								cluster.LogPrintf("DEBUG", "State failed set by topology detection INF00001")
							}
							sv.State = stateFailed
						}
					}
				}
			} else {
				sv.State = stateFailed
			}
		}(sv)

	}

	wg.Wait()
}

// Start of topology detection
// Create a connection to each host and build list of slaves.
func (cluster *Cluster) TopologyDiscover() error {

	wg := new(sync.WaitGroup)
	for _, server := range cluster.servers {
		wg.Add(1)
		go server.Ping(wg)
	}
	wg.Wait()

	cluster.pingServerList()
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("DEBUG", "In Failover skip topology detection")
		return errors.New("In Failover skip topology detection")
	}
	if cluster.conf.LogLevel > 2 {
		cluster.LogPrintf("DEBUG", "Entering topology detection")
	}
	// Check topology Cluster is down
	cluster.TopologyClusterDown()
	// Spider shard discover
	if cluster.conf.Spider == true {
		cluster.SpiderShardsDiscovery()
	}
	cluster.slaves = nil
	for k, sv := range cluster.servers {
		err := sv.Refresh()
		if err != nil {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Server %s could not be refreshed: %s", sv.URL, err)
			}
			continue
		}
		if sv.IsSlave {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Server %s is configured as a slave", sv.URL)
			}
			sv.replicationCheck()
			cluster.slaves = append(cluster.slaves, sv)
		} else {
			var n int
			err := sv.Conn.Get(&n, "SELECT COUNT(*) AS n FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command LIKE 'binlog dump%'")
			if err != nil {
				cluster.sme.AddState("ERR00014", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00014"], sv.URL, err), ErrFrom: "CONF"})
				if cluster.conf.LogLevel > 2 {
					cluster.LogPrint("DEBUG: State failed set by topology detection ERR00014")
				}
				sv.State = stateFailed
				continue
			}
			if n == 0 {
				sv.State = stateUnconn
				// TODO: fix flapping in case slaves are reconnecting
				if cluster.conf.LogLevel > 2 {
					cluster.LogPrintf("DEBUG", "Server %s has no slaves connected", sv.URL)
				}
			} else {
				if cluster.conf.LogLevel > 2 {
					cluster.LogPrintf("DEBUG", "Server %s was set master as last non slave", sv.URL)
				}
				cluster.master = cluster.servers[k]
				cluster.master.State = stateMaster
				cluster.master.SetReadWrite()
			}
		}
		// Check replication manager user privileges on live servers
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG", "Privilege check on %s", sv.URL)
		}
		if sv.State != "" && !sv.IsDown() && sv.IsRelay == false {
			myhost, err := dbhelper.GetHostFromConnection(sv.Conn, cluster.dbUser)
			if err != nil {
				cluster.LogPrintf("ERROR", "Cant get host for connection user on %s: %s", sv.URL, err)
			}
			myip, err := misc.GetIPSafe(myhost)
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Client connection found on server %s with IP %s for host %s", sv.URL, myip, myhost)
			}
			if err != nil {
				cluster.sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], cluster.dbUser, sv.URL, err), ErrFrom: "CONF"})
			} else {
				priv, err := dbhelper.GetPrivileges(sv.Conn, cluster.dbUser, cluster.repmgrHostname, myip)
				if err != nil {
					cluster.sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], cluster.dbUser, cluster.repmgrHostname, err), ErrFrom: "CONF"})
				}
				if priv.Repl_client_priv == "N" {
					cluster.sme.AddState("ERR00006", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00006"], ErrFrom: "CONF"})
				}
				if priv.Super_priv == "N" {
					cluster.sme.AddState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00008"], ErrFrom: "CONF"})
				}
				if priv.Reload_priv == "N" {
					cluster.sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00009"], ErrFrom: "CONF"})
				}
			}
			// Check replication user has correct privs.
			for _, sv2 := range cluster.servers {
				if sv2.URL != sv.URL && sv2.IsRelay == false && !sv2.IsDown() {
					rplhost, _ := misc.GetIPSafe(sv2.Host)
					rpriv, err := dbhelper.GetPrivileges(sv2.Conn, cluster.rplUser, sv2.Host, rplhost)
					if err != nil {
						cluster.sme.AddState("ERR00015", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00015"], cluster.rplUser, sv2.URL, err), ErrFrom: "CONF"})
					}
					if rpriv.Repl_slave_priv == "N" {
						cluster.sme.AddState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00007"], ErrFrom: "CONF"})
					}
					// Additional health checks go here
					if sv.acidTest() == false && cluster.sme.IsDiscovered() {
						cluster.sme.AddState("WARN0007", state.State{ErrType: "WARN", ErrDesc: "At least one server is not ACID-compliant. Please make sure that sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF"})
					}
				}
			}
		}
	}

	// If no cluster.slaves are detected, generate an error
	if len(cluster.slaves) == 0 {
		cluster.sme.AddState("ERR00010", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00010"]), ErrFrom: "TOPO"})
	}

	// Check that all slave servers have the same master and conformity.
	if cluster.conf.MultiMaster == false && cluster.conf.Spider == false {
		for _, sl := range cluster.slaves {

			if sl.IsMaxscale == false && !sl.IsDown() {
				sl.SlaveCheck()
				if sl.HasCycling() {
					if cluster.conf.MultiMaster == false && len(cluster.servers) == 2 {
						cluster.sme.AddState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00011"]), ErrFrom: "TOPO"})
						cluster.conf.MultiMaster = true
					}
					if cluster.conf.MultiMasterRing == false && len(cluster.servers) > 2 {
						cluster.conf.MultiMasterRing = true
					}
					if cluster.conf.MultiMasterRing == true && cluster.GetMaster() == nil {
						cluster.vmaster = sl
					}

					//broken replication ring
				} else if cluster.conf.MultiMasterRing == true {
					//setting a virtual master if none

					cluster.sme.AddState("ERR00048", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00048"]), ErrFrom: "TOPO"})
					cluster.master = cluster.GetFailedServer()
				}

			}
			if cluster.conf.MultiMaster == false && sl.IsMaxscale == false {
				if sl.IsSlave == true && sl.HasSlaves(cluster.slaves) == true {
					sl.IsRelay = true
					sl.State = stateRelay
				} else if sl.IsRelay {
					sl.IsRelay = false
				}
			}
		}
	}
	if cluster.conf.MultiMaster == true {
		srw := 0
		for _, s := range cluster.servers {
			if s.ReadOnly == "OFF" {
				srw++
			}
		}
		if srw > 1 {
			cluster.sme.AddState("WARN0003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range cluster.servers {
			if s.ReadOnly == "ON" {
				srw++
			}
		}
		if srw > 1 {
			cluster.sme.AddState("WARN0004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to preferred master.", ErrFrom: "TOPO"})
			server := cluster.getPreferedMaster()
			if server != nil {
				dbhelper.SetReadOnly(server.Conn, false)
			} else {
				cluster.sme.AddState("WARN0006", state.State{ErrType: "WARNING", ErrDesc: "Multi-master need a preferred master.", ErrFrom: "TOPO"})
			}
		}
	}

	if cluster.slaves != nil {
		if len(cluster.slaves) > 0 {
			// Depending if we are doing a failover or a switchover, we will find the master in the list of
			// failed hosts or unconnected hosts.
			// First of all, get a server id from the cluster.slaves slice, they should be all the same
			sid := cluster.slaves[0].GetReplicationServerID()
			for k, s := range cluster.servers {
				if cluster.conf.MultiMaster == false && s.State == stateUnconn {
					if s.ServerID == sid {
						cluster.master = cluster.servers[k]
						cluster.master.State = stateMaster
						cluster.master.SetReadWrite()
						if cluster.conf.LogLevel > 2 {
							cluster.LogPrintf("DEBUG", "Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
				if cluster.conf.MultiMaster == true && !cluster.servers[k].IsDown() {
					if s.ReadOnly == "OFF" {
						cluster.master = cluster.servers[k]
						cluster.master.State = stateMaster
						if cluster.conf.LogLevel > 2 {
							cluster.LogPrintf("DEBUG", "Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
			}

			// If master is not initialized, find it in the failed hosts list
			if cluster.master == nil {
				// Slave master_host variable must point to failed master

				smh := cluster.slaves[0].GetReplicationMasterHost()
				for k, s := range cluster.servers {
					if s.State == stateFailed {
						if (s.Host == smh || s.IP == smh) && s.Port == cluster.slaves[0].GetReplicationMasterPort() {
							if cluster.conf.FailRestartUnsafe {
								cluster.master = cluster.servers[k]
								cluster.master.PrevState = stateMaster
								cluster.LogPrintf("INFO", "Assuming failed server %s was a master", s.URL)
							}
							break
						}
					}
				}
			}
		}
	}
	// Final check if master has been found
	if cluster.master == nil {
		// could not detect master
		if cluster.GetMaster() == nil {
			cluster.sme.AddState("ERR00012", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00012"]), ErrFrom: "TOPO"})
		}
	} else {
		cluster.master.RplMasterStatus = false
		// End of autodetection code
		if !cluster.master.IsDown() {
			cluster.master.MasterCheck()

		}
		// Replication checks
		if cluster.conf.MultiMaster == false {
			for _, sl := range cluster.slaves {

				if sl.IsRelay == false {
					if cluster.conf.LogLevel > 2 {
						cluster.LogPrintf("DEBUG", "Checking if server %s is a slave of server %s", sl.Host, cluster.master.Host)
					}
					replMaster, _ := cluster.GetMasterFromReplication(sl)

					if replMaster != nil && replMaster.Id != cluster.master.Id {
						cluster.sme.AddState("WARN00005", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Server %s is not a slave of declared master %s, and replication no relay is enable: Pointing to %s", sl.URL, cluster.master.URL, replMaster.URL), ErrFrom: "TOPO"})

						if cluster.conf.ReplicationNoRelay {
							cluster.RejoinFixRelay(sl, cluster.master)
						}

					}
					if sl.LogBin == "OFF" {
						cluster.sme.AddState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00013"], sl.URL), ErrFrom: "TOPO"})
					}
				}
				if sl.GetReplicationDelay() <= cluster.conf.FailMaxDelay && sl.IsSQLThreadRunning() {
					cluster.master.RplMasterStatus = true
				}

			}
		}
		// State also check in failover_check false positive
		if cluster.master.IsDown() && cluster.slaves.checkAllSlavesRunning() {
			cluster.sme.AddState("ERR00016", state.State{
				ErrType: "ERROR",
				ErrDesc: clusterError["ERR00016"],
				ErrFrom: "NET",
			})
		}

		cluster.sme.SetMasterUpAndSync(cluster.master.SemiSyncMasterStatus, cluster.master.RplMasterStatus)
	}

	// Fecth service Status
	/*	if cluster.conf.Enterprise {
		status, err := cluster.GetOpenSVCSeviceStatus()
		cluster.openSVCServiceStatus = status
		if err != nil {
			cluster.sme.AddState("ERR00044", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00044"], cluster.conf.ProvHost), ErrFrom: "TOPO"})
		}
	}*/
	if cluster.IsProvision() {
		if len(cluster.crashes) > 0 {
			cluster.LogPrintf("DEBUG", "Purging crashes, all databses nodes up")
			cluster.crashes = nil
			cluster.Save()

		}
	}
	if cluster.sme.CanMonitor() {
		return nil
	}
	return errors.New("Error found in State Machine Engine")
}

// TopologyClusterDown track state all ckuster down
func (cluster *Cluster) TopologyClusterDown() bool {
	// search for all cluster down
	if cluster.GetMaster() == nil || cluster.GetMaster().State == stateFailed {
		//	if cluster.conf.Interactive == false {
		allslavefailed := true
		for _, s := range cluster.slaves {
			if s.State != stateFailed && misc.Contains(cluster.ignoreList, s.URL) == false {
				allslavefailed = false
			}
		}
		if allslavefailed {
			if cluster.master != nil && cluster.conf.Interactive == false && cluster.conf.FailRestartUnsafe == false {
				// forget the master if safe mode
				cluster.LogPrintf("INFO", "Backing up last seen master: %s for safe failover restart", cluster.master.URL)
				cluster.lastmaster = cluster.master
				cluster.master = nil

			}
			cluster.sme.AddState("ERR00021", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00021"]), ErrFrom: "TOPO"})
			return true
		}
		//}
	}
	return false
}

func (cluster *Cluster) PrintTopology() {
	for k, v := range cluster.servers {
		cluster.LogPrintf("INFO", "Server [%d] %s %s %s", k, v.URL, v.State, v.PrevState)
	}
}

// CountFailed Count number of failed node
func (cluster *Cluster) CountFailed(s []*ServerMonitor) int {
	failed := 0
	for _, server := range cluster.servers {
		if server.State == stateFailed {
			failed = failed + 1
		}
	}
	return failed
}

// LostMajority should be call in case of splitbrain to set maintenance mode
func (cluster *Cluster) LostMajority() bool {
	failed := cluster.CountFailed(cluster.servers)
	alive := len(cluster.servers) - failed
	if alive > len(cluster.servers)/2 {
		return false
	} else {
		return true
	}

}
