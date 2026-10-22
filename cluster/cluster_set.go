// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

func (cluster *Cluster) SetStatus() {
	if cluster.master == nil {
		cluster.StateMachine.SetMasterUpAndSync(false, false, false)
	} else {
		cluster.StateMachine.SetMasterUpAndSync(!cluster.master.IsDown(), cluster.master.SemiSyncMasterStatus, cluster.master.HaveHealthyReplica)
	}
	cluster.Uptime = cluster.GetStateMachine().GetUptime()
	cluster.UptimeFailable = cluster.GetStateMachine().GetUptimeFailable()
	cluster.UptimeSemiSync = cluster.GetStateMachine().GetUptimeSemiSync()
	cluster.IsNotMonitoring = cluster.StateMachine.IsInFailover()
	cluster.IsCapturing = cluster.IsInCaptureMode()
	cluster.MonitorSpin = fmt.Sprintf("%d ", cluster.GetStateMachine().GetHeartbeats())
	cluster.IsProvision = cluster.IsProvisioned()
	cluster.IsAppProvisioned()
	cluster.IsNeedProxiesRestart = cluster.HasRequestProxiesRestart()
	cluster.IsNeedProxiesReprov = cluster.HasRequestProxiesReprov()
	cluster.IsNeedDatabasesConfigChange = cluster.HasRequestDBConfigChange()
	cluster.IsNeedDatabasesRollingRestart = cluster.HasRequestDBRollingRestart()
	cluster.IsNeedDatabasesRollingReprov = cluster.HasRequestDBRollingReprov()
	for _, srv := range cluster.Servers { // per-server over/under x config/plan consumed states
		if srv != nil {
			srv.CheckResourceConsumed()
		}
	}
	cluster.checkFreshNodeStates()                // CINF0013 on a StandAlone server proven fresh (#1979)
	cluster.checkStagingCreationStates()          // CINF0014 while create-staging/child runs, WARN0239 when it stopped (#1988)
	cluster.checkEngineSwitchStates()             // CINF0015 while an engine switch runs, WARN0240 when it stopped (#1999)
	cluster.checkColumnstoreRebalanceStates()     // CINF0018 while a ColumnStore rebalance runs, WARN0241 when it stopped (#2008)
	cluster.checkArchiveRemoveOriginalStates()    // WARN0238 while an archived original could not be removed (#1987)
	cluster.checkBackupVerificationStates()       // CINF0016 while a backup is verified, ERR00117 when a last logical backup failed it (#2023)
	cluster.checkBackupDiscoveryStates()          // WARN0249 while a registered backup changed, WARN0250 while candidates are dropped (#2073)
	cluster.checkBackupMountStates()              // CINF0019 while the local backups move into the bucket (gates the backups), WARN0261 while a mount turned on is not mounted (#2091)
	cluster.checkBinlogCopyStates()               // WARN0257 while the binlog copies of a server wait for a backup, snapshot or failover (#2091)
	cluster.checkArchiveEncryptionStates()        // WARN0263 an archive encrypted client-side kept without a key (#2091)
	cluster.checkSnapbackStates()                 // CINF0020 snapback running, WARN0255 a backup directory not at its latest snapshot (backups locked), WARN0256 a snapback failed (#2091)
	cluster.checkRegisteredVerificationStates()   // WARN0253 while a registered backup's last verification failed (#2108)
	cluster.saveBackupCatalogue()                 // the global backup catalogue files, written when changed on the active replication-manager (#2091)
	cluster.checkCatalogueStates()                // the catalogue's automatic rebuild when due, CINF0021 while one runs, WARN0260 when it differs from an archive (#2091)
	cluster.CheckResourceCapPlan()                // compose cluster cap-up/down from the per-server plan states
	cluster.RefreshDBUPlan()                      // DBU plan: project per-node prov-db-dbu into the ResourceManager (DB track)
	cluster.RefreshComputePlanAPU()               // APU plan: project app-deployment + proxy resources into the ResourceManager (Compute track)
	cluster.DriveDailyDynamicResize()             // daily-time policy: reconcile live memory in the off-peak window
	cluster.DriveDynamicResize()                  // dynamic-resize trigger: turn a sustained saturation state into a real resize
	cluster.CheckDynamicResourceDeploymentReady() // WARN0214 when live resize is on but the container is still docker-capped (not resize-ready)
	cluster.IsNeedDatabasesRestart = cluster.HasRequestDBRestart()
	cluster.IsNeedDatabasesReprov = cluster.HasRequestDBReprov()
	cluster.IsNeedDatabasesConfigChange = cluster.HasRequestDBConfigChange()
	cluster.IsNeedAppsReprov = cluster.HasRequestAppReprov()
	cluster.WaitingRejoin = cluster.rejoinCond.Len()
	cluster.WaitingFailover = cluster.failoverCond.Len()
	cluster.WaitingSwitchover = cluster.switchoverCond.Len()
	if len(cluster.Servers) > 0 {
		cluster.WorkLoad.QPS = cluster.GetQps()
		cluster.WorkLoad.Connections = cluster.GetConnections()
		cluster.WorkLoad.CpuThreadPool = cluster.GetCpuTime()
		cluster.WorkLoad.CpuUserStats = cluster.GetCpuTimeFromStat()
	}
}

func (cluster *Cluster) SetCertificate(svc opensvc.Collector) {
	var err error
	if !cluster.Conf.Enterprise {
		return
	}
	if cluster.Conf.ProvSSLCa != "" {
		cluster.Conf.ProvSSLCaUUID, err = svc.PostSafe(cluster.Conf.ProvSSLCa)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Can't upload root CA to Collector Safe %s", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Upload root Certificate to Safe %s", cluster.Conf.ProvSSLCaUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLCaUUID, "replication-manager")
	}
	if cluster.Conf.ProvSSLCert != "" {
		cluster.Conf.ProvSSLCertUUID, err = svc.PostSafe(cluster.Conf.ProvSSLCert)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Can't upload Server TLS Certificate to Collector Safe %s", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Upload Server TLS Certificate to Safe %s", cluster.Conf.ProvSSLCertUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLCertUUID, "replication-manager")
	}
	if cluster.Conf.ProvSSLKey != "" {
		cluster.Conf.ProvSSLKeyUUID, err = svc.PostSafe(cluster.Conf.ProvSSLKey)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Can't upload Server TLS Private Key to Collector Safe %s", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Upload Server TLS Private Key to Safe %s", cluster.Conf.ProvSSLKeyUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLKeyUUID, "replication-manager")
	}
}

func (cluster *Cluster) CompressBackups() {
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "COUCOU compress backups")
}

func (cluster *Cluster) SetCfgGroupDisplay(cfgGroup string) {
	cluster.cfgGroupDisplay = cfgGroup
}

func (cluster *Cluster) SetInteractive(check bool) {
	cluster.Conf.Interactive = check
	if cluster.Conf.Interactive {
		cluster.Conf.FailMode = "manual"
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover monitor switched to interactive mode")
	} else {
		cluster.Conf.FailMode = "automatic"
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover monitor switched to automatic mode")
	}

}

func (cluster *Cluster) SetDBDiskSize(value string) {
	oldGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	cluster.Configurator.SetDBDisk(value)
	cluster.Conf.ProvDisk = cluster.Configurator.GetConfigDBDisk()
	newGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	// Live move (#1854): with the dynamic resource on, the declared disk moves the volumes
	// through the orchestrator instead of asking for a reprovision, up or down (a shrink
	// the orchestrator does not honour yet is tracked as WARN0221, never a reprovision:
	// it is data). Without it, the reprovision path as before.
	if cluster.Conf.ProvDBDynamicResource && newGB != oldGB {
		cluster.applyDiskResize(int(oldGB), int(newGB))
		return
	}
	if newGB == oldGB {
		return
	}
	cluster.SetDBReprovCookie(ReprovReasonDiskSize)
}

func (cluster *Cluster) SetDBCores(value string) {
	old := cluster.Conf.ProvCores
	cluster.Configurator.SetDBCores(value)
	cluster.Conf.ProvCores = cluster.Configurator.GetConfigDBCores()
	// Live resize: a core change re-tunes the cores-driven DB variables
	// (innodb_read_io_threads) via SET GLOBAL. The container cpu cgroup limit
	// resize (pg_cpus) is a follow-up; for now this only re-tunes the DB side.
	if cluster.Conf.ProvDBDynamicResource {
		if cluster.Conf.ProvCores != old {
			newC, _ := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
			oldC, _ := strconv.ParseFloat(old, 64)
			cluster.lastDynamicResize = time.Now() // a manual change opens the same cooldown as a dynamic step
			cluster.ResizeDynamicResources(resizeCPU, newC > oldC)
		}
		return
	}
	// Without the live path, the rolling restart pushes the new container cap: never a
	// reprovision for a resource (it recreates the volume, #1969).
	if cluster.Conf.ProvCores != old {
		cluster.SetDBRestartCookie()
	}
}

func (cluster *Cluster) SetDBMemorySize(value string) {
	oldMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	cluster.Configurator.SetDBMemory(value)
	cluster.Conf.ProvMem = cluster.Configurator.GetConfigDBMemory()
	// When live resource resize is enabled, drive it (SET GLOBAL + client
	// infra hook) instead of a full container recreation. grow is decided from
	// the memory delta; ResizeDynamicResources no-ops when the feature is off.
	if cluster.Conf.ProvDBDynamicResource {
		newMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
		if newMB != oldMB { // skip no-op reloads (would re-run the feasibility script for nothing)
			cluster.lastDynamicResize = time.Now() // a manual change opens the same cooldown as a dynamic step
			cluster.ResizeDynamicResources(resizeMemory, newMB > oldMB)
		}
		return
	}
	// Without the live path, the rolling restart pushes the new container cap: never a
	// reprovision for a resource (it recreates the volume, #1969).
	if newMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true); newMB != oldMB {
		cluster.SetDBRestartCookie()
	}
}

