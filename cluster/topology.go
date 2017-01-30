// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"

	"log"
	"strings"
	"sync"
)

type topologyError struct {
	Code int
	Msg  string
}

func (cluster *Cluster) newServerList() {
	cluster.servers = make([]*ServerMonitor, len(cluster.hostList))
	for k, url := range cluster.hostList {
		var err error
		cluster.servers[k], err = cluster.newServerMonitor(url)
		if err != nil {
			log.Fatalf("ERROR: Could not open connection to server %s : %s", cluster.servers[k].URL, err)
		}
		if cluster.conf.Verbose {
			cluster.tlog.Add(fmt.Sprintf("DEBUG: New server created: %v", cluster.servers[k].URL))
		}
		if cluster.conf.Heartbeat {
			err := dbhelper.SetHeartbeatTable(cluster.servers[k].Conn)
			if err != nil {
				log.Fatalf("ERROR: Can not set heartbeat table to  %s  ", url)
			}
		}
	}
	// Spider shard discover
	if cluster.conf.Spider == true {
		cluster.SpiderShardsDiscovery()
	}
}

func (cluster *Cluster) SpiderShardsDiscovery() {
	for _, s := range cluster.servers {
		cluster.tlog.Add(fmt.Sprintf("INFO: Is Spider Monitor server %s ", s.URL))
		mon, err := dbhelper.GetSpiderMonitor(s.Conn)
		if err == nil {
			if mon != "" {
				cluster.tlog.Add(fmt.Sprintf("INFO: Retriving Spider Shards Server %s ", s.URL))
				extraUrl, err := dbhelper.GetSpiderShardUrl(s.Conn)
				if err == nil {
					if extraUrl != "" {
						for j, url := range strings.Split(extraUrl, ",") {
							var err error
							srv, err := cluster.newServerMonitor(url)
							srv.State = stateShard
							cluster.servers = append(cluster.servers, srv)
							if err != nil {
								log.Fatalf("ERROR: Could not open connection to Spider Shard server %s : %s", cluster.servers[j].URL, err)
							}
							if cluster.conf.Verbose {
								cluster.tlog.Add(fmt.Sprintf("DEBUG: New server created: %v", cluster.servers[j].URL))
							}
						}
					}
				}
			}
		}
	}
}

func (cluster *Cluster) SpiderSetShardsRepl() {
	for k, s := range cluster.servers {
		url := s.URL

		if cluster.conf.Heartbeat {
			for _, s2 := range cluster.servers {
				url2 := s2.URL
				if url2 != url {
					host, port := misc.SplitHostPort(url2)
					err := dbhelper.SetHeartbeatTable(cluster.servers[k].Conn)
					if err != nil {
						log.Println("WARN : Can not set heartbeat table to %s", url)
						return
					}
					err = dbhelper.SetMultiSourceRepl(cluster.servers[k].Conn, host, port, cluster.rplUser, cluster.rplPass, "")
					if err != nil {
						log.Fatalf("ERROR: Can not set heartbeat replication from %s to %s : %s", url, url2, err)
					}
				}
			}
		}
	}
}

func (cluster *Cluster) pingServerList() {
	if cluster.sme.IsInState("WARN00008") {
		cluster.LogPrintf("DEBUG: In Failover skip topology detection")
		return
	}
	wg := new(sync.WaitGroup)
	for _, sv := range cluster.servers {
		wg.Add(1)
		go func(sv *ServerMonitor) {
			defer wg.Done()
			err := sv.Conn.Ping()
			if err != nil {
				if driverErr, ok := err.(*mysql.MySQLError); ok {
					if driverErr.Number == 1045 {
						sv.State = stateUnconn
						cluster.sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Database %s access denied: %s.", sv.URL, err.Error()), ErrFrom: "TOPO"})
					}
				} else {
					cluster.sme.AddState("INF00001", state.State{ErrType: "INFO", ErrDesc: fmt.Sprintf("Server %s is down", sv.URL), ErrFrom: "TOPO"})
					// We can set the failed state at this point if we're in the initial loop
					// Otherwise, let the monitor check function handle failures
					if sv.State == "" {
						if cluster.conf.LogLevel > 2 {
							cluster.LogPrint("DEBUG: State failed set by topology detection INF00001")
						}
						sv.State = stateFailed
					}

				}
			}
		}(sv)
		// If not yet dicovered we can initiate hearbeat table on each node
		if cluster.conf.Heartbeat {
			if cluster.sme.IsDiscovered() == false {
				err := dbhelper.SetHeartbeatTable(sv.Conn)
				if err != nil {
					cluster.sme.AddState("WARN00010", state.State{ErrType: "WARNING", ErrDesc: "Disable heartbeat table can't create table", ErrFrom: "RUN"})
					cluster.conf.Heartbeat = false
				}
			}

			if cluster.runStatus == "A" && dbhelper.CheckHeartbeat(sv.Conn, cluster.runUUID, "A") != true {
				cluster.sme.AddState("ERR00019", state.State{ErrType: "ERROR", ErrDesc: "Multiple Active Replication Monitor Switching Passive Mode", ErrFrom: "RUN"})
				cluster.runStatus = "P"
			}
			if cluster.runStatus == "P" {
				cluster.sme.AddState("ERR00020", state.State{ErrType: "ERROR", ErrDesc: "Monitoring in Passive Mode Can't Failover", ErrFrom: "RUN"})
			}
			dbhelper.WriteHeartbeat(sv.Conn, cluster.runUUID, cluster.runStatus)
		}
	}

	wg.Wait()
}

