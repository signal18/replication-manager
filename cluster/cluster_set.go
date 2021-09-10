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
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	log "github.com/sirupsen/logrus"
)

func (cluster *Cluster) SetStatus() {
	if cluster.master == nil {
		cluster.sme.SetMasterUpAndSync(false, false)
	} else {
		cluster.sme.SetMasterUpAndSync(cluster.master.SemiSyncMasterStatus, cluster.master.RplMasterStatus)
	}
	cluster.Uptime = cluster.GetStateMachine().GetUptime()
	cluster.UptimeFailable = cluster.GetStateMachine().GetUptimeFailable()
	cluster.UptimeSemiSync = cluster.GetStateMachine().GetUptimeSemiSync()
	cluster.IsNotMonitoring = cluster.sme.IsInFailover()
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
		cluster.QPS = cluster.GetQps()
		cluster.Connections = cluster.GetConnections()
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
			cluster.LogPrintf(LvlErr, "Can't upload root CA to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload root Certificate to Safe %s", cluster.Conf.ProvSSLCaUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLCaUUID, "replication-manager")
	}
	if cluster.Conf.ProvSSLCert != "" {
		cluster.Conf.ProvSSLCertUUID, err = svc.PostSafe(cluster.Conf.ProvSSLCert)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't upload Server TLS Certificate to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload Server TLS Certificate to Safe %s", cluster.Conf.ProvSSLCertUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLCertUUID, "replication-manager")
	}
	if cluster.Conf.ProvSSLKey != "" {
		cluster.Conf.ProvSSLKeyUUID, err = svc.PostSafe(cluster.Conf.ProvSSLKey)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't upload Server TLS Private Key to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload Server TLS Private Key to Safe %s", cluster.Conf.ProvSSLKeyUUID)
		}
		svc.PublishSafe(cluster.Conf.ProvSSLKeyUUID, "replication-manager")
	}
}

func (cluster *Cluster) SetSchedulerBackupLogical() {

	if cluster.HasSchedulerEntry("backuplogical") {
		cluster.scheduler.Remove(cluster.idSchedulerLogicalBackup)
		cluster.LogPrintf(LvlInfo, "Disable database logical backup ")
	}
	if cluster.Conf.SchedulerBackupLogical {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule logical backup time at: %s", cluster.Conf.BackupLogicalCron)
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
	if cluster.HasSchedulerEntry("backupphysical") {
		cluster.LogPrintf(LvlInfo, "Disable database physical backup")
		cluster.scheduler.Remove(cluster.idSchedulerPhysicalBackup)
	}
	if cluster.Conf.SchedulerBackupPhysical {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule Physical backup time at: %s", cluster.Conf.BackupPhysicalCron)
		cluster.idSchedulerPhysicalBackup, err = cluster.scheduler.AddFunc(cluster.Conf.BackupPhysicalCron, func() {
			cluster.master.JobBackupPhysical()
		})
		if err == nil {
			cluster.Schedule["backupphysical"] = cluster.scheduler.Entry(cluster.idSchedulerPhysicalBackup)
		}
	}
}

func (cluster *Cluster) SetSchedulerLogsTableRotate() {
	if cluster.HasSchedulerEntry("logstablerotate") {
		cluster.LogPrintf(LvlInfo, "Disable database logs table rotate")
		cluster.scheduler.Remove(cluster.idSchedulerLogRotateTable)
	}
	if cluster.Conf.SchedulerDatabaseLogsTableRotate {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule database logs table rotate time at: %s", cluster.Conf.SchedulerDatabaseLogsTableRotateCron)
		cluster.idSchedulerLogRotateTable, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerDatabaseLogsTableRotateCron, func() {
			cluster.RotateLogs()
		})
		if err == nil {
			cluster.Schedule["logstablerotate"] = cluster.scheduler.Entry(cluster.idSchedulerLogRotateTable)
		}
	}
}