func (cluster *Cluster) SetDBCoresFromConfigurator() {
	cluster.Conf.ProvCores = cluster.Configurator.GetConfigDBCores()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBMemoryFromConfigurator() {
	cluster.Conf.ProvMem = cluster.Configurator.GetConfigDBMemory()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBIOPSFromConfigurator() {
	cluster.Conf.ProvIops = cluster.Configurator.GetConfigDBDiskIOPS()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetTagsFromConfigurator() {
	cluster.Conf.ProvTags = cluster.Configurator.GetConfigDBTags()
	cluster.Conf.ProvProxTags = cluster.Configurator.GetConfigProxyTags()
	cluster.SetConfigRefreshCookie() //Need to refresh the config
}

func (cluster *Cluster) SetDBDiskIOPS(value string) {
	old := cluster.Conf.ProvIops
	cluster.Configurator.SetDBDiskIOPS(value)
	cluster.Conf.ProvIops = cluster.Configurator.GetConfigDBDiskIOPS()
	// Live resize: an iops change re-tunes the iops-driven DB variables
	// (innodb_io_capacity/_max, innodb_write_io_threads) via SET GLOBAL.
	if cluster.Conf.ProvDBDynamicResource {
		if cluster.Conf.ProvIops != old {
			cluster.ResizeDynamicResources(resizeIO, false)
		}
		return
	}
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBMaxConnections(value string) {
	cluster.Configurator.SetDBMaxConnections(value)
	cluster.Conf.ProvMaxConnections = cluster.Configurator.GetConfigDBMaxConnections()
	cluster.SetDBRestartCookie()
}

// SetDBBinlogTotalSizePct sets prov-db-binlog-total-size-pct; the dynamic config applies the new
// max_binlog_total_size live (MariaDB 11.4+), no restart.
func (cluster *Cluster) SetDBBinlogTotalSizePct(value string) {
	cluster.Configurator.SetDBBinlogTotalSizePct(value)
	cluster.Conf.ProvBinlogTotalSizePct = cluster.Configurator.ClusterConfig.ProvBinlogTotalSizePct
}

func (cluster *Cluster) SetDBExpireLogDays(value string) {
	cluster.Configurator.SetDBExpireLogDays(value)
	cluster.Conf.ProvExpireLogDays = cluster.Configurator.GetConfigDBExpireLogDays()
	cluster.SetDBRestartCookie()
}

// SetDBReplicationParallelThreads sets slave_parallel_threads for the configurator template.
// Decision 2026-09-16: replication workers do NOT follow the core count -- they idle on the
// thread pool and their number is the concurrency that hides network/commit latency. The
// value reaches the running slaves at the next config apply (restart cookie), like the
// other template-driven replication settings.
func (cluster *Cluster) SetDBReplicationParallelThreads(value string) {
	cluster.Configurator.SetDBReplicationParallelThreads(value)
	cluster.Conf.ProvReplicationParallelThreads = cluster.Configurator.GetConfigDBReplicationParallelThreads()
	cluster.SetDBRestartCookie()
}

// SetDBReplicationDomainParallelThreads sets slave_domain_parallel_threads (0 = no per-domain
// cap); keep 0 on a single-master cluster, otherwise the pool is capped per domain.
func (cluster *Cluster) SetDBReplicationDomainParallelThreads(value string) {
	cluster.Configurator.SetDBReplicationDomainParallelThreads(value)
	cluster.Conf.ProvReplicationDomainParallelThreads = cluster.Configurator.GetConfigDBReplicationDomainParallelThreads()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetProxyCores(value string) {
	cluster.Configurator.SetProxyCores(value)
	cluster.Conf.ProvProxCores = cluster.Configurator.GetConfigProxyCores()
	cluster.SetProxiesRestartCookie()
}

func (cluster *Cluster) SetProxyMemorySize(value string) {
	cluster.Configurator.SetProxyMemorySize(value)
	cluster.Conf.ProvProxMem = cluster.Configurator.GetProxyMemorySize()
	cluster.SetProxiesRestartCookie()
}

func (cluster *Cluster) SetProxyDiskSize(value string) {
	cluster.Configurator.SetProxyDiskSize(value)
	cluster.Conf.ProvProxDisk = cluster.Configurator.GetProxyDiskSize()
	cluster.SetProxiesReprovCookie()
}

func (cluster *Cluster) SetCloud18MonthlyInfraCost(value float64) {
	cluster.Conf.Cloud18MonthlyInfraCost = value
}

func (cluster *Cluster) SetCloud18MonthlyLicenseCost(value float64) {
	cluster.Conf.Cloud18MonthlyLicenseCost = value
}

func (cluster *Cluster) SetCloud18MonthlySysopsCost(value float64) {
	cluster.Conf.Cloud18MonthlySysopsCost = value
}

func (cluster *Cluster) SetCloud18MonthlyDbopsCost(value float64) {
	cluster.Conf.Cloud18MonthlyDbopsCost = value
}

func (cluster *Cluster) SetCloud18CostCurrency(value string) {
	cluster.Conf.Cloud18CostCurrency = value
}

func (cluster *Cluster) SetCloud18InfraCPUModel(value string) {
	cluster.Conf.Cloud18InfraCPUModel = value
}

func (cluster *Cluster) SetCloud18ExternalSysOps(value string) {
	cluster.Conf.Cloud18ExternalSysOps = value
}

func (cluster *Cluster) SetCloud18ExternalDbOps(value string) {
	cluster.Conf.Cloud18ExternalDbOps = value
}

func (cluster *Cluster) SetCloud18InfraCertifications(value string) {
	cluster.Conf.Cloud18InfraCertifications = value
}

func (cluster *Cluster) SetCloud18InfraCPUFreq(value string) {
	cluster.Conf.Cloud18InfraCPUFreq = value
}

func (cluster *Cluster) SetCloud18InfraDescription(value string) {
	cluster.Conf.Cloud18InfraDescription = value
}

func (cluster *Cluster) SetCloud18InfraDataCenters(value string) {
	cluster.Conf.Cloud18InfraDataCenters = value
}

func (cluster *Cluster) SetCloud18InfraPublicBandwidth(value float64) {
	cluster.Conf.Cloud18InfraPublicBandwidth = value
}

func (cluster *Cluster) SetCloud18InfraGeoLocalizations(value string) {
	cluster.Conf.Cloud18InfraGeoLocalizations = value
}

func (cluster *Cluster) SetCloud18SlaResponseTime(value float64) {
	cluster.Conf.Cloud18SlaResponseTime = value
}

func (cluster *Cluster) SetCloud18SlaRepairTime(value float64) {
	cluster.Conf.Cloud18SlaRepairTime = value
}

func (cluster *Cluster) SetCloud18SlaProvisionTime(value float64) {
	cluster.Conf.Cloud18SlaProvisionTime = value
}

func (cluster *Cluster) SetCloud18PromotionPct(value float64) {
	cluster.Conf.Cloud18PromotionPct = value
}

func (cluster *Cluster) SetCloud18DatabaseReadWriteSplitSrvRecord(value string) {
	cluster.Conf.Cloud18DatabaseReadWriteSplitSrvRecord = value
}

func (cluster *Cluster) SetCloud18DatabaseReadSrvRecord(value string) {
	cluster.Conf.Cloud18DatabaseReadSrvRecord = value
}

func (cluster *Cluster) SetCloud18DatabaseReadWriteSrvRecord(value string) {
	cluster.Conf.Cloud18DatabaseReadWriteSrvRecord = value
}

func (cluster *Cluster) SetTraffic(traffic bool) {
	cluster.Conf.TestInjectTraffic = traffic
}

func (cluster *Cluster) SetTrafficStaging(traffic bool) {
	cluster.Conf.TestInjectTrafficStaging = traffic
}

func (cluster *Cluster) SetBenchMethod(m string) {
	cluster.benchmarkType = m
}

func (cluster *Cluster) AddPrefMaster(node *ServerMonitor) {
	// Deny change for child cluster due to possibility of preferred list only use node name (db1, db2...) etc.
	if node.SourceClusterName != cluster.Name {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Unable to set child node %s as preferred", node.URL)
		return
	}

	if cluster.IsInPreferedHosts(node) {
		return
	}

	savedPrefMaster := cluster.GetPreferedMasterList()
	if savedPrefMaster == "" {
		cluster.SetPrefMaster(node.URL)
	} else {
		cluster.SetPrefMaster(savedPrefMaster + "," + node.URL)
	}
}

func (cluster *Cluster) RemovePrefMaster(node *ServerMonitor) error {
	// Deny change for child cluster due to possibility of preferred list only use node name (db1, db2...) etc.
	if node.SourceClusterName != cluster.Name {
		return fmt.Errorf("Unable to remove child node %s as preferred", node.URL)
	}

	if !cluster.IsInPreferedHosts(node) {
		return fmt.Errorf("Host not found in prefered list")
	}

	savedPrefMaster := cluster.GetPreferedMasterList()
	if savedPrefMaster == node.URL {
		cluster.SetPrefMaster("")
	} else {
		list := strings.Split(savedPrefMaster, ",")
		res := make([]string, 0)

		for _, sv := range list {
			// Skip (remove) if found
			if sv == node.URL || sv == node.Name {
				continue
			}
			res = append(res, sv)
		}
		cluster.SetPrefMaster(strings.Join(res, ","))
	}

	return nil
}

// SetPrefMaster is used by regtest test_switchover_semisync_switchback_prefmaster_norplcheck and API to force a server
func (cluster *Cluster) SetPrefMaster(PrefMasterURL string) {
	var prefmasterlist []string
	for _, srv := range cluster.Servers {
		// Deny change for child cluster due to possibility of preferred list only use node name (db1, db2...) etc.
		if srv.SourceClusterName != cluster.Name {
			srv.SetPrefered(false)
		} else {
			if strings.Contains(PrefMasterURL, srv.URL) {
				srv.SetPrefered(true)
				prefmasterlist = append(prefmasterlist, strings.ReplaceAll(srv.URL, srv.Domain+":3306", ""))
			} else {
				srv.SetPrefered(false)
			}
		}
	}
	cluster.Conf.PrefMaster = strings.Join(prefmasterlist, ",")
	// fmt.Printf("Update config prefered Master: " + cluster.Conf.PrefMaster + "\n")
}

// Set Ignored Host for Election
func (cluster *Cluster) AddIgnoreSrv(node *ServerMonitor) {
	if cluster.IsInIgnoredHosts(node) {
		return
	}

	savedIgnoredHost := cluster.GetIgnoredHostList()
	if savedIgnoredHost == "" {
		cluster.SetIgnoreSrv(node.URL)
	} else {
		cluster.SetIgnoreSrv(savedIgnoredHost + "," + node.URL)
	}
}

// Set Ignored Host for Election
func (cluster *Cluster) RemoveIgnoreSrv(node *ServerMonitor) error {
	if node.SourceClusterName != cluster.Name {
		return fmt.Errorf("Host is in child cluster. Cannot remove ignore")
	}

	if !cluster.IsInIgnoredHosts(node) {
		return fmt.Errorf("Host not found in ignored list")
	}

	savedIgnoredHost := cluster.GetIgnoredHostList()
	if savedIgnoredHost == node.URL {
		cluster.SetIgnoreSrv("")
	} else {
		//Remove the prefered from list
		newIgnoredHost := strings.ReplaceAll(savedIgnoredHost, node.URL+",", "")
		newIgnoredHost = strings.ReplaceAll(newIgnoredHost, ","+node.URL, "")
		cluster.SetIgnoreSrv(newIgnoredHost)
	}

	return nil
}

// Set Ignored Host for Election
func (cluster *Cluster) SetIgnoreSrv(IgnoredHostURL string) {
	var ignoresrvlist []string
	for _, srv := range cluster.Servers {
		// Always ignore if child cluster but don't add to config
		if srv.GetSourceClusterName() != cluster.Name {
			srv.SetIgnored(true)
		} else {
			// Only add to config if node is in main cluster
			if strings.Contains(IgnoredHostURL, srv.URL) {
				srv.SetIgnored(true)
				ignoresrvlist = append(ignoresrvlist, strings.ReplaceAll(srv.URL, srv.Domain+":3306", ""))
			} else {
				srv.SetIgnored(false)
			}
		}
	}
	cluster.Conf.IgnoreSrv = strings.Join(ignoresrvlist, ",")
	// fmt.Printf("Update config ignored server: " + cluster.Conf.IgnoreSrv + "\n")
}

// SetMaintenanceSrv replaces the durable maintenance-host membership list
// with newList (a comma-separated set of host tokens, deduplicated; matched
// EXACTLY -- see maintenanceListHasHost, never by substring), then syncs the
// runtime IsMaintenance flag for every currently-instantiated server to match
// it. It trusts newList as the complete intended membership -- it does not
// filter tokens against cluster.Servers, so a host temporarily absent from
// the live server list keeps its entry (see AddMaintenanceSrv/
// RemoveMaintenanceSrv, which build newList by editing the existing token
// list rather than reconstructing it from live servers). It does not run the
// db-servers-state-change script or touch proxy backends -- callers
// (SetMaintenance/DelMaintenance/SwitchMaintenance) own those side effects;
// this only tracks membership so it survives restart and config reload (see
// newServerMonitor).
func (cluster *Cluster) SetMaintenanceSrv(newList string) {
	seen := make(map[string]bool)
	var deduped []string
	for _, tok := range maintenanceTokens(newList) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		deduped = append(deduped, tok)
	}
	cluster.Conf.MaintenanceSrv = strings.Join(deduped, ",")

	for _, srv := range cluster.Servers {
		if srv == nil || srv.GetSourceClusterName() != cluster.Name {
			continue
		}
		srv.IsMaintenance = maintenanceListHasHost(cluster.Conf.MaintenanceSrv, srv.URL, srv.Name)
	}
}

// AddMaintenanceSrv adds node to the durable maintenance-host membership and
// persists it, preserving every other entry already tracked -- including
// hosts not currently represented in cluster.Servers.
func (cluster *Cluster) AddMaintenanceSrv(node *ServerMonitor) {
	if maintenanceListHasHost(cluster.Conf.MaintenanceSrv, node.URL, node.Name) {
		node.IsMaintenance = true
		return
	}
	entry := strings.ReplaceAll(node.URL, node.Domain+":3306", "")
	newList := append(maintenanceTokens(cluster.Conf.MaintenanceSrv), entry)
	cluster.SetMaintenanceSrv(strings.Join(newList, ","))
	cluster.ConfigManager.SaveConfig(cluster, false)
}

// RemoveMaintenanceSrv removes node from the durable maintenance-host
// membership and persists it, preserving every other entry already tracked --
// including hosts not currently represented in cluster.Servers.
func (cluster *Cluster) RemoveMaintenanceSrv(node *ServerMonitor) error {
	if node.SourceClusterName != cluster.Name {
		return fmt.Errorf("Host is in child cluster. Cannot remove maintenance")
	}

	if !maintenanceListHasHost(cluster.Conf.MaintenanceSrv, node.URL, node.Name) {
		return fmt.Errorf("Host not found in maintenance list")
	}

	var kept []string
	for _, tok := range maintenanceTokens(cluster.Conf.MaintenanceSrv) {
		if tok == node.URL || tok == node.Name {
			continue
		}
		kept = append(kept, tok)
	}
	cluster.SetMaintenanceSrv(strings.Join(kept, ","))
	cluster.ConfigManager.SaveConfig(cluster, false)
	return nil
}

// Set Ignored for ReadOnly Check
func (cluster *Cluster) SetIgnoreRO(IgnoredReadOnlyHostURL string) {
	var ignoreROList []string
	for _, srv := range cluster.Servers {
		// Always ignore if child cluster but don't add to config
		if srv.GetSourceClusterName() != cluster.Name {
			srv.SetIgnored(true)
		} else {
			// Only add to config if node is in main cluster
			if strings.Contains(IgnoredReadOnlyHostURL, srv.URL) {
				srv.SetIgnoredReadonly(true)
				ignoreROList = append(ignoreROList, strings.ReplaceAll(srv.URL, srv.Domain+":3306", ""))
			} else {
				srv.SetIgnoredReadonly(false)
			}
		}
	}
	cluster.Conf.IgnoreSrvRO = strings.Join(ignoreROList, ",")
	// fmt.Printf("Update config ignored server: " + cluster.Conf.IgnoreSrv + "\n")
}

func (cluster *Cluster) SetFailoverCtr(failoverCtr int) {
	cluster.FailoverCtr = failoverCtr
}

func (cluster *Cluster) SetFailoverTs(failoverTs int64) {
	cluster.FailoverTs = failoverTs
}

func (cluster *Cluster) SetCheckFalsePositiveHeartbeat(CheckFalsePositiveHeartbeat bool) {
	cluster.Conf.CheckFalsePositiveHeartbeat = CheckFalsePositiveHeartbeat
}
func (cluster *Cluster) SetFailRestartUnsafe(check bool) {
	cluster.Conf.FailRestartUnsafe = check
}

func (cluster *Cluster) SetSlavesReadOnly(check bool) {
	for _, sl := range cluster.slaves {
		dbhelper.SetReadOnly(sl.Conn, check)
	}
}
func (cluster *Cluster) SetReadOnly(check bool) {
	cluster.Conf.ReadOnly = check
}

func (cluster *Cluster) SetRplChecks(check bool) {
	cluster.Conf.RplChecks = check
}

func (cluster *Cluster) SetRplMaxDelay(delay int64) {
	cluster.Conf.FailMaxDelay = delay
}

func (cluster *Cluster) SetCleanAll(check bool) {
	cluster.CleanAll = check
}

func (cluster *Cluster) SetFailLimit(limit int) {
	cluster.Conf.FailLimit = limit
}

func (cluster *Cluster) SetFailTime(time int64) {
	cluster.Conf.FailTime = time
}

func (cluster *Cluster) SetMasterStateFailed() {
	cluster.master.SetState(stateFailed)
}

func (cluster *Cluster) SetFailSync(check bool) {
	cluster.Conf.FailSync = check
}

func (cluster *Cluster) SetRejoinDump(check bool) {
	cluster.Conf.AutorejoinMysqldump = check
}

func (cluster *Cluster) SetRejoinBackupBinlog(check bool) {
	cluster.Conf.AutorejoinBackupBinlog = check
}

func (cluster *Cluster) SetRejoinSemisync(check bool) {
	cluster.Conf.AutorejoinSemisync = check
}

func (cluster *Cluster) SetRejoinFlashback(check bool) {
	cluster.Conf.AutorejoinFlashback = check
}

func (cluster *Cluster) SetForceSlaveNoGtid(forceslavenogtid bool) {
	cluster.Conf.ForceSlaveNoGtid = forceslavenogtid
}

func (cluster *Cluster) SetReplicationNoRelay(norelay bool) {
	cluster.Conf.ReplicationNoRelay = norelay
}

// topology setter
func (cluster *Cluster) SetActivePassive(activepassive bool) {
	cluster.Conf.ActivePassive = activepassive
}

func (cluster *Cluster) SetMultiTierSlave(multitierslave bool) {
	cluster.Conf.MultiTierSlave = multitierslave
}
func (cluster *Cluster) SetMultiMasterRing(multimasterring bool) {
	cluster.Conf.MultiMasterRing = multimasterring
}
func (cluster *Cluster) SetMultiMaster(multimaster bool) {
	cluster.Conf.MultiMaster = multimaster
}
func (cluster *Cluster) SetBinlogServer(binlogserver bool) {
	cluster.Conf.MxsBinlogOn = binlogserver
}
func (cluster *Cluster) SetMultiMasterWsrep(wsrep bool) {
	cluster.Conf.MultiMasterWsrep = wsrep
}
func (cluster *Cluster) SetMultiMasterGroupRep(grep bool) {
	cluster.Conf.MultiMasterGrouprep = grep
}
func (cluster *Cluster) SetMultiMasterGroupRepPort(port int) {
	cluster.Conf.MultiMasterGrouprepPort = port
}
func (cluster *Cluster) SetBackupRestic(check bool) error {
	cluster.Conf.BackupRestic = check
	return nil
}
func (cluster *Cluster) SetBackupResticPurgeOldestOnDiskSpace(check bool) {
	cluster.Conf.BackupResticPurgeOldestOnDiskSpace = check
}

func (cluster *Cluster) SetBackupResticPurgeOldestOnDiskTreshold(threshold int) {
	cluster.Conf.BackupResticPurgeOldestOnDiskThreshold = threshold
}

func (cluster *Cluster) SetBackupResticTags(value string) error {
	cluster.Conf.BackupResticTags = strings.Join(parseResticTagTemplates(value), ",")
	return nil
}

func (cluster *Cluster) SetBackupResticHost(value string) error {
	normalized := strings.TrimSpace(value)
	if strings.EqualFold(normalized, "default") || strings.EqualFold(normalized, "none") {
		normalized = ""
	}
	cluster.Conf.BackupResticHost = normalized
	return nil
}

func (cluster *Cluster) SetBackupResticPurgeGroupBy(value string) error {
	cluster.Conf.BackupResticPurgeGroupBy = strings.TrimSpace(value)
	return nil
}

func (cluster *Cluster) SetBackupResticPurgeKeepTag(value string) error {
	normalized := strings.TrimSpace(value)
	if err := validateResticKeepTagTemplatesStrict(normalized); err != nil {
		return err
	}
	cluster.Conf.BackupResticPurgeKeepTag = normalized
	return nil
}

func (cluster *Cluster) SetBackupResticPurgeHost(value string) error {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		cluster.Conf.BackupResticPurgeHost = ""
		return nil
	}
	values := splitResticPurgeFilterValues(normalized)
	cluster.Conf.BackupResticPurgeHost = strings.Join(values, ",")
	return nil
}

func (cluster *Cluster) SetBackupResticPurgeTag(value string) error {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		cluster.Conf.BackupResticPurgeTag = ""
		return nil
	}
	values, hadUnmatched := splitResticTagFilterValues(normalized)
	if hadUnmatched {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Ignoring restic purge tag filter with unmatched quotes in %q", normalized)
	}
	cluster.Conf.BackupResticPurgeTag = strings.Join(values, " ")
	return nil
}

func (cluster *Cluster) SetBackupResticMountTag(value string) error {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		cluster.Conf.BackupResticMountTag = ""
		return nil
	}
	values, hadUnmatched := splitResticTagFilterValues(normalized)
	if hadUnmatched {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Ignoring restic mount tag filter with unmatched quotes in %q", normalized)
	}
	cluster.Conf.BackupResticMountTag = strings.Join(values, " ")
	return nil
}

func (cluster *Cluster) SetBackupResticPurgePath(value string) error {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		cluster.Conf.BackupResticPurgePath = ""
		return nil
	}
	values := splitResticPurgeFilterValues(normalized)
	values = filterResticAbsolutePaths(values, cluster)
	cluster.Conf.BackupResticPurgePath = strings.Join(values, ",")
	return nil
}

