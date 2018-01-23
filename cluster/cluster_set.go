// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "github.com/signal18/replication-manager/dbhelper"
import "github.com/signal18/replication-manager/state"
import "github.com/signal18/replication-manager/opensvc"

func (cluster *Cluster) SetCertificate(svc opensvc.Collector) {
	var err error
	if cluster.conf.Enterprise == false {
		return
	}
	if cluster.conf.ProvSSLCa != "" {
		cluster.conf.ProvSSLCaUUID, err = svc.PostSafe(cluster.conf.ProvSSLCa)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't upload root CA to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload root Certificate to Safe %s", cluster.conf.ProvSSLCaUUID)
		}
		svc.PublishSafe(cluster.conf.ProvSSLCaUUID, "replication-manager")
	}
	if cluster.conf.ProvSSLCert != "" {
		cluster.conf.ProvSSLCertUUID, err = svc.PostSafe(cluster.conf.ProvSSLCert)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't upload Server TLS Certificate to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload Server TLS Certificate to Safe %s", cluster.conf.ProvSSLCertUUID)
		}
		svc.PublishSafe(cluster.conf.ProvSSLCertUUID, "replication-manager")
	}
	if cluster.conf.ProvSSLKey != "" {
		cluster.conf.ProvSSLKeyUUID, err = svc.PostSafe(cluster.conf.ProvSSLKey)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't upload Server TLS Private Key to Collector Safe %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Upload Server TLS Private Key to Safe %s", cluster.conf.ProvSSLKeyUUID)
		}
		svc.PublishSafe(cluster.conf.ProvSSLKeyUUID, "replication-manager")
	}
}

func (cluster *Cluster) SetCfgGroupDisplay(cfgGroup string) {
	cluster.cfgGroupDisplay = cfgGroup
}

func (cluster *Cluster) SetInteractive(check bool) {
	cluster.conf.Interactive = check
}

func (cluster *Cluster) SetTraffic(traffic bool) {
	//cluster.SetBenchMethod("table")
	//cluster.PrepareBench()
	cluster.conf.TestInjectTraffic = traffic
}

func (cluster *Cluster) SetBenchMethod(m string) {
	cluster.benchmarkType = m
}

func (cluster *Cluster) SetPrefMaster(PrefMaster string) {
	for _, srv := range cluster.servers {
		if srv.URL == PrefMaster {
			srv.SetPrefered(true)
		} else {
			srv.SetPrefered(false)
		}
	}
	cluster.conf.PrefMaster = PrefMaster
}

func (cluster *Cluster) SetFailoverCtr(failoverCtr int) {
	cluster.failoverCtr = failoverCtr
}

func (cluster *Cluster) SetFailoverTs(failoverTs int64) {
	cluster.failoverTs = failoverTs
}

func (cluster *Cluster) SetCheckFalsePositiveHeartbeat(CheckFalsePositiveHeartbeat bool) {
	cluster.conf.CheckFalsePositiveHeartbeat = CheckFalsePositiveHeartbeat
}
func (cluster *Cluster) SetFailRestartUnsafe(check bool) {
	cluster.conf.FailRestartUnsafe = check
}

func (cluster *Cluster) SetSlavesReadOnly(check bool) {
	for _, sl := range cluster.slaves {
		dbhelper.SetReadOnly(sl.Conn, check)
	}
}
func (cluster *Cluster) SetReadOnly(check bool) {
	cluster.conf.ReadOnly = check
}

func (cluster *Cluster) SetRplChecks(check bool) {
	cluster.conf.RplChecks = check
}

func (cluster *Cluster) SetRplMaxDelay(delay int64) {
	cluster.conf.FailMaxDelay = delay
}

func (cluster *Cluster) SetCleanAll(check bool) {
	cluster.CleanAll = check
}

func (cluster *Cluster) SetFailLimit(limit int) {
	cluster.conf.FailLimit = limit
}

func (cluster *Cluster) SetFailTime(time int64) {
	cluster.conf.FailTime = time
}

func (cluster *Cluster) SetMasterStateFailed() {
	cluster.master.State = stateFailed
}

func (cluster *Cluster) SetFailSync(check bool) {
	cluster.conf.FailSync = check
}

func (cluster *Cluster) SetRejoinDump(check bool) {
	cluster.conf.AutorejoinMysqldump = check
}

func (cluster *Cluster) SetRejoinBackupBinlog(check bool) {
	cluster.conf.AutorejoinBackupBinlog = check
}

func (cluster *Cluster) SetRejoinSemisync(check bool) {
	cluster.conf.AutorejoinSemisync = check
}

func (cluster *Cluster) SetRejoinFlashback(check bool) {
	cluster.conf.AutorejoinFlashback = check
}

func (cluster *Cluster) SetForceSlaveNoGtid(forceslavenogtid bool) {
	cluster.conf.ForceSlaveNoGtid = forceslavenogtid
}

// topology setter
func (cluster *Cluster) SetMultiTierSlave(multitierslave bool) {
	cluster.conf.MultiTierSlave = multitierslave
}
func (cluster *Cluster) SetMultiMasterRing(multimasterring bool) {
	cluster.conf.MultiMasterRing = multimasterring
}
func (cluster *Cluster) SetMultiMaster(multimaster bool) {
	cluster.conf.MultiMaster = multimaster
}
func (cluster *Cluster) SetBinlogServer(binlogserver bool) {
	cluster.conf.MxsBinlogOn = binlogserver
}
func (cluster *Cluster) SetMultiMasterWsrep(wsrep bool) {
	cluster.conf.MultiMasterWsrep = wsrep
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
	cluster.conf.SwitchSync = check
}

func (cluster *Cluster) SetLogLevel(level int) {
	cluster.conf.LogLevel = level
}

func (cluster *Cluster) SetRejoin(check bool) {
	cluster.conf.Autorejoin = check
}

func (cluster *Cluster) SetTestMode(check bool) {
	cluster.conf.Test = check
}

func (cluster *Cluster) SetTestStopCluster(check bool) {
	cluster.testStopCluster = check
}

func (cluster *Cluster) SetActiveStatus(status string) {
	cluster.runStatus = status
}

func (cluster *Cluster) SetTestStartCluster(check bool) {
	cluster.testStartCluster = check
}

func (cluster *Cluster) SetLogStdout() {
	cluster.conf.Daemon = true
}

func (cluster *Cluster) SetClusterList(clusters map[string]*Cluster) {
	cluster.clusterList = clusters
}

func (cluster *Cluster) SetState(key string, s state.State) {
	cluster.sme.AddState(key, s)
}
