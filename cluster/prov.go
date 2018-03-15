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

// Bootstrap provisions && setup topology
func (cluster *Cluster) Bootstrap() error {
	var err error
	// create service template and post
	err = cluster.ProvisionServices()
	if err != nil {
		return err
	}
	err = cluster.waitDatabaseCanConn()
	if err != nil {
		return err
	}

	err = cluster.BootstrapReplication()
	if err != nil {
		return err
	}
	if cluster.Conf.Test {
		cluster.initProxies()
		err = cluster.WaitProxyEqualMaster()
		if err != nil {
			return err
		}
		err = cluster.WaitBootstrapDiscovery()
		if err != nil {
			return err
		}

		if cluster.GetMaster() == nil {
			return errors.New("Abording test, no master found")
		}
		err = cluster.InitBenchTable()
		if err != nil {
			return errors.New("Abording test, can't create bench table")
		}
	}
	return nil
}

func (cluster *Cluster) ProvisionServices() error {

	// create service template and post
	if !(cluster.Conf.Test || cluster.Conf.Enterprise) {
		return errors.New("Version does not support provisioning.")
	}

	var err error
	cluster.sme.SetFailoverState()
	// delete the cluster state here
	path := cluster.Conf.WorkingDir + "/" + cluster.Name + ".json"
	os.Remove(path)
	if cluster.Conf.Enterprise {
		err = cluster.OpenSVCProvisionCluster()
	} else {
		err = cluster.LocalhostProvisionCluster()
	}
	cluster.sme.RemoveFailoverState()
	if err != nil {
		return err
	}

	return nil

}

func (cluster *Cluster) InitDatabaseService(server *ServerMonitor) error {
	cluster.sme.SetFailoverState()
	if cluster.Conf.Enterprise {
		cluster.OpenSVCProvisionDatabaseService(server)
	} else {
		cluster.LocalhostProvisionDatabaseService(server)
	}
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) InitProxyService(prx *Proxy) error {
	if cluster.Conf.Enterprise {
		cluster.OpenSVCProvisionProxyService(prx)
	} else {
		cluster.LocalhostProvisionProxyService(prx)
	}
	return nil
}

func (cluster *Cluster) Unprovision() {
	cluster.sme.SetFailoverState()
	if cluster.Conf.Enterprise {
		cluster.OpenSVCUnprovision()
	} else {
		cluster.LocalhostUnprovision()
	}
	cluster.Servers = nil
	cluster.slaves = nil
	cluster.master = nil
	cluster.vmaster = nil
	cluster.sme.UnDiscovered()
	cluster.newServerList()
	cluster.sme.RemoveFailoverState()
}

func (cluster *Cluster) UnprovisionProxyService(prx *Proxy) error {
	if cluster.Conf.Enterprise {
		cluster.OpenSVCUnprovisionProxyService(prx)
	} else {
		//		cluster.LocalhostUnprovisionProxyService(prx)
	}
	return nil
}

