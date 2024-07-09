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
	"strconv"
	"strings"
	"sync"
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
	cluster.IsNeedProxiesRestart = cluster.HasRequestProxiesRestart()
	cluster.IsNeedProxiesReprov = cluster.HasRequestProxiesReprov()
	cluster.IsNeedDatabasesRollingRestart = cluster.HasRequestDBRollingRestart()
	cluster.IsNeedDatabasesRollingReprov = cluster.HasRequestDBRollingReprov()
	cluster.IsNeedDatabasesRestart = cluster.HasRequestDBRestart()
	cluster.IsNeedDatabasesReprov = cluster.HasRequestDBReprov()
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
	if cluster.Conf.Enterprise == false {
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

func (cluster *Cluster) SetSchedulerBackupLogical() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("backuplogical") {
		cluster.scheduler.Remove(cluster.idSchedulerLogicalBackup)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database logical backup ")
		delete(cluster.Schedule, "backuplogical")
	}
	if cluster.Conf.SchedulerBackupLogical {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule logical backup time at: %s", cluster.Conf.BackupLogicalCron)
		cluster.idSchedulerLogicalBackup, err = cluster.scheduler.AddFunc(cluster.Conf.BackupLogicalCron, func() {
			mysrv := cluster.GetBackupServer()
			if mysrv != nil {
				mysrv.JobBackupLogical()
			} else {
				cluster.master.JobBackupLogical()
			}
		})
		if err == nil {
			cluster.Schedule["backuplogical"] = cluster.scheduler.Entry(cluster.idSchedulerPhysicalBackup)
		}
	}
}

func (cluster *Cluster) SetSchedulerBackupPhysical() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("backupphysical") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database physical backup")
		cluster.scheduler.Remove(cluster.idSchedulerPhysicalBackup)
		delete(cluster.Schedule, "backupphysical")
	}
	if cluster.Conf.SchedulerBackupPhysical {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule Physical backup time at: %s", cluster.Conf.BackupPhysicalCron)
		cluster.idSchedulerPhysicalBackup, err = cluster.scheduler.AddFunc(cluster.Conf.BackupPhysicalCron, func() {
			cluster.master.JobBackupPhysical()
		})
		if err == nil {
			cluster.Schedule["backupphysical"] = cluster.scheduler.Entry(cluster.idSchedulerPhysicalBackup)
		}
	}
}

func (cluster *Cluster) SetSchedulerLogsTableRotate() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("logstablerotate") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database logs table rotate")
		cluster.scheduler.Remove(cluster.idSchedulerLogRotateTable)
		delete(cluster.Schedule, "logstablerotate")
	}
	if cluster.Conf.SchedulerDatabaseLogsTableRotate {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule database logs table rotate time at: %s", cluster.Conf.SchedulerDatabaseLogsTableRotateCron)
		cluster.idSchedulerLogRotateTable, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerDatabaseLogsTableRotateCron, func() {
			cluster.RotateLogs()
		})
		if err == nil {
			cluster.Schedule["logstablerotate"] = cluster.scheduler.Entry(cluster.idSchedulerLogRotateTable)
		}
	}
}

func (cluster *Cluster) SetSchedulerBackupLogs() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("errorlogs") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database logs error fetching")
		cluster.scheduler.Remove(cluster.idSchedulerErrorLogs)
		delete(cluster.Schedule, "errorlogs")
	}
	if cluster.Conf.SchedulerDatabaseLogs && cluster.scheduler != nil {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule database logs error fetching at: %s", cluster.Conf.BackupDatabaseLogCron)
		cluster.idSchedulerErrorLogs, err = cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseLogCron, func() {
			cluster.BackupLogs()
		})
		if err == nil {
			cluster.Schedule["errorlogs"] = cluster.scheduler.Entry(cluster.idSchedulerErrorLogs)
		}
	}
}

func (cluster *Cluster) SetSchedulerOptimize() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("optimize") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database optimize")
		cluster.scheduler.Remove(cluster.idSchedulerOptimize)
		delete(cluster.Schedule, "optimize")
	}
	if cluster.Conf.SchedulerDatabaseOptimize {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule database optimize at: %s", cluster.Conf.BackupDatabaseOptimizeCron)
		cluster.idSchedulerOptimize, err = cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseOptimizeCron, func() {
			cluster.RollingOptimize()
		})
		if err == nil {
			cluster.Schedule["optimize"] = cluster.scheduler.Entry(cluster.idSchedulerOptimize)
		}
	}
}