func (cluster *Cluster) SetSchedulerBackupLogs() {
	if cluster.HasSchedulerEntry("errorlogs") {
		cluster.LogPrintf(LvlInfo, "Disable database logs error fetching")
		cluster.scheduler.Remove(cluster.idSchedulerErrorLogs)
	}
	if cluster.Conf.SchedulerDatabaseLogs {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule database logs error fetching at: %s", cluster.Conf.BackupDatabaseLogCron)
		cluster.idSchedulerErrorLogs, err = cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseLogCron, func() {
			cluster.BackupLogs()
		})
		if err == nil {
			cluster.Schedule["errorlogs"] = cluster.scheduler.Entry(cluster.idSchedulerErrorLogs)
		}
	}
}

func (cluster *Cluster) SetSchedulerOptimize() {
	if cluster.HasSchedulerEntry("optimize") {
		cluster.LogPrintf(LvlInfo, "Disable database optimize")
		cluster.scheduler.Remove(cluster.idSchedulerOptimize)
	}
	if cluster.Conf.SchedulerDatabaseOptimize {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule database optimize at: %s", cluster.Conf.BackupDatabaseOptimizeCron)
		cluster.idSchedulerOptimize, err = cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseOptimizeCron, func() {
			cluster.RollingOptimize()
		})
		if err == nil {
			cluster.Schedule["optimize"] = cluster.scheduler.Entry(cluster.idSchedulerOptimize)
		}
	}
}

func (cluster *Cluster) SetSchedulerRollingRestart() {
	if cluster.HasSchedulerEntry("rollingrestart") {
		cluster.LogPrintf(LvlInfo, "Disable rolling restart")
		cluster.scheduler.Remove(cluster.idSchedulerRollingRestart)
	}
	if cluster.Conf.SchedulerRollingRestart {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule rolling restart at: %s", cluster.Conf.SchedulerRollingRestartCron)
		cluster.idSchedulerRollingRestart, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerRollingRestartCron, func() {
			cluster.RollingRestart()
		})
		if err == nil {
			cluster.Schedule["rollingrestart"] = cluster.scheduler.Entry(cluster.idSchedulerRollingRestart)
		}
	}
}

func (cluster *Cluster) SetSchedulerRollingReprov() {
	if cluster.HasSchedulerEntry("rollingreprov") {
		cluster.LogPrintf(LvlInfo, "Disable rolling reprov")
		cluster.scheduler.Remove(cluster.idSchedulerRollingReprov)
	}
	if cluster.Conf.SchedulerRollingReprov {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule rolling reprov at: %s", cluster.Conf.SchedulerRollingReprovCron)
		cluster.idSchedulerRollingReprov, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerRollingReprovCron, func() {
			cluster.RollingReprov()
		})
		if err == nil {
			cluster.Schedule["rollingreprov"] = cluster.scheduler.Entry(cluster.idSchedulerRollingReprov)
		}
	}
}

func (cluster *Cluster) SetSchedulerSlaRotate() {
	if cluster.HasSchedulerEntry("slarotate") {
		cluster.LogPrintf(LvlInfo, "Disable rotate Sla ")
		cluster.scheduler.Remove(cluster.idSchedulerSLARotate)
	}

	var err error
	cluster.LogPrintf(LvlInfo, "Schedule Sla rotate at: %s", cluster.Conf.SchedulerSLARotateCron)
	cluster.idSchedulerSLARotate, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerSLARotateCron, func() {
		cluster.SetEmptySla()
	})
	if err == nil {
		cluster.Schedule["slarotate"] = cluster.scheduler.Entry(cluster.idSchedulerSLARotate)
	}
}

