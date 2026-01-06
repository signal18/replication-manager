// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// Constants for restart RID validation
const (
	RestartRidJobsContainer = "container#jobs"
)

// validateRestartRid validates the resource ID parameter for restart operations
// Only container#jobs is allowed for targeted restarts
func validateRestartRid(rid string) error {
	if rid != "" && rid != RestartRidJobsContainer {
		return fmt.Errorf("invalid rid '%s': only '%s' is allowed for restart", rid, RestartRidJobsContainer)
	}
	return nil
}

// Bootstrap provisions && setup topology
func (cluster *Cluster) Bootstrap() error {
	var err error
	// create service template and post
	err = cluster.ProvisionServices()
	if err != nil {
		return err
	}

	err = cluster.BootstrapReplication(true)
	if err != nil {
		return err
	}

	if cluster.GetSponsorEmail() != "" {
		err = cluster.CreateDBUserFromConfig("sponsor")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot create sponsor user: %s", err)
		}
	}

	if cluster.Conf.Test {
		//cluster.initProxies()
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
		if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
			for _, server := range cluster.Servers {
				server.JobsCreateTable()
			}
		}
	}
	return nil
}

func (cluster *Cluster) ProvisionServices() error {
	hasConfigPath := make(map[string]bool)
	cluster.StateMachine.SetFailoverState()
	// delete the cluster state here
	path := cluster.WorkingDir + ".json"
	os.Remove(path)
	cluster.ResetCrashes()
	for _, server := range cluster.Servers {
		hasConfigPath[server.HostCnf] = server.HasConfigPathCookie()
		server.DelConfigPathCookie() // remove the config path cookie since we are going to provision it
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionDatabaseService(server)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseProvisionDatabaseService(server)

		default:

		}
		cluster.ProvisionDatabaseScript(server)
		if cluster.GetConf().ProvSerialized {
			server.WaitDatabaseStart()
		}

	}

	for _, server := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+server.Name)

				if hasConfigPath[server.HostCnf] {
					server.SetConfigPathCookie() // revert the config path cookie since error occured
				}
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+server.Name)
				server.SetProvisionCookie()
				server.DelReprovisionCookie()
				server.DelRestartCookie()
			}
		}
	}
	err := cluster.WaitDatabaseCanConn()
	if err != nil {
		return err
	}
	for _, prx := range cluster.Proxies {
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionProxyService(prx)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseProvisionProxyService(prx)
		default:

		}
		cluster.ProvisionProxyScript(prx)
	}
	for _, pri := range cluster.Proxies {
		prx, ok := pri.(*Proxy)
		if !ok {
			continue
		}
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Provisionning proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Provisionning done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
				prx.SetProvisionCookie()
			}
		}
	}

	cluster.StateMachine.RemoveFailoverState()

	return nil

}

func (cluster *Cluster) InitDatabaseService(server *ServerMonitor) error {
	cluster.StateMachine.SetFailoverState()
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseProvisionDatabaseService(server)
	default:
		cluster.StateMachine.RemoveFailoverState()
		return nil
	}
	cluster.ProvisionDatabaseScript(server)
	select {
	case err := <-cluster.errorChan:
		cluster.StateMachine.RemoveFailoverState()
		if err == nil {
			server.SetProvisionCookie()
		} else {
			return err
		}
	}

	return nil
}