func (cluster *Cluster) SetSchedulerAnalyze() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("analyze") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable database analyze")
		cluster.scheduler.Remove(cluster.idSchedulerAnalyze)
		delete(cluster.Schedule, "analyze")
	}
	if cluster.Conf.SchedulerDatabaseAnalyze {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule database analyze at: %s", cluster.Conf.BackupDatabaseAnalyzeCron)
		cluster.idSchedulerAnalyze, err = cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseAnalyzeCron, func() {
			cluster.JobAnalyzeSQL()
		})
		if err == nil {
			cluster.Schedule["analyze"] = cluster.scheduler.Entry(cluster.idSchedulerAnalyze)
		}
	}
}

func (cluster *Cluster) SetSchedulerRollingRestart() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("rollingrestart") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable rolling restart")
		cluster.scheduler.Remove(cluster.idSchedulerRollingRestart)
		delete(cluster.Schedule, "rollingrestart")
	}
	if cluster.Conf.SchedulerRollingRestart {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule rolling restart at: %s", cluster.Conf.SchedulerRollingRestartCron)
		cluster.idSchedulerRollingRestart, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerRollingRestartCron, func() {
			cluster.RollingRestart()
		})
		if err == nil {
			cluster.Schedule["rollingrestart"] = cluster.scheduler.Entry(cluster.idSchedulerRollingRestart)
		}
	}
}

func (cluster *Cluster) SetSchedulerRollingReprov() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("rollingreprov") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable rolling reprov")
		cluster.scheduler.Remove(cluster.idSchedulerRollingReprov)
		delete(cluster.Schedule, "rollingreprov")
	}
	if cluster.Conf.SchedulerRollingReprov {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule rolling reprov at: %s", cluster.Conf.SchedulerRollingReprovCron)
		cluster.idSchedulerRollingReprov, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerRollingReprovCron, func() {
			cluster.RollingReprov()
		})
		if err == nil {
			cluster.Schedule["rollingreprov"] = cluster.scheduler.Entry(cluster.idSchedulerRollingReprov)
		}
	}
}

func (cluster *Cluster) SetSchedulerSlaRotate() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("slarotate") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable rotate Sla ")
		cluster.scheduler.Remove(cluster.idSchedulerSLARotate)
		delete(cluster.Schedule, "slarotate")
	}

	var err error
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule Sla rotate at: %s", cluster.Conf.SchedulerSLARotateCron)
	cluster.idSchedulerSLARotate, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerSLARotateCron, func() {
		cluster.SetEmptySla()
	})
	if err == nil {
		cluster.Schedule["slarotate"] = cluster.scheduler.Entry(cluster.idSchedulerSLARotate)
	}
}

func (cluster *Cluster) SetSchedulerDbJobsSsh() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("dbjobsssh") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable Db Jobs SSH Execution ")
		cluster.scheduler.Remove(cluster.idSchedulerDbsjobsSsh)
		delete(cluster.Schedule, "dbjobsssh")
	}
	if cluster.Conf.SchedulerJobsSSH {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule SshDbJob rotate at: %s", cluster.Conf.SchedulerJobsSSHCron)
		cluster.idSchedulerDbsjobsSsh, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerJobsSSHCron, func() {
			for _, s := range cluster.Servers {
				if s != nil {
					s.JobRunViaSSH()
				}

			}
		})
		if err == nil {
			cluster.Schedule["dbjobsssh"] = cluster.scheduler.Entry(cluster.idSchedulerDbsjobsSsh)
		}
	}
}

func (cluster *Cluster) SetSchedulerAlertDisable() {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}
	if cluster.HasSchedulerEntry("alertdisable") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping scheduler to disable alert")
		cluster.scheduler.Remove(cluster.idSchedulerAlertDisable)
		delete(cluster.Schedule, "alertdisable")
	}
	if cluster.Conf.SchedulerAlertDisable {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule disable alert at: %s", cluster.Conf.SchedulerAlertDisableCron)
		cluster.idSchedulerAlertDisable, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerAlertDisableCron, func() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Alerting is disabled from scheduler")
			cluster.IsAlertDisable = true
			go cluster.WaitAlertDisable()
		})
		if err == nil {
			cluster.Schedule["alertdisable"] = cluster.scheduler.Entry(cluster.idSchedulerAlertDisable)
		}
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
}

