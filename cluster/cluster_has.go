// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) HasServer(srv *ServerMonitor) bool {
	for _, sv := range cluster.Servers {
		//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "HasServer:%s %s, %s %s", sv.Id, srv.Id, sv.URL, srv.URL)
		// id can not be used for checking equality because  same srv in different clusters
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "HasServer check  %s  vs  %s  ", sv.URL, srv.URL)
		// When server has no port URL has no port then discovery use port
		if sv.URL == srv.URL || sv.URL == srv.URL+":3306" {
			return true
		}
	}
	return false
}

// HasConfigTopoActivePassive reports whether the cluster is configured for
// active-passive topology, either via the legacy boolean flag or an explicit
// topology target.
func (cluster *Cluster) HasConfigTopoActivePassive() bool {
	return cluster.Conf.ActivePassive || cluster.Conf.TopologyTarget == config.TopoActivePassive
}

func (cluster *Cluster) HasValidBackup() bool {
	logical := false
	physical := false

	// Check backup server
	sv := cluster.GetBackupServer()
	if sv != nil {
		if sv.HasBackupLogicalCookie() {
			logical = true
		}
		if sv.HasBackupPhysicalCookie() {
			physical = true
		}
	}

	// Check master (with nil safety)
	if cluster.master != nil {
		if cluster.master.HasBackupLogicalCookie() {
			logical = true
		}
		if cluster.master.HasBackupPhysicalCookie() {
			physical = true
		}
	}

	// Manage state warnings
	if logical {
		cluster.StateMachine.DeleteState("WARN0111")
	} else {
		cluster.SetState("WARN0111", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0111"], ErrFrom: "TOPO"})
	}

	if physical {
		cluster.StateMachine.DeleteState("WARN0112")
	} else {
		cluster.SetState("WARN0112", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0112"], ErrFrom: "TOPO"})
	}

	return logical || physical
}

func (cluster *Cluster) HasSchedulerEntry(myname string) bool {
	if _, ok := cluster.Schedule[myname]; ok {
		return true
	}

	return false
}

func (cluster *Cluster) HasNoValidSlave() bool {
	if cluster.Topology == config.TopoActivePassive {
		return true
	}
	//All slave stopped
	if cluster.StateMachine.IsInState("ERR00010") {
		return true
	}
	// Any issues on all slaves expeting delay and network
	if cluster.StateMachine.IsInState("ERR00085") {
		return true
	}
	return false
}

func (cluster *Cluster) IsProvisioned() bool {
	cluster.Lock()
	defer cluster.Unlock()

	if cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return true
	}
	if cluster.Conf.Hosts == "" {
		return false
	}
	for _, db := range cluster.Servers {
		if !db.HasProvisionCookie() {
			if db.IsRunning() {
				db.SetProvisionCookie()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Can DB Connect creating cookie %s: %s", db.URL, db.State)
			} else {
				return false
			}
		}
	}
	for _, px := range cluster.Proxies {
		if !px.HasProvisionCookie() {
			if px.IsRunning() {
				px.SetProvisionCookie()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Can Proxy Connect creating cookie %s: %s", px.GetURL(), px.GetState())
			} else {
				return false
			}
		}
	}
	return true
}

func (cluster *Cluster) IsAppProvisioned() bool {
	if len(cluster.Apps) == 0 {
		return true
	}

	for _, app := range cluster.Apps {
		if !app.HasProvisionCookie() {
			if app.IsRunning() && !app.HasUnprovisionCookie() {
				// App is running without a provision cookie — recover state from a restart.
				// Skip entirely when the unprovision cookie is present: the app was explicitly
				// unprovisioned and may just be in a brief shutdown transition.
				if err := app.SetProvisionCookie(); err != nil {
					cluster.SetState("APPERR006", state.State{ErrType: "WARNING", ErrDesc: clusterError["APPERR006"], ErrFrom: "TOPO", ServerUrl: app.GetURL()})
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Can App Connect creating cookie %s: %s", app.GetURL(), app.GetState())
				if app.AppConfig.ProvAppCreditUsed == 0 && app.AppConfig.ProvAppCreditPlanned > 0 {
					app.AppConfig.ProvAppCreditUsed = app.AppConfig.ProvAppCreditPlanned
					if _, err := cluster.SaveApp(app, ""); err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
							"Failed to persist backfilled credit usage for %s: %s", app.Name, err)
					}
					cluster.recomputeAppCredits()
				}
			} else if !app.IsRunning() {
				return false
			}
		}
	}
	return true
}

