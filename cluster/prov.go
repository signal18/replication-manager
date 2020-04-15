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
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Bootstrap provisions && setup topology
func (cluster *Cluster) Bootstrap() error {
	var err error
	// create service template and post
	err = cluster.ProvisionServices()
	if err != nil {
		return err
	}
	err = cluster.WaitDatabaseCanConn()
	if err != nil {
		return err
	}

	err = cluster.BootstrapReplication(true)
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

	cluster.sme.SetFailoverState()
	// delete the cluster state here
	path := cluster.WorkingDir + ".json"
	os.Remove(path)
	cluster.ResetCrashes()
	for _, server := range cluster.Servers {
		switch cluster.Conf.ProvOrchestrator {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionDatabaseService(server)
		default:
			cluster.sme.RemoveFailoverState()
			return nil
		}
	}
	for _, server := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+server.Name)
				server.SetProvisionCookie()
			}
		}
	}
	for _, prx := range cluster.Proxies {
		switch cluster.Conf.ProvOrchestrator {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionProxyService(prx)
		default:
			cluster.sme.RemoveFailoverState()
			return nil
		}
	}
	for _, prx := range cluster.Proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for proxy %s", cluster.Name+"/svc/"+prx.Name)
				prx.SetProvisionCookie()
			}
		}
	}

	cluster.sme.RemoveFailoverState()

	return nil

}

func (cluster *Cluster) InitDatabaseService(server *ServerMonitor) error {
	cluster.sme.SetFailoverState()
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionDatabaseService(server)
	default:
		cluster.sme.RemoveFailoverState()
		return nil
	}
	select {
	case err := <-cluster.errorChan:
		cluster.sme.RemoveFailoverState()
		if err == nil {
			server.SetProvisionCookie()
		} else {
		}
		return err
	}

	return nil
}

func (cluster *Cluster) InitProxyService(prx *Proxy) error {
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionProxyService(prx)
	default:
		return nil
	}
	select {
	case err := <-cluster.errorChan:
		cluster.sme.RemoveFailoverState()
		if err == nil {
			prx.SetProvisionCookie()
		}
		return err
	}
	return nil
}

func (cluster *Cluster) Unprovision() error {

	cluster.sme.SetFailoverState()
	for _, server := range cluster.Servers {
		switch cluster.Conf.ProvOrchestrator {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionDatabaseService(server)
		default:
			cluster.sme.RemoveFailoverState()
			return nil
		}
	}
	for _, server := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovision error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovision done for database %s", cluster.Name+"/svc/"+server.Name)
				server.DelProvisionCookie()
			}
		}
	}
	for _, prx := range cluster.Proxies {
		switch cluster.Conf.ProvOrchestrator {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionProxyService(prx)
		default:
			cluster.sme.RemoveFailoverState()
			return nil
		}
	}
	for _, prx := range cluster.Proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovision proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovision done for proxy %s", cluster.Name+"/svc/"+prx.Name)
				prx.DelProvisionCookie()
			}
		}
	}

	cluster.slaves = nil
	cluster.master = nil
	cluster.vmaster = nil
	cluster.IsAllDbUp = false
	cluster.sme.UnDiscovered()
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) UnprovisionProxyService(prx *Proxy) error {
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostUnprovisionProxyService(prx)
	default:
	}
	select {
	case err := <-cluster.errorChan:
		if err == nil {
			prx.DelProvisionCookie()
		}
		return err
	}
	return nil
}

func (cluster *Cluster) UnprovisionDatabaseService(server *ServerMonitor) error {
	cluster.ResetCrashes()
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionDatabaseService(server)
	default:
		go cluster.LocalhostUnprovisionDatabaseService(server)
	}
	select {

	case err := <-cluster.errorChan:
		if err == nil {
			server.DelProvisionCookie()
		}
		return err
	}
	return nil
}