func (cluster *Cluster) SetDBDiskSize(value string) {

	cluster.Configurator.SetDBDisk(value)
	cluster.Conf.ProvDisk = cluster.Configurator.GetConfigDBDisk()

	cluster.SetDBReprovCookie()
}

func (cluster *Cluster) SetDBCores(value string) {

	cluster.Configurator.SetDBCores(value)
	cluster.Conf.ProvCores = cluster.Configurator.GetConfigDBCores()
	cluster.SetDBReprovCookie()
}

func (cluster *Cluster) SetDBMemorySize(value string) {

	cluster.Configurator.SetDBMemory(value)
	cluster.Conf.ProvMem = cluster.Configurator.GetConfigDBMemory()
	cluster.SetDBReprovCookie()
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
}

func (cluster *Cluster) SetDBDiskIOPS(value string) {
	cluster.Configurator.SetDBDiskIOPS(value)
	cluster.Conf.ProvIops = cluster.Configurator.GetConfigDBDiskIOPS()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBMaxConnections(value string) {
	cluster.Configurator.SetDBMaxConnections(value)
	cluster.Conf.ProvMaxConnections = cluster.Configurator.GetConfigDBMaxConnections()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBExpireLogDays(value string) {
	cluster.Configurator.SetDBExpireLogDays(value)
	cluster.Conf.ProvExpireLogDays = cluster.Configurator.GetConfigDBExpireLogDays()
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

func (cluster *Cluster) SetTraffic(traffic bool) {
	cluster.Conf.TestInjectTraffic = traffic
}

func (cluster *Cluster) SetBenchMethod(m string) {
	cluster.benchmarkType = m
}

// SetPrefMaster is used by regtest test_switchover_semisync_switchback_prefmaster_norplcheck and API to force a server
func (cluster *Cluster) SetPrefMaster(PrefMasterURL string) {
	var prefmasterlist []string
	for _, srv := range cluster.Servers {
		if strings.Contains(PrefMasterURL, srv.URL) {
			srv.SetPrefered(true)
			prefmasterlist = append(prefmasterlist, strings.Replace(srv.URL, srv.Domain+":3306", "", -1))
		} else {
			srv.SetPrefered(false)
		}
	}
	cluster.Conf.PrefMaster = strings.Join(prefmasterlist, ",")
	// fmt.Printf("Update config prefered Master: " + cluster.Conf.PrefMaster + "\n")
}

// Set Ignored Host for Election
func (cluster *Cluster) SetIgnoreSrv(IgnoredHostURL string) {
	var ignoresrvlist []string
	for _, srv := range cluster.Servers {
		if strings.Contains(IgnoredHostURL, srv.URL) {
			srv.SetIgnored(true)
			ignoresrvlist = append(ignoresrvlist, strings.Replace(srv.URL, srv.Domain+":3306", "", -1))
		} else {
			srv.SetIgnored(false)
		}
	}
	cluster.Conf.IgnoreSrv = strings.Join(ignoresrvlist, ",")
	// fmt.Printf("Update config ignored server: " + cluster.Conf.IgnoreSrv + "\n")
}

// Set Ignored for ReadOnly Check
func (cluster *Cluster) SetIgnoreRO(IgnoredReadOnlyHostURL string) {
	var ignoreROList []string
	for _, srv := range cluster.Servers {
		if strings.Contains(IgnoredReadOnlyHostURL, srv.URL) {
			srv.SetIgnoredReadonly(true)
			ignoreROList = append(ignoreROList, strings.Replace(srv.URL, srv.Domain+":3306", "", -1))
		} else {
			srv.SetIgnoredReadonly(false)
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
func (cluster *Cluster) SetBackupRestic(check bool) error {
	cluster.Conf.BackupRestic = check
	return nil
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

func (cluster *Cluster) SetLogLevel(level int) {
	cluster.Conf.LogLevel = level
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
	cluster.Conf.DecryptSecretsFromConfig()
	cluster.DecryptSecretsFromVault()
	cluster.SetClusterMonitorCredentialsFromConfig()
	cluster.SetClusterReplicationCredentialsFromConfig()
	cluster.SetClusterProxyCredentialsFromConfig()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Reveal Secrets %v", cluster.Conf.Secrets)
}

func (cluster *Cluster) SetClusterProxyCredentialsFromConfig() {

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
				newSecret.OldValue = cluster.Conf.Secrets["proxysql-password"].Value
				newSecret.Value = pass
				cluster.Conf.Secrets["proxysql-password"] = newSecret
			}

			if user != "" {
				newSecret.OldValue = cluster.Conf.Secrets["proxysql-user"].Value
				newSecret.Value = user
				cluster.Conf.Secrets["proxysql-user"] = newSecret
			}

		}
		if cluster.Conf.MdbsProxyOn && cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) {
			user, pass, _ := cluster.GetVaultShardProxyCredentials(client)
			var newSecret config.Secret
			if user != "" && pass != "" {

				newSecret.OldValue = cluster.Conf.Secrets["shardproxy-credential"].Value
				newSecret.Value = user + ":" + pass
				cluster.Conf.Secrets["shardproxy-credential"] = newSecret
			}
		}

	}
}

func (cluster *Cluster) SetClusterMonitorCredentialsFromConfig() {
	cluster.Configurator.SetConfig(cluster.Conf)
	//splitmonitoringuser := cluster.Conf.User

	var err error
	err = cluster.loadDBCertificates(cluster.WorkingDir)
	if err != nil {
		cluster.HaveDBTLSCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database TLS certificates")
	} else {
		cluster.HaveDBTLSCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS certificates correctly loaded")
	}
	err = cluster.loadDBOldCertificates(cluster.WorkingDir + "/old_certs")
	if err != nil {
		cluster.HaveDBTLSOldCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database previous TLS certificates")
	} else {
		cluster.HaveDBTLSOldCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS previous certificates correctly loaded")
	}

	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.User) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		user, pass, _ := cluster.GetVaultMonitorCredentials(client)
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets["db-servers-credential"].Value
		newSecret.Value = user + ":" + pass
		cluster.Conf.Secrets["db-servers-credential"] = newSecret

	}

	cluster.hostList = strings.Split(cluster.Conf.Hosts, ",")
	/*
		cluster.dbUser, cluster.dbPass = misc.SplitPair(splitmonitoringuser)

		cluster.dbPass = cluster.GetDecryptedPassword("db-servers-credential", cluster.dbPass)*/
	cluster.LoadAPIUsers()
	cluster.Save()
}

func (cluster *Cluster) SetClusterReplicationCredentialsFromConfig() {
	cluster.Configurator.SetConfig(cluster.Conf)

	//splitreplicationuser := cluster.Conf.RplUser

	var err error
	err = cluster.loadDBCertificates(cluster.WorkingDir)
	if err != nil {
		cluster.HaveDBTLSCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database TLS certificates")
	} else {
		cluster.HaveDBTLSCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS certificates correctly loaded")
	}
	err = cluster.loadDBOldCertificates(cluster.WorkingDir + "/old_certs")
	if err != nil {
		cluster.HaveDBTLSOldCert = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "No database previous TLS certificates")
	} else {
		cluster.HaveDBTLSOldCert = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Database TLS previous certificates correctly loaded")
	}
	if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(cluster.Conf.RplUser) {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		user, pass, _ := cluster.GetVaultReplicationCredentials(client)
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets["replication-credential"].Value
		newSecret.Value = user + ":" + pass
		cluster.Conf.Secrets["replication-credential"] = newSecret

	}
}