func (cluster *Cluster) SetBackupResticPurgePrune(check bool) {
	cluster.Conf.BackupResticPurgePrune = check
}

func (cluster *Cluster) SetBackupResticPurgePruneCompact(check bool) {
	cluster.Conf.BackupResticPurgePruneCompact = check
}

func (cluster *Cluster) SetBackupResticPurgePruneMaxUnused(value string) error {
	cluster.Conf.BackupResticPurgePruneMaxUnused = strings.TrimSpace(value)
	return nil
}

func (cluster *Cluster) SetBackupResticPurgePruneMaxRepackSize(value string) error {
	cluster.Conf.BackupResticPurgePruneMaxRepackSize = strings.TrimSpace(value)
	return nil
}

func (cluster *Cluster) SetBackupResticPurgePruneRepackCacheableOnly(check bool) {
	cluster.Conf.BackupResticPurgePruneRepackCacheableOnly = check
}

func (cluster *Cluster) SetBackupResticPurgePruneRepackSmall(check bool) {
	cluster.Conf.BackupResticPurgePruneRepackSmall = check
}

func (cluster *Cluster) SetBackupResticPurgePruneRepackUncompressed(check bool) {
	cluster.Conf.BackupResticPurgePruneRepackUncompressed = check
}

func (cluster *Cluster) SetMasterReadOnly() {
	if cluster.GetMaster() != nil {
		logs, err := cluster.GetMaster().SetReadOnly()
		cluster.LogSQL(logs, err, cluster.GetMaster().URL, "MasterFailover", config.LvlErr, "Could not set  master as read-only, %s", err)

	}
}

func (cluster *Cluster) SetSwitchSync(check bool) {
	cluster.Conf.SwitchSync = check
}

func (cluster *Cluster) SetFalsePositiveCheck(key string, check bool) {
	cluster.FalsePositiveChecks[key] = check
}

func (cluster *Cluster) SetSwitchoverLockUserOnFreeze(check bool) {
	cluster.Conf.SwitchLockUserOnFreeze = check
}

func (cluster *Cluster) SetLogLevel(level int) {
	cluster.Conf.LogLevel = level
}

func (cluster *Cluster) SetLogFileLevel(level int) {
	cluster.Conf.LogFileLevel = level
}

func (cluster *Cluster) SetRejoin(check bool) {
	cluster.Conf.Autorejoin = check
}

func (cluster *Cluster) SetTestMode(check bool) {
	cluster.Conf.Test = check
}

func (cluster *Cluster) SetTestStopCluster(check bool) {
	cluster.testStopCluster = check
}

func (cluster *Cluster) SetClusterCredentialsFromConfig() {
	// the secrets are the credential chain's cache: rebuilt from its stores, a value set at
	// runtime is kept (#2017)
	cluster.CredentialChain().Refresh()
	cluster.DecryptSecretsFromVault()
	cluster.SetClusterMonitorCredentialsFromConfig()
	cluster.SetClusterReplicationCredentialsFromConfig()
	cluster.SetClusterProxyCredentialsFromConfig()
	// This is not needed! Only for debug!
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Reveal Secrets %v", cluster.Conf.Secrets)
}

func (cluster *Cluster) SetClusterProxyCredentialsFromConfig() {
	secretsUpdated := false

	if cluster.Conf.IsVaultUsed() {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		if cluster.Conf.ProxysqlOn && cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) {
			user, pass, _ := cluster.GetVaultProxySQLCredentials(client)
			var newSecret config.Secret
			if pass != "" {
				newSecret.OldValue = cluster.Conf.Secrets.Get("proxysql-password").Value
				newSecret.Value = pass
				cluster.Conf.Secrets.Set("proxysql-password", newSecret)
				secretsUpdated = true
			}

			if user != "" {
				newSecret.OldValue = cluster.Conf.Secrets.Get("proxysql-user").Value
				newSecret.Value = user
				cluster.Conf.Secrets.Set("proxysql-user", newSecret)
				secretsUpdated = true
			}

		}
		if cluster.Conf.MdbsProxyOn && cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) {
			user, pass, _ := cluster.GetVaultShardProxyCredentials(client)
			var newSecret config.Secret
			if user != "" && pass != "" {

				newSecret.OldValue = cluster.Conf.Secrets.Get("shardproxy-credential").Value
				newSecret.Value = user + ":" + pass
				cluster.Conf.Secrets.Set("shardproxy-credential", newSecret)
				secretsUpdated = true
			}
		}

	}

	if secretsUpdated {
		cluster.MarkSecretVersionStoreDirty()
	}
}

// reloadDBTLSCertificates loads the current and previous database TLS certificates and,
// when what is available changed (an ssl tag added or dropped, certificates configured or
// removed), rebuilds every server DSN: SetDSN writes the TLS mode into the DSN only when
// called, so a stale "&tls=tlsconfig" kept repman unable to reach healthy servers after
// the ssl tag was dropped (#1974).
func (cluster *Cluster) reloadDBTLSCertificates() {
	prevCert, prevOld := cluster.HaveDBTLSCert, cluster.HaveDBTLSOldCert
	if err := cluster.loadDBCertificates(cluster.WorkingDir); err != nil {
		cluster.HaveDBTLSCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database TLS certificates")
	} else {
		cluster.HaveDBTLSCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS certificates correctly loaded")
	}
	if err := cluster.loadDBOldCertificates(cluster.WorkingDir + "/old_certs"); err != nil {
		cluster.HaveDBTLSOldCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database previous TLS certificates")
	} else {
		cluster.HaveDBTLSOldCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS previous certificates correctly loaded")
	}
	if cluster.HaveDBTLSCert == prevCert && cluster.HaveDBTLSOldCert == prevOld {
		return
	}
	// Only the DSN strings change here: the pools follow at the next connection, Ping
	// opening one per tick (GetNewDBConn). TLSConfigUsed goes back to the current
	// certificates, dropping any per-server fallback to the previous ones: the set of
	// certificates it chose between has just changed. Written like every other SetDSN
	// caller, outside the cluster lock (a tick reads it, a reload writes it).
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		server.TLSConfigUsed = ConstTLSCurrentConfig
		server.SetDSN()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Database TLS certificates changed (current %t, previous %t): server connection strings rebuilt", cluster.HaveDBTLSCert, cluster.HaveDBTLSOldCert)
}

func (cluster *Cluster) SetClusterMonitorCredentialsFromConfig() {
	cluster.Configurator.SetConfig(*cluster.Conf)
	//splitmonitoringuser := cluster.Conf.User

	cluster.reloadDBTLSCertificates()

	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.User) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		user, pass, _ := cluster.GetVaultMonitorCredentials(client)
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets.Get("db-servers-credential").Value
		newSecret.Value = user + ":" + pass
		cluster.Conf.Secrets.Set("db-servers-credential", newSecret)
		cluster.MarkSecretVersionStoreDirty()

	}

	cluster.SetDbServerHosts(cluster.Conf.Hosts)
	/*
		cluster.dbUser, cluster.dbPass = misc.SplitPair(splitmonitoringuser)

		cluster.dbPass = cluster.GetDecryptedPassword("db-servers-credential", cluster.dbPass)*/
	cluster.LoadAPIUsers()
	cluster.ConfigManager.SaveConfig(cluster, false)
}

func (cluster *Cluster) SetClusterReplicationCredentialsFromConfig() {
	cluster.Configurator.SetConfig(*cluster.Conf)

	//splitreplicationuser := cluster.Conf.RplUser

	cluster.reloadDBTLSCertificates()
	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.RplUser) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		user, pass, _ := cluster.GetVaultReplicationCredentials(client)
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets.Get("replication-credential").Value
		newSecret.Value = user + ":" + pass
		cluster.Conf.Secrets.Set("replication-credential", newSecret)
		cluster.MarkSecretVersionStoreDirty()

	}
}

func (cluster *Cluster) SetAgentsMaxCpuFreq() {
	if len(cluster.Agents) != 0 {
		for _, a := range cluster.Agents {
			old, ok := cluster.AgentMaxFreq[a.Id]
			if ok && a.CpuFreq < old {
				a.CpuFreq = old
			} else {
				cluster.AgentMaxFreq[a.Id] = a.CpuFreq
			}
		}
	}
}