func (cluster *Cluster) IsInIgnoredHosts(server *ServerMonitor) bool {
	// Ignore if child cluster
	if server.SourceClusterName != cluster.Name {
		return true
	}
	ihosts := strings.Split(cluster.Conf.IgnoreSrv, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInPreferedBackupHosts(server *ServerMonitor) bool {
	// Ignore if child cluster
	if server.SourceClusterName != cluster.Name {
		return false
	}
	ihosts := strings.Split(cluster.Conf.BackupServers, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInIgnoredReadonly(server *ServerMonitor) bool {
	// Ignore if child cluster
	if server.SourceClusterName != cluster.Name {
		return true
	}
	ihosts := strings.Split(cluster.Conf.IgnoreSrvRO, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInPreferedHosts(server *ServerMonitor) bool {
	// Ignore if child cluster
	if server.SourceClusterName != cluster.Name {
		return false
	}
	ihosts := strings.Split(cluster.Conf.PrefMaster, ",")
	for _, ihost := range ihosts {
		if server.URL == ihost || server.Name == ihost {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsInCaptureMode() bool {
	if !cluster.Conf.MonitorCapture || cluster.IsNotMonitoring || len(cluster.Servers) > 0 {
		return false
	}
	for _, server := range cluster.Servers {
		if server.InCaptureMode {
			return true
		}
	}
	return false
}

func (cluster *Cluster) HasAllDbUp() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.State == stateFailed /*|| s.State == stateErrorAuth /*&& misc.Contains(cluster.ignoreList, s.URL) == false*/ {
				return false
			}
			if s.State == stateSuspect && cluster.GetTopology() != config.TopoUnknown {
				//supect is used to reload config and avoid backend state change to failed that would disable servers in proxies and cause glinch in cluster traffic
				// at the same time to enbale bootstrap replication we need to know when server are up
				return false
			}
			if s.Conn == nil {
				return false
			}

		}
	}

	if cluster.GetTopology() != config.TopoUnknown {
		cluster.GetStateMachine().Discovered = true
	}
	return true
}

func (cluster *Cluster) HasBadConfigMeasurement() bool {
	return len(cluster.ErrorConfigs) > 0
}

func (cluster *Cluster) HasAllDbDown() bool {
	if cluster.Servers == nil {
		return true
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.State != stateFailed /*&& misc.Contains(cluster.ignoreList, s.URL) == false*/ {
				return false
			}

		}
	}

	return true
}

func (cluster *Cluster) HasAllProxyUp() bool {
	if cluster.Proxies == nil {
		return false
	}

	for _, pri := range cluster.Proxies {

		if prx, ok := pri.(*Proxy); ok {
			if prx.IsDown() {
				return false
			}
		}

	}
	return true
}

func (cluster *Cluster) HasNoDbUnconnected() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.State == stateFailed || s.State == stateUnconn /*&& misc.Contains(cluster.ignoreList, s.URL) == false*/ {
				return false
			}
			if s.State == stateSuspect && cluster.GetTopology() != config.TopoUnknown {
				//supect is used to reload config and avoid backend state change to failed that would disable servers in proxies and cause glinch in cluster traffic
				// at the same time to enbale bootstrap replication we need to know when server are up
				return false
			}
			if s.Conn == nil {
				return false
			}
		}
	}

	return true
}

func (cluster *Cluster) HasRequestDBRestart() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.HasRestartCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestDBConfigChange() bool {
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if s.HasConfigCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestDBRollingRestart() bool {
	ret := true
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if !s.HasRestartCookie() {
				return false
			}
		}
	}
	return ret
}

func (cluster *Cluster) HasRequestDBRollingReprov() bool {
	ret := true
	if cluster.Servers == nil {
		return false
	}
	for _, s := range cluster.Servers {
		if s != nil {
			if !s.HasReprovCookie() {
				return false
			}
		}
	}

	return ret
}

func (cluster *Cluster) HasRequestDBReprov() bool {
	for _, s := range cluster.Servers {
		if s != nil {
			if s.HasReprovCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestProxiesRestart() bool {
	for _, p := range cluster.Proxies {
		if p != nil {
			if p.HasRestartCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestProxiesReprov() bool {
	for _, p := range cluster.Proxies {
		if p != nil {
			if p.HasReprovCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasRequestAppReprov() bool {
	for _, a := range cluster.Apps {
		if a != nil {
			if a.HasReprovCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) HasConfigPathChanged() bool {
	for _, srv := range cluster.Servers {
		if srv != nil {
			if srv.HasConfigPathCookie() {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) IsInHostList(host string) bool {
	for _, v := range cluster.hostList {
		if v == host {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsMasterFailed() bool {
	// get real master or the virtual master
	mymaster := cluster.GetMaster()
	if mymaster == nil {
		return true
	}
	if mymaster.State == stateFailed {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsActive() bool {
	if cluster.Status == ConstMonitorActif {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsVerbose() bool {
	if cluster.Conf.Verbose {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsInFailover() bool {
	return cluster.StateMachine.IsInFailover()
}

func (cluster *Cluster) IsDiscovered() bool {
	return cluster.StateMachine.IsDiscovered()
}

func (cluster *Cluster) IsMultiMaster() bool {
	if cluster.GetTopology() == config.TopoMultiMasterWsrep || cluster.GetTopology() == config.TopoMultiMaster || cluster.GetTopology() == config.TopoMultiMasterRing {
		return true
	}
	return false
}

func (cluster *Cluster) HasReplicationCredentialsRotation() bool {
	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.RplUser) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Fail Vault connection: %v", err)
			return false
		}
		_, newpass, err := cluster.GetVaultReplicationCredentials(client)
		if newpass != cluster.GetRplPass() && err == nil {
			var new_Secret config.Secret

			new_Secret.OldValue = cluster.Conf.Secrets["replication-credential"].Value
			new_Secret.Value = cluster.GetRplUser() + ":" + newpass
			cluster.Conf.Secrets["replication-credential"] = new_Secret

			return true
		}
		return false
	}
	return false
}

func (cluster *Cluster) HasMonitoringCredentialsRotation() bool {
	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.User) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Fail Vault connection: %v", err)
			return false
		}
		newuser, newpass, err := cluster.GetVaultMonitorCredentials(client)
		if (newpass != cluster.GetDbPass() || newuser != cluster.GetDbUser()) && err == nil {
			//cluster.SetClusterMonitorCredentialsFromConfig()
			//cluster.oldDbUser = cluster.GetDbUser()
			//cluster.oldDbPass = cluster.GetDbPass()
			return true
		}
		return false
	}
	return false
}

func (cluster *Cluster) HasProxyCredentialsRotation() bool {
	if cluster.Conf.IsVaultUsed() {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail Vault connection: %v", err)
			return false
		}
		if cluster.Conf.ProxysqlOn && cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) {
			newuser, newpass, err := cluster.GetVaultProxySQLCredentials(client)
			if (newpass != cluster.Conf.Secrets["proxysql-password"].Value || newuser != cluster.Conf.Secrets["proxysql-user"].Value) && err == nil {
				//cluster.SetClusterProxyCredentialsFromConfig()
				//cluster.oldDbUser = cluster.GetDbUser()
				//cluster.oldDbPass = cluster.GetDbPass()
				return true
			}
		}

		if cluster.Conf.MdbsProxyOn && cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) {
			newuser, newpass, err := cluster.GetVaultShardProxyCredentials(client)
			if (newpass != cluster.GetShardPass() || newuser != cluster.GetShardUser()) && err == nil {
				//cluster.SetClusterProxyCredentialsFromConfig()
				//cluster.oldDbUser = cluster.GetDbUser()
				//cluster.oldDbPass = cluster.GetDbPass()
				return true
			}
		}
		return false
	}
	return false
}

func (cluster *Cluster) IsVariableDiffFromRepmanDefault(v string) bool {
	values_clust := reflect.ValueOf(*cluster.Conf)
	types_clust := values_clust.Type()

	values_def := reflect.ValueOf(cluster.Confs.ConfInit)
	types_def := values_def.Type()

	var val_clust reflect.Value
	var val_def reflect.Value

	for i := 0; i < values_clust.NumField(); i++ {
		if types_clust.Field(i).Name == v {
			val_clust = values_clust.Field(i)
		}
		if types_def.Field(i).Name == v {
			val_def = values_def.Field(i)
		}

	}

	return val_clust == val_def
}

func (cluster *Cluster) IsVariableImmutable(v string) bool {
	_, ok := cluster.Conf.ImmuableFlagMap[v]
	return ok
}

func (cluster *Cluster) IsVariableServerLevel(v string) bool {
	_, ok := cluster.Conf.ImmuableFlagMap[v]
	return ok
}

func (cluster *Cluster) IsInBackup() bool {
	return cluster.InPhysicalBackup || cluster.InLogicalBackup || cluster.InBinlogBackup || cluster.IsInResticBackup()
}

func (cluster *Cluster) IsInResticBackup() bool {
	return cluster.InResticLogicalBackup || cluster.InResticPhysicalBackup || cluster.InResticBackup
}

func (cluster *Cluster) IsInResticBackupForMethod(backupMethod backupmgr.BackupMethod) bool {
	switch backupMethod {
	case backupmgr.BackupMethodLogical:
		return cluster.InResticLogicalBackup || cluster.InResticBackup
	case backupmgr.BackupMethodPhysical:
		return cluster.InResticPhysicalBackup || cluster.InResticBackup
	default:
		return cluster.InResticBackup
	}
}

func (cluster *Cluster) HasWaitDBACredCookie() bool {
	for _, srv := range cluster.Servers {
		if srv.HasWaitDBACredCookie() {
			return true
		}
	}
	return false
}

func (cluster *Cluster) HasWaitSponsorCredCookie() bool {
	for _, srv := range cluster.Servers {
		if srv.HasWaitSponsorCredCookie() {
			return true
		}
	}
	return false
}

func (cluster *Cluster) HasDiscoverTopologyMismatchTarget() bool {
	return cluster.Topology != cluster.Conf.TopologyTarget
}

func (cluster *Cluster) HasDiscoverTopologyReachTarget() bool {
	return cluster.Topology == cluster.Conf.TopologyTarget
}

func (cluster *Cluster) IsTopologyTargetEqual(target string) bool {
	return cluster.Conf.TopologyTarget == target
}

func (cluster *Cluster) IsInErrorState(key, serverURL string) bool {
	if key == "" {
		return false
	} else if serverURL == "" {
		return cluster.StateMachine.IsInState(key)
	} else {
		return cluster.StateMachine.IsInState(fmt.Sprintf("%s@%s", key, serverURL))
	}
}

func (cluster *Cluster) IsInSchemaIgnore(table string) bool {
	if cluster.Conf.MonitorSchemaIgnoreTables == "" {
		return false
	}

	for _, pattern := range strings.Split(cluster.Conf.MonitorSchemaIgnoreTables, ",") {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		matched, _ := path.Match(trimmed, table)
		if matched {
			return true
		}
	}

	return false
}

func (cluster *Cluster) IsInSchemaTableList(tablelist string, schema string, table string) bool {
	if tablelist == "" {
		return false
	}
	if !strings.Contains(tablelist, table) {
		return false
	}
	for _, pattern := range strings.Split(tablelist, ",") {

		lschema := strings.TrimSpace(strings.Split(pattern, ".")[0])
		ltable := strings.TrimSpace(strings.Split(pattern, ".")[1])
		if table == ltable && schema == lschema {
			return true
		}
	}

	return false
}