func (cluster *Cluster) SetAgentsCpuCoreMem() {
	if len(cluster.Agents) != 0 {
		min_cpu := cluster.Agents[0].CpuCores
		min_mem := cluster.Agents[0].MemBytes
		for _, a := range cluster.Agents {
			if a.CpuCores < min_cpu {
				min_cpu = a.CpuCores
			}
			if a.MemBytes < min_mem {
				min_mem = a.MemBytes
			}
		}
		cluster.Conf.ImmuableFlagMap["agent-cpu-cores"] = min_cpu
		cluster.Conf.ImmuableFlagMap["agent-memory"] = min_mem
	}
}

func (cluster *Cluster) SetBackupKeepYearly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepYearly = numkeep
	return nil
}

func (cluster *Cluster) SetBackupKeepHourly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepHourly = numkeep
	return nil
}

func (cluster *Cluster) SetBackupKeepMonthly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepMonthly = numkeep
	return nil
}
func (cluster *Cluster) SetBackupKeepDaily(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepDaily = numkeep
	return nil
}

func (cluster *Cluster) SetBackupKeepWeekly(keep string) error {
	numkeep, err := strconv.Atoi(keep)
	if err != nil {
		return err
	}
	cluster.Conf.BackupKeepWeekly = numkeep
	return nil
}

