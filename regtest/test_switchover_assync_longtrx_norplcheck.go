// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (regtest *RegTest) TestSwitchoverLongTransactionNoRplCheckNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(8)
	cluster.SetRplChecks(false)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	db, err := cluster.GetClusterProxyConn()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return false
	}
	go dbhelper.InjectLongTrx(db, 20)
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return false
	}
	cluster.LogPrintf("TEST", "Waiting in some trx 12s more wait-trx  default %d ", cluster.GetWaitTrx())
	time.Sleep(14 * time.Second)
	cluster.LogPrintf("TEST :  Master is %s", cluster.GetMaster().URL)
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST : New Master  %s ", cluster.GetMaster().URL)
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Saved  master %s <> from master  %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}
	return true
}
