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

// HasCatalogBackupForRejoin reports, per method, whether a backup usable for a
// REJOIN exists ANYWHERE — any node, any repo, any location — by running the
// per-method autorejoin selector (autorejoin-backup-selector-{logical,physical})
// against the full backup catalog. It is the rejoin-scoped counterpart to
// HasValidBackup: HasValidBackup is master-scoped (cookie on the current master)
// and drives the WARN0111/0112 orchestrator contract, while this is catalog-
// scoped and drives what a rejoin is allowed to do. After a failover the freshly
// promoted master has no backup, but the OLD master (or restic/S3) still does —
// so this stays true and justifies allowing the rejoin. Sets
// IsValidRejoinBackup{Logical,Physical} and asserts WARN0190/WARN0191 (ErrFrom
// "JOIN" — rejoin-scoped, vs HasValidBackup's TOPO) when the catalog has nothing.
func (cluster *Cluster) HasCatalogBackupForRejoin() (bool, bool) {
	catalog := cluster.buildBackupCatalog()
	ctx := ResolveContext{}
	if m := cluster.GetMaster(); m != nil {
		ctx.MasterURL = m.URL
	}

	logical := ResolveRestore(catalog, cluster.getAutorejoinBackupSelector("logical"), ctx) != nil
	physical := ResolveRestore(catalog, cluster.getAutorejoinBackupSelector("physical"), ctx) != nil

	cluster.IsValidRejoinBackupLogical = logical
	cluster.IsValidRejoinBackupPhysical = physical

	if logical {
		cluster.StateMachine.DeleteState("WARN0190")
	} else {
		cluster.SetState("WARN0190", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0190"], ErrFrom: "JOIN"})
	}

	if physical {
		cluster.StateMachine.DeleteState("WARN0191")
	} else {
		cluster.SetState("WARN0191", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0191"], ErrFrom: "JOIN"})
	}

	return logical, physical
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

// HasValidReadSlave reports whether at least one non-leader, non-maintenance
// server in the cluster is currently eligible to serve reads. This is the
// exact "is there a valid alternative reader" computation
// prx_haproxy.go's masterShouldRead (standby/runtimeapi) uses for its
// proxy-servers-read-on-master-no-slave fallback -- exported here so
// ShouldServeReadsFromMaster() (used by haproxy-mode=externalcheck's
// IsValidReaderCheck, srv_has.go) can apply the identical rule.
func (cluster *Cluster) HasValidReadSlave() bool {
	for _, s := range cluster.Servers {
		if s.IsMaintenance || s.IsLeader() {
			continue
		}
		if !s.standbyReadIneligible() {
			return true
		}
	}
	return false
}

// ShouldServeReadsFromMaster is the single canonical answer to "should the
// master/leader be a member of the read backend": always when
// proxy-servers-read-on-master is set, or as a fallback
// (proxy-servers-read-on-master-no-slave) when there is no other valid
// alternative reader right now. Used by both prx_haproxy.go's Init()
// (standby's read-backend render) and ServerMonitor.IsValidReaderCheck
// (srv_has.go, externalcheck's checkslave HTTP handler, reached via the
// reader-status route) so the no-slave fallback behaves identically across
// modes -- before this existed, the externalcheck handler had its own inline
// check that only consulted PRXServersReadOnMaster and never
// PRXServersReadOnMasterNoSlave at all (bug #6,
// HAPROXY_LIVE_K8S_TEST_REPORT.md), so a cluster with every slave down and
// the no-slave flag on took its entire read backend offline instead of
// falling back to the master.
func (cluster *Cluster) ShouldServeReadsFromMaster() bool {
	if cluster.Configurator.HasProxyReadLeader() {
		return true
	}
	return cluster.Configurator.HasProxyReadLeaderNoSlave() &&
		(cluster.HasNoValidSlave() || !cluster.HasValidReadSlave())
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

// HasProvisionedHaproxy reports whether any HAProxy proxy has already been
// provisioned -- scoped to type "haproxy" specifically, not any proxy
// (ProxySQL/MaxScale/... provisioned in the same cluster must not block an
// HAProxy-only setting). Refresh() (cluster/prx_haproxy.go) reads settings
// like HaproxyMode/HaproxyAPIBootstrapServers live every monitoring tick, so
// changing them while a proxy is already deployed would alter its
// reconciliation behavior immediately, ahead of the deployed config being
// regenerated to match -- callers use this to refuse that live change.
func (cluster *Cluster) HasProvisionedHaproxy() bool {
	for _, p := range cluster.Proxies {
		if p != nil && p.GetType() == config.ConstProxyHaproxy && p.HasProvisionCookie() {
			return true
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

// CanSendGraphiteMetrics reports whether this monitor should collect and emit
// graphite metrics this tick. The active monitor always does. A passive/standby
// monitor does so only when carbon is embedded: it then records its OWN
// observation of the cluster into its OWN local carbon — each monitor keeps its
// independent view of the world (comparing the active vs passive perspective is
// diagnostic, e.g. split-brain). This way a standby never duplicates the active
// monitor's series on a shared carbon, and never doubles the metric query load
// on the monitored DB for nothing. See issue #1680.
func (cluster *Cluster) CanSendGraphiteMetrics() bool {
	if !cluster.Conf.GraphiteMetrics {
		return false
	}
	return cluster.IsActive() || cluster.Conf.GraphiteEmbedded
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

// planDrivenProvFlags are the provisioning specs a service plan (DBU) drives.
var planDrivenProvFlags = []string{
	"prov-db-cpu-cores",
	"prov-db-memory",
	"prov-db-disk-size",
	"prov-db-disk-iops",
	"prov-proxy-cpu-cores",
	"prov-proxy-disk-size",
}

// planDrivenDBVars are the DB variables a service plan (DBU) drives through the
// configurator. If the operator has preserved any of them (the "agree", pinning
// it to its running value), a plan change can no longer move it, so it must be
// refused just like an immutable prov flag.
var planDrivenDBVars = []string{
	"innodb_buffer_pool_size",    // prov-db-memory
	"key_buffer_size",            // prov-db-memory (myisam)
	"aria_pagecache_buffer_size", // prov-db-memory (aria)
	"innodb_io_capacity",         // prov-db-disk-iops
	"innodb_io_capacity_max",     // prov-db-disk-iops
	"innodb_read_io_threads",     // prov-db-cpu-cores
	"innodb_write_io_threads",    // prov-db-cpu-cores
	"innodb_purge_threads",       // prov-db-cpu-cores
}

// CanChangePlan reports whether a service-plan (DBU) change may be applied. It
// returns false when the operator has pinned any plan-driven spec so the plan
// could no longer move it — either a provisioning flag made immutable
// (immutable.toml) or a driven DB variable preserved (the "agree",
// 01_preserved.cnf). The whole plan change is refused until the operator unpins.
func (cluster *Cluster) CanChangePlan() bool {
	for _, f := range planDrivenProvFlags {
		if cluster.IsVariableImmutable(f) {
			return false
		}
	}
	for _, v := range planDrivenDBVars {
		if cluster.IsVariablePreserved(v) {
			return false
		}
	}
	return true
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
