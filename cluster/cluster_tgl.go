// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "os"

func (cluster *Cluster) SwitchForceSlaveNoGtid() {
	cluster.Conf.ForceSlaveNoGtid = !cluster.Conf.ForceSlaveNoGtid
}

func (cluster *Cluster) SwitchDBDynamicConfig() {
	cluster.Conf.ProvDBApplyDynamicConfig = !cluster.Conf.ProvDBApplyDynamicConfig
}

func (cluster *Cluster) SwitchMonitoringPause() {
	cluster.Conf.MonitorPause = !cluster.Conf.MonitorPause
}

func (cluster *Cluster) SwitchDBApplyDynamicConfig() {
	cluster.Conf.ProvDBApplyDynamicConfig = !cluster.Conf.ProvDBApplyDynamicConfig
}

func (cluster *Cluster) SwitchForceSlaveReadOnly() {
	cluster.Conf.ForceSlaveReadOnly = !cluster.Conf.ForceSlaveReadOnly
}

func (cluster *Cluster) SwitchForceBinlogRow() {
	cluster.Conf.ForceBinlogRow = !cluster.Conf.ForceBinlogRow
}

func (cluster *Cluster) SwitchForceSlaveSemisync() {
	cluster.Conf.ForceSlaveSemisync = !cluster.Conf.ForceSlaveSemisync
}

func (cluster *Cluster) SwitchForceSlaveHeartbeat() {
	cluster.Conf.ForceSlaveHeartbeat = !cluster.Conf.ForceSlaveHeartbeat
}

func (cluster *Cluster) SwitchForceSlaveGtid() {
	cluster.Conf.ForceSlaveGtid = !cluster.Conf.ForceSlaveGtid
}

func (cluster *Cluster) SwitchForceSlaveGtidStrict() {
	cluster.Conf.ForceSlaveGtidStrict = !cluster.Conf.ForceSlaveGtidStrict
}

func (cluster *Cluster) SwitchForceBinlogCompress() {
	cluster.Conf.ForceBinlogCompress = !cluster.Conf.ForceBinlogCompress
}

func (cluster *Cluster) SwitchForceBinlogAnnotate() {
	cluster.Conf.ForceBinlogAnnotate = !cluster.Conf.ForceBinlogAnnotate
}

func (cluster *Cluster) SwitchForceBinlogSlowqueries() {
	cluster.Conf.ForceBinlogSlowqueries = !cluster.Conf.ForceBinlogSlowqueries
}

func (cluster *Cluster) SwitchServerMaintenance(serverid uint64) {
	server := cluster.GetServerFromId(serverid)
	server.SwitchMaintenance()
	cluster.SetProxyServerMaintenance(server.ServerID)
}
func (cluster *Cluster) SwitchProvNetCNI() {
	cluster.Conf.ProvNetCNI = !cluster.Conf.ProvNetCNI
}
func (cluster *Cluster) SwitchProvDockerDaemonPrivate() {
	cluster.Conf.ProvDockerDaemonPrivate = !cluster.Conf.ProvDockerDaemonPrivate
}

func (cluster *Cluster) SwitchBackupRestic() {
	cluster.Conf.BackupRestic = !cluster.Conf.BackupRestic
}
func (cluster *Cluster) SwitchBackupBinlogs() {
	cluster.Conf.BackupBinlogs = !cluster.Conf.BackupBinlogs
}

func (cluster *Cluster) SwitchInteractive() {
	if cluster.Conf.Interactive == true {
		cluster.Conf.Interactive = false
		cluster.LogPrintf(LvlInfo, "Failover monitor switched to automatic mode")
	} else {
		cluster.Conf.Interactive = true
		cluster.LogPrintf(LvlInfo, "Failover monitor switched to manual mode")
	}
}

func (cluster *Cluster) SwitchReadOnly() {
	cluster.Conf.ReadOnly = !cluster.Conf.ReadOnly
	cluster.Configurator.Init(cluster.Conf)
}

func (cluster *Cluster) SwitchRplChecks() {
	cluster.Conf.RplChecks = !cluster.Conf.RplChecks
}

func (cluster *Cluster) SwitchCleanAll() {
	cluster.CleanAll = !cluster.CleanAll
}

func (cluster *Cluster) SwitchFailSync() {
	cluster.Conf.FailSync = !cluster.Conf.FailSync
}

func (cluster *Cluster) SwitchSwitchoverSync() {
	cluster.Conf.SwitchSync = !cluster.Conf.SwitchSync
}

func (cluster *Cluster) SwitchVerbosity() {

	if cluster.GetLogLevel() > 0 {
		cluster.SetLogLevel(0)
	} else {
		cluster.SetLogLevel(4)
	}
}

func (cluster *Cluster) SwitchRejoin() {
	cluster.Conf.Autorejoin = !cluster.Conf.Autorejoin
}

func (cluster *Cluster) SwitchAutoseed() {
	cluster.Conf.Autoseed = !cluster.Conf.Autoseed
}