func (cluster *Cluster) InitProxyService(prx DatabaseProxy) error {
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionProxyService(prx)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseProvisionProxyService(prx)
	default:
		return nil
	}
	cluster.ProvisionProxyScript(prx)
	select {
	case err := <-cluster.errorChan:
		cluster.StateMachine.RemoveFailoverState()
		if err == nil {
			prx.SetProvisionCookie()
		} else {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) InitAppService(app *App) error {
	if app != nil {
		app.ApplyPlannedCredits()
	}

	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionAppService(app)
	default:
		return nil
	}
	// cluster.ProvisionAppScript(app)
	select {
	case err := <-cluster.errorChan:
		cluster.StateMachine.RemoveFailoverState()
		if err == nil {
			app.SetProvisionCookie()
		} else {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) Unprovision() error {

	cluster.StateMachine.SetFailoverState()
	// Unprovision proxies first, since they are dependent on databases
	for _, prx := range cluster.Proxies {
		/*	prx, ok := pri.(*Proxy)
			if !ok {
				continue
			}*/
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionProxyService(prx)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseUnprovisionProxyService(prx)
		default:

		}
		cluster.UnprovisionProxyScript(prx)
	}
	for _, prx := range cluster.Proxies {
		/*	prx, ok := pri.(*Proxy)
			if !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Unprovision proxy continue ")
				continue
			}*/
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unprovision proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Unprovision done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
				prx.DelProvisionCookie()
				prx.DelRestartCookie()
				prx.DelReprovisionCookie()
			}
		}
	}

	for _, server := range cluster.Servers {
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionDatabaseService(server)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseUnprovisionDatabaseService(server)
		default:
		}
		cluster.UnprovisionDatabaseScript(server)
	}
	for _, server := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unprovision error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Unprovision done for database %s", cluster.Name+"/svc/"+server.Name)
				server.DelProvisionCookie()
				server.DelRestartCookie()
				server.DelReprovisionCookie()
				server.DelConfigPathCookie()
			}
		}
	}
	err := cluster.WaitClusterStop()
	if err == nil {
		cluster.ResetStates()
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to wait for all databases down : %s", err)
	}
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		cluster.OpenSVCUnprovisionSecret()
	default:
	}

	return nil
}

func (cluster *Cluster) UnprovisionProxyService(prx DatabaseProxy) error {
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostUnprovisionProxyService(prx)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseUnprovisionProxyService(prx)
	default:
	}
	cluster.UnprovisionProxyScript(prx)
	select {
	case err := <-cluster.errorChan:
		if err == nil {
			prx.DelProvisionCookie()
			prx.DelReprovisionCookie()
			prx.DelRestartCookie()
		}
		return err
	}
	return nil
}

func (cluster *Cluster) UnprovisionDatabaseService(server *ServerMonitor) error {
	cluster.ResetCrashes()
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseUnprovisionDatabaseService(server)
	default:
		go cluster.LocalhostUnprovisionDatabaseService(server)
	}
	cluster.UnprovisionDatabaseScript(server)
	select {

	case err := <-cluster.errorChan:
		if err == nil {
			server.DelProvisionCookie()
			server.DelReprovisionCookie()
			server.DelRestartCookie()
		} else {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) RollingUpgrade() {
}

func (cluster *Cluster) StopDatabaseService(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping database service %s", cluster.Name+"/svc/"+server.URL)
	var err error

	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStopDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStopDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStopDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStopDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.OnPremiseStopDatabaseService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StopDatabaseScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	return err
}

func (cluster *Cluster) StopProxyService(server DatabaseProxy) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error

	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStopProxyService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStopProxyService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStopProxyService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStopProxyService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStopProxyService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StopProxyScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	return err
}

func (cluster *Cluster) StartProxyService(server DatabaseProxy) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStartProxyService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStartProxyService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStartProxyService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStartProxyService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStartProxyService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StartProxyScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	return err
}

func (cluster *Cluster) ShutdownDatabase(server *ServerMonitor) error {
	_, err := server.Conn.Exec("SHUTDOWN")
	server.DelRestartCookie()
	return err
}

func (cluster *Cluster) StartDatabaseService(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting Database service %s", cluster.Name+"/svc/"+server.Name)
	var err error
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStartDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStartDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStartDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStartDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStartDatabaseService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StartDatabaseScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	server.SetConfigRefreshCookie()
	return err
}

func (cluster *Cluster) RestartDatabaseService(server *ServerMonitor, node string, rid string) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restarting Database service %s", cluster.Name+"/svc/"+server.Name)
	var err error
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCRestartDatabaseService(server, node, rid)
	default:
		return errors.New("Restart not supported for this orchestrator yet")
	}
	if err == nil {
		server.DelRestartCookie()
		// Clear stored parameters
		server.RestartNode = ""
		server.RestartRid = ""
	}
	return err
}