// Start of topology detection
// Create a connection to each host and build list of slaves.
func (cluster *Cluster) TopologyDiscover() error {
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("DEBUG: In Failover skip topology detection")
		return nil
	}
	if cluster.conf.LogLevel > 2 {
		cluster.LogPrintf("DEBUG: Entering topology detection")
	}
	cluster.slaves = nil
	for k, sv := range cluster.servers {
		err := sv.refresh()
		if err != nil {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG: Server %s could not be refreshed: %s", sv.URL, err)
			}
			continue
		}
		if sv.UsingGtid != "" {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG: Server %s is cluster.configured as a slave", sv.URL)
			}
			sv.replicationCheck()
			//		sv.State = stateSlave
			cluster.slaves = append(cluster.slaves, sv)
		} else {
			var n int
			err := sv.Conn.Get(&n, "SELECT COUNT(*) AS n FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command='binlog dump'")
			if err != nil {
				cluster.sme.AddState("ERR00014", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting binlog dump count on server %s: %s", sv.URL, err), ErrFrom: "CONF"})
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
					cluster.LogPrintf("DEBUG: Server %s has no slaves connected", sv.URL)
				}
			} else {
				cluster.master = cluster.servers[k]
				cluster.master.State = stateMaster
			}
		}
		// Check replication manager user privileges on live servers
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG: Privilege check on %s", sv.URL)
		}
		if sv.State != stateFailed {
			myhost := dbhelper.GetHostFromConnection(sv.Conn, cluster.dbUser)
			myip, err := misc.GetIPSafe(myhost)
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG: Client connection found on server %s with IP %s for host %s", sv.URL, myip, myhost)
			}
			if err != nil {
				cluster.sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s@%s: %s", cluster.dbUser, sv.URL, err), ErrFrom: "CONF"})
			} else {
				priv, err := dbhelper.GetPrivileges(sv.Conn, cluster.dbUser, cluster.repmgrHostname, myip)
				if err != nil {
					cluster.sme.AddState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s@%s: %s", cluster.dbUser, cluster.repmgrHostname, err), ErrFrom: "CONF"})
				}
				if priv.Repl_client_priv == "N" {
					cluster.sme.AddState("ERR00006", state.State{ErrType: "ERROR", ErrDesc: "User must have REPLICATION CLIENT privilege", ErrFrom: "CONF"})
				}
				if priv.Super_priv == "N" {
					cluster.sme.AddState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: "User must have SUPER privilege", ErrFrom: "CONF"})
				}
				if priv.Reload_priv == "N" {
					cluster.sme.AddState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: "User must have RELOAD privilege", ErrFrom: "CONF"})
				}
			}
			// Check replication user has correct privs.
			for _, sv2 := range cluster.servers {
				if sv2.URL != sv.URL {
					rplhost, _ := misc.GetIPSafe(sv2.Host)
					rpriv, err := dbhelper.GetPrivileges(sv2.Conn, cluster.rplUser, sv2.Host, rplhost)
					if err != nil {
						cluster.sme.AddState("ERR00015", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Error getting privileges for user %s on server %s: %s", cluster.rplUser, sv2.URL, err), ErrFrom: "CONF"})
					}
					if rpriv.Repl_slave_priv == "N" {
						cluster.sme.AddState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: "User must have REPLICATION SLAVE privilege", ErrFrom: "CONF"})
					}
					// Additional health checks go here
					if sv.acidTest() == false && cluster.sme.IsDiscovered() {
						cluster.sme.AddState("WARN00007", state.State{ErrType: "WARN", ErrDesc: "At least one server is not ACID-compliant. Please check that the values of sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF"})
					}
				}
			}
		}
	}

	// If no cluster.slaves are detected, generate an error
	if len(cluster.slaves) == 0 {
		cluster.sme.AddState("ERR00010", state.State{ErrType: "ERROR", ErrDesc: "No slaves were detected", ErrFrom: "TOPO"})
	}

	// Check that all slave servers have the same master.
	if cluster.conf.MultiMaster == false {
		for _, sl := range cluster.slaves {

			if sl.hasSiblings(cluster.slaves) == false {
				// possibly buggy code
				// cluster.sme.AddState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: "Multiple masters were detected, auto switching to multimaster monitoring", ErrFrom: "TOPO"})
				cluster.sme.AddState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: "Multiple masters were detected", ErrFrom: "TOPO"})
				// cluster.conf.MultiMaster = true
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
			cluster.sme.AddState("WARN00003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range cluster.servers {
			if s.ReadOnly == "ON" {
				srw++
			}
		}
		if srw > 1 {
			cluster.sme.AddState("WARN00004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to prefered master.", ErrFrom: "TOPO"})
			server := cluster.getPreferedMaster()
			if server != nil {
				dbhelper.SetReadOnly(server.Conn, false)
			} else {
				cluster.sme.AddState("WARN00006", state.State{ErrType: "WARNING", ErrDesc: "Multi-master need a prefered master.", ErrFrom: "TOPO"})
			}
		}
	} else if cluster.conf.ReadOnly {
		// In non-multimaster mode, enforce read-only flag if the option is set
		for _, s := range cluster.slaves {
			if s.ReadOnly == "OFF" && cluster.conf.Spider == false {
				dbhelper.SetReadOnly(s.Conn, true)
			}
		}
	}

	if cluster.slaves != nil {
		if len(cluster.slaves) > 0 {
			// Depending if we are doing a failover or a switchover, we will find the master in the list of
			// failed hosts or unconnected hosts.
			// First of all, get a server id from the cluster.slaves slice, they should be all the same

			sid := cluster.slaves[0].MasterServerID
			for k, s := range cluster.servers {
				if cluster.conf.MultiMaster == false && s.State == stateUnconn {
					if s.ServerID == sid {
						cluster.master = cluster.servers[k]
						cluster.master.State = stateMaster
						if cluster.conf.LogLevel > 2 {
							cluster.LogPrintf("DEBUG: Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
				if cluster.conf.MultiMaster == true && cluster.servers[k].State != stateFailed {
					if s.ReadOnly == "OFF" {
						cluster.master = cluster.servers[k]
						cluster.master.State = stateMaster
						if cluster.conf.LogLevel > 2 {
							cluster.LogPrintf("DEBUG: Server %s was autodetected as a master", s.URL)
						}
						break
					}
				}
			}

			// If master is not initialized, find it in the failed hosts list
			if cluster.master == nil {
				// Slave master_host variable must point to failed master
				smh := cluster.slaves[0].MasterHost
				for k, s := range cluster.servers {
					if s.State == stateFailed {
						if s.Host == smh || s.IP == smh {
							cluster.master = cluster.servers[k]
							cluster.master.PrevState = stateMaster
							if cluster.conf.LogLevel > 2 {
								cluster.LogPrintf("DEBUG: Assuming failed server %s was a master", s.URL)
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

		cluster.sme.AddState("ERR00012", state.State{ErrType: "ERROR", ErrDesc: "Could not autodetect a master", ErrFrom: "TOPO"})

	} else {
		cluster.master.RplMasterStatus = false
		// End of autodetection code

		// Replication checks
		if cluster.conf.MultiMaster == false {
			for _, sl := range cluster.slaves {
				if cluster.conf.LogLevel > 2 {
					cluster.LogPrintf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, cluster.master.Host)
				}
				if dbhelper.IsSlaveof(sl.Conn, sl.Host, cluster.master.IP) == false {
					cluster.sme.AddState("WARN00005", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Server %s is not a slave of declared master %s", cluster.master.URL, cluster.master.Host), ErrFrom: "TOPO"})
				}
				if sl.LogBin == "OFF" {
					cluster.sme.AddState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Binary log disabled on slave: %s", sl.URL), ErrFrom: "TOPO"})
				}
				if sl.Delay.Int64 <= cluster.conf.MaxDelay && sl.SQLThread == "Yes" {
					cluster.master.RplMasterStatus = true
				}
			}
		}

		if cluster.master.State == stateFailed && cluster.slaves.checkAllSlavesRunning() {
			cluster.sme.AddState("ERR00016", state.State{
				ErrType: "ERROR",
				ErrDesc: "Network issue - Master is unreachable but slaves are replicating",
				ErrFrom: "NET",
			})
		}

		cluster.sme.SetMasterUpAndSync(cluster.master.SemiSyncMasterStatus, cluster.master.RplMasterStatus)
	}
	if cluster.sme.CanMonitor() {
		return nil
	}
	return errors.New("Error found in State Machine Engine")
}

func (cluster *Cluster) PrintTopology() {
	for k, v := range cluster.servers {
		cluster.LogPrintf("DEBUG: Server [%d] %s %s %s", k, v.URL, v.State, v.PrevState)
	}
}

func (cluster *Cluster) getPreferedMaster() *ServerMonitor {
	for _, server := range cluster.servers {
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG: Server %s was lookup if prefered master: %s", server.URL, cluster.conf.PrefMaster)
		}
		if server.URL == cluster.conf.PrefMaster {
			return server
		}
	}
	return nil
}