// SetBackupKeepLastN set the number of backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepLastN(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepLast = numkeep
	return nil
}

// SetBackupLogicalRemotePickupTimeout sets the seconds a remote logical backup waits for dbjobs to take it.
func (cluster *Cluster) SetBackupLogicalRemotePickupTimeout(value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.BackupLogicalRemotePickupTimeout = v
	return nil
}

// SetBackupLogicalRemoteReportTimeout sets the seconds a remote logical backup waits for the report of dbjobs.
func (cluster *Cluster) SetBackupLogicalRemoteReportTimeout(value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.BackupLogicalRemoteReportTimeout = v
	return nil
}

// SetBackupLogicalRemoteArmTimeout sets the seconds dbjobs' receive-task request waits for a remote logical backup to be ready.
func (cluster *Cluster) SetBackupLogicalRemoteArmTimeout(value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.BackupLogicalRemoteArmTimeout = v
	return nil
}

// SetBackupKeepHourly set the number of hourly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepHourly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepHourly = numkeep
	return nil
}

// SetBackupKeepDaily set the number of daily backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepDaily(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepDaily = numkeep
	return nil
}

// SetBackupKeepWeekly set the number of weekly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWeekly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepWeekly = numkeep
	return nil
}

// SetBackupKeepMonthly set the number of monthly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepMonthly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepMonthly = numkeep
	return nil
}

// SetBackupKeepYearly set the number of yearly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepYearly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepYearly = numkeep
	return nil
}

// SetBackupKeepWithin set the duration of backups to keep.
// If the value is empty, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithin(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithin = keep
	return nil
}

// SetBackupKeepWithinHourly set the number of hourly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithinHourly(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithinHourly = keep
	return nil
}

// SetBackupKeepWithinDaily set the number of daily backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithinDaily(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithinDaily = keep
	return nil
}

// SetBackupKeepWithinWeekly set the number of weekly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithinWeekly(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithinWeekly = keep
	return nil
}

// SetBackupKeepWithinMonthly set the number of monthly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithinMonthly(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithinMonthly = keep
	return nil
}

// SetBackupKeepWithinYearly set the number of yearly backups to keep.
// If the value is 0, the arguments will be omitted for purge, which means that the backup can be purged due to arguments not found.
func (cluster *Cluster) SetBackupKeepWithinYearly(keep string) error {
	if !config.ResticDurationChecker(keep) {
		return fmt.Errorf("Invalid duration format")
	}

	cluster.Conf.BackupKeepWithinYearly = keep
	return nil
}

// SetBackupKeepTagPolicies sets the cluster's tagged purge policies (#2091), checked, written
// back in their canonical form.
func (cluster *Cluster) SetBackupKeepTagPolicies(value string) error {
	rules, err := config.ParseArchivePurgeRules(value, false)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepTagPolicies = config.FormatArchivePurgeRules(rules)
	return nil
}

func (cluster *Cluster) SetBackupLogicalType(backup string) {
	if cluster.Conf.BackupLogicalType != backup {
		cluster.Conf.BackupLogicalType = backup
		bcksrv := cluster.GetBackupServer()
		if bcksrv != nil {
			bcksrv.DelBackupLogicalCookie()
		}
	}
}

func (cluster *Cluster) SetBackupPhysicalType(backup string) {
	if cluster.Conf.BackupPhysicalType != backup {
		cluster.Conf.BackupPhysicalType = backup
		bcksrv := cluster.GetBackupServer()
		if bcksrv != nil {
			bcksrv.DelBackupLogicalCookie()
		}
	}
}

func (cluster *Cluster) SetBackupBinlogType(backup string) {
	cluster.Conf.BinlogCopyMode = backup
}

func (cluster *Cluster) SetBackupBinlogScript(filename string) {
	cluster.Conf.BinlogCopyScript = filename
}

func (cluster *Cluster) SetBinlogParseMode(tool string) {
	cluster.Conf.BinlogParseMode = tool
}

func (cluster *Cluster) SetEmptySla() {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rotate SLA")
	cluster.SLAHistory = append(cluster.SLAHistory, cluster.StateMachine.GetSla())
	cluster.StateMachine.ResetUptime()
}

func (cluster *Cluster) SetDbServersMonitoringCredential(credential string) {
	cluster.Conf.User = credential
	for _, srv := range cluster.Servers {
		srv.SetCredential(srv.URL, cluster.GetDbUser(), cluster.GetDbPass())
	}
	cluster.SetUnDiscovered()
	cluster.SetDBRestartCookie()
	if cluster.Conf.VaultMode == VaultConfigStoreV2 && !cluster.isMasterFailed() {
		found_user := false
		for _, u := range cluster.master.Users.ToNewMap() {
			if u.User == cluster.GetDbUser() {
				found_user = true
				logs, err := dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, cluster.GetDbPass())
				cluster.LogSQL(logs, err, cluster.master.URL, "Security", config.LvlErr, "Alter user : %s", err)

			}

		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Monitoring password rotation")
		if !found_user {
			oldDbUser, _ := misc.SplitPair(cluster.Conf.Secrets.Get("db-servers-credential").OldValue)
			if oldDbUser != "root" {

				for _, u := range cluster.master.Users.ToNewMap() {
					if u.User == oldDbUser {
						logs, err := dbhelper.RenameUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, cluster.GetDbPass(), cluster.GetDbUser())
						cluster.LogSQL(logs, err, cluster.master.URL, "Security", config.LvlErr, "Alter user : %s", err)
						logs, err = dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, cluster.GetDbUser(), cluster.GetDbPass())
						cluster.LogSQL(logs, err, cluster.master.URL, "Security", config.LvlErr, "Alter user : %s", err)

					}

				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Monitoring user rotation")
			} else {
				//si on est en dynamique config:
				//créer un nouveau user diff de root qui possède les mêmes droits
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Changing root user is not allowed")
			}
		}
		for _, pri := range cluster.Proxies {
			if prx, ok := pri.(*ProxySQLProxy); ok {
				prx.RotateMonitoringPasswords(cluster.GetDbPass())
			}
		}
		err := cluster.ProvisionRotatePasswords(cluster.GetDbPass())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password: %v", err)
		}
	}
}

func (cluster *Cluster) SetProxyServersCredential(credential string, proxytype string) error {
	chain := cluster.CredentialChain()
	switch proxytype {
	case config.ConstProxySpider:
		//cluster.Conf.MdbsProxyCredential = credential
		if err := chain.Set("shardproxy-credential", credential); err != nil {
			return err
		}
		cluster.MarkSecretVersionStoreDirty()

		for _, pri := range cluster.Proxies {
			_, pass := misc.SplitPair(credential)
			if prx, ok := pri.(*MariadbShardProxy); ok {

				prx.RotateProxyPasswords(pass)
				prx.SetCredential(credential)
				prx.ShardProxy.SetCredential(prx.ShardProxy.URL, prx.User, pass)
				for _, u := range prx.ShardProxy.Users.ToNewMap() {
					if u.User == prx.User {
						dbhelper.SetUserPassword(prx.ShardProxy.Conn, prx.ShardProxy.DBVersion, u.Host, u.User, pass)
					}

				}

			}
		}
	case config.ConstProxySqlproxy:
		//cluster.Conf.ProxysqlUser, cluster.Conf.ProxysqlPassword
		user, pass := misc.SplitPair(credential)
		if err := chain.CanSet("proxysql-user"); err != nil {
			return err
		}
		if err := chain.Set("proxysql-password", pass); err != nil {
			return err
		}
		if err := chain.Set("proxysql-user", user); err != nil {
			return err
		}
		cluster.MarkSecretVersionStoreDirty()
		for _, pri := range cluster.Proxies {
			_, pass := misc.SplitPair(credential)
			if prx, ok := pri.(*ProxySQLProxy); ok {

				prx.RotateProxyPasswords(pass)
				prx.SetCredential(credential)
				pri.SetCredential(credential)
				prx.SetRestartCookie()

			}
		}
	case config.ConstProxyMaxscale:
		user, pass := misc.SplitPair(credential)
		if err := chain.Set("maxscale-pass", pass); err != nil {
			return err
		}
		cluster.Conf.MxsUser = user
		cluster.Conf.MxsPass = pass
		cluster.MarkSecretVersionStoreDirty()

		/*for _, pri := range cluster.Proxies {

			pri.SetCredential(credential)
			pri.SetRestartCookie()

		}*/
	}
	return nil
}

// Set Cookie for all servers that configuration has changed
func (cluster *Cluster) SetConfigChangeCookie() {
	for _, srv := range cluster.Servers {
		if srv == nil || srv.IsIgnored() {
			continue
		}
		srv.SetConfigCookie()        // Persist until config deployed
		srv.SetConfigRefreshCookie() // Until config refreshed
	}
}

// Set Cookie for all servers that configuration variables need refresh
func (cluster *Cluster) SetConfigRefreshCookie() {
	for _, srv := range cluster.Servers {
		if srv == nil || srv.IsIgnored() {
			continue
		}
		srv.SetConfigRefreshCookie()
	}
}

func (cluster *Cluster) SetDBRestartCookie() {
	for _, srv := range cluster.Servers {
		srv.SetRestartCookie()
	}
}

// SetDBReprovCookie asks every database for a reprovision, recording why (#1969).
func (cluster *Cluster) SetDBReprovCookie(reason string) {
	for _, srv := range cluster.Servers {
		srv.SetReprovCookieFor(reason)
	}
}
func (cluster *Cluster) SetDBConfigPathCookie() {
	for _, srv := range cluster.Servers {
		if srv == nil || srv.IsIgnored() {
			continue
		}
		srv.SetConfigPathCookie()
	}
}

func (cluster *Cluster) SetWaitDBACredCookie() {
	for _, srv := range cluster.Servers {
		srv.SetWaitDBACredCookie()
	}
}

func (cluster *Cluster) SetWaitSponsorCredCookie() {
	for _, srv := range cluster.Servers {
		srv.SetWaitSponsorCredCookie()
	}
}

func (cluster *Cluster) SetWaitMonitorSchema() {
	cluster.SetState("WARN0163", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["WARN0163"], cluster.Name), ErrFrom: "API"})
	atomic.StoreInt32(&cluster.SchemaMonitorRequested, 1)
}

func (cluster *Cluster) SetDBDynamicConfig() {
	for _, srv := range cluster.Servers {
		//conf:=
		cmd := "mariadb_command"
		if !srv.IsMariaDB() {
			cmd = "mysql_command"
		}
		srv.GetDatabaseConfig()
		srv.ExecScriptSQL(strings.Split(srv.GetDatabaseDynamicConfig("", cmd), ";"))
	}
}

func (cluster *Cluster) SetProxiesRestartCookie() {
	for _, prx := range cluster.Proxies {
		prx.SetRestartCookie()
	}
}
func (cluster *Cluster) SetProxiesReprovCookie() {
	for _, prx := range cluster.Proxies {
		prx.SetReprovCookie()
	}
}

func (cluster *Cluster) SetReplicationCredential(credential string) error {
	if err := cluster.CredentialChain().Set("replication-credential", credential); err != nil {
		return err
	}
	cluster.Conf.RplUser = credential
	cluster.MarkSecretVersionStoreDirty()
	cluster.SetClusterReplicationCredentialsFromConfig()
	cluster.SetUnDiscovered()
	return nil
}

func (cluster *Cluster) SetUnDiscovered() {
	cluster.StateMachine.UnDiscovered()
	cluster.Topology = config.TopoUnknown
}

func (cluster *Cluster) SetActiveStatus(status string) {
	if cluster.Status == status {
		return
	}
	if cluster.Status == ConstMonitorStandby && status == ConstMonitorActif {
		// a standby never writes the catalogue: the backups only in an archive are read again
		cluster.requestCatalogueRebuild("standby became active")
	}
	cluster.Status = status
	if cluster.Conf.MonitorScheduler {
		if cluster.Status == ConstMonitorActif {
			cluster.scheduler.Start()
		} else {
			cluster.scheduler.Stop()
		}
	}
}