func (cluster *Cluster) StartAllNodes() error {

	return nil
}

func (cluster *Cluster) BootstrapReplicationCleanup() error {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cleaning up replication on existing servers")
	cluster.StateMachine.SetFailoverState()
	for _, server := range cluster.Servers {
		err := server.Refresh()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Refresh failed in Cleanup on server %s %s", server.URL, err)
			cluster.StateMachine.RemoveFailoverState()
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SetDefaultMasterConn on server %s ", server.URL)
		}
		logs, err := dbhelper.SetDefaultMasterConn(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlDbg, "BootstrapReplicationCleanup %s %s ", server.URL, err)
		if err != nil {
			if cluster.Conf.Verbose {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "RemoveFailoverState on server %s ", server.URL)
			}
			continue
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reset Master on server %s ", server.URL)

		logs, err = dbhelper.ResetMaster(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Reset Master on server %s %s", server.URL, err)
		if cluster.Conf.Verbose {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop all slaves or stop slave %s ", server.URL)
		}
		if server.DBVersion.IsMariaDB() {
			logs, err = dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
		} else {
			logs, err = server.StopSlave()
		}
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Stop all slaves or just slave %s %s", server.URL, err)

		if server.DBVersion.IsMariaDB() {
			if cluster.Conf.Verbose {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SET GLOBAL gtid_slave_pos='' on %s", server.URL)
			}
			logs, err := dbhelper.SetGTIDSlavePos(server.Conn, "")
			cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Can reset GTID slave pos %s %s", server.URL, err)
		}

		// reset all replication if go to master-slave
		if !cluster.Conf.MultiTierSlave && !cluster.Conf.MultiMaster && !cluster.Conf.MultiMasterRing && !cluster.Conf.MultiMasterGrouprep && !cluster.Conf.MultiMasterWsrep {
			server.ResetSlave()
		}

	}
	cluster.master = nil
	cluster.vmaster = nil
	cluster.slaves = nil
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) BootstrapReplication(clean bool) error {

	// default to master slave
	var err error
	oldMaster := cluster.GetMaster()

	if cluster.Conf.MultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Galera cluster ignoring replication setup")
		return nil
	}
	if clean {
		err := cluster.BootstrapReplicationCleanup()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cleanup error %s", err)
		}
	}
	for _, server := range cluster.Servers {
		if server.State == stateFailed {
			continue
		} else {
			// Cleanup relay and vmaster state
			server.IsRelay = false
			server.IsVirtualMaster = false
			server.Refresh()
		}
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	err = cluster.TopologyDiscover(wg)
	wg.Wait()
	if err == nil {
		return errors.New("Environment already has an existing master/slave setup")
	}

	cluster.StateMachine.SetFailoverState()
	masterKey := 0

	masterKey = func() int {
		for k, server := range cluster.Servers {
			// Skip child cluster
			if server.SourceClusterName != cluster.Name {
				continue
			}
			if oldMaster != nil {
				if server == oldMaster {
					cluster.StateMachine.RemoveFailoverState()
					return k
				}
			} else if cluster.Conf.PrefMaster != "" {
				if server.IsPrefered() {
					cluster.StateMachine.RemoveFailoverState()
					return k
				}
			}
		}
		cluster.StateMachine.RemoveFailoverState()
		if cluster.Conf.PrefMaster != "" {
			return -1
		}

		return 0
	}()

	if masterKey == -1 {
		return errors.New("Preferred master could not be found in existing servers")
	}

	// Assume master-slave if nothing else is declared
	if cluster.Conf.ActivePassive == false && cluster.Conf.MultiMasterRing == false && cluster.Conf.MultiMaster == false && cluster.Conf.MxsBinlogOn == false && cluster.Conf.MultiTierSlave == false {

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()

				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
					}
				}

				if cluster.Conf.MultiMasterGrouprep {
					// All clsuter node need a specialreplicaton for recovery parameters are not important as leader host is not needed
					server.ChangeMasterTo(server, "CURRENT_POS")
					server.BootstrapGroupReplication()
				}
				continue
			} else {
				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
					}
				}

				if cluster.Conf.MultiMasterGrouprep {
					err = server.ChangeMasterTo(server, "SLAVE_POS")
					server.StartGroupReplication()
				} else {
					err = server.ChangeMasterTo(cluster.Servers[masterKey], "SLAVE_POS")
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
			}

		}
	}
	// Slave Relay
	if cluster.Conf.ActivePassive == false && cluster.Conf.MultiTierSlave == true {
		relaykey := 1
		if masterKey == 1 {
			relaykey = 0
		}
		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()
				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
					}
				}
				continue
			} else {
				dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
				dbhelper.ResetAllSlaves(server.Conn, server.DBVersion)

				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
					}
				}

				if relaykey == key {
					err = server.ChangeMasterTo(cluster.Servers[masterKey], "CURRENT_POS")
					if err != nil {
						cluster.StateMachine.RemoveFailoverState()
						return err
					}

				} else {
					err = server.ChangeMasterTo(cluster.Servers[relaykey], "CURRENT_POS")
					if err != nil {
						cluster.StateMachine.RemoveFailoverState()
						return err
					}
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}

			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Environment bootstrapped with %s as master", cluster.Servers[masterKey].URL)
	}
	// Multi Master
	if cluster.Conf.ActivePassive == false && cluster.Conf.MultiMaster == true {
		for _, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
				}
			}
		}

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == 0 {
				err = server.ChangeMasterTo(cluster.Servers[1], "CURRENT_POS")
				if err != nil {
					cluster.StateMachine.RemoveFailoverState()
					return err
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
			}
			if key == 1 {
				err = server.ChangeMasterTo(cluster.Servers[0], "CURRENT_POS")
				if err != nil {
					cluster.StateMachine.RemoveFailoverState()
					return err
				}
			}
			if !server.ClusterGroup.IsInIgnoredReadonly(server) {
				server.SetReadOnly()
			}
		}
	}
	// Ring
	if cluster.Conf.ActivePassive == false && cluster.Conf.MultiMasterRing == true {
		for _, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
				}
			}
		}

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}

			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
				}
			}

			i := (len(cluster.Servers) + key - 1) % len(cluster.Servers)
			err = server.ChangeMasterTo(cluster.Servers[i], "SLAVE_POS")
			if err != nil {
				cluster.StateMachine.RemoveFailoverState()
				return err
			}

			cluster.vmaster = cluster.Servers[0]

		}
	}
	cluster.StateMachine.RemoveFailoverState()
	// speed up topology discovery
	wg.Add(1)
	cluster.TopologyDiscover(wg)
	wg.Wait()

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

func (cluster *Cluster) GetProxyAgent(server DatabaseProxy) (Agent, error) {
	var agent Agent
	agents := strings.Split(cluster.Conf.ProvProxAgents, ",")
	if len(agents) == 0 {
		return agent, errors.New("No databases agent list provided")
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.GetId() {
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
	cluster.Lock()
	defer cluster.Unlock()
	for _, node := range cluster.Agents {
		if name == node.HostName {
			return node, nil
		}
	}
	return node, errors.New("Agent not found in orechestrator node list")
}

func (cluster *Cluster) ProvisionRotatePasswords(password string) error {
	if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		svc := cluster.OpenSVCConnect()
		err := svc.CreateSecretKeyValueV2(cluster.Name, "env", "MYSQL_ROOT_PASSWORD", password)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ProvisionRotatePasswords error: Can not add key to secret: %s %s ", "MYSQL_ROOT_PASSWORD", err)
		}
	}
	return nil
}

func (cluster *Cluster) ReloadOpenSVCDaemonNodeStats() error {
	if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		svc := cluster.OpenSVCConnect()
		stats, err := svc.GetDaemonNodeStats()
		if err != nil {
			return err
		}

		cluster.OpenSVCStats.Swap(stats)
	}
	return nil
}
