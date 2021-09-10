// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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

	"github.com/signal18/replication-manager/utils/state"
)

type topologyError struct {
	Code int
	Msg  string
}

const (
	topoMasterSlave         string = "master-slave"
	topoUnknown             string = "unknown"
	topoBinlogServer        string = "binlog-server"
	topoMultiTierSlave      string = "multi-tier-slave"
	topoMultiMaster         string = "multi-master"
	topoMultiMasterRing     string = "multi-master-ring"
	topoMultiMasterWsrep    string = "multi-master-wsrep"
	topoMasterSlavePgLog    string = "master-slave-pg-logical"
	topoMasterSlavePgStream string = "master-slave-pg-stream"
)

func (cluster *Cluster) newServerList() error {
	//sva issue to monitor server should not be fatal

	var err error
	cluster.SetClusterVariablesFromConfig()
	err = cluster.isValidConfig()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed to validate config: %s", err)
	}
	//cluster.LogPrintf(LvlErr, "hello %+v", cluster.Conf.Hosts)
	cluster.Lock()
	//cluster.LogPrintf(LvlErr, "hello %+v", cluster.Conf.Hosts)
	cluster.Servers = make([]*ServerMonitor, len(cluster.hostList))
	// split("")  return len = 1

	if cluster.Conf.Hosts != "" {

		for k, url := range cluster.hostList {
			cluster.Servers[k], err = cluster.newServerMonitor(url, cluster.dbUser, cluster.dbPass, false, cluster.GetDomain())
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to server %s : %s", cluster.Servers[k].URL, err)
			}
			cluster.Servers[k].SetPlacement(k, cluster.Conf.ProvAgents, cluster.Conf.SlapOSDBPartitions, cluster.Conf.SchedulerReceiverPorts)

			if cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "New database monitored: %v", cluster.Servers[k].URL)
			}

		}
	}
	cluster.Unlock()
	return nil
}

// AddChildServers Add child clusters nodes  if they get same  source name
func (cluster *Cluster) AddChildServers() error {
	mychilds := cluster.GetChildClusters()
	for _, c := range mychilds {
		for _, sv := range c.Servers {

			if sv.IsSlaveOfReplicationSource(cluster.Conf.MasterConn) {
				mymaster, _ := cluster.GetMasterFromReplication(sv)
				if mymaster != nil {
					//	cluster.slaves = append(cluster.slaves, sv)
					if !cluster.HasServer(sv) {
						srv, err := cluster.newServerMonitor(sv.Name+":"+sv.Port, sv.ClusterGroup.dbUser, sv.ClusterGroup.dbPass, false, c.GetDomain())
						if err != nil {
							return err
						}
						srv.Ignored = true
						cluster.Servers = append(cluster.Servers, srv)
					}
				}
			}
		}
	}
	return nil
	// End  child clusters  same multi source server discorvery
}

// Start of topology detection
// Create a connection to each host and build list of slaves.
func (cluster *Cluster) TopologyDiscover(wcg *sync.WaitGroup) error {
	defer wcg.Done()
	cluster.AddChildServers()
	//monitor ignored server fist so that their replication position get oldest
	wg := new(sync.WaitGroup)
	if cluster.Conf.Hosts == "" {
		return errors.New("Can not discover empty cluster")
	}
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
	// Check topology Cluster all servers down
	cluster.IsDown = cluster.AllServersFailed()
	cluster.CheckSameServerID()
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
		// count wsrep node as  slaves
		if sv.IsSlave || sv.IsWsrepPrimary {
			if cluster.Conf.LogLevel > 2 {
				cluster.LogPrintf(LvlDbg, "Server %s is configured as a slave", sv.URL)
			}
			cluster.slaves = append(cluster.slaves, sv)
		} else {
			// not slave

			if sv.BinlogDumpThreads == 0 && sv.State != stateMaster {
				//sv.State = stateUnconn
				//transition to standalone may happen despite server have never connect successfully when default to suspect
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "Server %s has no slaves ", sv.URL)
				}
			} else {
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "Server %s was set master as last non slave", sv.URL)
				}
				if cluster.Status == ConstMonitorActif && cluster.master != nil && cluster.GetTopology() == topoMasterSlave && cluster.Servers[k].URL != cluster.master.URL {
					//Extra master in master slave topology rejoin it after split brain
					cluster.SetState("ERR00063", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00063"]), ErrFrom: "TOPO"})
					cluster.Servers[k].RejoinMaster()
				} else {
					cluster.master = cluster.Servers[k]
					cluster.master.SetMaster()
					if cluster.master.IsReadOnly() && !cluster.master.IsRelay {
						cluster.master.SetReadWrite()
						cluster.LogPrintf(LvlInfo, "Server %s disable read only as last non slave", cluster.master.URL)
					}
				}
			}

		}
		// end not slave
	}

	// If no cluster.slaves are detected, generate an error
	if len(cluster.slaves) == 0 && cluster.GetTopology() != topoMultiMasterWsrep {
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
	if cluster.Conf.MultiMaster == true || cluster.GetTopology() == topoMultiMasterWsrep {
		srw := 0
		for _, s := range cluster.Servers {
			if s.IsReadWrite() {
				srw++
			}
		}
		if srw > 1 {
			cluster.SetState("WARN0003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, choosing prefered master", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range cluster.Servers {
			if s.IsReadOnly() {
				srw++
			}
		}
		if srw > 1 {
			cluster.SetState("WARN0004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to preferred master.", ErrFrom: "TOPO"})
			server := cluster.getOnePreferedMaster()
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
						cluster.master.SetMaster()
						cluster.master.SetReadWrite()
						if cluster.Conf.LogLevel > 2 {
							cluster.LogPrintf(LvlDbg, "Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
				if (cluster.Conf.MultiMaster == true || cluster.GetTopology() == topoMultiMasterWsrep) && !cluster.Servers[k].IsDown() {
					if s.IsReadWrite() {
						cluster.master = cluster.Servers[k]
						if cluster.Conf.MultiMaster == true {
							cluster.master.SetMaster()
						} else {
							cluster.vmaster = cluster.Servers[k]
						}
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

				}
				if sl.GetReplicationDelay() <= cluster.Conf.FailMaxDelay && sl.IsSQLThreadRunning() {
					cluster.master.RplMasterStatus = true
				}

			}
		}
		// State also check in failover_check false positive
		if cluster.master.IsFailed() && cluster.slaves.HasAllSlavesRunning() {
			cluster.SetState("ERR00016", state.State{
				ErrType:   "ERROR",
				ErrDesc:   clusterError["ERR00016"],
				ErrFrom:   "NET",
				ServerUrl: cluster.master.URL,
			})
		}

	}

	if cluster.HasAllDbUp() {
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
			cluster.SetState("WARN0090", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0090"], cluster.Conf.ArbitratorAddress), ErrFrom: "ARB"})
		}
	}
	if cluster.sme.CanMonitor() {
		return nil
	}
	return errors.New("Error found in State Machine Engine")
}

// AllServersDown track state of unvailable cluster
func (cluster *Cluster) AllServersFailed() bool {
	for _, s := range cluster.Servers {
		if s.IsFailed() == false {
			return false
		}
	}
	//"ERR00077": "All databases state down",
	cluster.SetState("ERR00077", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00077"]), ErrFrom: "TOPO"})
	return true
}

// TopologyClusterDown track state of unvailable cluster
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
			cluster.IsClusterDown = true
			return true
		}
		cluster.IsClusterDown = false

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