func (cluster *Cluster) UnprovisionDatabaseService(server *ServerMonitor) error {

	if cluster.Conf.Enterprise {
		cluster.OpenSVCUnprovisionDatabaseService(server)
	} else {
		cluster.LocalhostUnprovisionDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) RollingUpgrade() {
}

func (cluster *Cluster) StopDatabaseService(server *ServerMonitor) error {

	if cluster.Conf.Enterprise {
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
	cluster.LogPrintf(LvlInfo, "Starting Database service %s", server.Id)
	if cluster.Conf.Enterprise {
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
	cluster.Conf.Hosts = cluster.Conf.Hosts + "," + srv
	cluster.sme.SetFailoverState()
	cluster.newServerList()
	cluster.TopologyDiscover()
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) WaitFailoverEndState() {
	for cluster.sme.IsInFailover() {
		time.Sleep(time.Second)
		cluster.LogPrintf(LvlInfo, "Waiting for failover stopped.")
	}
	time.Sleep(recoverTime * time.Second)
}

func (cluster *Cluster) WaitFailoverEnd() error {
	cluster.WaitFailoverEndState()
	return nil

}

func (cluster *Cluster) WaitFailover(wg *sync.WaitGroup) {
	cluster.LogPrintf(LvlInfo, "Waiting failover end")
	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 15 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting failover end")
			exitloop++
		case <-cluster.failoverCond.Recv:
			cluster.LogPrintf(LvlInfo, "Failover end receive from channel failoverCond")
			return
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Failover end")
	} else {
		cluster.LogPrintf(LvlErr, "Failover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitSwitchover(wg *sync.WaitGroup) {

	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 15 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting switchover end")
			exitloop++
		case <-cluster.switchoverCond.Recv:
			return
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Switchover end")
	} else {
		cluster.LogPrintf(LvlErr, "Switchover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitRejoin(wg *sync.WaitGroup) {

	defer wg.Done()

	exitloop := 0

	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 15 {

		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting Rejoin")
			exitloop++
		case <-cluster.rejoinCond.Recv:
			return

		}

	}
	if exitloop < 15 {
		cluster.LogPrintf(LvlInfo, "Rejoin Finished")

	} else {
		cluster.LogPrintf(LvlErr, "Rejoin timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitClusterStop() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	cluster.LogPrintf(LvlInfo, "Waiting for cluster shutdown")
	for exitloop < 10 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for cluster shutdown")
			exitloop++
			// All cluster down
			if cluster.sme.IsInState("ERR00021") == true {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Cluster is shutdown")
	} else {
		cluster.LogPrintf(LvlErr, "Cluster shutdown timeout")
		return errors.New("Failed to stop the cluster")
	}
	return nil
}

func (cluster *Cluster) WaitProxyEqualMaster() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	cluster.LogPrintf(LvlInfo, "Waiting for proxy to join master")
	for exitloop < 60 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for proxy to join master")
			exitloop++
			// All cluster down
			if cluster.IsProxyEqualMaster() == true {
				exitloop = 100
			}
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Proxy can join master")
	} else {
		cluster.LogPrintf(LvlErr, "Proxy to join master timeout")
		return errors.New("Failed to join master via proxy")
	}
	return nil
}

func (cluster *Cluster) WaitMariaDBStop(server *ServerMonitor) error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting MariaDB shutdown")
			exitloop++
			_, err := os.FindProcess(server.Process.Pid)
			if err != nil {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "MariaDB shutdown")
	} else {
		cluster.LogPrintf(LvlInfo, "MariaDB shutdown timeout")
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
			cluster.LogPrintf(LvlInfo, "Waiting for database start %s", server.URL)
			exitloop++

			_, err := dbhelper.GetStatus(server.Conn)
			if err == nil {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Database started")
	} else {
		cluster.LogPrintf(LvlInfo, "Database start timeout")
		return errors.New("Failed to Start MariaDB")
	}
	return nil
}

func (cluster *Cluster) WaitBootstrapDiscovery() error {
	cluster.LogPrintf(LvlInfo, "Waiting Bootstrap and discovery")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting Bootstrap and discovery")
			exitloop++
			if cluster.sme.IsDiscovered() {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Cluster is Bootstraped and discovery")
	} else {
		cluster.LogPrintf(LvlErr, "Bootstrap timeout")
		return errors.New("Failed Bootstrap timeout")
	}
	return nil
}

func (cluster *Cluster) waitMasterDiscovery() error {
	cluster.LogPrintf(LvlInfo, "Waiting Master Found")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting Master Found")
			exitloop++
			if cluster.GetMaster() != nil {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "Master founded")
	} else {
		cluster.LogPrintf(LvlErr, "Master found timeout")
		return errors.New("Failed Master search timeout")
	}
	return nil
}

func (cluster *Cluster) AllDatabaseCanConn() bool {
	for _, s := range cluster.Servers {
		if s.State == stateFailed {
			return false
		}
	}
	return true
}

func (cluster *Cluster) waitDatabaseCanConn() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)

	cluster.LogPrintf(LvlInfo, "Waiting for databases to start")
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for databases to start")
			exitloop++
			if cluster.AllDatabaseCanConn() && cluster.IsProvision() {
				exitloop = 100
			}

		}
	}
	if exitloop == 100 {
		cluster.LogPrintf(LvlInfo, "All databases can connect")
	} else {
		cluster.LogPrintf(LvlErr, "Timeout waiting for database to be connected")
		return errors.New("Connections to databases failure")
	}
	return nil
}

