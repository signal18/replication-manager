// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/dbhelper"
)

func (cluster *Cluster) InitCluster() error {
	var err error
	if cluster.conf.Enterprise {
		err = cluster.OpenSVCProvisionCluster()

	} else {
		err = cluster.LocalhostProvisionDatabases()
	}
	return err
}

func (cluster *Cluster) InitDatabaseService(server *ServerMonitor) error {
	if cluster.conf.Enterprise {
		cluster.OpenSVCProvisionDatabaseService(server)
	} else {
		cluster.LocalhostProvisionDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) InitProxyService(prx *Proxy) error {
	if cluster.conf.Enterprise {
		cluster.OpenSVCProvisionProxyService(prx)
	} else {
		//cluster.LocalhostProvisionProxyService(server)
	}
	return nil
}

func (cluster *Cluster) Unprovision() {
	cluster.sme.SetFailoverState()
	if cluster.conf.Enterprise {
		cluster.OpenSVCUnprovision()
	} else {
		cluster.LocalhostUnprovision()
	}
	cluster.servers = nil
	cluster.slaves = nil
	cluster.master = nil
	cluster.sme.UnDiscovered()
	cluster.newServerList()
	cluster.sme.RemoveFailoverState()
}

func (cluster *Cluster) UnprovisionProxyService(prx *Proxy) error {
	if cluster.conf.Enterprise {
		cluster.OpenSVCUnprovisionProxyService(prx)
	} else {
		//		cluster.LocalhostUnprovisionProxyService(prx)
	}
	return nil
}

func (cluster *Cluster) UnprovisionDatabaseService(server *ServerMonitor) error {

	if cluster.conf.Enterprise {
		cluster.OpenSVCUnprovisionDatabaseService(server)
	} else {
		cluster.LocalhostUnprovisionDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) RollingUpgrade() {
}

func (cluster *Cluster) StopDatabaseService(server *ServerMonitor) error {

	if cluster.conf.Enterprise {
		cluster.OpenSVCStopDatabaseService(server)
	} else {
		cluster.LocalhostStopDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) ShutdownDatabase(server *ServerMonitor) error {
	_, _ = server.Conn.Exec("SHUTDOWN")
	return nil
}

func (cluster *Cluster) StartDatabaseService(server *ServerMonitor) error {
	cluster.LogPrintf("INFO", "Starting Database service %s", server.Id)
	if cluster.conf.Enterprise {
		cluster.OpenSVCStartService(server)
	} else {
		cluster.LocalhostStartDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) StartAllNodes() error {

	return nil
}

func (cluster *Cluster) AddSeededServer(srv string) error {
	cluster.conf.Hosts = cluster.conf.Hosts + "," + srv
	cluster.sme.SetFailoverState()
	cluster.newServerList()
	cluster.TopologyDiscover()
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) WaitFailoverEndState() {
	for cluster.sme.IsInFailover() {
		time.Sleep(time.Second)
		cluster.LogPrintf("INFO", "Waiting for failover stopped.")
	}
	time.Sleep(recoverTime * time.Second)
}

func (cluster *Cluster) WaitFailoverEnd() error {
	cluster.WaitFailoverEndState()
	return nil

}

func (cluster *Cluster) WaitFailover(wg *sync.WaitGroup) {
	cluster.LogPrintf("INFO", "Waiting failover end")
	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 15 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting failover end")
			exitloop++
		case <-cluster.failoverCond.Recv:
			cluster.LogPrintf("INFO", "Failover end receive from channel failoverCond")
			return
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Failover end")
	} else {
		cluster.LogPrintf("ERROR", "Failover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitSwitchover(wg *sync.WaitGroup) {

	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("INFO", "Waiting switchover end")
			exitloop++
		case <-cluster.switchoverCond.Recv:
			return
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Switchover end")
	} else {
		cluster.LogPrintf("ERROR", "Switchover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitRejoin(wg *sync.WaitGroup) {

	defer wg.Done()

	exitloop := 0

	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {

		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting Rejoin")
			exitloop++
		case <-cluster.rejoinCond.Recv:
			return

		default:

		}

	}
	if exitloop < 30 {
		cluster.LogPrintf("INFO", "Rejoin Finished")

	} else {
		cluster.LogPrintf("ERROR", "Rejoin timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitClusterStop() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	cluster.LogPrintf("INFO", "Waiting for cluster shutdown")
	for exitloop < 60 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting for cluster shutdown")
			exitloop++
			// All cluster down
			if cluster.sme.IsInState("ERR00021") == true {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Cluster is shutdown")
	} else {
		cluster.LogPrintf("ERROR", "Cluster shutdown timeout")
		return errors.New("Failed to stop the cluster")
	}
	return nil
}

func (cluster *Cluster) WaitMariaDBStop(server *ServerMonitor) error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting MariaDB shutdown")
			exitloop++
			_, err := os.FindProcess(server.Process.Pid)
			if err != nil {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "MariaDB shutdown")
	} else {
		cluster.LogPrintf("INFO", "MariaDB shutdown timeout")
		return errors.New("Failed to Stop MariaDB")
	}
	return nil
}

func (cluster *Cluster) WaitDatabaseStart(server *ServerMonitor) error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting for database start %s", server.URL)
			exitloop++

			dbhelper.GetStatus(server.Conn)
			if server.IsDown() == false {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Database started")
	} else {
		cluster.LogPrintf("INFO", "Database start timeout")
		return errors.New("Failed to Start MariaDB")
	}
	return nil
}

func (cluster *Cluster) WaitBootstrapDiscovery() error {
	cluster.LogPrintf("INFO", "Waiting Bootstrap and discovery")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting Bootstrap and discovery")
			exitloop++
			if cluster.sme.IsDiscovered() {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Cluster is Bootstraped and discovery")
	} else {
		cluster.LogPrintf("ERROR", "Bootstrap timeout")
		return errors.New("Failed Bootstrap timeout")
	}
	return nil
}

func (cluster *Cluster) waitMasterDiscovery() error {
	cluster.LogPrintf("INFO", "Waiting Master Found")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting Master Found")
			exitloop++
			if cluster.master != nil {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Master founded")
	} else {
		cluster.LogPrintf("ERROR", "Master found timeout")
		return errors.New("Failed Master search timeout")
	}
	return nil
}

func (cluster *Cluster) waitClusterStart() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	cluster.LogPrintf("INFO", "Waiting for cluster start")
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf("INFO", "Waiting for cluster start")
			exitloop++
			if cluster.sme.CanMonitor() {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("INFO", "Cluster was started")
	} else {
		cluster.LogPrintf("ERROR", "Timeout waiting for cluster start")
		return errors.New("Cluster search timeout")
	}
	return nil
}

// Bootstrap provisions && setup topology
func (cluster *Cluster) Bootstrap() error {
	var err error
	// create service template and post
	err = cluster.BootstrapServices()
	if err != nil {
		return err
	}
	time.Sleep(time.Millisecond * 3000)
	err = cluster.BootstrapReplication()
	if err != nil {
		return err
	}
	if cluster.conf.Test {
		cluster.WaitBootstrapDiscovery()
		cluster.initProxies()
		if cluster.master == nil {
			return errors.New("Abording test, no master found")
		}
		err = cluster.InitBenchTable()
		if err != nil {
			return errors.New("Abording test, can't create bench table")
		}
	}
	return nil
}

func (cluster *Cluster) BootstrapServices() error {

	// create service template and post
	if cluster.conf.Test {
		err := cluster.InitCluster()
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) BootstrapReplicationCleanup() error {

	cluster.LogPrintf("INFO", "Cleaning up replication on existing servers")
	cluster.sme.SetFailoverState()
	for _, server := range cluster.servers {
		err := server.Refresh()
		if err != nil {
			return err
		}
		if cluster.conf.Verbose {
			cluster.LogPrintf("INFO", "SetDefaultMasterConn on server %s ", server.URL)
		}
		err = dbhelper.SetDefaultMasterConn(server.Conn, cluster.conf.MasterConn)
		if err != nil {
			if cluster.conf.Verbose {
				cluster.LogPrintf("INFO", "RemoveFailoverState on server %s ", server.URL)
			}
			cluster.sme.RemoveFailoverState()
			return err
		}
		if cluster.conf.Verbose {
			cluster.LogPrintf("INFO", "ResetMaster on server %s ", server.URL)
		}
		err = dbhelper.ResetMaster(server.Conn)
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		err = dbhelper.StopAllSlaves(server.Conn)
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		err = dbhelper.ResetAllSlaves(server.Conn)
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		if server.DBVersion.IsMariaDB() {
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos=''")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
		}
	}
	cluster.master = nil
	cluster.slaves = nil
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) BootstrapReplication() error {

	// default to master slave
	if cluster.CleanAll {
		cluster.BootstrapReplicationCleanup()
	}
	err := cluster.TopologyDiscover()
	if err == nil {
		return errors.New("Environment already has an existing master/slave setup")
	}
	cluster.sme.SetFailoverState()
	masterKey := 0
	if cluster.conf.PrefMaster != "" {
		masterKey = func() int {
			for k, server := range cluster.servers {
				if server.URL == cluster.conf.PrefMaster {
					cluster.sme.RemoveFailoverState()
					return k
				}
			}
			cluster.sme.RemoveFailoverState()
			return -1
		}()
	}
	if masterKey == -1 {
		return errors.New("Preferred master could not be found in existing servers")
	}
	_, err = cluster.servers[masterKey].Conn.Exec("RESET MASTER")
	if err != nil {
		cluster.LogPrintf("INFO", "RESET MASTER failed on master")
	}
	// master-slave mariadb >10
	if cluster.conf.MultiMaster == false && cluster.conf.MxsBinlogOn == false && cluster.conf.MultiTierSlave == false {

		for key, server := range cluster.servers {
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				dbhelper.SetReadOnly(server.Conn, false)
				continue
			} else {
				var hasMyGTID bool
				hasMyGTID, err = dbhelper.HasMySQLGTID(server.Conn)
				if cluster.conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.servers[masterKey].Host,
						Port:      cluster.servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
						Mode:      "CURRENT_POS",
					})
					cluster.LogPrintf("INFO", "Environment bootstrapped with %s as master", cluster.servers[masterKey].URL)
				} else if hasMyGTID {

					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.servers[masterKey].Host,
						Port:      cluster.servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
						Mode:      "MASTER_AUTO_POSITION",
					})
					//  Missing  multi source cluster.conf.MasterConn
					cluster.LogPrintf("INFO", "Environment bootstrapped with MySQL GTID replication style and %s as master", cluster.servers[masterKey].URL)

				} else {

					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.servers[masterKey].Host,
						Port:      cluster.servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
						Mode:      "POSITIONAL",
						Logfile:   cluster.servers[masterKey].MasterLogFile,
						Logpos:    cluster.servers[masterKey].MasterLogPos,
					})
					//  Missing  multi source cluster.conf.MasterConn
					cluster.LogPrintf("INFO", "Environment bootstrapped with old replication style and %s as master", cluster.servers[masterKey].URL)

				}
				if err != nil {
					cluster.LogPrintf("ERROR", "Replication can't be bootstarp for server %s with %s as master: %s ", server.URL, cluster.servers[masterKey].URL, err)
				}
				if cluster.conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
					_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
				} else {
					_, err = server.Conn.Exec("START SLAVE")
				}

				if err != nil {
					cluster.LogPrintf("ERROR", "Replication can't be bootstarp for server %s with %s as master: %s ", server.URL, cluster.servers[masterKey].URL, err)
				}
				dbhelper.SetReadOnly(server.Conn, true)
			}
		}

	}

	// Slave realy
	if cluster.conf.MultiTierSlave == true {
		masterKey = 0
		relaykey := 1
		for key, server := range cluster.servers {
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				dbhelper.SetReadOnly(server.Conn, false)
				continue
			} else {
				dbhelper.StopAllSlaves(server.Conn)
				dbhelper.ResetAllSlaves(server.Conn)

				if relaykey == key {
					stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.conf.MasterConn, cluster.servers[masterKey].Host, cluster.servers[masterKey].Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry, 1)
					_, err := server.Conn.Exec(stmt)
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln(stmt, err))
					}
					_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln("Can't start slave: ", err))
					}
				} else {
					stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.conf.MasterConn, cluster.servers[relaykey].Host, cluster.servers[relaykey].Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry, 1)
					_, err := server.Conn.Exec(stmt)
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln(stmt, err))
					}
					_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln("Can't start slave: ", err))
					}

				}
				dbhelper.SetReadOnly(server.Conn, true)
			}
		}
		cluster.LogPrintf("INFO", "Environment bootstrapped with %s as master", cluster.servers[masterKey].URL)
	}
	if cluster.conf.MultiMaster == true {
		for key, server := range cluster.servers {
			if key == 0 {

				stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.conf.MasterConn, cluster.servers[1].Host, cluster.servers[1].Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry, 1)
				_, err := server.Conn.Exec(stmt)
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln(stmt, err))
				}
				_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln("Can't start slave: ", err))
				}
				dbhelper.SetReadOnly(server.Conn, true)
			}
			if key == 1 {

				stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.conf.MasterConn, cluster.servers[0].Host, cluster.servers[0].Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry, 1)
				_, err := server.Conn.Exec(stmt)
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln("ERROR:", stmt, err))
				}
				_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln("Can't start slave: ", err))
				}
			}
			dbhelper.SetReadOnly(server.Conn, true)
		}
	}

	cluster.sme.RemoveFailoverState()
	err = cluster.TopologyDiscover()
	if err != nil {
		return errors.New("Can't found topology after bootstrap")
	}
	//bootstrapChan <- true
	return nil
}
