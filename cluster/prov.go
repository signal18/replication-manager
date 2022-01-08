// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"os"
	"strings"
	"sync"

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
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseProvisionDatabaseService(server)

		default:

		}
		cluster.ProvisionDatabaseScript(server)
	}

	for _, server := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+server.Name)
				server.SetProvisionCookie()
				server.DelReprovisionCookie()
				server.DelRestartCookie()
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
				cluster.LogPrintf(LvlErr, "Provisionning proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
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
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseProvisionDatabaseService(server)
	default:
		cluster.sme.RemoveFailoverState()
		return nil
	}
	cluster.ProvisionDatabaseScript(server)
	select {
	case err := <-cluster.errorChan:
		cluster.sme.RemoveFailoverState()
		if err == nil {
			server.SetProvisionCookie()
		} else {
			return err
		}
	}

	return nil
}

func (cluster *Cluster) InitProxyService(prx DatabaseProxy) error {
	switch cluster.Conf.ProvOrchestrator {
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
		cluster.sme.RemoveFailoverState()
		if err == nil {
			prx.SetProvisionCookie()
		} else {
			return err
		}
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
				cluster.LogPrintf(LvlErr, "Unprovision error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovision done for database %s", cluster.Name+"/svc/"+server.Name)
				server.DelProvisionCookie()
				server.DelRestartCookie()
				server.DelReprovisionCookie()
			}
		}
	}
	cluster.slaves = nil
	cluster.master = nil
	cluster.vmaster = nil
	cluster.IsAllDbUp = false
	cluster.sme.RemoveFailoverState()

	for _, prx := range cluster.Proxies {
		/*	prx, ok := pri.(*Proxy)
			if !ok {
				continue
			}*/
		switch cluster.Conf.ProvOrchestrator {
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
				cluster.LogPrintf(LvlErr, "Unprovision proxy continue ")
				continue
			}*/
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovision proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovision done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
				prx.DelProvisionCookie()
				prx.DelRestartCookie()
				prx.DelReprovisionCookie()
			}
		}
	}
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		cluster.OpenSVCUnprovisionSecret()
	default:
	}

	return nil
}

func (cluster *Cluster) UnprovisionProxyService(prx DatabaseProxy) error {
	switch cluster.Conf.ProvOrchestrator {
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
	switch cluster.Conf.ProvOrchestrator {
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
	cluster.LogPrintf(LvlInfo, "Stopping database service %s", cluster.Name+"/svc/"+server.URL)
	var err error

	switch cluster.Conf.ProvOrchestrator {
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
	cluster.LogPrintf(LvlInfo, "Stopping Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error

	switch cluster.Conf.ProvOrchestrator {
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
	cluster.LogPrintf(LvlInfo, "Starting Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error
	switch cluster.Conf.ProvOrchestrator {
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
	cluster.LogPrintf(LvlInfo, "Starting Database service %s", cluster.Name+"/svc/"+server.Name)
	var err error
	switch cluster.Conf.ProvOrchestrator {
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
	return err
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
	wg := new(sync.WaitGroup)
	wg.Add(1)
	err = cluster.TopologyDiscover(wg)
	wg.Wait()
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
				err = server.ChangeMasterTo(cluster.Servers[masterKey], "SLAVE_POS")
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
					err = server.ChangeMasterTo(cluster.Servers[masterKey], "CURRENT_POS")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return err
					}

				} else {
					err = server.ChangeMasterTo(cluster.Servers[relaykey], "CURRENT_POS")
					if err != nil {
						cluster.sme.RemoveFailoverState()
						return err
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
				err = server.ChangeMasterTo(cluster.Servers[1], "CURRENT_POS")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return err
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
			}
			if key == 1 {
				err = server.ChangeMasterTo(cluster.Servers[0], "CURRENT_POS")
				if err != nil {
					cluster.sme.RemoveFailoverState()
					return err
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
			err = server.ChangeMasterTo(cluster.Servers[i], "SLAVE_POS")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}

			cluster.vmaster = cluster.Servers[0]

		}
	}
	cluster.sme.RemoveFailoverState()
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
	for _, node := range cluster.Agents {
		if name == node.HostName {
			return node, nil
		}
	}
	return node, errors.New("Agent not found in orechestrator node list")
}