func (cluster *Cluster) SwitchRejoinDump() {
	cluster.Conf.AutorejoinMysqldump = !cluster.Conf.AutorejoinMysqldump
}
func (cluster *Cluster) SwitchRejoinLogicalBackup() {
	cluster.Conf.AutorejoinLogicalBackup = !cluster.Conf.AutorejoinLogicalBackup
}
func (cluster *Cluster) SwitchRejoinPhysicalBackup() {
	cluster.Conf.AutorejoinPhysicalBackup = !cluster.Conf.AutorejoinPhysicalBackup
}
func (cluster *Cluster) SwitchRejoinBackupBinlog() {
	cluster.Conf.AutorejoinBackupBinlog = !cluster.Conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) SwitchRejoinSemisync() {
	cluster.Conf.AutorejoinSemisync = !cluster.Conf.AutorejoinSemisync
}
func (cluster *Cluster) SwitchRejoinFlashback() {
	cluster.Conf.AutorejoinFlashback = !cluster.Conf.AutorejoinFlashback
}

func (cluster *Cluster) SwitchRejoinPseudoGTID() {
	cluster.Conf.AutorejoinSlavePositionalHeartbeat = !cluster.Conf.AutorejoinSlavePositionalHeartbeat
}

func (cluster *Cluster) SwitchCheckReplicationFilters() {
	cluster.Conf.CheckReplFilter = !cluster.Conf.CheckReplFilter
}

func (cluster *Cluster) SwitchFailoverRestartUnsafe() {
	cluster.Conf.FailRestartUnsafe = !cluster.Conf.FailRestartUnsafe
}

func (cluster *Cluster) SwitchFailoverEventScheduler() {
	cluster.Conf.FailEventScheduler = !cluster.Conf.FailEventScheduler
}

func (cluster *Cluster) SwitchRejoinZFSFlashback() {
	cluster.Conf.AutorejoinZFSFlashback = !cluster.Conf.AutorejoinZFSFlashback
}

func (cluster *Cluster) SwitchBackup() {
	cluster.Conf.Backup = !cluster.Conf.Backup
}

func (cluster *Cluster) SwitchSchedulerBackupLogical() {
	cluster.Conf.SchedulerBackupLogical = !cluster.Conf.SchedulerBackupLogical
	cluster.SetSchedulerBackupLogical()
}

func (cluster *Cluster) SwitchSchedulerBackupPhysical() {
	cluster.Conf.SchedulerBackupPhysical = !cluster.Conf.SchedulerBackupPhysical
	cluster.SetSchedulerBackupPhysical()
}

func (cluster *Cluster) SwitchSchedulerDbJobsSsh() {
	cluster.Conf.SchedulerJobsSSH = !cluster.Conf.SchedulerJobsSSH
	cluster.SetSchedulerDbJobsSsh()
}

func (cluster *Cluster) SwitchSchedulerDatabaseLogs() {
	cluster.Conf.SchedulerDatabaseLogs = !cluster.Conf.SchedulerDatabaseLogs
	cluster.SetSchedulerBackupLogs()
}
func (cluster *Cluster) SwitchSchedulerDatabaseLogsTableRotate() {
	cluster.Conf.SchedulerDatabaseLogsTableRotate = !cluster.Conf.SchedulerDatabaseLogsTableRotate
	cluster.SetSchedulerLogsTableRotate()
}

func (cluster *Cluster) SwitchSchedulerDatabaseOptimize() {
	cluster.Conf.SchedulerDatabaseOptimize = !cluster.Conf.SchedulerDatabaseOptimize
	cluster.SetSchedulerOptimize()
}

func (cluster *Cluster) SwitchSchedulerRollingRestart() {
	cluster.Conf.SchedulerRollingRestart = !cluster.Conf.SchedulerRollingRestart
	cluster.SetSchedulerRollingRestart()
}

func (cluster *Cluster) SwitchSchedulerRollingReprov() {
	cluster.Conf.SchedulerRollingReprov = !cluster.Conf.SchedulerRollingReprov
	cluster.SetSchedulerRollingReprov()
}

func (cluster *Cluster) SwitchSchedulerAlertDisable() {
	cluster.Conf.SchedulerAlertDisable = !cluster.Conf.SchedulerAlertDisable

}

func (cluster *Cluster) SwitchGraphiteEmbedded() {
	cluster.Conf.GraphiteEmbedded = !cluster.Conf.GraphiteEmbedded
}

func (cluster *Cluster) SwitchGraphiteMetrics() {
	cluster.Conf.GraphiteMetrics = !cluster.Conf.GraphiteMetrics
}

func (cluster *Cluster) SwitchFailoverEventStatus() {
	cluster.Conf.FailEventStatus = !cluster.Conf.FailEventStatus
}

