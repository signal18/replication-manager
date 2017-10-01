// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/state"
)

func (cluster *Cluster) GetMaster() *ServerMonitor {
	if cluster.master == nil {
		return cluster.vmaster
	} else {
		return cluster.master
	}
}

func (cluster *Cluster) GetTraffic() bool {
	return cluster.conf.TestInjectTraffic
}

func (cluster *Cluster) GetServers() serverList {
	return cluster.servers
}

func (cluster *Cluster) GetSlaves() serverList {
	return cluster.slaves
}

func (cluster *Cluster) GetProxies() proxyList {
	return cluster.proxies
}

func (cluster *Cluster) GetConf() config.Config {
	return cluster.conf
}

func (cluster *Cluster) GetWaitTrx() int64 {
	return cluster.conf.SwitchWaitTrx
}

func (cluster *Cluster) GetStateMachine() *state.StateMachine {
	return cluster.sme
}

func (cluster *Cluster) GetMasterFailCount() int {
	return cluster.master.FailCount
}

func (cluster *Cluster) GetFailoverCtr() int {
	return cluster.failoverCtr
}

func (cluster *Cluster) GetFailoverTs() int64 {
	return cluster.failoverTs
}

func (cluster *Cluster) GetRunStatus() string {
	return cluster.runStatus
}
func (cluster *Cluster) GetFailSync() bool {
	return cluster.conf.FailSync
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.conf.RplChecks
}

func (cluster *Cluster) GetMaxFail() int {
	return cluster.conf.MaxFail
}

func (cluster *Cluster) GetLogLevel() int {
	return cluster.conf.LogLevel
}
func (cluster *Cluster) GetSwitchSync() bool {
	return cluster.conf.SwitchSync
}

func (cluster *Cluster) GetRejoin() bool {
	return cluster.conf.Autorejoin
}

func (cluster *Cluster) GetRejoinDump() bool {
	return cluster.conf.AutorejoinMysqldump
}

func (cluster *Cluster) GetRejoinBackupBinlog() bool {
	return cluster.conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) GetRejoinSemisync() bool {
	return cluster.conf.AutorejoinSemisync
}

func (cluster *Cluster) GetRejoinFlashback() bool {
	return cluster.conf.AutorejoinFlashback
}

func (cluster *Cluster) GetName() string {
	return cluster.cfgGroup
}

func (cluster *Cluster) GetTestMode() bool {
	return cluster.conf.Test
}

func (cluster *Cluster) GetDbUser() string {
	return cluster.dbUser
}

func (cluster *Cluster) GetDbPass() string {
	return cluster.dbPass
}

func (cluster *Cluster) GetStatus() bool {
	return cluster.sme.IsFailable()
}
