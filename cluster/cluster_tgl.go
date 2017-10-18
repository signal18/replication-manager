// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) SwitchServerMaintenance(serverid uint) {
	server := cluster.GetServerFromId(serverid)
	server.IsMaintenance = !server.IsMaintenance
	server.SwitchMaintenance()
	cluster.SetProxyServerMaintenance(server.ServerID)
}

func (cluster *Cluster) ToggleInteractive() {
	if cluster.conf.Interactive == true {
		cluster.conf.Interactive = false
		cluster.LogPrintf("INFO", "Failover monitor switched to automatic mode")
	} else {
		cluster.conf.Interactive = true
		cluster.LogPrintf("INFO", "Failover monitor switched to manual mode")
	}
}

func (cluster *Cluster) SwitchPseudoGTID() {
	cluster.conf.AutorejoinSlavePositionalHearbeat = !cluster.conf.AutorejoinSlavePositionalHearbeat
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