func (cluster *Cluster) SetMonitoringScheduler(isactive bool) {
	if cluster.Conf.MonitorScheduler != isactive {
		cluster.Conf.MonitorScheduler = isactive
		if cluster.Conf.MonitorScheduler {
			if cluster.scheduler == nil {
				// A cluster started with the scheduler off has none yet (#2120): initScheduler
				// creates it with its jobs, and starts it when the monitor is active.
				cluster.initScheduler()
			} else if cluster.Status == ConstMonitorActif {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting scheduler")
				cluster.scheduler.Start()
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Scheduler enabled but monitoring is in standby mode. Scheduler will start when monitoring is in active mode")
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping scheduler")
			cluster.scheduler.Stop()
		}
	}
}

func (cluster *Cluster) SetTestStartCluster(check bool) {
	cluster.testStartCluster = check
}

func (cluster *Cluster) SetLogStdout() {
	cluster.Conf.Daemon = true
}

func (cluster *Cluster) SetClusterList(clusters map[string]*Cluster) {
	cluster.Lock()
	cluster.clusterList = clusters
	cluster.Unlock()
}

// SetClusterOrder stores the ordered cluster-name slice that determines
// gateway-route ownership priority (first entry = highest priority).
// Must be called alongside SetClusterList so OpenSVCProvisionRoute can
// apply "first wins" conflict resolution at publish time.
func (cluster *Cluster) SetClusterOrder(order []string) {
	cluster.Lock()
	cluster.clusterOrder = order
	cluster.Unlock()
}

func (cluster *Cluster) SetDeprecatedKeys(keys map[string]map[string]bool) {
	cluster.deprecatedKeys = keys
}

func (cluster *Cluster) SetState(key string, s state.State) {
	if !strings.Contains(cluster.Conf.MonitorIgnoreErrors, key) {
		cluster.StateMachine.AddState(key, s)
	}
}

func (cl *Cluster) SetArbitratorReport() error {
	//	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)

	// Split-brain simulator: the arbitrator link is cut, so this node cannot report either —
	// a minority node must stop refreshing its arbitrator row so it goes
	// stale (~10s) and the majority wins the next per-tick election.
	if cl.IsArbitratorFailureSimulated() {
		return errors.New("split-brain simulation: arbitrator link down")
	}

	// Read the topology inputs under the cluster lock so this report is safe to
	// fire CONCURRENTLY from the server heartbeat every split-tick. cluster.Servers
	// is REASSIGNED (make/append/delete) during TopologyDiscover and server-delete
	// under this same lock; an unlocked read racing that reassignment can tear the
	// slice header and panic. The hold is ONLY the quick reads (no I/O) — the
	// network POST below runs UNLOCKED, so we never block TopologyDiscover on HTTP.
	cl.Lock()
	cl.IsLostMajority = cl.LostMajority()
	var mst string
	if m := cl.GetMaster(); m != nil {
		mst = m.URL
	}
	srvs := cl.GetServers()
	hosts := len(srvs)
	failed := cl.CountFailed(srvs)
	status := cl.Status
	name := cl.Name
	cl.Unlock()

	url := cl.arbitratorURL("/heartbeat")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlDbg, "SetArbitratorReport: sending hosts=%d failed=%d cluster=%s", hosts, failed, name)
	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + name + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + status + `","hosts":` + strconv.Itoa(hosts) + `,"failed":` + strconv.Itoa(failed) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		if cl.Conf.LogHeartbeat {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Failed to post http new request to arbitrator %s ", jsonStr)
		}
		cl.IsFailedArbitrator = true
		return err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	//client := &http.Client{Timeout: timeout}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cl.Conf.ArbitrationReadTimout)*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	startConnect := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		cl.IsFailedArbitrator = true
		return err
	}
	defer client.CloseIdleConnections()
	stopConnect := time.Now()
	// if cl.GetLogLevel() > 2 {
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, " Report abitrator connect took: %s\n", stopConnect.Sub(startConnect))
	// }
	io.ReadAll(resp.Body)
	defer resp.Body.Close()
	cl.IsFailedArbitrator = false
	return nil
}

// SetClusterHead for MariaDB spider we can arbtitraty shard tables to child clusters
func (cluster *Cluster) SetClusterHead(ClusterName string) {
	cluster.Conf.ClusterHead = ClusterName
}

func (cluster *Cluster) SetSysbenchTest(test string) {
	if config.GetSysbenchTests()[test] {
		cluster.Conf.SysbenchTest = test
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Sysbench test to %s", test)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unknown sysbench test: %s", test)
	}
}

func (cluster *Cluster) SetSysbenchThreads(Threads string) {
	i, err := strconv.Atoi(Threads)
	if err == nil {
		cluster.Conf.SysbenchThreads = i
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error converting threads to int %s", err)
	}
}

func (cluster *Cluster) SetSysbenchTime(t string) {
	i, err := strconv.Atoi(t)
	if err == nil {
		cluster.Conf.SysbenchTime = i
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error converting sysbench time to int %s", err)
	}
}

/*
Set Service Plan. Log Module : Topology
*/

func (cluster *Cluster) SetServicePlanInfos(theplan string) error {
	plans := cluster.GetServicePlans()
	for _, plan := range plans {
		if plan.Plan == theplan {

			cluster.Conf.ProvServicePlan = theplan

			// Provisioning specs are guarded by immutability: a value the
			// operator has pinned (present in ImmuableFlagMap / immutable.toml)
			// is a hard lock that attaching or refreshing a service plan must
			// NOT overwrite. Without this, a plan attach pushed the plan tier
			// onto prov-db-memory etc.; the immutable-flag reapply then reverted
			// the Conf side but not the Configurator side, leaving the GUI
			// showing the plan value (e.g. 4G) while provisioning used the
			// pinned value (e.g. 768M). To move a pinned spec the operator must
			// first unpin it. Pricing/SLA/infra descriptors below are plan
			// metadata, not resource knobs, so they always follow the plan.
			applyPlanSpec := func(flag string, apply func()) {
				if cluster.IsVariableImmutable(flag) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
						"Service plan %s: keeping pinned %s, plan value not applied (variable is immutable)", theplan, flag)
					return
				}
				apply()
			}
			applyPlanSpec("prov-db-cpu-cores", func() { cluster.SetDBCores(strconv.Itoa(plan.DbCores)) })
			applyPlanSpec("prov-db-memory", func() { cluster.SetDBMemorySize(strconv.Itoa(plan.DbMemory)) })
			applyPlanSpec("prov-db-disk-size", func() { cluster.SetDBDiskSize(strconv.Itoa(plan.DbDataSize)) })
			applyPlanSpec("prov-db-disk-iops", func() { cluster.SetDBDiskIOPS(strconv.Itoa(plan.DbIops)) })
			applyPlanSpec("prov-proxy-cpu-cores", func() { cluster.SetProxyCores(strconv.Itoa(plan.PrxCores)) })
			applyPlanSpec("prov-proxy-disk-size", func() { cluster.SetProxyDiskSize(strconv.Itoa(plan.PrxDataSize)) })
			cluster.SetCloud18MonthlyInfraCost(plan.InfraCost)
			cluster.SetCloud18MonthlyLicenseCost(plan.LicenceCost)
			cluster.SetCloud18MonthlySysopsCost(plan.SysCost)
			cluster.SetCloud18MonthlyDbopsCost(plan.DbaCost)
			cluster.SetCloud18CostCurrency(plan.Devise)
			cluster.SetCloud18InfraCPUFreq(plan.CPUFreq)
			cluster.SetCloud18InfraCPUModel(plan.CPU)
			cluster.SetCloud18InfraCertifications(plan.Certs)
			cluster.SetCloud18ExternalDbOps(plan.ExtDbOps)
			cluster.SetCloud18ExternalSysOps(plan.ExtSysOps)
			cluster.SetCloud18InfraDescription(plan.Infra)
			cluster.SetCloud18InfraDataCenters(plan.DC)
			cluster.SetCloud18InfraPublicBandwidth(plan.BP)
			cluster.SetCloud18InfraGeoLocalizations(plan.Zone)
			cluster.SetCloud18SlaResponseTime(plan.ResponseTime)
			cluster.SetCloud18SlaRepairTime(plan.RepairTime)
			cluster.SetCloud18SlaProvisionTime(plan.ProvisionTime)
			cluster.SetCloud18PromotionPct(plan.PromotionPct)
			cluster.ConfigManager.SaveConfig(cluster, false)
			return nil
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Service plan not found %s", theplan)
	return errors.New("Plan not found in repository")
}

func (cluster *Cluster) SetServicePlan(theplan string) error {
	plans := cluster.GetServicePlans()

	for _, plan := range plans {
		if plan.Plan == theplan {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Attaching service plan %s", theplan)
			srvcount, err := strconv.Atoi(string(strings.TrimPrefix(theplan, "x")[0]))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Can't add database monitor error %s ", err)
			}
			srvcountnow := len(strings.Split(cluster.Conf.Hosts, ","))
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Count exiting databases monitor: %d,", srvcountnow)
			if !cluster.IsProvision {

				if cluster.Conf.User == "" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Settting default database root credential to admin:repman ")
					cluster.Conf.User = "admin:repman"
				}
				if cluster.Conf.RplUser == "" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Settting default database replication credential to repl:repman ")
					cluster.Conf.RplUser = "repl:repman"
				}

				if srvcount > srvcountnow {

					hosts := []string{}
					for i := 1; i <= srvcount; i++ {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "'%s' '%s'", cluster.Conf.ProvOrchestrator, config.ConstOrchestratorLocalhost)
						if i <= srvcountnow {
							hosts = append(hosts, strings.Split(cluster.Conf.Hosts, ",")[i])
						} else {
							if cluster.GetOrchestrator() == config.ConstOrchestratorLocalhost {
								port, err := cluster.LocalhostGetFreePort()
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Adding DB monitor on 127.0.0.1 %s", err)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding DB monitor 127.0.0.1:%s", port)
								}
								hosts = append(hosts, "127.0.0.1:"+port)
							} else if cluster.GetOrchestrator() != config.ConstOrchestratorOnPremise {
								hosts = append(hosts, "db"+strconv.Itoa(i))
							}
						}
					}
					//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology,config.LvlErr, strings.Join(hosts, ","))
					err = cluster.SetDbServerHosts(strings.Join(hosts, ","))
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "SetServicePlan : Fail SetDbServerHosts : %s, for hosts : %s", err, strings.Join(hosts, ","))
					}
					cluster.StateMachine.SetFailoverState()
					err = cluster.newServerList()
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "SetServicePlan : Fail newServerList : %s", err)
					}
					wg := new(sync.WaitGroup)
					wg.Add(1)
					go cluster.TopologyDiscover(wg)
					wg.Wait()
					cluster.StateMachine.RemoveFailoverState()
					cluster.Conf.ProxysqlOn = true
					cluster.Conf.ProxysqlHosts = ""
					cluster.Conf.MdbsProxyOn = true
					cluster.Conf.MdbsProxyHosts = ""
					// cluster head is used to copy exiting proxy from an other cluster
					if cluster.Conf.ClusterHead == "" {
						if cluster.GetOrchestrator() == config.ConstOrchestratorLocalhost {
							portproxysql, err := cluster.LocalhostGetFreePort()
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Adding proxysql monitor on 127.0.0.1 %s", err)
							} else {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding proxysql monitor 127.0.0.1:%s", portproxysql)
							}
							portshardproxy, err := cluster.LocalhostGetFreePort()
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Adding shard proxy monitor on 127.0.0.1 %s", err)
							} else {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding shard proxy monitor 127.0.0.1:%s", portshardproxy)
							}
							err = cluster.AddSeededProxy(config.ConstProxySqlproxy, "127.0.0.1", portproxysql, "", "")
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Fail adding proxysql monitor on 127.0.0.1 %s", err)
							}
							err = cluster.AddSeededProxy(config.ConstProxySpider, "127.0.0.1", portshardproxy, "", "")
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Fail adding shard proxy monitor on 127.0.0.1 %s", err)
							}
						} else {
							err = cluster.AddSeededProxy(config.ConstProxySpider, "shardproxy1", "3306", "", "")
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Fail adding shard proxy monitor on 3306 %s", err)
							}
							//cluster.Conf.ProxysqlUser = "external"
							err = cluster.AddSeededProxy(config.ConstProxySqlproxy, "proxysql1", cluster.Conf.ProxysqlPort, "external", cluster.Conf.Secrets.Get("proxysql-password").Value)
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Fail adding proxysql monitor on %s %s", cluster.Conf.ProxysqlPort, err)
							}
						}
					} else { // not cluster head
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Copy proxy list from cluster head %s", cluster.Conf.ClusterHead)

						oriClusters, err := cluster.GetClusterFromName(cluster.Conf.ClusterHead)
						if err == nil {
							for _, oriProxy := range oriClusters.Proxies {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding new proxy %s copy %s:%s", oriProxy.GetType(), oriProxy.GetHost(), oriProxy.GetPort())
								if oriProxy.GetType() == config.ConstProxySpider {
									cluster.AddSeededProxy(oriProxy.GetType(), oriProxy.GetHost(), oriProxy.GetPort(), oriProxy.GetUser(), oriProxy.GetPass())
								}
							}
							if cluster.GetOrchestrator() == config.ConstOrchestratorLocalhost {
								portproxysql, err := cluster.LocalhostGetFreePort()
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Adding proxysql monitor on 127.0.0.1 %s", err)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding proxysql monitor 127.0.0.1:%s", portproxysql)
								}
								cluster.AddSeededProxy(config.ConstProxySqlproxy, "127.0.0.1", portproxysql, "", "")

							} else {
								cluster.AddSeededProxy(config.ConstProxySqlproxy, "proxysql1", cluster.Conf.ProxysqlPort, "", "")
							}
						}
					}
				} // No need to add database server
				cluster.ConfigManager.SaveConfig(cluster, false)
			} // End of cluster not already provisioned
			if srvcount == srvcountnow {
				cluster.SetServicePlanInfos(theplan)
				return nil
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Service plan databases count does not match monitored databases %s %d", theplan, srvcountnow)
				return errors.New("Plan not possible for that cluster")
			}
		} // End of if the plan
	} // End of for each plan
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Service plan not found %s", theplan)
	return errors.New("Plan not found in repository")
}