func (cluster *Cluster) SwitchProxyServersBackendCompression() {
	cluster.Conf.PRXServersBackendCompression = !cluster.Conf.PRXServersBackendCompression
}

func (cluster *Cluster) SwitchProxyServersReadOnMaster() {
	cluster.Conf.PRXServersReadOnMaster = !cluster.Conf.PRXServersReadOnMaster
	cluster.Configurator.Init(cluster.Conf)
}

func (cluster *Cluster) SwitchProxyServersReadOnMasterNoSlave() {
	cluster.Conf.PRXServersReadOnMasterNoSlave = !cluster.Conf.PRXServersReadOnMasterNoSlave
	cluster.Configurator.Init(cluster.Conf)
}

func (cluster *Cluster) SwitchProxySQL() {
	cluster.Conf.ProxysqlOn = !cluster.Conf.ProxysqlOn
}

func (cluster *Cluster) SwitchMdbsProxy() {
	cluster.Conf.MdbsProxyOn = !cluster.Conf.MdbsProxyOn
}

func (cluster *Cluster) SwitchHaProxy() {
	cluster.Conf.HaproxyOn = !cluster.Conf.HaproxyOn
}
func (cluster *Cluster) SwitchMaxscaleProxy() {
	cluster.Conf.MxsOn = !cluster.Conf.MxsOn
}

func (cluster *Cluster) SwitchMyProxy() {
	cluster.Conf.MyproxyOn = !cluster.Conf.MyproxyOn
}

func (cluster *Cluster) SwitchProxysqlBootstrap() {
	cluster.Conf.ProxysqlBootstrap = !cluster.Conf.ProxysqlBootstrap
}

func (cluster *Cluster) SwitchProxysqlCopyGrants() {
	cluster.Conf.ProxysqlCopyGrants = !cluster.Conf.ProxysqlCopyGrants
}

func (cluster *Cluster) SwitchProxysqlBootstrapVariables() {
	cluster.Conf.ProxysqlBootstrapVariables = !cluster.Conf.ProxysqlBootstrapVariables
}

func (cluster *Cluster) SwitchProxysqlBootstrapServers() {
	cluster.Conf.ProxysqlBootstrap = !cluster.Conf.ProxysqlBootstrap
}
func (cluster *Cluster) SwitchProxysqlBootstrapHostgroups() {
	cluster.Conf.ProxysqlBootstrapHG = !cluster.Conf.ProxysqlBootstrapHG
}
func (cluster *Cluster) SwitchProxysqlBootstrapQueryRules() {
	cluster.Conf.ProxysqlBootstrapQueryRules = !cluster.Conf.ProxysqlBootstrapQueryRules
}

func (cluster *Cluster) SwitchMonitoringSaveConfig() {
	cluster.Conf.ConfRewrite = !cluster.Conf.ConfRewrite
	if !cluster.Conf.ConfRewrite {
		os.Remove(cluster.Conf.WorkingDir + "/" + cluster.Name + "/config.toml")

	}
}
func (cluster *Cluster) SwitchMonitoringSchemaChange() {
	cluster.Conf.MonitorSchemaChange = !cluster.Conf.MonitorSchemaChange
}

func (cluster *Cluster) SwitchMonitoringCapture() {
	cluster.Conf.MonitorCapture = !cluster.Conf.MonitorCapture
	// delete cluster config
}

func (cluster *Cluster) SwitchMonitoringInnoDBStatus() {
	cluster.Conf.MonitorInnoDBStatus = !cluster.Conf.MonitorInnoDBStatus
}

func (cluster *Cluster) SwitchMonitoringVariableDiff() {
	cluster.Conf.MonitorVariableDiff = !cluster.Conf.MonitorVariableDiff
}

func (cluster *Cluster) SwitchMonitoringProcesslist() {
	cluster.Conf.MonitorProcessList = !cluster.Conf.MonitorProcessList
}

func (cluster *Cluster) SwitchCloud18Shared() {
	if cluster.Conf.Cloud18 {
		cluster.Conf.Cloud18Shared = !cluster.Conf.Cloud18Shared
	}

}
func (cluster *Cluster) SwitchCloud18() {
	cluster.Conf.Cloud18 = !cluster.Conf.Cloud18
}

func (cluster *Cluster) SwitchMonitoringScheduler() {
	cluster.Conf.MonitorScheduler = !cluster.Conf.MonitorScheduler
	if !cluster.Conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Stopping scheduler")
		cluster.scheduler.Stop()
	} else {
		cluster.LogPrintf(LvlInfo, "Starting scheduler")
		cluster.initScheduler()
	}
}

func (cluster *Cluster) SwitchMonitoringQueries() {
	cluster.Conf.MonitorQueries = !cluster.Conf.MonitorQueries
}

func (cluster *Cluster) SwitchTestMode() {
	cluster.Conf.Test = !cluster.Conf.Test
}

func (cluster *Cluster) SwitchTraffic() {
	cluster.SetTraffic(!cluster.GetTraffic())
}