func (cluster *Cluster) BootstrapReplicationCleanup() error {

	cluster.LogPrintf(LvlInfo, "Cleaning up replication on existing servers")
	cluster.sme.SetFailoverState()
	for _, server := range cluster.Servers {
		err := server.Refresh()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Refresh failed in Cleanup on server %s %s", server.URL, err)
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "SetDefaultMasterConn on server %s ", server.URL)
		}
		err = dbhelper.SetDefaultMasterConn(server.Conn, cluster.Conf.MasterConn)
		if err != nil {
			if cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "RemoveFailoverState on server %s ", server.URL)
			}
			cluster.sme.RemoveFailoverState()
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "ResetMaster on server %s ", server.URL)
		}
		err = dbhelper.ResetMaster(server.Conn)
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "Stop all slaves or stop slave %s ", server.URL)
		}
		if server.DBVersion.IsMariaDB() {
			err = dbhelper.StopAllSlaves(server.Conn)
		} else {
			err = server.StopSlave()
		}
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "Reset all slaves", server.URL)
		}
		err = dbhelper.ResetAllSlaves(server.Conn)
		if err != nil {
			cluster.sme.RemoveFailoverState()
			return err
		}
		if server.DBVersion.IsMariaDB() {
			if cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "SET GLOBAL gtid_slave_pos='' on %s", server.URL)
			}
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos=''")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
		}
	}
	cluster.master = nil
	cluster.vmaster = nil
	cluster.slaves = nil
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) BootstrapReplication() error {

	// default to master slave
	var err error

	if cluster.Conf.MultiMasterWsrep {
		cluster.LogPrintf(LvlInfo, "Galera cluster ignoring replication setup")
		return nil
	}
	if cluster.CleanAll {
		err := cluster.BootstrapReplicationCleanup()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cleanup error %s", err)
		}
	}
	for _, server := range cluster.Servers {
		if server.State == stateFailed {
			continue
		} else {
			server.Refresh()
		}
	}
	err = cluster.TopologyDiscover()
	if err == nil {
		return errors.New("Environment already has an existing master/slave setup")
	}

	cluster.sme.SetFailoverState()
	masterKey := 0
	if cluster.Conf.PrefMaster != "" {
		masterKey = func() int {
			for k, server := range cluster.Servers {
				if server.URL == cluster.Conf.PrefMaster {
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
	//	_, err = cluster.Servers[masterKey].Conn.Exec("RESET MASTER")
	//	if err != nil {
	//		cluster.LogPrintf(LvlInfo, "RESET MASTER failed on master"
	//	}
	// Assume master-slave if nothing else is declared && mariadb >10
	if cluster.Conf.MultiMasterRing == false && cluster.Conf.MultiMaster == false && cluster.Conf.MxsBinlogOn == false && cluster.Conf.MultiTierSlave == false {

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()
				continue
			} else {
				var hasMyGTID bool
				hasMyGTID, err = dbhelper.HasMySQLGTID(server.Conn)

				if server.State != stateFailed && cluster.Conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
					cluster.Servers[masterKey].Refresh()
					_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + cluster.Servers[masterKey].CurrentGtid.Sprint() + "\"")
					if err != nil {
						return err
					}
					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.Servers[masterKey].Host,
						Port:      cluster.Servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
						Mode:      "SLAVE_POS",
					})
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with %s as master", cluster.Servers[masterKey].URL)
				} else if hasMyGTID {

					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.Servers[masterKey].Host,
						Port:      cluster.Servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
						Mode:      "MASTER_AUTO_POSITION",
					})
					//  Missing  multi source cluster.Conf.MasterConn
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with MySQL GTID replication style and %s as master", cluster.Servers[masterKey].URL)

				} else {
					//*ss, errss := cluster.Servers[masterKey].GetSlaveStatus(cluster.Servers[masterKey].ReplicationSourceName)

					err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:      cluster.Servers[masterKey].Host,
						Port:      cluster.Servers[masterKey].Port,
						User:      cluster.rplUser,
						Password:  cluster.rplPass,
						Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
						Mode:      "POSITIONAL",
						Logfile:   cluster.Servers[masterKey].BinaryLogFile,
						Logpos:    cluster.Servers[masterKey].BinaryLogPos,
					})

					//  Missing  multi source cluster.Conf.MasterConn
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with old replication style and %s as master", cluster.Servers[masterKey].URL)

				}
				if err != nil {
					cluster.LogPrintf(LvlErr, "Replication can't be bootstarp for server %s with %s as master: %s ", server.URL, cluster.Servers[masterKey].URL, err)
				}
				if server.State != stateFailed && cluster.Conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
					_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
				} else {
					_, err = server.Conn.Exec("START SLAVE")
				}

				if err != nil {
					cluster.LogPrintf(LvlErr, "Replication can't be bootstrap for server %s with %s as master: %s ", server.URL, cluster.Servers[masterKey].URL, err)
				}
				server.SetReadOnly()
			}

		}
	}
	// Slave Relay
	if cluster.Conf.MultiTierSlave == true {
		masterKey = 0
		relaykey := 1
		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()
				continue
			} else {
				dbhelper.StopAllSlaves(server.Conn)
				dbhelper.ResetAllSlaves(server.Conn)

				if relaykey == key {
					stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.Conf.MasterConn, cluster.Servers[masterKey].Host, cluster.Servers[masterKey].Port, cluster.rplUser, cluster.rplPass, cluster.Conf.MasterConnectRetry, 1)
					_, err := server.Conn.Exec(stmt)
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln(stmt, err))
					}
					_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln("Can't start slave: ", err))
					}
				} else {
					stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.Conf.MasterConn, cluster.Servers[relaykey].Host, cluster.Servers[relaykey].Port, cluster.rplUser, cluster.rplPass, cluster.Conf.MasterConnectRetry, 1)
					_, err := server.Conn.Exec(stmt)
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln(stmt, err))
					}
					_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return errors.New(fmt.Sprintln("Can't start slave: ", err))
					}

				}
				server.SetReadOnly()
			}
		}
		cluster.LogPrintf(LvlInfo, "Environment bootstrapped with %s as master", cluster.Servers[masterKey].URL)
	}
	// Multi Master
	if cluster.Conf.MultiMaster == true {
		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == 0 {

				stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.Conf.MasterConn, cluster.Servers[1].Host, cluster.Servers[1].Port, cluster.rplUser, cluster.rplPass, cluster.Conf.MasterConnectRetry, 1)
				_, err := server.Conn.Exec(stmt)
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln(stmt, err))
				}
				_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln("Can't start slave: ", err))
				}
				server.SetReadOnly()
			}
			if key == 1 {

				stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.Conf.MasterConn, cluster.Servers[0].Host, cluster.Servers[0].Port, cluster.rplUser, cluster.rplPass, cluster.Conf.MasterConnectRetry, 1)
				_, err := server.Conn.Exec(stmt)
				if err != nil {
					cluster.sme.RemoveFailoverState()

					return errors.New(fmt.Sprintln("ERROR:", stmt, err))
				}
				_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return errors.New(fmt.Sprintln("Can't start slave: ", err))
				}
			}
			server.SetReadOnly()
		}
	}
	// Ring
	if cluster.Conf.MultiMasterRing == true {
		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			i := (len(cluster.Servers) + key - 1) % len(cluster.Servers)
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + cluster.Servers[i].CurrentGtid.Sprint() + "\"")
			if err != nil {
				cluster.LogPrintf(LvlErr, "Replication bootstrap failed for setting gtid %s", cluster.Servers[i].CurrentGtid.Sprint())
				return err
			}
			stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=slave_pos, master_connect_retry=%d, master_heartbeat_period=%d", cluster.Servers[i].ReplicationSourceName, cluster.Servers[i].Host, cluster.Servers[i].Port, cluster.rplUser, cluster.rplPass, cluster.Conf.MasterConnectRetry, 1)
			_, err := server.Conn.Exec(stmt)
			if err != nil {
				cluster.sme.RemoveFailoverState()
				cluster.LogPrintf(LvlErr, "Bootstrap Relication error %s %s", stmt, err)

				return errors.New(fmt.Sprintln(stmt, err))
			}
			_, err = server.Conn.Exec("START SLAVE '" + cluster.Conf.MasterConn + "'")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return errors.New(fmt.Sprintln("Can't start slave: ", err))
			}
			cluster.vmaster = cluster.Servers[0]

		}
	}
	cluster.sme.RemoveFailoverState()
	// speed up topology discovery
	cluster.TopologyDiscover()

	//bootstrapChan <- true
	return nil
}