func (cluster *Cluster) SetProvNetCniCluster(value string) error {
	cluster.Conf.ProvNetCNICluster = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvOrchestratorCluster(value string) error {
	cluster.Conf.ProvOrchestratorCluster = value
	cluster.SetProxiesReprovCookie()
	return nil
}

// SetProvKubeStorageClass only affects the PVC built for a server's *next*
// (re)provision (k8sDatabasePVC, prov_k8s_db.go), same as SetDBDiskSize --
// so it needs the DB reprov cookie, not the proxy one. Note this only takes
// effect for a server whose PVC doesn't already exist: k8sDatabasePVC's
// Create() is a no-op (AlreadyExists) against an existing PVC, and
// StorageClassName is immutable on a PVC once created -- reprovisioning an
// existing server alone will never migrate it to a newly-set StorageClass;
// the old PVC would need to be deleted first (destructive, and today never
// done automatically -- see k8sUnprovisionDatabaseServiceWithClient's own
// retention comment).
func (cluster *Cluster) SetProvKubeStorageClass(value string) error {
	cluster.Conf.ProvKubeStorageClass = value
	cluster.SetDBReprovCookie(ReprovReasonStorageClass)
	return nil
}

// SetProvKubeProxyStorageClass is SetProvKubeStorageClass's proxy-side
// counterpart (k8sProxyPVC, prov_k8s_prx.go) -- only affects a proxy's
// *next* (re)provision, so it needs the proxy reprov cookie, not the DB one.
// Same existing-PVC caveat as SetProvKubeStorageClass applies: proxy PVCs
// are retained on unprovision (k8sUnprovisionProxyServiceWithClient), so a
// reprovision of an existing proxy reuses that same PVC and its original
// StorageClass -- this setting only takes effect for a proxy name whose PVC
// doesn't exist yet.
func (cluster *Cluster) SetProvKubeProxyStorageClass(value string) error {
	cluster.Conf.ProvKubeProxyStorageClass = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbAgents(value string) error {
	cluster.Conf.ProvAgents = value
	return nil
}

func (cluster *Cluster) SetProvProxyAgents(value string) error {
	cluster.Conf.ProvProxAgents = value
	return nil
}

func (cluster *Cluster) SetMonitoringAddress(value string) error {
	cluster.Conf.MonitorAddress = value
	return nil
}

// SetArchivePurgeTrigger sets when the attached archive is asked to apply the retention
// policy: after-backup or schedule (#2091).
func (cluster *Cluster) SetArchivePurgeTrigger(value string) error {
	if err := config.ValidateArchivePurgeTrigger(value); err != nil {
		return err
	}
	cluster.Conf.BackupArchivePurgeTrigger = value
	cluster.SetSchedulerArchivePurge()
	return nil
}

func (cluster *Cluster) SetSchedulerArchivePurgeCron(value string) error {
	cluster.Conf.BackupArchivePurgeCron = value
	cluster.SetSchedulerArchivePurge()
	return nil
}

// SetBackupStreamingProvider sets the S3 provider of the backups folder mount; the mount
// follows at once (#2091).
func (cluster *Cluster) SetBackupStreamingProvider(value string) error {
	if value != "" {
		found := false
		for _, p := range cluster.GetS3ProvidersSnapshot() {
			found = found || p.Name == value
		}
		if !found {
			return fmt.Errorf("no S3 provider %q", value)
		}
	}
	cluster.Conf.BackupStreamingProvider = value
	go cluster.RefreshBackupMount()
	return nil
}

// SetBackupStreamingBucket sets the bucket of the backups folder mount.
func (cluster *Cluster) SetBackupStreamingBucket(value string) error {
	cluster.Conf.BackupStreamingBucket = value
	go cluster.RefreshBackupMount()
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersLogicalBackupCron(value string) error {
	cluster.Conf.BackupLogicalCron = value
	cluster.SetSchedulerBackupLogical()
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersPhysicalBackupCron(value string) error {
	cluster.Conf.BackupPhysicalCron = value
	cluster.SetSchedulerBackupPhysical()
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersOptimizeCron(value string) error {
	cluster.Conf.BackupDatabaseOptimizeCron = value
	cluster.SetSchedulerOptimize()
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersAnalyzeCron(value string) error {
	cluster.Conf.BackupDatabaseAnalyzeCron = value
	cluster.SetSchedulerAnalyze()
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersLogsCron(value string) error {
	cluster.Conf.BackupDatabaseLogCron = value
	cluster.SetSchedulerBackupLogs()
	return nil
}

func (cluster *Cluster) SetProxyServersBackendMaxConnections(value string) error {
	numvalue, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.PRXServersBackendMaxConnections = numvalue
	return nil
}

// SetSwitchoverWaitWriteQuery sets switchover-wait-write-query: the number of seconds a
// write query or an open InnoDB transaction may have been running on the master before a
// switchover is cancelled (the guard in MasterFailover, CheckLongRunningWrites). At least 1:
// with 0 every running write matches and no switchover would ever pass.
func (cluster *Cluster) SetSwitchoverWaitWriteQuery(value string) error {
	numvalue, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("switchover-wait-write-query: %w", err)
	}
	if numvalue < 1 {
		return fmt.Errorf("switchover-wait-write-query must be at least 1 second, got %d", numvalue)
	}
	cluster.Conf.SwitchWaitWrite = numvalue
	return nil
}

// SetSwitchoverWaitTrx sets switchover-wait-trx: the seconds a switchover waits, before
// anything is frozen, for the long writes found by the switchover-wait-write-query guard to
// complete, then again for the pre-flush of the master tables. At least 1: with 0 the flush
// timeout fires at once and no switchover would ever pass.
func (cluster *Cluster) SetSwitchoverWaitTrx(value string) error {
	numvalue, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("switchover-wait-trx: %w", err)
	}
	if numvalue < 1 {
		return fmt.Errorf("switchover-wait-trx must be at least 1 second, got %d", numvalue)
	}
	cluster.Conf.SwitchWaitTrx = numvalue
	return nil
}

func (cluster *Cluster) SetSwitchoverWaitRouteChange(value string) error {
	numvalue, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.SwitchSlaveWaitRouteChange = numvalue
	return nil
}

func (cluster *Cluster) SetBackupBinlogsKeep(value string) error {
	numvalue, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.BackupBinlogsKeep = numvalue
	return nil
}

func (cluster *Cluster) SetProxyServersBackendMaxReplicationLag(value string) error {
	numvalue, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	cluster.Conf.PRXServersBackendMaxReplicationLag = numvalue
	return nil
}

func (cluster *Cluster) SetSchedulerDbServersLogsTableRotateCron(value string) error {
	cluster.Conf.SchedulerDatabaseLogsTableRotateCron = value
	cluster.SetSchedulerLogsTableRotate()
	return nil
}

func (cluster *Cluster) SetSchedulerSlaRotateCron(value string) error {
	cluster.Conf.SchedulerSLARotateCron = value
	cluster.SetSchedulerSlaRotate()
	return nil
}

func (cluster *Cluster) SetSchedulerRollingRestartCron(value string) error {
	cluster.Conf.SchedulerRollingRestartCron = value
	cluster.SetSchedulerRollingRestart()
	return nil
}

func (cluster *Cluster) SetSchedulerRollingReprovCron(value string) error {
	cluster.Conf.SchedulerRollingReprovCron = value
	cluster.SetSchedulerRollingReprov()
	return nil
}

func (cluster *Cluster) SetSchedulerJobsSshCron(value string) error {
	cluster.Conf.SchedulerJobsSSHCron = value
	cluster.SetSchedulerDbJobsSsh()
	return nil
}

func (cluster *Cluster) SetSchedulerAlertDisableCron(value string) error {
	cluster.Conf.SchedulerAlertDisableCron = value
	cluster.SetSchedulerAlertDisable()
	return nil
}

func (cluster *Cluster) SetMonitoringSchemaSchedulerCron(value string) error {
	cluster.Conf.MonitorSchemaSchedulerCron = value
	cluster.SetSchedulerMonitorSchema()
	return nil
}

func (cluster *Cluster) SetMonitoringChecksumSchedulerCron(value string) error {
	cluster.Conf.MonitorChecksumSchedulerCron = value
	cluster.SetSchedulerMonitorChecksum()
	return nil
}

func (cluster *Cluster) SetMonitoringSchemaIgnoreTables(value string) error {
	if value == "&nbsp;" {
		value = ""
	}
	cluster.Conf.MonitorSchemaIgnoreTables = value
	return nil
}

func (cluster *Cluster) SetDbServerHosts(value string) error {
	cluster.Conf.Hosts = value
	cluster.hostList = make([]string, 0)
	if value != "" {
		cluster.hostList = strings.Split(value, ",")
	}
	return nil
}

/*
Set Prov Orchestrator. Log Module: Orchestrator
*/
func (cluster *Cluster) SetProvOrchestrator(value string) error {
	orchetrators := cluster.Conf.GetOrchestratorsProv()
	for _, orch := range orchetrators {
		if orch.Name == value {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Cluster orchestrator set to %s", orch.Name)
			cluster.Conf.ProvOrchestrator = value
			return nil
		}
	}
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cluster orchestrator set to default %s", config.ConstOrchestratorOnPremise)
	return nil
}

// SetProvDBImage declares the database image. A pinned image (prov-db-docker-img in the
// immutable cluster.d config) is not moved by a setting or a rolling upgrade: the operator
// changes the pin first (#1862).
func (cluster *Cluster) SetProvDBImage(value string) error {
	if value != cluster.Conf.ProvDbImg && cluster.IsVariableImmutable("prov-db-docker-img") {
		return fmt.Errorf("prov-db-image %s is pinned in the immutable configuration: %s not applied, change the pin first", cluster.Conf.ProvDbImg, value)
	}
	if value != cluster.Conf.ProvDbImg {
		cluster.Conf.ProvDbImgResolved = "" // resolved again at the next provision / upgrade
	}
	cluster.Conf.ProvDbImg = value
	cluster.SetDBReprovCookie(ReprovReasonImage)
	return nil
}

// SetProvDbDockerXtrabackupImg sets prov-db-docker-xtrabackup-img: empty (the injection off), "auto" or the reference
// of an official xtrabackup image (see doc/implementation/cluster/XTRABACKUP_BUNDLE_INJECTION.md). The value is checked
// here (ValidateXtrabackupImage) so that a bad one is refused to the operator instead of being accepted and only
// logged by the render. Only the OpenSVC and Kubernetes provisioners render the injection; elsewhere the value would
// have no effect and would only raise a reprovision cookie for nothing. Reprovisioning remains an explicit operator
// action: the cookie only surfaces that the rendered service is now stale.
func (cluster *Cluster) SetProvDbDockerXtrabackupImg(value string) error {
	value = strings.TrimSpace(value)
	// An empty value turns the injection off: always allowed, whatever the orchestrator, so that a setting can be
	// cleared (the dashboard's Off) on a cluster where it can no longer be set.
	if value != "" {
		if orchestrator := cluster.GetOrchestrator(); orchestrator != config.ConstOrchestratorOpenSVC && orchestrator != config.ConstOrchestratorKubernetes {
			return fmt.Errorf("prov-db-docker-xtrabackup-img is only supported with the %s and %s orchestrators, this cluster uses %q",
				config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes, orchestrator)
		}
		if err := ValidateXtrabackupImage(value); err != nil {
			return err
		}
	}
	if cluster.Conf.ProvDbDockerXtrabackupImg == value {
		return nil
	}
	cluster.Conf.ProvDbDockerXtrabackupImg = value
	cluster.SetDBReprovCookie(ReprovReasonXtrabackupImg)
	return nil
}

// SetProvDBRunAsUID sets the numeric UID[:GID] a provisioned database container
// runs as (OpenSVC --user, Kubernetes securityContext). Empty restores the
// legacy behavior; 0 is root, taken literally (for the process; the dbjobs script
// db_owner keeps the legacy owner for the few files it writes when the datadir is owned
// by root, see doc/implementation/cluster/DATABASE_RUNTIME_UID_GID.md). It is
// independent of prov-db-volume-uid. Reprovisioning remains an explicit operator action; the cookie
// only surfaces that the rendered service is now stale.
func (cluster *Cluster) SetProvDBRunAsUID(value string) error {
	return cluster.setProvDBIdentity("prov-db-run-as-uid", &cluster.Conf.ProvDBRunAsUID, value)
}

// SetProvDBVolumeUID sets the numeric UID[:GID] that owns a provisioned database's
// data volume (OpenSVC volume owner and bootstrap chown, Kubernetes init
// chown). See SetProvDBRunAsUID.
func (cluster *Cluster) SetProvDBVolumeUID(value string) error {
	return cluster.setProvDBIdentity("prov-db-volume-uid", &cluster.Conf.ProvDBVolumeUID, value)
}

func (cluster *Cluster) setProvDBIdentity(setting string, field *string, value string) error {
	// Only the OpenSVC and Kubernetes provisioners create database containers with a
	// run-as identity or an owned data volume. Elsewhere (local, on-premise, SlapOS)
	// the value would have no effect and would only raise a reprovision cookie for
	// nothing.
	if orchestrator := cluster.GetOrchestrator(); orchestrator != config.ConstOrchestratorOpenSVC && orchestrator != config.ConstOrchestratorKubernetes {
		return fmt.Errorf("%s is only supported with the %s and %s orchestrators, this cluster uses %q",
			setting, config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes, orchestrator)
	}
	if _, _, _, err := ParseDBIdentity(setting, value); err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if *field == value {
		return nil
	}
	*field = value
	cluster.SetDBReprovCookie(ReprovReasonIdentity)
	return nil
}

func (cluster *Cluster) SetProvMaxscaleImage(value string) error {
	cluster.Conf.ProvProxMaxscaleImg = value
	cluster.SetProxiesReprovCookie()
	return nil
}
func (cluster *Cluster) SetProvHaproxyImage(value string) error {
	cluster.Conf.ProvProxHaproxyImg = value
	cluster.SetProxiesReprovCookie()
	return nil
}
func (cluster *Cluster) SetProvShardproxyImage(value string) error {
	cluster.Conf.ProvProxShardingImg = value
	cluster.SetProxiesReprovCookie()
	return nil
}
func (cluster *Cluster) SetProvProxySQLImage(value string) error {
	cluster.Conf.ProvProxProxysqlImg = value
	cluster.SetProxiesReprovCookie()
	return nil
}
func (cluster *Cluster) SetProvSphinxImage(value string) error {
	cluster.Conf.ProvSphinxImg = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbDiskType(value string) error {
	cluster.Conf.ProvDiskType = value
	cluster.SetDBReprovCookie(ReprovReasonDiskType)
	return nil
}

func (cluster *Cluster) SetProvDbDiskFS(value string) error {
	cluster.Conf.ProvDiskFS = value
	cluster.SetDBReprovCookie(ReprovReasonDiskFS)
	return nil
}

// containerStartTimeout validates a container start timeout setting: a positive duration
// (2m, 90s). The orchestrator's default is 5s, too short for a container whose image was
// purged (#1924).
func containerStartTimeout(name, value string) (string, error) {
	v := strings.TrimSpace(value)
	d, err := time.ParseDuration(v)
	if err != nil {
		return "", fmt.Errorf("%s: %q is not a duration (2m, 90s): %w", name, value, err)
	}
	if d <= 0 {
		return "", fmt.Errorf("%s: %q is not a positive duration (2m, 90s)", name, value)
	}
	return v, nil
}

// SetProvDbStartTimeout: the start timeout written on the database containers and their
// jobs sidecar; the template refresh carries it, the next start uses it (#1924).
func (cluster *Cluster) SetProvDbStartTimeout(value string) error {
	v, err := containerStartTimeout("prov-db-start-timeout", value)
	if err != nil {
		return err
	}
	cluster.Conf.ProvDbStartTimeout = v
	return nil
}

// SetProvProxyStartTimeout: the same for the proxy containers (#1924).
func (cluster *Cluster) SetProvProxyStartTimeout(value string) error {
	v, err := containerStartTimeout("prov-proxy-start-timeout", value)
	if err != nil {
		return err
	}
	cluster.Conf.ProvProxyStartTimeout = v
	return nil
}

func (cluster *Cluster) SetProvDbDiskPool(value string) error {
	cluster.Conf.ProvDiskPool = value
	cluster.SetDBReprovCookie(ReprovReasonDiskPool)
	return nil
}

func (cluster *Cluster) SetProvDbDiskDevice(value string) error {
	cluster.Conf.ProvDiskDevice = value
	cluster.SetDBReprovCookie(ReprovReasonDiskDevice)
	return nil
}

func (cluster *Cluster) SetProvDbServiceType(value string) error {
	cluster.Conf.ProvType = value
	cluster.SetDBReprovCookie(ReprovReasonServiceType)
	return nil
}

func (cluster *Cluster) SetProvProxyDiskType(value string) error {
	cluster.Conf.ProvProxDiskType = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvProxyDiskFS(value string) error {
	cluster.Conf.ProvProxDiskFS = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvProxyDiskPool(value string) error {
	cluster.Conf.ProvProxDiskPool = value
	return nil
}

func (cluster *Cluster) SetProvProxyDiskDevice(value string) error {
	cluster.Conf.ProvProxDiskDevice = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvProxyServiceType(value string) error {
	cluster.Conf.ProvProxType = value
	cluster.SetProxiesReprovCookie()
	return nil
}

func (cluster *Cluster) Exit() {
	cluster.exit.Store(true)
}

func (cluster *Cluster) SetInjectVariables() {
	for key, val := range config.CopyDynamicFlags(cluster.Conf.DynamicFlagMap) {
		switch key {
		case "vault-role-id":
			cluster.Conf.VaultRoleId = fmt.Sprintf("%v", val)
		case "vault-secret-id":
			cluster.Conf.VaultSecretId = fmt.Sprintf("%v", val)
			var secret config.Secret
			secret.Value = fmt.Sprintf("%v", val)
			cluster.Conf.Secrets.Set("vault-secret-id", secret)
			cluster.SetSecretsToVault()
			cluster.GetVaultToken()
		case "api-oauth-client-id":
			cluster.Conf.OAuthClientID = fmt.Sprintf("%v", val)
		case "api-oauth-client-secret":
			cluster.Conf.OAuthClientSecret = fmt.Sprintf("%v", val)
			var secret config.Secret
			secret.Value = fmt.Sprintf("%v", val)
			cluster.Conf.Secrets.Set("api-oauth-client-secret", secret)
		case "cloud18-sso-client-id":
			// the GitLab application the Cloud18 storages trust, pushed by the BO like
			// api-oauth-client-id (#2091)
			cluster.Conf.Cloud18SSOClientID = fmt.Sprintf("%v", val)
		case "cloud18-sso-client-secret":
			cluster.Conf.Cloud18SSOClientSecret = fmt.Sprintf("%v", val)
			var secret config.Secret
			secret.Value = fmt.Sprintf("%v", val)
			cluster.Conf.Secrets.Set("cloud18-sso-client-secret", secret)
		case "api-credentials-external":
			cluster.Conf.APIUsersExternal = fmt.Sprintf("%v", val)
		case "api-credentials-acl-allow":
			cluster.Conf.APIUsersACLAllow = fmt.Sprintf("%v", val)
		case "api-credentials-acl-allow-external":
			cluster.Conf.APIUsersACLAllowExternal = fmt.Sprintf("%v", val)
		case "api-credentials-acl-discard-external":
			cluster.Conf.APIUsersACLDiscardExternal = fmt.Sprintf("%v", val)
		}
	}
}

func (cluster *Cluster) SetSecretsToVault() {
	if cluster.Conf.VaultRoleId != "" && cluster.Conf.VaultSecretId != "" {
		//	cluster.Conf.VaultServerAddr = "http://vault.infra.svc.cloud18:8200"
		if cluster.Conf.VaultServerAddr == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "No vault-server-addr defined for writing secrets to Vault")
			return
		}
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		secrets_map := make(map[string]interface{})
		for key, val := range cluster.Conf.Secrets.Snapshot() {
			if val.Value != "" {
				secrets_map[key] = val.Value
			}

		}
		secret_path := cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone + "/" + cluster.Name
		_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), secret_path, secrets_map)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Failed to write secrets to Vault: %v", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Success of writing secrets to Vault: %s", secret_path)
		}
	}
}

func (cluster *Cluster) SetDelayStatRotate(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	if numkeep > 72 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot set delaystat more than 72 hours (3 days). Adjusting value from %s to 72 hours", keep)
		numkeep = 72
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Delay Stat Rotate set to %s", strconv.Itoa(numkeep))
	cluster.Conf.DelayStatRotate = numkeep
	return nil
}

func (cluster *Cluster) SetPrintDelayStatInterval(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Print delay statistic interval set to %s", strconv.Itoa(numkeep))
	cluster.Conf.PrintDelayStatInterval = numkeep
	return nil
}

func (cluster *Cluster) SetLogWriterElectionLevel(value int) {
	cluster.Conf.LogWriterElectionLevel = value
	if value > 0 {
		cluster.Conf.LogWriterElection = true
	} else {
		cluster.Conf.LogWriterElection = false
	}
}
func (cluster *Cluster) SetLogSSTLevel(value int) {
	cluster.Conf.LogSSTLevel = value
	if value > 0 {
		cluster.Conf.LogSST = true
	} else {
		cluster.Conf.LogSST = false
	}
}
func (cluster *Cluster) SetLogHeartbeatLevel(value int) {
	cluster.Conf.LogHeartbeatLevel = value
	if value > 0 {
		cluster.Conf.LogHeartbeat = true
	} else {
		cluster.Conf.LogHeartbeat = false
	}
}
func (cluster *Cluster) SetLogSQLLevel(value int) {
	cluster.Conf.LogSQLLevel = value
	if value > 0 {
		cluster.Conf.LogSQLInMonitoring = true
	} else {
		cluster.Conf.LogSQLInMonitoring = false
	}
}
func (cluster *Cluster) SetLogConfigLoadLevel(value int) {
	cluster.Conf.LogConfigLoadLevel = value
	if value > 0 {
		cluster.Conf.LogConfigLoad = true
	} else {
		cluster.Conf.LogConfigLoad = false
	}
}
func (cluster *Cluster) SetLogGitLevel(value int) {
	cluster.Conf.LogGitLevel = value
	if value > 0 {
		cluster.Conf.LogGit = true
	} else {
		cluster.Conf.LogGit = false
	}
}
func (cluster *Cluster) SetLogSupportLevel(value int) {
	cluster.Conf.LogSupportLevel = value
	if value > 0 {
		cluster.Conf.LogSupport = true
	} else {
		cluster.Conf.LogSupport = false
	}
}
func (cluster *Cluster) SetLogBackupStreamLevel(value int) {
	cluster.Conf.LogBackupStreamLevel = value
	if value > 0 {
		cluster.Conf.LogBackupStream = true
	} else {
		cluster.Conf.LogBackupStream = false
	}
}
func (cluster *Cluster) SetLogOrchestratorLevel(value int) {
	cluster.Conf.LogOrchestratorLevel = value
	if value > 0 {
		cluster.Conf.LogOrchestrator = true
	} else {
		cluster.Conf.LogOrchestrator = false
	}
}
func (cluster *Cluster) SetLogVaultLevel(value int) {
	cluster.Conf.LogVaultLevel = value
	if value > 0 {
		cluster.Conf.LogVault = true
	} else {
		cluster.Conf.LogVault = false
	}
}
func (cluster *Cluster) SetLogTopologyLevel(value int) {
	cluster.Conf.LogTopologyLevel = value
	if value > 0 {
		cluster.Conf.LogTopology = true
	} else {
		cluster.Conf.LogTopology = false
	}
}
func (cluster *Cluster) SetLogProxyLevel(value int) {
	cluster.Conf.LogProxyLevel = value
	if value > 0 {
		cluster.Conf.LogProxy = true
	} else {
		cluster.Conf.LogProxy = false
	}
}
func (cluster *Cluster) SetProxysqlLogLevel(value int) {
	cluster.Conf.ProxysqlLogLevel = value
	if value > 0 {
		cluster.Conf.ProxysqlDebug = true
	} else {
		cluster.Conf.ProxysqlDebug = false
	}
}
func (cluster *Cluster) SetHaproxyLogLevel(value int) {
	cluster.Conf.HaproxyLogLevel = value
	if value > 0 {
		cluster.Conf.HaproxyDebug = true
	} else {
		cluster.Conf.HaproxyDebug = false
	}
}
func (cluster *Cluster) SetProxyJanitorLogLevel(value int) {
	cluster.Conf.ProxyJanitorLogLevel = value
	if value > 0 {
		cluster.Conf.ProxyJanitorDebug = true
	} else {
		cluster.Conf.ProxyJanitorDebug = false
	}
}
func (cluster *Cluster) SetMxsLogLevel(value int) {
	cluster.Conf.MxsLogLevel = value
	if value > 0 {
		cluster.Conf.MxsDebug = true
	} else {
		cluster.Conf.MxsDebug = false
	}
}

func (cluster *Cluster) SetLogTaskLevel(value int) {
	cluster.Conf.LogTaskLevel = value
	if value > 0 {
		cluster.Conf.LogTask = true
	} else {
		cluster.Conf.LogTask = false
	}
}
func (cluster *Cluster) SetLogExternalScriptLevel(value int) {
	cluster.Conf.LogExternalScriptLevel = value
	if value > 0 {
		cluster.Conf.LogExternalScript = true
	} else {
		cluster.Conf.LogExternalScript = false
	}
}

func (cluster *Cluster) SetLogLevelDatabaseErrors(value int) {
	cluster.Conf.LogLevelDatabaseErrors = value
}

func (cluster *Cluster) SetLogLevelDatabaseSqlErrors(value int) {
	cluster.Conf.LogLevelDatabaseSqlErrors = value
}

func (cluster *Cluster) SetLogLevelDatabaseSlowquery(value int) {
	cluster.Conf.LogLevelDatabaseSlowquery = value
}

func (cluster *Cluster) SetLogLevelDatabaseAudit(value int) {
	cluster.Conf.LogLevelDatabaseAudit = value
}

func (cluster *Cluster) SetLogLevelDatabaseOptimize(value int) {
	cluster.Conf.LogLevelDatabaseOptimize = value
}

func (cluster *Cluster) SetSlavesOldestMasterFile(value string) error {

	parts := strings.Split(value, ".")
	prefix := strings.Join(parts[:len(parts)-1], ".")
	suffix, err := strconv.Atoi(parts[len(parts)-1])

	if err != nil {
		return err
	}

	cluster.SlavesOldestMasterFile.Prefix = prefix
	cluster.SlavesOldestMasterFile.Suffix = suffix

	return nil
}

func (cluster *Cluster) SetSlavesConnected(value int) {
	cluster.SlavesConnected = value
}

func (cluster *Cluster) SetForceBinlogPurgeTotalSize(value int) {
	cluster.Conf.ForceBinlogPurgeTotalSize = value
}

func (cluster *Cluster) SetForceBinlogPurgeMinReplica(value int) {
	cluster.Conf.ForceBinlogPurgeMinReplica = value
}

func (cluster *Cluster) SetCarbonLogger(value *logrus.Logger) {
	cluster.clog = value
}

func (cluster *Cluster) SetLogGraphiteLevel(value int) {
	cluster.Conf.LogGraphiteLevel = value
	if value > 0 {
		cluster.Conf.LogGraphite = true
	} else {
		cluster.Conf.LogGraphite = false
	}

	cluster.clog.SetLevel(config.ToLogrusLevel(value))
}

func (cluster *Cluster) SetLogBinlogPurgeLevel(value int) {
	cluster.Conf.LogBinlogPurgeLevel = value
	if value > 0 {
		cluster.Conf.LogBinlogPurge = true
	} else {
		cluster.Conf.LogBinlogPurge = false
	}
}

func (cluster *Cluster) SetLogResticLevel(value int) {
	cluster.Conf.LogResticLevel = value
}

func (cluster *Cluster) SetLogMailerLevel(value int) {
	cluster.Conf.LogMailerLevel = value
}

func (cluster *Cluster) SetInPhysicalBackupState(value bool) {
	cluster.InPhysicalBackup = value
}

func (cluster *Cluster) SetInLogicalBackupState(value bool) {
	cluster.InLogicalBackup = value
}

func (cluster *Cluster) SetInBinlogBackupState(value bool) {
	cluster.InBinlogBackup = value
}

func (cluster *Cluster) SetInResticLogicalBackupState(value bool) {
	cluster.InResticLogicalBackup = value
	cluster.InResticBackup = cluster.InResticLogicalBackup || cluster.InResticPhysicalBackup
}

func (cluster *Cluster) SetInResticPhysicalBackupState(value bool) {
	cluster.InResticPhysicalBackup = value
	cluster.InResticBackup = cluster.InResticLogicalBackup || cluster.InResticPhysicalBackup
}

func (cluster *Cluster) SetInResticBackupState(value bool) {
	cluster.InResticBackup = value
	if !value {
		cluster.InResticLogicalBackup = false
		cluster.InResticPhysicalBackup = false
	}
}

func (cluster *Cluster) CheckBackupStates() {
	if cluster.InPhysicalBackup || cluster.InResticPhysicalBackup {
		cluster.StateMachine.PreserveState("WARN0073")
	}
	if cluster.InLogicalBackup || cluster.InResticLogicalBackup {
		cluster.StateMachine.PreserveState("WARN0175")
	}
}

func (cluster *Cluster) SetGraphiteWhitelistTemplate(value string) {
	cluster.Conf.GraphiteWhitelistTemplate = value
}

func (cluster *Cluster) SetTopologyTarget(value string) {
	cluster.Conf.TopologyTarget = value
}

func (cluster *Cluster) SetMonitorIgnoreErrors(value string) {
	if value == "{undefined}" {
		value = ""
	}
	cluster.Conf.MonitorIgnoreErrors = value
}

func (cluster *Cluster) SetMonitorCaptureTrigger(value string) {
	if value == "{undefined}" {
		value = ""
	}
	cluster.Conf.MonitorCaptureTrigger = value
}

func (cluster *Cluster) SetMasterNil() {
	cluster.master = nil
}

func (cluster *Cluster) SetApiTokenTimeout(value int) {
	cluster.Conf.TokenTimeout = value
}

func (cluster *Cluster) SetSSTBufferSize(value int) {
	cluster.Conf.SSTSendBuffer = value
}

func (cluster *Cluster) SetAlertPushoverAppToken(value string) {
	cluster.Conf.PushoverAppToken = value
}

func (cluster *Cluster) SetAlertPushoverUserToken(value string) {
	cluster.Conf.PushoverUserToken = value
}

func (cluster *Cluster) SetAlertScript(value string) {
	cluster.Conf.AlertScript = value
}

func (cluster *Cluster) SetAlertSlackChannel(value string) {
	cluster.Conf.SlackChannel = value
	cluster.LogSlack.SetChannel("slack", value)
}

func (cluster *Cluster) SetAlertSlackUrl(value string) {
	cluster.Conf.SlackURL = value
	cluster.LogSlack.SetURL("slack", value)
}

func (cluster *Cluster) SetAlertSlackUser(value string) {
	cluster.Conf.SlackUser = value
	cluster.LogSlack.SetUser("slack", value)
}

func (cluster *Cluster) SetCloud18AlertSlackChannel(value string) {
	cluster.Conf.Cloud18AlertSlackChannel = value
	cluster.LogSlack.SetChannel("cloud18", value)
}

func (cluster *Cluster) SetCloud18AlertSlackUrl(value string) {
	cluster.Conf.Cloud18AlertSlackURL = value
	cluster.LogSlack.SetURL("cloud18", value)
}

func (cluster *Cluster) SetCloud18AlertSlackUser(value string) {
	cluster.Conf.Cloud18AlertSlackUser = value
	cluster.LogSlack.SetUser("cloud18", value)
}

func (cluster *Cluster) SetAlertTeamsProxyUrl(value string) {
	cluster.Conf.TeamsProxyUrl = value
}

func (cluster *Cluster) SetAlertTeamsState(value string) {
	cluster.Conf.TeamsAlertState = value
}

func (cluster *Cluster) SetAlertTeamsUrl(value string) {
	cluster.Conf.TeamsUrl = value
}

func (cluster *Cluster) SetMonitoringAlertTriggerl(value string) {
	cluster.Conf.MonitoringAlertTrigger = value
}

func (cluster *Cluster) SetSchedulerAlertDisableTime(value int) {
	cluster.Conf.SchedulerAlertDisableTime = value
}

func (cluster *Cluster) SetInRollingRestart(value bool) {
	cluster.InRollingRestart = value
}

func (cluster *Cluster) RenameCluster(newClusterName string) error {

	if cluster.IsProvisioned() {
		return errors.New("Cannot rename provisioned cluster")
	}

	cluster.Lock()
	defer func() {
		cluster.Unlock()
		cluster.ConfigManager.SaveConfig(cluster, true)
	}()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Initiate rename cluster %s to %s", cluster.Name, newClusterName)

	// Rename cluster directory
	err := os.Rename(cluster.Conf.WorkingDir+"/"+cluster.Name, cluster.Conf.WorkingDir+"/"+newClusterName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rename cluster working directory fail: %s", err)
		return err
	}

	// Rename cluster configuration file
	err = os.Rename(cluster.Conf.WorkingDir+"/"+newClusterName+"/"+cluster.Name+".toml", cluster.Conf.WorkingDir+"/"+newClusterName+"/"+newClusterName+".toml")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rename cluster working directory fail: %s", err)
		return err
	}

	// Rename cluster name
	cluster.Name = newClusterName

	return nil

}

func (cluster *Cluster) SetLogStatsLevel(value int) {
	cluster.Conf.LogStatsLevel = value
}

func (cluster *Cluster) SetMonitorPFSMutex(value bool) {
	cluster.Conf.MonitorPFSMutex = value
	if value {
		cluster.AddDBTag("logmutex", true)
	} else {
		cluster.DropDBTag("logmutex", true)
	}
}

func (cluster *Cluster) SetMonitorPFSLatch(value bool) {
	cluster.Conf.MonitorPFSLatch = value
	if value {
		cluster.AddDBTag("loglatch", true)
	} else {
		cluster.DropDBTag("loglatch", true)
	}
}

func (cluster *Cluster) SetMonitorPFSMemory(value bool) {
	cluster.Conf.MonitorPFSMemory = value
}

func (cluster *Cluster) SetMonitorPFSInstruments(value bool) {
	cluster.Conf.MonitorPFSInstruments = value
}

func (cluster *Cluster) SetPreserveFlagToAllNodes(variable string, value string) {
	for _, srv := range cluster.Servers {
		v, ok := srv.VariablesMap.CheckAndGet(variable)
		if ok {
			v.SetPreservedValue(value)
		}
	}
}

func (cluster *Cluster) DelPreserveFlagToAllNodes(variable string) {
	for _, srv := range cluster.Servers {
		v, ok := srv.VariablesMap.CheckAndGet(variable)
		if ok {
			v.UnsetPreservedValue()
		}
	}
}

func (cluster *Cluster) PreserveVariable(variable string, preserve bool) error {
	parts := strings.SplitN(variable, "=", 2)
	key := ""
	value := ""
	if len(parts) == 2 {
		variable = strings.TrimSpace(parts[0])
		value = strings.TrimSpace(parts[1])

		key = strings.ToUpper(variable)
		if value != "" {
			variable = variable + "=" + value
		}
	}

	var found bool
	list := strings.Split(cluster.Conf.ProvDBConfigPreserveVars, ";")
	for i, v := range list {
		parts = strings.SplitN(v, "=", 2)
		if key == strings.ToUpper(parts[0]) {
			found = true
			if preserve {
				list[i] = variable
			} else {
				if strings.ToUpper(cluster.Conf.ProvDBConfigPreserveVars) == key {
					list = make([]string, 0) // prevent unwanted spaces
				} else {
					list = append(list[:i], list[i+1:]...)
				}
			}
			break
		}
	}

	if !found && preserve {
		if cluster.Conf.ProvDBConfigPreserveVars == "" {
			list = make([]string, 0) // prevent unwanted spaces
		}

		list = append(list, variable)
	}

	cluster.Conf.ProvDBConfigPreserveVars = strings.Join(list, ";")

	if preserve {
		cluster.SetPreserveFlagToAllNodes(key, value)
	} else {
		cluster.DelPreserveFlagToAllNodes(key)
	}

	return nil
}

func (cluster *Cluster) SetDatabaseCredentials(role string, cred string) error {
	switch role {
	case "dba":
		if err := cluster.CredentialChain().Set("cloud18-dba-user-credentials", cred); err != nil {
			return err
		}
		cluster.Conf.Cloud18DbaUserCredentials = cred
		cluster.MarkSecretVersionStoreDirty()
	case "sponsor":
		if err := cluster.CredentialChain().Set("cloud18-sponsor-user-credentials", cred); err != nil {
			return err
		}
		cluster.Conf.Cloud18SponsorUserCredentials = cred
		cluster.MarkSecretVersionStoreDirty()
	default:
		return errors.New("Unknown secret role")
	}

	return nil
}

func (cluster *Cluster) SetResticPassword(cred string) error {
	if err := cluster.CredentialChain().Set("backup-restic-password", cred); err != nil {
		return err
	}
	cluster.Conf.BackupResticPassword = cred
	cluster.MarkSecretVersionStoreDirty()
	return nil
}

// SetBackupVerificationCredential sets the verification server's user:password, kept as a
// secret like the other credentials.
func (cluster *Cluster) SetBackupVerificationCredential(cred string) error {
	if err := cluster.CredentialChain().Set("backup-verification-credential", cred); err != nil {
		return err
	}
	cluster.Conf.BackupVerificationCredential = cred
	cluster.MarkSecretVersionStoreDirty()
	return nil
}

// SetBackupVerificationTimeout sets backup-verification-timeout: the seconds a test restore
// may run, 0 for no limit.
func (cluster *Cluster) SetBackupVerificationTimeout(value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("backup-verification-timeout must be 0 or more seconds, got %d", v)
	}
	cluster.Conf.BackupVerificationTimeout = v
	return nil
}

// SetBackupDiscoveryCandidateMax sets backup-discovery-candidate-max: the artifacts a backup
// discovery lists per server, 0 for the default.
func (cluster *Cluster) SetBackupDiscoveryCandidateMax(value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("backup-discovery-candidate-max must be 0 (the default) or more, got %d", v)
	}
	cluster.Conf.BackupDiscoveryCandidateMax = v
	return nil
}

