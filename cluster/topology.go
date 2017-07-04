// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
)

type topologyError struct {
	Code int
	Msg  string
}

func (cluster *Cluster) newServerList() error {
	//sva issue to monitor server should not be fatal
	cluster.servers = make([]*ServerMonitor, len(cluster.hostList))
	for k, url := range cluster.hostList {
		var err error
		cluster.servers[k], err = cluster.newServerMonitor(url, cluster.dbUser, cluster.dbPass)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not open connection to server %s : %s", cluster.servers[k].URL, err)
			//return err
		}
		if cluster.conf.Verbose {
			cluster.LogPrintf("INFO", "New server monitored: %v", cluster.servers[k].URL)
		}

	}
	// Spider shard discover
	if cluster.conf.Spider == true {
		cluster.SpiderShardsDiscovery()
	}
	err := cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not set proxy list %s", err)
	}
	return nil
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
							srv, err := cluster.newServerMonitor(url, cluster.dbUser, cluster.dbPass)
							srv.State = stateShard
							cluster.servers = append(cluster.servers, srv)
							if err != nil {
								log.Fatalf("ERROR: Could not open connection to Spider Shard server %s : %s", cluster.servers[j].URL, err)
							}
							if cluster.conf.Verbose {
								cluster.tlog.Add(fmt.Sprintf("[%s] DEBUG: New server created: %v", cluster.cfgGroup, cluster.servers[j].URL))
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
						cluster.LogPrintf("WARN", "Can not set heartbeat table to %s", url)
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
		cluster.LogPrintf("INFO", "In Failover skip ping server list")
		return
	}
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
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("INFO", "In Failover skip topology detection")
		return nil
	}
	if cluster.conf.LogLevel > 2 {
		cluster.LogPrintf("DEBUG", "Entering topology detection")
	}
	m := maxscale.MaxScale{Host: cluster.conf.MxsHost, Port: cluster.conf.MxsPort, User: cluster.conf.MxsUser, Pass: cluster.conf.MxsPass}
	if cluster.conf.MxsOn {
		err := m.Connect()
		if err != nil {
			cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
		}
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
		if cluster.conf.MxsOn {
			sv.getMaxscaleInfos(&m)
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
					cluster.LogPrintf("DEBUG", "Server %s was set mastrer as last non slave", sv.URL)
				}
				cluster.master = cluster.servers[k]
				cluster.master.State = stateMaster
			}
		}
		// Check replication manager user privileges on live servers
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG", "Privilege check on %s", sv.URL)
		}
		if sv.State != "" && !sv.IsDown() && sv.IsRelay == false {
			myhost := dbhelper.GetHostFromConnection(sv.Conn, cluster.dbUser)
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
						cluster.sme.AddState("WARN00007", state.State{ErrType: "WARN", ErrDesc: "At least one server is not ACID-compliant. Please make sure that sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF"})
					}
				}
			}
		}
	}
	m.Close()
	// If no cluster.slaves are detected, generate an error
	if len(cluster.slaves) == 0 {
		cluster.sme.AddState("ERR00010", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00010"]), ErrFrom: "TOPO"})
	}

	// Check that all slave servers have the same master and conformity.
	if cluster.conf.MultiMaster == false && cluster.conf.Spider == false {
		for _, sl := range cluster.slaves {
			if sl.IsRelay == false && !sl.IsDown() {
				if cluster.conf.ForceSlaveSemisync && sl.HaveSemiSync == false {
					cluster.LogPrintf("DEBUG", "Enforce semisync on slave %s", sl.DSN)
					dbhelper.InstallSemiSync(sl.Conn)
				}
				if cluster.conf.ForceBinlogRow && sl.HaveBinlogRow == false {
					// In non-multimaster mode, enforce read-only flag if the option is set
					dbhelper.SetBinlogFormat(sl.Conn, "ROW")
					cluster.LogPrintf("INFO", "Enforce binlog format ROW on slave %s", sl.DSN)
				}
				if cluster.conf.ForceSlaveReadOnly && sl.ReadOnly == "OFF" {
					// In non-multimaster mode, enforce read-only flag if the option is set
					dbhelper.SetReadOnly(sl.Conn, true)
					cluster.LogPrintf("INFO", "Enforce read only on slave %s", sl.DSN)
				}
				if cluster.conf.ForceSlaveHeartbeat && sl.MasterHeartbeatPeriod > 1 {
					dbhelper.SetSlaveHeartbeat(sl.Conn, "1")
					cluster.LogPrintf("INFO", "Enforce heartbeat to 1s on slave %s", sl.DSN)
				}
				if cluster.conf.ForceSlaveGtid && sl.MasterUseGtid == "No" {
					dbhelper.SetSlaveGTIDMode(sl.Conn, "slave_pos")
					cluster.LogPrintf("INFO", "Enforce GTID replication on slave %s", sl.DSN)
				}
				if cluster.conf.ForceSyncInnoDB && sl.HaveInnodbTrxCommit == false {
					dbhelper.SetSyncInnodb(sl.Conn)
					cluster.LogPrintf("INFO", "Enforce sync InnoDB  on slave %s", sl.DSN)
				}
				if cluster.conf.ForceBinlogChecksum && sl.HaveChecksum == false {
					dbhelper.SetBinlogChecksum(sl.Conn)
					cluster.LogPrintf("INFO", "Enforce checksum on slave %s", sl.DSN)
				}
				if cluster.conf.ForceBinlogSlowqueries && sl.HaveBinlogSlowqueries == false {
					dbhelper.SetBinlogSlowqueries(sl.Conn)
					cluster.LogPrintf("INFO", "Enforce log slow queries of replication on slave %s", sl.DSN)
				}
				if cluster.conf.ForceBinlogAnnotate && sl.HaveBinlogAnnotate == false {
					dbhelper.SetBinlogAnnotate(sl.Conn)
					cluster.LogPrintf("INFO", "Enforce annotate on slave %s", sl.DSN)
				}
				if cluster.conf.ForceBinlogCompress && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
					dbhelper.SetBinlogCompress(sl.Conn)
					cluster.LogPrintf("INFO", "Enforce binlog compression on slave %s", sl.DSN)
				}
				/* Disable because read-only variable
				if cluster.conf.ForceDiskRelayLogSizeLimit && sl.RelayLogSize != cluster.conf.ForceDiskRelayLogSizeLimitSize {
					dbhelper.SetRelayLogSpaceLimit(sl.Conn, strconv.FormatUint(cluster.conf.ForceDiskRelayLogSizeLimitSize, 10))
					cluster.LogPrintf("DEBUG: Enforce relay disk space limit on slave %s", sl.DSN)
				}*/
				if sl.HasCycling(sl.ServerID) && cluster.conf.MultiTierSlave == false && cluster.conf.MultiMaster == false {
					cluster.sme.AddState("ERR00011", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00011"]), ErrFrom: "TOPO"})
					cluster.conf.MultiMaster = true
				}
			}
			if cluster.conf.MultiMaster == false && sl.IsMaxscale == false {
				if sl.HasSlaves(cluster.slaves) == true {
					sl.IsRelay = true
					sl.State = stateRelay
				} else if sl.IsSlave {
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
			cluster.sme.AddState("WARN00003", state.State{ErrType: "WARNING", ErrDesc: "RW server count > 1 in multi-master mode. set read_only=1 in cnf is a must have, switching to prefered master", ErrFrom: "TOPO"})
		}
		srw = 0
		for _, s := range cluster.servers {
			if s.ReadOnly == "ON" {
				srw++
			}
		}
		if srw > 1 {
			cluster.sme.AddState("WARN00004", state.State{ErrType: "WARNING", ErrDesc: "RO server count > 1 in multi-master mode.  switching to preferred master.", ErrFrom: "TOPO"})
			server := cluster.getPreferedMaster()
			if server != nil {
				dbhelper.SetReadOnly(server.Conn, false)
			} else {
				cluster.sme.AddState("WARN00006", state.State{ErrType: "WARNING", ErrDesc: "Multi-master need a preferred master.", ErrFrom: "TOPO"})
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
				smh := cluster.slaves[0].MasterHost
				for k, s := range cluster.servers {
					if s.State == stateFailed {
						if (s.Host == smh || s.IP == smh) && s.Port == cluster.slaves[0].MasterPort {
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
		cluster.sme.AddState("ERR00012", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00012"]), ErrFrom: "TOPO"})
	} else {
		cluster.master.RplMasterStatus = false
		// End of autodetection code
		if !cluster.master.IsDown() {
			if cluster.conf.ForceSlaveSemisync && cluster.master.HaveSemiSync == false {
				cluster.LogPrintf("INFO", "Enforce semisync on Master %s", cluster.master.DSN)
				dbhelper.InstallSemiSync(cluster.master.Conn)
			}
			if cluster.conf.ForceBinlogRow && cluster.master.HaveBinlogRow == false {
				dbhelper.SetBinlogFormat(cluster.master.Conn, "ROW")
				cluster.LogPrintf("INFO", "Enforce binlog format ROW on Master %s", cluster.master.DSN)
			}
			if cluster.conf.ForceSyncBinlog && cluster.master.HaveSyncBinLog == false {
				dbhelper.SetSyncBinlog(cluster.master.Conn)
				cluster.LogPrintf("INFO", "Enforce sync binlog on Master %s", cluster.master.DSN)
			}
			if cluster.conf.ForceSyncInnoDB && cluster.master.HaveSyncBinLog == false {
				dbhelper.SetSyncInnodb(cluster.master.Conn)
				cluster.LogPrintf("INFO", "Enforce innodb sync on Master %s", cluster.master.DSN)
			}
			if cluster.conf.ForceBinlogAnnotate && cluster.master.HaveBinlogAnnotate == false {
				dbhelper.SetBinlogAnnotate(cluster.master.Conn)
				cluster.LogPrintf("INFO", "Enforce binlog annotate on master %s", cluster.master.DSN)
			}
			if cluster.conf.ForceBinlogChecksum && cluster.master.HaveChecksum == false {
				dbhelper.SetBinlogChecksum(cluster.master.Conn)
				cluster.LogPrintf("INFO", "Enforce ckecsum annotate on master %s", cluster.master.DSN)
			}
			if cluster.conf.ForceBinlogCompress && cluster.master.HaveBinlogCompress == false && cluster.master.DBVersion.IsMariaDB() && cluster.master.DBVersion.Major >= 10 && cluster.master.DBVersion.Minor >= 2 {
				dbhelper.SetBinlogCompress(cluster.master.Conn)
				cluster.LogPrintf("INFO", "Enforce binlog compression on master %s", cluster.master.DSN)
			}
		}
		// Replication checks
		if cluster.conf.MultiMaster == false {
			for _, sl := range cluster.slaves {
				if sl.IsRelay == false {
					if cluster.conf.LogLevel > 2 {
						cluster.LogPrintf("DEBUG", "Checking if server %s is a slave of server %s", sl.Host, cluster.master.Host)
					}
					is, err := dbhelper.IsSlaveof(sl.Conn, sl.Host, cluster.master.IP, cluster.master.Port)
					if !is {
						cluster.sme.AddState("WARN00005", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Server %s is not a slave of declared master %s, reason: %s", sl.URL, cluster.master.URL, err), ErrFrom: "TOPO"})
					}
					if sl.LogBin == "OFF" {
						cluster.sme.AddState("ERR00013", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00013"], sl.URL), ErrFrom: "TOPO"})
					}
				}
				if sl.Delay.Int64 <= cluster.conf.SwitchMaxDelay && sl.SQLThread == "Yes" {
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
	// Check topology Cluster is down
	cluster.TopologyClusterDown()
	if cluster.sme.CanMonitor() {
		return nil
	}
	return errors.New("Error found in State Machine Engine")
}

// TopologyClusterDown track state all ckuster down
func (cluster *Cluster) TopologyClusterDown() bool {
	// search for all cluster down
	if cluster.master == nil || cluster.master.State == stateFailed {
		if cluster.conf.Interactive == false {
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
		}
	}
	return false
}

func (cluster *Cluster) GetTopology() string {
	cluster.conf.Topology = "unknown"
	if cluster.conf.MultiMaster {
		cluster.conf.Topology = "multi-master"
	} else if cluster.conf.MxsBinlogOn {
		cluster.conf.Topology = "binlog-server"
	} else {
		relay := cluster.GetRelayServer()
		if relay != nil {
			cluster.conf.Topology = "multi-tier-slave"
		} else if cluster.master != nil {
			cluster.conf.Topology = "master-slave"
		}
	}
	return cluster.conf.Topology
}

func (cluster *Cluster) PrintTopology() {
	for k, v := range cluster.servers {
		cluster.LogPrintf("INFO", "Server [%d] %s %s %s", k, v.URL, v.State, v.PrevState)
	}
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

func (cluster *Cluster) GetServerFromId(serverid uint) *ServerMonitor {
	for _, server := range cluster.servers {
		if server.ServerID == serverid {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetMasterFromReplication(s *ServerMonitor) (*ServerMonitor, error) {

	for _, server := range cluster.servers {

		if len(s.Replications) > 0 {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Rejoin replication master id %d was lookup if master %s is that one : %d", s.MasterServerID, server.DSN, server.ServerID)
			}
			if s.MasterServerID == server.ServerID {
				return server, nil
			}
		}

	}
	return nil, nil
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