func (cluster *Cluster) SetSchedulerDbJobsSsh() {
	if cluster.HasSchedulerEntry("dbjobsssh") {
		cluster.LogPrintf(LvlInfo, "Disable Db Jobs SSH Execution ")
		cluster.scheduler.Remove(cluster.idSchedulerDbsjobsSsh)
	}
	if cluster.Conf.SchedulerJobsSSH {
		var err error
		cluster.LogPrintf(LvlInfo, "Schedule Sla rotate at: %s", cluster.Conf.SchedulerJobsSSHCron)
		cluster.idSchedulerDbsjobsSsh, err = cluster.scheduler.AddFunc(cluster.Conf.SchedulerJobsSSHCron, func() {
			for _, s := range cluster.Servers {
				s.JobRunViaSSH()
			}
		})
		if err == nil {
			cluster.Schedule["dbjobsssh"] = cluster.scheduler.Entry(cluster.idSchedulerDbsjobsSsh)
		}
	}
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
		cluster.Conf.ProvIops=cluster.Configurator.GetConfigDBDiskIOPS()
		cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetTagsFromConfigurator() {
	cluster.Conf.ProvTags = cluster.Configurator.GetConfigDBTags()
	cluster.Conf.ProvTags = cluster.Configurator.GetConfigProxyTags()
}

func (cluster *Cluster) SetDBDiskIOPS(value string) {
	cluster.Configurator.SetDBDiskIOPS(value)
  cluster.Conf.ProvIops = cluster.Configurator.GetConfigDBDiskIOPS()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBMaxConnections(value string) {
	cluster.Configurator.SetDBMaxConnections(value)
	cluster.Conf.ProvMaxConnections =cluster.Configurator.GetConfigDBMaxConnections()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetDBExpireLogDays(value string) {
	cluster.Configurator.SetDBExpireLogDays(value)
	cluster.Conf.ProvExpireLogDays = cluster.Configurator.GetConfigDBExpireLogDays()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetProxyCores(value string) {
	cluster.Configurator.SetProxyCores(value)
	cluster.Conf.ProvProxCores =cluster.Configurator.GetConfigProxyCores()
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
func (cluster *Cluster) SetPrefMaster(PrefMaster string) {
	cluster.Conf.PrefMaster = PrefMaster
	for _, srv := range cluster.Servers {
		if strings.Contains(PrefMaster, srv.URL) {
			srv.SetPrefered(true)
		} else {
			srv.SetPrefered(false)
		}
	}
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
	cluster.master.State = stateFailed
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
		cluster.LogSQL(logs, err, cluster.GetMaster().URL, "MasterFailover", LvlErr, "Could not set  master as read-only, %s", err)

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

func (cluster *Cluster) SetClusterVariablesFromConfig() {
	cluster.Configurator.SetConfig(cluster.Conf)

	var err error
	err = cluster.loadDBCertificates(cluster.WorkingDir)
	if err != nil {
		cluster.HaveDBTLSCert = false
		cluster.LogPrintf(LvlInfo, "No database TLS certificates")
	} else {
		cluster.HaveDBTLSCert = true
		cluster.LogPrintf(LvlInfo, "Database TLS certificates correctly loaded")
	}
	err = cluster.loadDBOldCertificates(cluster.WorkingDir + "/old_certs")
	if err != nil {
		cluster.HaveDBTLSOldCert = false
		cluster.LogPrintf(LvlInfo, "No database previous TLS certificates")
	} else {
		cluster.HaveDBTLSOldCert = true
		cluster.LogPrintf(LvlInfo, "Database TLS previous certificates correctly loaded")
	}
	cluster.hostList = strings.Split(cluster.Conf.Hosts, ",")
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.Conf.User)
	cluster.rplUser, cluster.rplPass = misc.SplitPair(cluster.Conf.RplUser)

	if cluster.key != nil {
		p := crypto.Password{Key: cluster.key}
		p.CipherText = cluster.dbPass
		p.Decrypt()
		cluster.dbPass = p.PlainText
		p.CipherText = cluster.rplPass
		p.Decrypt()
		cluster.rplPass = p.PlainText
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
	cluster.Conf.BackupLogicalType = backup
}

func (cluster *Cluster) SetBackupPhysicalType(backup string) {
	cluster.Conf.BackupPhysicalType = backup
}

func (cluster *Cluster) SetEmptySla() {
	cluster.LogPrintf(LvlInfo, "Rotate SLA")
	cluster.SLAHistory = append(cluster.SLAHistory, cluster.sme.GetSla())
	cluster.sme.ResetUptime()
}

func (cluster *Cluster) SetDbServersCredential(credential string) {
	cluster.Conf.User = credential
	cluster.SetClusterVariablesFromConfig()
	for _, srv := range cluster.Servers {
		srv.SetCredential(srv.URL, cluster.dbUser, cluster.dbPass)
	}
	cluster.SetUnDiscovered()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetProxyServersCredential(credential string, proxytype string) {
	switch proxytype {
	case config.ConstProxySpider:
		cluster.Conf.MdbsProxyCredential = credential
	case config.ConstProxySqlproxy:
		cluster.Conf.ProxysqlUser, cluster.Conf.ProxysqlPassword = misc.SplitPair(credential)
	case config.ConstProxyMaxscale:
		cluster.Conf.MxsUser, cluster.Conf.MxsPass = misc.SplitPair(credential)
	}
	for _, prx := range cluster.Proxies {
		prx.SetCredential(credential)
		prx.SetRestartCookie()
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
	cluster.SetClusterVariablesFromConfig()
	cluster.SetUnDiscovered()
}

func (cluster *Cluster) SetUnDiscovered() {
	cluster.sme.UnDiscovered()
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
	cluster.clusterList = clusters
}

func (cluster *Cluster) SetState(key string, s state.State) {
	if !strings.Contains(cluster.Conf.MonitorIgnoreError, key) {
		cluster.sme.AddState(key, s)
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
			cl.LogPrintf("INFO", "Failed to post http new request to arbitrator %s ", jsonStr)
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
	if cl.GetLogLevel() > 2 {
		log.Printf(" Report abitrator connect took: %s\n", stopConnect.Sub(startConnect))
	}
	ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	cl.IsFailedArbitrator = false
	return nil
}

// SetClusterHead for MariaDB spider we can arbtitraty shard tables to child clusters
func (cluster *Cluster) SetClusterHead(ClusterName string) {
	cluster.Conf.ClusterHead = ClusterName
}

func (cluster *Cluster) SetServicePlan(theplan string) error {
	plans := cluster.GetServicePlans()
	for _, plan := range plans {
		if plan.Plan == theplan {
			cluster.LogPrintf(LvlInfo, "Attaching service plan %s", theplan)
			cluster.Conf.ProvServicePlan = theplan

			if cluster.Conf.User == "" {
				cluster.LogPrintf(LvlInfo, "Settting database root credential to admin:repman ")
				cluster.Conf.User = "admin:repman"
			}
			if cluster.Conf.RplUser == "" {
				cluster.LogPrintf(LvlInfo, "Settting database replication credential to repl:repman ")
				cluster.Conf.RplUser = "repl:repman"
			}
			cluster.LogPrintf(LvlInfo, "Adding %s database monitor on %s", string(strings.TrimPrefix(theplan, "x")[0]), cluster.Conf.ProvOrchestrator)
			if cluster.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost || cluster.Conf.ProvOrchestrator == config.ConstOrchestratorOnPremise {
				cluster.DropDBTag("docker")
				cluster.DropDBTag("threadpool")
				cluster.AddDBTag("pkg")
				cluster.Conf.ProvNetCNI = false
			}
			srvcount, err := strconv.Atoi(string(strings.TrimPrefix(theplan, "x")[0]))
			if err != nil {
				cluster.LogPrintf(LvlInfo, "Can't add database monitor error %s ", err)
			}
			hosts := []string{}
			for i := 1; i <= srvcount; i++ {
				cluster.LogPrintf(LvlInfo, "'%s' '%s'", cluster.Conf.ProvOrchestrator, config.ConstOrchestratorLocalhost)
				if cluster.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
					port, err := cluster.LocalhostGetFreePort()
					if err != nil {
						cluster.LogPrintf(LvlErr, "Adding DB monitor on 127.0.0.1 %s", err)
					} else {
						cluster.LogPrintf(LvlInfo, "Adding DB monitor 127.0.0.1:%s", port)
					}
					hosts = append(hosts, "127.0.0.1:"+port)
				} else if cluster.Conf.ProvOrchestrator != config.ConstOrchestratorOnPremise {
					hosts = append(hosts, "db"+strconv.Itoa(i))
				}
			}
			//	cluster.LogPrintf(LvlErr, strings.Join(hosts, ","))
			cluster.SetDbServerHosts(strings.Join(hosts, ","))

			cluster.sme.SetFailoverState()
			cluster.newServerList()
			wg := new(sync.WaitGroup)
			wg.Add(1)
			go cluster.TopologyDiscover(wg)
			wg.Wait()
			cluster.sme.RemoveFailoverState()
			cluster.Conf.ProxysqlOn = true
			cluster.Conf.ProxysqlHosts = ""
			cluster.Conf.MdbsProxyOn = true
			cluster.Conf.MdbsProxyHosts = ""
			// cluster head is used to copy exiting proxy from an other cluster
			if cluster.Conf.ClusterHead == "" {
				if cluster.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
					portproxysql, err := cluster.LocalhostGetFreePort()
					if err != nil {
						cluster.LogPrintf(LvlErr, "Adding proxysql monitor on 127.0.0.1 %s", err)
					} else {
						cluster.LogPrintf(LvlInfo, "Adding proxysql monitor 127.0.0.1:%s", portproxysql)
					}
					portshardproxy, err := cluster.LocalhostGetFreePort()
					if err != nil {
						cluster.LogPrintf(LvlErr, "Adding shard proxy monitor on 127.0.0.1 %s", err)
					} else {
						cluster.LogPrintf(LvlInfo, "Adding shard proxy monitor 127.0.0.1:%s", portshardproxy)
					}
					cluster.AddSeededProxy(config.ConstProxySqlproxy, "127.0.0.1", portproxysql, "", "")
					cluster.AddSeededProxy(config.ConstProxySpider, "127.0.0.1", portshardproxy, "", "")
				} else {
					cluster.AddSeededProxy(config.ConstProxySpider, "shardproxy1", "3306", "", "")
					cluster.AddSeededProxy(config.ConstProxySqlproxy, "proxysql1", cluster.Conf.ProxysqlPort, "", "")
				}
			} else {
				cluster.LogPrintf(LvlInfo, "Copy proxy list from cluster head %s", cluster.Conf.ClusterHead)

				oriClusters, err := cluster.GetClusterFromName(cluster.Conf.ClusterHead)
				if err == nil {
					for _, oriProxy := range oriClusters.Proxies {
						cluster.LogPrintf(LvlInfo, "Adding new proxy %s copy %s:%s", oriProxy.GetType(), oriProxy.GetHost(), oriProxy.GetPort())
						if oriProxy.GetType() == config.ConstProxySpider {
							cluster.AddSeededProxy(oriProxy.GetType(), oriProxy.GetHost(), oriProxy.GetPort(), oriProxy.GetUser(), oriProxy.GetPass())
						}
					}
					if cluster.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
						portproxysql, err := cluster.LocalhostGetFreePort()
						if err != nil {
							cluster.LogPrintf(LvlErr, "Adding proxysql monitor on 127.0.0.1 %s", err)
						} else {
							cluster.LogPrintf(LvlInfo, "Adding proxysql monitor 127.0.0.1:%s", portproxysql)
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
			return nil
		}
	}
	cluster.LogPrintf(LvlErr, "Service plan not found %s", theplan)
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
	cluster.Conf.BackupPhysicalCron = value
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

func (cluster *Cluster) SetDbServerHosts(value string) error {
	cluster.Conf.Hosts = value
	cluster.hostList = strings.Split(value, ",")
	return nil
}

func (cluster *Cluster) SetProvOrchestrator(value string) error {
	orchetrators := cluster.Conf.GetOrchestratorsProv()
	for _, orch := range orchetrators {
		if orch.Name == value {
			cluster.LogPrintf(LvlInfo, "Cluster orchestrator set to %s", orch.Name)
			cluster.Conf.ProvOrchestrator = value
			return nil
		}
	}
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	cluster.LogPrintf(LvlErr, "Cluster orchestrator set to default %s", config.ConstOrchestratorOnPremise)
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