// SetBackupVerificationLevel sets backup-verification-level: integrity or restore.
func (cluster *Cluster) SetBackupVerificationLevel(level string) error {
	switch level {
	case config.ConstBackupVerificationLevelIntegrity, config.ConstBackupVerificationLevelRestore:
		cluster.Conf.BackupVerificationLevel = level
		return nil
	}
	return fmt.Errorf("backup-verification-level must be %s or %s, got %q", config.ConstBackupVerificationLevelIntegrity, config.ConstBackupVerificationLevelRestore, level)
}

func (cluster *Cluster) SetStagingServer(srv *ServerMonitor) {
	srvhost := ""
	cluster.StagingServer = srv
	if srv != nil {
		srvhost = srv.HostCnf
	}
	cluster.Conf.StagingServerHost = srvhost
}

func (cluster *Cluster) SetStandaloneAsStaging() *ServerMonitor {
	stagingsrv, _ := cluster.GetStandaloneServerByIndex(0)

	stagingcnf := cluster.GetStagingServerFromConfig()
	// Check if staging server is valid and standalone
	if stagingcnf != nil && stagingcnf.IsStandAlone() {
		stagingsrv = stagingcnf
	}

	cluster.SetStagingServer(stagingsrv)

	return stagingsrv
}

func (cluster *Cluster) SetRollingJobsUpgradeState() {
	if cluster.Conf.ProvOrchestrator != "opensvc" {
		return
	}

	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		s.SetRollingJobsUpgradeCookie()
	}

	cluster.SetState("WARN0155", state.State{ErrType: "WARN0155", ErrDesc: clusterError["WARN0155"], ErrFrom: "MAINTENANCE"})
}

