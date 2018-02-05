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
		if srv.URL == PrefMaster {
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

func (cluster *Cluster) SetActiveStatus(status string) {
	cluster.runStatus = status
	if cluster.runStatus == ConstMonitorActif {
		cluster.scheduler.Start()
	} else {
		cluster.scheduler.Stop()
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
