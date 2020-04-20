// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

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

func (cluster *Cluster) SetCfgGroupDisplay(cfgGroup string) {
	cluster.cfgGroupDisplay = cfgGroup
}

func (cluster *Cluster) SetInteractive(check bool) {
	cluster.Conf.Interactive = check
}

func (cluster *Cluster) SetDBDiskSize(value string) {
	cluster.Conf.ProvDisk = value
	cluster.SetDBReprovCookie()
}
func (cluster *Cluster) SetDBCores(value string) {
	cluster.Conf.ProvCores = value
	cluster.SetDBRestartCookie()
}
func (cluster *Cluster) SetDBMemorySize(value string) {
	cluster.Conf.ProvMem = value
	cluster.SetDBRestartCookie()
}
func (cluster *Cluster) SetDBDiskIOPS(value string) {
	cluster.Conf.ProvIops = value
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) SetProxyCores(value string) {
	cluster.Conf.ProvProxCores = value
	cluster.SetProxiesRestartCookie()
}
func (cluster *Cluster) SetProxyMemorySize(value string) {
	cluster.Conf.ProvProxMem = value
	cluster.SetProxiesRestartCookie()
}
func (cluster *Cluster) SetProxyDiskSize(value string) {
	cluster.Conf.ProvProxDisk = value
	cluster.SetProxiesReprovCookie()
}

func (cluster *Cluster) SetTraffic(traffic bool) {
	//cluster.SetBenchMethod("table")
	//cluster.PrepareBench()
	cluster.Conf.TestInjectTraffic = traffic
}

func (cluster *Cluster) SetBenchMethod(m string) {
	cluster.benchmarkType = m
}

func (cluster *Cluster) SetPrefMaster(PrefMaster string) {
	for _, srv := range cluster.Servers {
		if srv.URL == PrefMaster || srv.Name == PrefMaster {
			srv.SetPrefered(true)
		} else {
			srv.SetPrefered(false)
		}
	}
	cluster.Conf.PrefMaster = PrefMaster
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
	cluster.DBTags = cluster.GetDatabaseTags()
	cluster.ProxyTags = cluster.GetProxyTags()
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
		cluster.Conf.MdbsProxyUser = credential
	case config.ConstProxySqlproxy:
		cluster.Conf.ProxysqlUser, cluster.Conf.ProxysqlPassword = misc.SplitPair(credential)
	case config.ConstProxyMaxscale:
		cluster.Conf.MxsUser, cluster.Conf.MxsPass = misc.SplitPair(credential)
	}
	for _, prx := range cluster.Proxies {
		prx.User, prx.Pass = misc.SplitPair(credential)
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

func (cluster *Cluster) DropDBTag(dtag string) {
	var newtags []string
	for _, tag := range cluster.DBTags {
		//	cluster.LogPrintf(LvlInfo, "%s %s", tag, dtag)
		if dtag != tag {
			newtags = append(newtags, tag)
		}
	}
	cluster.DBTags = newtags
	cluster.Conf.ProvTags = strings.Join(cluster.DBTags, ",")
	cluster.SetClusterVariablesFromConfig()
	cluster.SetDBRestartCookie()
}

func (cluster *Cluster) DropProxyTag(dtag string) {
	var newtags []string
	for _, tag := range cluster.ProxyTags {
		//	cluster.LogPrintf(LvlInfo, "%s %s", tag, dtag)
		if dtag != tag {
			newtags = append(newtags, tag)
		}
	}
	cluster.ProxyTags = newtags
	cluster.Conf.ProvProxTags = strings.Join(cluster.ProxyTags, ",")
	cluster.SetClusterVariablesFromConfig()
	cluster.SetProxiesRestartCookie()
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
	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker) * time.Second * 4)

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

	client := &http.Client{Timeout: timeout}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		cl.IsFailedArbitrator = true
		return err
	}
	defer resp.Body.Close()
	ioutil.ReadAll(resp.Body)
	cl.IsFailedArbitrator = false
	return nil

}

func (cluster *Cluster) SetClusterHead(ClusterName string) {
	cluster.Conf.ClusterHead = ClusterName
}

func (cluster *Cluster) SetServicePlan(theplan string) error {
	plans := cluster.GetServicePlans()
	for _, plan := range plans {
		if plan.Plan == theplan {
			cluster.LogPrintf(LvlInfo, "Attaching service plan %s", theplan)
			cluster.Conf.ProvServicePlan = theplan
			cluster.SetDBCores(strconv.Itoa(plan.DbCores))
			cluster.SetDBMemorySize(strconv.Itoa(plan.DbMemory))
			cluster.SetDBDiskSize(strconv.Itoa(plan.DbDataSize))
			cluster.SetDBDiskIOPS(strconv.Itoa(plan.DbIops))
			cluster.SetProxyCores(strconv.Itoa(plan.PrxCores))
			cluster.SetProxyDiskSize(strconv.Itoa(plan.PrxDataSize))
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
			cluster.TopologyDiscover()
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
						cluster.LogPrintf(LvlInfo, "Adding new proxy %s copy %s:%s", oriProxy.Type, oriProxy.Host, oriProxy.Port)
						if oriProxy.Type == config.ConstProxySpider {
							cluster.AddSeededProxy(oriProxy.Type, oriProxy.Host, oriProxy.Port, oriProxy.User, oriProxy.Pass)
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
