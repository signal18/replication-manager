// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// TestSwitchoverLongTransactionWaitNoRplCheckNoSemiSync is the twin of
// TestSwitchoverLongTransactionNoRplCheckNoSemiSync: same 20 s transaction on the master,
// same switchover asked at 14 s, but switchover-wait-trx raised to 30 s. The long-write guard
// must wait for the transaction to commit (about 6 s), never kill it, and the switchover must
// then proceed: the master changes.
func (regtest *RegTest) TestSwitchoverLongTransactionWaitNoRplCheckNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(8)
	cluster.SetRplChecks(false)
	saveWaitTrx := cluster.Conf.SwitchWaitTrx
	cluster.SetSwitchoverWaitTrx("30")
	defer func() { cluster.Conf.SwitchWaitTrx = saveWaitTrx }()
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	db, err := cluster.GetClusterProxyConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		return false
	}
	go dbhelper.InjectLongTrx(db, 20)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Waiting in some trx 14s, past switchover-wait-write-query %d, switchover-wait-trx %d: switchover must wait then proceed", cluster.Conf.SwitchWaitWrite, cluster.GetWaitTrx())
	time.Sleep(14 * time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SwitchoverWaitTest()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master  %s ", cluster.GetMaster().URL)
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.GetMaster() == nil || cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover must proceed once the long transaction completed within switchover-wait-trx: master still %s", SaveMasterURL)
		return false
	}
	return true
}
