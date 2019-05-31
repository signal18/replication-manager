// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

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
}
func (cluster *Cluster) SetDBCores(value string) {
	cluster.Conf.ProvCores = value
}
func (cluster *Cluster) SetDBMemorySize(value string) {
	cluster.Conf.ProvMem = value
}
func (cluster *Cluster) SetDBDiskIOPS(value string) {
	cluster.Conf.ProvIops = value
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

func (cluster *Cluster) SetMasterReadOnly() {
	if cluster.GetMaster() != nil {
		err := cluster.GetMaster().SetReadOnly()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Could not set  master as read-only, %s", err)
		}
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

	err := cluster.loadDBCertificate()
	if err != nil {
		cluster.haveDBTLSCert = false
		cluster.LogPrintf(LvlInfo, "No database TLS certificates")
	} else {
		cluster.haveDBTLSCert = true
		cluster.LogPrintf(LvlInfo, "Database TLS certificates correctly loaded")
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
	cluster.DBTags = cluster.GetDatabaseTags()
	cluster.ProxyTags = cluster.GetProxyTags()
}

func (cluster *Cluster) SetClusterCredential(credential string) {
	cluster.Conf.User = credential
	cluster.SetClusterVariablesFromConfig()
	for _, srv := range cluster.Servers {
		srv.SetCredential(srv.URL, cluster.dbUser, cluster.dbPass)
	}
	cluster.SetUnDiscovered()
}
func (cluster *Cluster) DropDBTag(dtag string) {
	var newtags []string
	for _, tag := range cluster.DBTags {
		if dtag != tag {
			newtags = append(newtags, tag)
		}
	}
	cluster.Conf.ProvTags = strings.Join(newtags, ",")
	cluster.SetClusterVariablesFromConfig()
}

func (cluster *Cluster) AddDBTag(tag string) {
	cluster.DBTags = append(cluster.DBTags, tag)
	cluster.Conf.ProvTags = strings.Join(cluster.DBTags, ",")
	cluster.SetClusterVariablesFromConfig()
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
	cluster.sme.AddState(key, s)
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
	resp, err := client.Do(req)
	if err != nil {
		cl.IsFailedArbitrator = true
		return err
	}
	defer resp.Body.Close()
	ioutil.ReadAll(resp.Body)
	cl.IsFailedArbitrator = false
	return nil

}