func (cluster *Cluster) SetBackupLogicalType(backup string) {
	if cluster.Conf.BackupLogicalType != backup {
		cluster.Conf.BackupLogicalType = backup
		cluster.GetBackupServer().DelBackupLogicalCookie()
	}
}

func (cluster *Cluster) SetBackupPhysicalType(backup string) {
	if cluster.Conf.BackupPhysicalType != backup {
		cluster.Conf.BackupPhysicalType = backup
		cluster.GetBackupServer().DelBackupPhysicalCookie()
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
		for _, u := range cluster.master.Users {
			if u.User == cluster.GetDbUser() {
				found_user = true
				logs, err := dbhelper.SetUserPassword(cluster.master.Conn, cluster.master.DBVersion, u.Host, u.User, cluster.GetDbPass())
				cluster.LogSQL(logs, err, cluster.master.URL, "Security", config.LvlErr, "Alter user : %s", err)

			}

		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Monitoring password rotation")
		if !found_user {
			oldDbUser, _ := misc.SplitPair(cluster.Conf.Secrets["db-servers-credential"].OldValue)
			if oldDbUser != "root" {

				for _, u := range cluster.master.Users {
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
		}
	}
}

func (cluster *Cluster) SetProxyServersCredential(credential string, proxytype string) {
	switch proxytype {
	case config.ConstProxySpider:
		//cluster.Conf.MdbsProxyCredential = credential
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets["shardproxy-credential"].Value
		newSecret.Value = credential
		cluster.Conf.Secrets["shardproxy-credential"] = newSecret

		for _, pri := range cluster.Proxies {
			_, pass := misc.SplitPair(credential)
			if prx, ok := pri.(*MariadbShardProxy); ok {

				prx.RotateProxyPasswords(pass)
				prx.SetCredential(credential)
				prx.ShardProxy.SetCredential(prx.ShardProxy.URL, prx.User, pass)
				for _, u := range prx.ShardProxy.Users {
					if u.User == prx.User {
						dbhelper.SetUserPassword(prx.ShardProxy.Conn, prx.ShardProxy.DBVersion, u.Host, u.User, pass)
					}

				}

			}
		}
	case config.ConstProxySqlproxy:
		//cluster.Conf.ProxysqlUser, cluster.Conf.ProxysqlPassword
		user, pass := misc.SplitPair(credential)
		var newSecret config.Secret
		newSecret.OldValue = cluster.Conf.Secrets["proxysql-password"].Value
		newSecret.Value = pass
		cluster.Conf.Secrets["proxysql-password"] = newSecret
		newSecret.OldValue = cluster.Conf.Secrets["proxysql-user"].Value
		newSecret.Value = user
		cluster.Conf.Secrets["proxysql-user"] = newSecret
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
		cluster.Conf.MxsUser, cluster.Conf.MxsPass = misc.SplitPair(credential)

		/*for _, pri := range cluster.Proxies {

			pri.SetCredential(credential)
			pri.SetRestartCookie()

		}*/
	}
}

func (cluster *Cluster) SetDBRestartCookie() {
	for _, srv := range cluster.Servers {
		srv.SetRestartCookie()
	}
}
func (cluster *Cluster) SetDBReprovCookie() {
	for _, srv := range cluster.Servers {
		srv.SetReprovCookie()
	}
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

func (cluster *Cluster) SetReplicationCredential(credential string) {
	cluster.Conf.RplUser = credential
	cluster.SetClusterReplicationCredentialsFromConfig()
	cluster.SetUnDiscovered()
}

func (cluster *Cluster) SetUnDiscovered() {
	cluster.StateMachine.UnDiscovered()
	cluster.Topology = topoUnknown
}

func (cluster *Cluster) SetActiveStatus(status string) {
	cluster.Status = status
	if cluster.Conf.MonitorScheduler {
		if cluster.Status == ConstMonitorActif {
			cluster.scheduler.Start()
		} else {
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

func (cluster *Cluster) SetState(key string, s state.State) {
	if !strings.Contains(cluster.Conf.MonitorIgnoreError, key) {
		cluster.StateMachine.AddState(key, s)
	}
}

func (cl *Cluster) SetArbitratorReport() error {
	//	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)

	cl.IsLostMajority = cl.LostMajority()
	// SplitBrain

	url := "http://" + cl.Conf.ArbitrationSasHosts + "/heartbeat"
	var mst string
	if cl.GetMaster() != nil {
		mst = cl.GetMaster().URL
	}
	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + cl.Status + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		if cl.Conf.LogHeartbeat {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "INFO", "Failed to post http new request to arbitrator %s ", jsonStr)
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
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlInfo, " Report abitrator connect took: %s\n", stopConnect.Sub(startConnect))
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

func (cluster *Cluster) SetSysbenchThreads(Threads string) {
	i, err := strconv.Atoi(Threads)
	if err == nil {
		cluster.Conf.SysbenchThreads = i
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error converting threads to int %s", err)
	}
}

/*
Set Service Plan. Log Module : Topology
*/
func (cluster *Cluster) SetServicePlan(theplan string) error {
	plans := cluster.GetServicePlans()
	for _, plan := range plans {
		if plan.Plan == theplan {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Attaching service plan %s", theplan)
			cluster.Conf.ProvServicePlan = theplan

			if cluster.Conf.User == "" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Settting database root credential to admin:repman ")
				cluster.Conf.User = "admin:repman"
			}
			if cluster.Conf.RplUser == "" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Settting database replication credential to repl:repman ")
				cluster.Conf.RplUser = "repl:repman"
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Adding %s database monitor on %s", string(strings.TrimPrefix(theplan, "x")[0]), cluster.Conf.ProvOrchestrator)
			if cluster.GetOrchestrator() == config.ConstOrchestratorLocalhost || cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
				cluster.DropDBTag("docker")
				cluster.DropDBTag("threadpool")
				cluster.AddDBTag("pkg")
				cluster.Conf.ProvNetCNI = false
			}
			srvcount, err := strconv.Atoi(string(strings.TrimPrefix(theplan, "x")[0]))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Can't add database monitor error %s ", err)
			}
			hosts := []string{}
			for i := 1; i <= srvcount; i++ {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "'%s' '%s'", cluster.Conf.ProvOrchestrator, config.ConstOrchestratorLocalhost)
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
					err = cluster.AddSeededProxy(config.ConstProxySqlproxy, "proxysql1", cluster.Conf.ProxysqlPort, "external", cluster.Conf.Secrets["proxysql-password"].Value)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Fail adding proxysql monitor on %s %s", cluster.Conf.ProxysqlPort, err)
					}
				}
			} else {
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
			cluster.SetDBCores(strconv.Itoa(plan.DbCores))
			cluster.SetDBMemorySize(strconv.Itoa(plan.DbMemory))
			cluster.SetDBDiskSize(strconv.Itoa(plan.DbDataSize))
			cluster.SetDBDiskIOPS(strconv.Itoa(plan.DbIops))
			cluster.SetProxyCores(strconv.Itoa(plan.PrxCores))
			cluster.SetProxyDiskSize(strconv.Itoa(plan.PrxDataSize))
			cluster.Save()
			return nil
		}
	}
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

func (cluster *Cluster) SetDbServerHosts(value string) error {
	cluster.Conf.Hosts = value
	cluster.hostList = strings.Split(value, ",")
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

func (cluster *Cluster) SetProvDBImage(value string) error {
	cluster.Conf.ProvDbImg = value
	cluster.SetDBReprovCookie()
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
	cluster.SetDBReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbDiskFS(value string) error {
	cluster.Conf.ProvDiskFS = value
	cluster.SetDBReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbDiskPool(value string) error {
	cluster.Conf.ProvDiskPool = value
	cluster.SetDBReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbDiskDevice(value string) error {
	cluster.Conf.ProvDiskDevice = value
	cluster.SetDBReprovCookie()
	return nil
}

func (cluster *Cluster) SetProvDbServiceType(value string) error {
	cluster.Conf.ProvType = value
	cluster.SetDBReprovCookie()
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
	cluster.exit = true
}

func (cluster *Cluster) SetInjectVariables() {
	for key, val := range cluster.Conf.DynamicFlagMap {
		switch key {
		case "vault-role-id":
			cluster.Conf.VaultRoleId = fmt.Sprintf("%v", val)
		case "vault-secret-id":
			cluster.Conf.VaultSecretId = fmt.Sprintf("%v", val)
			var secret config.Secret
			secret.Value = fmt.Sprintf("%v", val)
			cluster.Conf.Secrets["vault-secret-id"] = secret
			cluster.SetSecretsToVault()
			cluster.GetVaultToken()
		case "api-oauth-client-id":
			cluster.Conf.OAuthClientID = fmt.Sprintf("%v", val)
		case "api-oauth-client-secret":
			cluster.Conf.OAuthClientSecret = fmt.Sprintf("%v", val)
			var secret config.Secret
			secret.Value = fmt.Sprintf("%v", val)
			cluster.Conf.Secrets["api-oauth-client-secret"] = secret
		case "api-credentials-external":
			cluster.Conf.APIUsersExternal = fmt.Sprintf("%v", val)
		case "api-credentials-acl-allow":
			cluster.Conf.APIUsersACLAllow = fmt.Sprintf("%v", val)
		}
	}
}

func (cluster *Cluster) SetSecretsToVault() {
	if cluster.Conf.VaultRoleId != "" && cluster.Conf.VaultSecretId != "" {
		cluster.Conf.VaultServerAddr = "http://vault.infra.svc.cloud18:8200"
		client, err := cluster.GetVaultConnection()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			return
		}
		secrets_map := make(map[string]interface{})
		for key, val := range cluster.Conf.Secrets {
			if val.Value != "" {
				secrets_map[key] = val.Value
			}

		}
		secret_path := cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone + "/" + cluster.Name
		_, err = client.KVv2(cluster.Conf.VaultMount).Patch(context.Background(), secret_path, secrets_map)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Failed to write secrets to Vault: %v", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Success of writing secrets to Vault: %v", err)
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
	cluster.Lock()
	cluster.clog = value
	cluster.Unlock()
}

func (cluster *Cluster) SetLogGraphiteLevel(value int) {
	cluster.Conf.LogGraphiteLevel = value
	if value > 0 {
		cluster.Conf.LogGraphite = true
	} else {
		cluster.Conf.LogGraphite = false
	}

	cluster.clog.SetLevel(cluster.Conf.ToLogrusLevel(value))
}

func (cluster *Cluster) SetLogBinlogPurgeLevel(value int) {
	cluster.Conf.LogBinlogPurgeLevel = value
	if value > 0 {
		cluster.Conf.LogBinlogPurge = true
	} else {
		cluster.Conf.LogBinlogPurge = false
	}
}

func (cluster *Cluster) SetInPhysicalBackupState(value bool) {
	cluster.Lock()
	cluster.InPhysicalBackup = value
	cluster.Unlock()
}

func (cluster *Cluster) SetInLogicalBackupState(value bool) {
	cluster.Lock()
	cluster.InLogicalBackup = value
	cluster.Unlock()
}

func (cluster *Cluster) SetInBinlogBackupState(value bool) {
	cluster.Lock()
	cluster.InBinlogBackup = value
	cluster.Unlock()
}

func (cluster *Cluster) SetInResticBackupState(value bool) {
	cluster.Lock()
	cluster.InResticBackup = value
	cluster.Unlock()
}

func (cluster *Cluster) SetGraphiteWhitelistTemplate(value string) {
	cluster.Lock()
	cluster.Conf.GraphiteWhitelistTemplate = value
	cluster.Unlock()
}

func (cluster *Cluster) SetTopologyTarget(value string) {
	cluster.Lock()
	cluster.Conf.TopologyTarget = value
	cluster.Unlock()
}