func (cluster *Cluster) SetOrchestratorVersion(value string) {
	cluster.orchestratorVersionMu.Lock()
	defer cluster.orchestratorVersionMu.Unlock()
	cluster.OrchestratorVersion = value
}

func (cluster *Cluster) GetOrchestratorVersion() string {
	cluster.orchestratorVersionMu.RLock()
	defer cluster.orchestratorVersionMu.RUnlock()
	return cluster.OrchestratorVersion
}

func (cluster *Cluster) ShouldProbeOrchestratorVersion(interval time.Duration) bool {
	cluster.orchestratorVersionMu.Lock()
	defer cluster.orchestratorVersionMu.Unlock()
	if !cluster.lastOrchestratorProbe.IsZero() && time.Since(cluster.lastOrchestratorProbe) < interval {
		return false
	}
	cluster.lastOrchestratorProbe = time.Now()
	return true
}

// SetAutoTLS records that a server asked for TLS (error 3159), so proxies enable
// SSL on their backends too. Safe from any goroutine, including one called while
// the cluster mutex is held (see autoTLSMu).
func (cluster *Cluster) SetAutoTLS(on bool) {
	cluster.autoTLSMu.Lock()
	cluster.HaveAutoTLS = on
	cluster.autoTLSMu.Unlock()
}