func (cluster *Cluster) RollingUpgrade() {
}

func (cluster *Cluster) StopDatabaseService(server *ServerMonitor) error {

	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCStopDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		cluster.K8SStopDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		cluster.SlapOSStopDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		server.JobServerStop()
	default:
		return cluster.LocalhostStopDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) StopProxyService(server *Proxy) error {

	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCStopProxyService(server)
	case config.ConstOrchestratorKubernetes:
		cluster.K8SStopProxyService(server)
	case config.ConstOrchestratorSlapOS:
		cluster.SlapOSStopProxyService(server)
	default:
		return cluster.LocalhostStopProxyService(server)
	}
	return nil
}

func (cluster *Cluster) StartProxyService(server *Proxy) error {

	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCStartProxyService(server)
	case config.ConstOrchestratorKubernetes:
		cluster.K8SStartProxyService(server)
	case config.ConstOrchestratorSlapOS:
		cluster.SlapOSStartProxyService(server)
	default:
		return cluster.LocalhostStartProxyService(server)
	}
	return nil
}

func (cluster *Cluster) ShutdownDatabase(server *ServerMonitor) error {
	_, err := server.Conn.Exec("SHUTDOWN")
	return err
}

func (cluster *Cluster) StartDatabaseService(server *ServerMonitor) error {
	cluster.LogPrintf(LvlInfo, "Starting Database service %s", cluster.Name+"/svc/"+server.Name)
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCStartDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		cluster.K8SStartDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		cluster.SlapOSStartDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		server.JobServerStart()
	default:
		return cluster.LocalhostStartDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) GetOchestaratorPlacement(server *ServerMonitor) error {
	cluster.LogPrintf(LvlInfo, "Starting Database service %s", cluster.Name+"/svc/"+server.Name)
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCStartDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		cluster.K8SStartDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		cluster.SlapOSStartDatabaseService(server)
	default:
		return cluster.LocalhostStartDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) StartAllNodes() error {

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
		logs, err := dbhelper.SetDefaultMasterConn(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", LvlDbg, "BootstrapReplicationCleanup %s %s ", server.URL, err)
		if err != nil {
			if cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "RemoveFailoverState on server %s ", server.URL)
			}
			continue
		}

		cluster.LogPrintf(LvlInfo, "Reset Master on server %s ", server.URL)

		logs, err = dbhelper.ResetMaster(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", LvlErr, "Reset Master on server %s %s", server.URL, err)
		if cluster.Conf.Verbose {
			cluster.LogPrintf(LvlInfo, "Stop all slaves or stop slave %s ", server.URL)
		}
		if server.DBVersion.IsMariaDB() {
			logs, err = dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
		} else {
			logs, err = server.StopSlave()
		}
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", LvlErr, "Stop all slaves or just slave %s %s", server.URL, err)

		if server.DBVersion.IsMariaDB() {
			if cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "SET GLOBAL gtid_slave_pos='' on %s", server.URL)
			}
			logs, err := dbhelper.SetGTIDSlavePos(server.Conn, "")
			cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", LvlErr, "Can reset GTID slave pos %s %s", server.URL, err)
		}

	}
	cluster.master = nil
	cluster.vmaster = nil
	cluster.slaves = nil
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) BootstrapReplication(clean bool) error {

	// default to master slave
	var err error
	logs := ""

	if cluster.Conf.MultiMasterWsrep {
		cluster.LogPrintf(LvlInfo, "Galera cluster ignoring replication setup")
		return nil
	}
	if clean {
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
				if server.IsPrefered() {
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
	// Assume master-slave if nothing else is declared
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
				// A slave
				hasMyGTID := server.HasMySQLGTID()
				//mariadb
				if server.State != stateFailed && cluster.Conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
					cluster.Servers[masterKey].Refresh()
					_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + cluster.Servers[masterKey].CurrentGtid.Sprint() + "\"")
					if err != nil {
						return err
					}
					logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:        cluster.Servers[masterKey].Host,
						Port:        cluster.Servers[masterKey].Port,
						User:        cluster.rplUser,
						Password:    cluster.rplPass,
						Retry:       strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat:   strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
						Mode:        "SLAVE_POS",
						Channel:     cluster.Conf.MasterConn,
						PostgressDB: server.PostgressDB,
					}, server.DBVersion)
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with %s as master", cluster.Servers[masterKey].URL)
				} else if hasMyGTID && cluster.Conf.ForceSlaveNoGtid == false {

					logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:        cluster.Servers[masterKey].Host,
						Port:        cluster.Servers[masterKey].Port,
						User:        cluster.rplUser,
						Password:    cluster.rplPass,
						Retry:       strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat:   strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
						Mode:        "MASTER_AUTO_POSITION",
						Channel:     cluster.Conf.MasterConn,
						PostgressDB: server.PostgressDB,
					}, server.DBVersion)
					//  Missing  multi source cluster.Conf.MasterConn
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with MySQL GTID replication style and %s as master", cluster.Servers[masterKey].URL)

				} else {
					//*ss, errss := cluster.Servers[masterKey].GetSlaveStatus(cluster.Servers[masterKey].ReplicationSourceName)

					logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
						Host:        cluster.Servers[masterKey].Host,
						Port:        cluster.Servers[masterKey].Port,
						User:        cluster.rplUser,
						Password:    cluster.rplPass,
						Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
						Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
						Mode:        "POSITIONAL",
						Logfile:     cluster.Servers[masterKey].BinaryLogFile,
						Logpos:      cluster.Servers[masterKey].BinaryLogPos,
						Channel:     cluster.Conf.MasterConn,
						PostgressDB: server.PostgressDB,
					}, server.DBVersion)

					//  Missing  multi source cluster.Conf.MasterConn
					cluster.LogPrintf(LvlInfo, "Environment bootstrapped with old replication style and %s as master", cluster.Servers[masterKey].URL)

				}
				cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", LvlErr, "Replication can't be bootstrap for server %s with %s as master: %s ", server.URL, cluster.Servers[masterKey].URL, err)
				if err != nil {

				} else if !server.IsDown() {
					logs, err = server.StartSlave()
					cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", LvlErr, "Replication can't be bootstrap for server %s with %s as master: %s ", server.URL, cluster.Servers[masterKey].URL, err)
				}

				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
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
				dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
				dbhelper.ResetAllSlaves(server.Conn, server.DBVersion)

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
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
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
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
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
			if !server.ClusterGroup.IsInIgnoredReadonly(server) {
				server.SetReadOnly()
			}
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

func (cluster *Cluster) GetDatabaseAgent(server *ServerMonitor) (Agent, error) {
	var agent Agent
	agents := strings.Split(cluster.Conf.ProvAgents, ",")
	if len(agents) == 0 {
		return agent, errors.New("No databases agent list provided")
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			agentName := agents[i%len(agents)]
			agent, err := cluster.GetAgentInOrchetrator(agentName)
			if err != nil {
				return agent, err
			} else {
				return agent, nil
			}
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

func (cluster *Cluster) GetProxyAgent(server *Proxy) (Agent, error) {
	var agent Agent
	agents := strings.Split(cluster.Conf.ProvProxAgents, ",")
	if len(agents) == 0 {
		return agent, errors.New("No databases agent list provided")
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			agentName := agents[i%len(agents)]
			agent, err := cluster.GetAgentInOrchetrator(agentName)
			if err != nil {
				return agent, err
			} else {
				return agent, nil
			}
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

func (cluster *Cluster) GetAgentInOrchetrator(name string) (Agent, error) {
	var node Agent
	for _, node := range cluster.Agents {
		if name == node.HostName {
			return node, nil
		}
	}
	return node, errors.New("Agent not found in orechestrator node list")
}
