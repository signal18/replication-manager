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

	"github.com/signal18/replication-manager/utils/state"
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
	cluster.SetClusterVariablesFromConfig()
	err = cluster.isValidConfig()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed to validate config: %s", err)
		return err
	}
	cluster.Lock()
	cluster.Servers = make([]*ServerMonitor, len(cluster.hostList))

	for k, url := range cluster.hostList {
		cluster.Servers[k], err = cluster.newServerMonitor(url, cluster.dbUser, cluster.dbPass, "semisync.cnf")
		if err != nil {
			cluster.LogPrintf(LvlErr, "Could not open connection to server %s : %s", cluster.Servers[k].URL, err)
		}

		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "New database monitored: %v", cluster.Servers[k].URL)
		}

	}
	cluster.Unlock()
	return nil
}

// DEAD CODE NO MORE CALLED
func (cluster *Cluster) pingServerList() {
	wg := new(sync.WaitGroup)
	for _, sv := range cluster.Servers {
		wg.Add(1)
		go func(sv *ServerMonitor) {
			defer wg.Done()
			//	tcpAddr, err := net.ResolveTCPAddr("tcp4", sv.)
			if sv.Conn != nil {
				conn, err := sv.GetNewDBConn()
				defer conn.Close()
				if err != nil {
					if driverErr, ok := err.(*mysql.MySQLError); ok {
						// access denied
						if driverErr.Number == 1045 {
							sv.State = stateErrorAuth
							cluster.SetState("ERR00004", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00004"], sv.URL, err.Error()), ErrFrom: "TOPO"})
						}
					} else {
						cluster.SetState("INF00001", state.State{ErrType: "INFO", ErrDesc: fmt.Sprintf("Server %s is down", sv.URL), ErrFrom: "TOPO"})
						// We can set the failed state at this point if we're in the initial loop
						// Otherwise, let the monitor check function handle failures
						if sv.State == "" {
							cluster.LogPrintf(LvlDbg, "State failed set by topology detection INF00001")
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
	//monitor ignored server fist so that their replication position get oldest
	wg := new(sync.WaitGroup)
	for _, server := range cluster.Servers {
		if server.IsIgnored() {
			wg.Add(1)
			go server.Ping(wg)
		}
	}
	wg.Wait()

	wg = new(sync.WaitGroup)
	for _, server := range cluster.Servers {
		if !server.IsIgnored() {
			wg.Add(1)
			go server.Ping(wg)
		}
	}
	wg.Wait()

	//	cluster.pingServerList()
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf(LvlDbg, "In Failover skip topology detection")
		return errors.New("In Failover skip topology detection")
	}
	// Check topology Cluster is down
	cluster.TopologyClusterDown()
	// Spider shard discover
	if cluster.Conf.Spider == true {
		cluster.SpiderShardsDiscovery()
	}
	cluster.slaves = nil
	for k, sv := range cluster.Servers {
		// Failed Do not ignore suspect or topology will change to fast
		if sv.IsFailed() {
			continue
		}
		if sv.IsSlave {
			if cluster.Conf.LogLevel > 2 {
				cluster.LogPrintf(LvlDbg, "Server %s is configured as a slave", sv.URL)
			}
			cluster.slaves = append(cluster.slaves, sv)
		} else {
			if sv.BinlogDumpThreads == 0 && sv.State != stateMaster {
				sv.State = stateUnconn
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "Server %s has no slaves connected and was set as standalone", sv.URL)
				}
			} else {
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "Server %s was set master as last non slave", sv.URL)
				}
				if cluster.Status == ConstMonitorActif && cluster.master != nil && cluster.GetTopology() == topoMasterSlave && cluster.Servers[k].URL != cluster.master.URL {
					cluster.SetState("ERR00063", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00063"]), ErrFrom: "TOPO"})
					cluster.Servers[k].RejoinMaster()
				} else {
					cluster.master = cluster.Servers[k]
					cluster.master.State = stateMaster
					cluster.master.SetReadWrite()
				}
			}
		}
		sv.CheckPrivileges()
	}

	// If no cluster.slaves are detected, generate an error
	if len(cluster.slaves) == 0 {
		cluster.SetState("ERR00010", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00010"]), ErrFrom: "TOPO"})
	}

	// Check that all slave servers have the same master and conformity.
	if cluster.Conf.MultiMaster == false && cluster.Conf.Spider == false {
		for _, sl := range cluster.slaves {
			if sl.IsMaxscale == false && !sl.IsFailed() {
				sl.CheckSlaveSettings()
				sl.CheckSlaveSameMasterGrants()
				if sl.HasCycling() {
					if cluster.Conf.MultiMaster == false && len(cluster.Servers) == 2 {
						cluster.SetState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00011"]), ErrFrom: "TOPO", ServerUrl: sl.URL})
						cluster.Conf.MultiMaster = true
					}
					if cluster.Conf.MultiMasterRing == false && len(cluster.Servers) > 2 {
						cluster.Conf.MultiMasterRing = true
					}
					if cluster.Conf.MultiMasterRing == true && cluster.GetMaster() == nil {
						cluster.vmaster = sl
					}

					//broken replication ring
				} else if cluster.Conf.MultiMasterRing == true {
					//setting a virtual master if none
					cluster.SetState("ERR00048", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00048"]), ErrFrom: "TOPO"})
					cluster.master = cluster.GetFailedServer()
				}

			}
			if cluster.Conf.MultiMaster == false && sl.IsMaxscale == false {
				if sl.IsSlave == true && sl.HasSlaves(cluster.slaves) == true {
					sl.IsRelay = true
					sl.State = stateRelay
				} else if sl.IsRelay {
					sl.IsRelay = false
				}
			}
		}
	}
	if cluster.Conf.MultiMaster == true {
		srw := 0
		for _, s := range cluster.Servers {
			if s.IsReadWrite() {
				srw++
			}
		}
		if srw > 1 {
			cluster.SetState("WARN0003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range cluster.Servers {
			if s.IsReadOnly() {
				srw++
			}
		}
		if srw > 1 {
			cluster.SetState("WARN0004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to preferred master.", ErrFrom: "TOPO"})
			server := cluster.getPreferedMaster()
			if server != nil {
				server.SetReadWrite()
			} else {
				cluster.SetState("WARN0006", state.State{ErrType: "WARNING", ErrDesc: "Multi-master need a preferred master.", ErrFrom: "TOPO"})
			}
		}
	}

	if cluster.slaves != nil {
		if len(cluster.slaves) > 0 {
			// Depending if we are doing a failover or a switchover, we will find the master in the list of
			// failed hosts or unconnected hosts.
			// First of all, get a server id from the cluster.slaves slice, they should be all the same
			sid := cluster.slaves[0].GetReplicationServerID()
			for k, s := range cluster.Servers {
				if cluster.Conf.MultiMaster == false && s.State == stateUnconn {
					if s.ServerID == sid {
						cluster.master = cluster.Servers[k]
						cluster.master.State = stateMaster
						cluster.master.SetReadWrite()
						if cluster.Conf.LogLevel > 2 {
							cluster.LogPrintf(LvlDbg, "Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
				if cluster.Conf.MultiMaster == true && !cluster.Servers[k].IsDown() {
					if s.IsReadWrite() {
						cluster.master = cluster.Servers[k]
						cluster.master.State = stateMaster
						if cluster.Conf.LogLevel > 2 {
							cluster.LogPrintf(LvlDbg, "Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
			}

			// If master is not initialized, find it in the failed hosts list
			if cluster.master == nil {
				cluster.FailedMasterDiscovery()
			}
		}
	}
	// Final check if master has been found
	if cluster.master == nil {
		// could not detect master
		if cluster.GetMaster() == nil {
			cluster.SetState("ERR00012", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00012"]), ErrFrom: "TOPO"})
		}
	} else {
		cluster.master.RplMasterStatus = false
		// End of autodetection code
		if !cluster.master.IsDown() {
			cluster.master.CheckMasterSettings()
		}
		// Replication checks
		if cluster.Conf.MultiMaster == false {
			for _, sl := range cluster.slaves {

				if sl.IsRelay == false {
					if cluster.Conf.LogLevel > 2 {
						cluster.LogPrintf(LvlDbg, "Checking if server %s is a slave of server %s", sl.Host, cluster.master.Host)
					}
					replMaster, _ := cluster.GetMasterFromReplication(sl)

					if replMaster != nil && replMaster.Id != cluster.master.Id {
						cluster.SetState("ERR00064", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00064"], sl.URL, cluster.master.URL, replMaster.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})

						if cluster.Conf.ReplicationNoRelay && cluster.Status == ConstMonitorActif {
							cluster.RejoinFixRelay(sl, cluster.master)
						}

					}
					if sl.LogBin == "OFF" {
						cluster.SetState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00013"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
					}
				}
				if sl.GetReplicationDelay() <= cluster.Conf.FailMaxDelay && sl.IsSQLThreadRunning() {
					cluster.master.RplMasterStatus = true
				}

			}
		}
		// State also check in failover_check false positive
		if cluster.master.IsFailed() && cluster.slaves.checkAllSlavesRunning() {
			cluster.SetState("ERR00016", state.State{
				ErrType:   "ERROR",
				ErrDesc:   clusterError["ERR00016"],
				ErrFrom:   "NET",
				ServerUrl: cluster.master.URL,
			})
		}

		cluster.sme.SetMasterUpAndSync(cluster.master.SemiSyncMasterStatus, cluster.master.RplMasterStatus)
	}

	if cluster.IsProvision() {
		if len(cluster.Crashes) > 0 {
			cluster.LogPrintf(LvlDbg, "Purging crashes, all databses nodes up")
			cluster.Crashes = nil
			cluster.Save()
		}
	}
	if cluster.Conf.Arbitration {
		if cluster.IsSplitBrain {
			cluster.SetState("WARN0079", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0079"]), ErrFrom: "ARB"})
		}
		if cluster.IsLostMajority {
			cluster.SetState("WARN0080", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0080"]), ErrFrom: "ARB"})
		}
		if cluster.IsFailedArbitrator {
			cluster.SetState("WARN0081", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0081"]), ErrFrom: "ARB"})
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

		allslavefailed := true
		for _, s := range cluster.slaves {
			if s.State != stateFailed && s.State != stateErrorAuth && !s.IsIgnored() {
				allslavefailed = false
			}
		}
		if allslavefailed {
			if cluster.IsDiscovered() {
				if cluster.master != nil && cluster.Conf.Interactive == false && cluster.Conf.FailRestartUnsafe == false {
					// forget the master if safe mode
					//		cluster.LogPrintf(LvlInfo, "Backing up last seen master: %s for safe failover restart", cluster.master.URL)
					//		cluster.lastmaster = cluster.master
					//		cluster.master = nil

				}
			}
			cluster.SetState("ERR00021", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00021"]), ErrFrom: "TOPO"})
			cluster.IsDown = true
			return true
		}

	}
	cluster.IsDown = false
	return false
}

func (cluster *Cluster) PrintTopology() {
	for k, v := range cluster.Servers {
		cluster.LogPrintf(LvlInfo, "Server [%d] %s %s %s", k, v.URL, v.State, v.PrevState)
	}
}

// CountFailed Count number of failed node
func (cluster *Cluster) CountFailed(s []*ServerMonitor) int {
	failed := 0
	for _, server := range cluster.Servers {
		if server.State == stateFailed || server.State == stateErrorAuth {
			failed = failed + 1
		}
	}
	return failed
}

// LostMajority should be call in case of splitbrain to set maintenance mode
func (cluster *Cluster) LostMajority() bool {
	failed := cluster.CountFailed(cluster.Servers)
	alive := len(cluster.Servers) - failed
	if alive > len(cluster.Servers)/2 {
		return false
	} else {
		return true
	}

}

func (cluster *Cluster) FailedMasterDiscovery() {

	// Slave master_host variable must point to failed master

	smh := cluster.slaves[0].GetReplicationMasterHost()
	for k, s := range cluster.Servers {
		if s.State == stateFailed || s.State == stateErrorAuth {
			if (s.Host == smh || s.IP == smh) && s.Port == cluster.slaves[0].GetReplicationMasterPort() {
				if cluster.Conf.FailRestartUnsafe || cluster.MultipleSlavesUp(s) {
					cluster.master = cluster.Servers[k]
					cluster.master.PrevState = stateMaster
					cluster.LogPrintf(LvlInfo, "Assuming failed server %s was a master", s.URL)
				}
				break
			}
		}
	}
}

func (cluster *Cluster) MultipleSlavesUp(candidate *ServerMonitor) bool {
	ct := 0
	for _, s := range cluster.slaves {

		if !s.IsDown() && (candidate.Host == s.GetReplicationMasterHost() || candidate.IP == s.GetReplicationMasterHost()) && candidate.Port == s.GetReplicationMasterPort() {
			ct++
		}
	}
	if ct > 0 {
		return true
	}
	return false
}
