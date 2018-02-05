// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) SwitchServerMaintenance(serverid uint) {
	server := cluster.GetServerFromId(serverid)
	server.SwitchMaintenance()
	cluster.SetProxyServerMaintenance(server.ServerID)
}

func (cluster *Cluster) SwitchInteractive() {
	if cluster.conf.Interactive == true {
		cluster.conf.Interactive = false
		cluster.LogPrintf(LvlInfo, "Failover monitor switched to automatic mode")
	} else {
		cluster.conf.Interactive = true
		cluster.LogPrintf(LvlInfo, "Failover monitor switched to manual mode")
	}
}

func (cluster *Cluster) SwitchReadOnly() {
	cluster.conf.ReadOnly = !cluster.conf.ReadOnly
}

func (cluster *Cluster) SwitchRplChecks() {
	cluster.conf.RplChecks = !cluster.conf.RplChecks
}

func (cluster *Cluster) SwitchCleanAll() {
	cluster.CleanAll = !cluster.CleanAll
}

func (cluster *Cluster) SwitchFailSync() {
	cluster.conf.FailSync = !cluster.conf.FailSync
}

func (cluster *Cluster) SwitchSwitchoverSync() {
	cluster.conf.SwitchSync = !cluster.conf.SwitchSync
}

func (cluster *Cluster) SwitchVerbosity() {

	if cluster.GetLogLevel() > 0 {
		cluster.SetLogLevel(0)
	} else {
		cluster.SetLogLevel(4)
	}
}

func (cluster *Cluster) SwitchRejoin() {
	cluster.conf.Autorejoin = !cluster.conf.Autorejoin
}

func (cluster *Cluster) SwitchRejoinDump() {
	cluster.conf.AutorejoinMysqldump = !cluster.conf.AutorejoinMysqldump
}

func (cluster *Cluster) SwitchRejoinBackupBinlog() {
	cluster.conf.AutorejoinBackupBinlog = !cluster.conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) SwitchRejoinSemisync() {
	cluster.conf.AutorejoinSemisync = !cluster.conf.AutorejoinSemisync
}
func (cluster *Cluster) SwitchRejoinFlashback() {
	cluster.conf.AutorejoinFlashback = !cluster.conf.AutorejoinFlashback
}

func (cluster *Cluster) SwitchRejoinPseudoGTID() {
	cluster.conf.AutorejoinSlavePositionalHearbeat = !cluster.conf.AutorejoinSlavePositionalHearbeat
}

func (cluster *Cluster) SwitchCheckReplicationFilters() {
	cluster.conf.CheckReplFilter = !cluster.conf.CheckReplFilter
}

func (cluster *Cluster) SwitchFailoverRestartUnsafe() {
	cluster.conf.FailRestartUnsafe = !cluster.conf.FailRestartUnsafe
}

func (cluster *Cluster) SwitchFailoverEventScheduler() {
	cluster.conf.FailEventScheduler = !cluster.conf.FailEventScheduler
}

func (cluster *Cluster) SwitchRejoinZFSFlashback() {
	cluster.conf.AutorejoinZFSFlashback = !cluster.conf.AutorejoinZFSFlashback
}

func (cluster *Cluster) SwitchBackup() {
	cluster.conf.Backup = !cluster.conf.Backup
}

func (cluster *Cluster) SwitchSchedulerBackupLogical() {
	cluster.conf.SchedulerBackupLogical = !cluster.conf.SchedulerBackupLogical
}

func (cluster *Cluster) SwitchSchedulerBackupPhysical() {
	cluster.conf.SchedulerBackupPhysical = !cluster.conf.SchedulerBackupPhysical
}

func (cluster *Cluster) SwitchSchedulerDatabaseLogs() {
	cluster.conf.SchedulerDatabaseLogs = !cluster.conf.SchedulerDatabaseLogs
}

func (cluster *Cluster) SwitchSchedulerDatabaseOptimize() {
	cluster.conf.SchedulerDatabaseOptimize = !cluster.conf.SchedulerDatabaseOptimize
}

func (cluster *Cluster) SwitchGraphiteEmbedded() {
	cluster.conf.GraphiteEmbedded = !cluster.conf.GraphiteEmbedded
}

func (cluster *Cluster) SwitchGraphiteMetrics() {
	cluster.conf.GraphiteMetrics = !cluster.conf.GraphiteMetrics
}

func (cluster *Cluster) SwitchFailoverEventStatus() {
	cluster.conf.FailEventStatus = !cluster.conf.FailEventStatus
}

func (cluster *Cluster) SwitchProxysqlBootstrap() {
	cluster.conf.ProxysqlBootstrap = !cluster.conf.ProxysqlBootstrap
}

func (cluster *Cluster) SwitchProxysqlCopyGrants() {
	cluster.conf.ProxysqlCopyGrants = !cluster.conf.ProxysqlCopyGrants
}

func (cluster *Cluster) SwitchMonitoringSchemaChange() {
	cluster.conf.MonitorSchemaChange = !cluster.conf.MonitorSchemaChange
}

func (cluster *Cluster) SwitchMonitoringScheduler() {
	cluster.conf.MonitorScheduler = !cluster.conf.MonitorScheduler
}

func (cluster *Cluster) SwitchMonitoringQueries() {
	cluster.conf.MonitorQueries = !cluster.conf.MonitorQueries
}

func (cluster *Cluster) SwitchTestMode() {
	cluster.conf.Test = !cluster.conf.Test
}
