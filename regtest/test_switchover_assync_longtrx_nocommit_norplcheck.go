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

func (regtest *RegTest) TestSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
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
		cluster.LogPrintf(LvlErr, "Can't take proxy conn %s ", err)
		return false
	}
	dbhelper.InjectTrxWithoutCommit(db)
	time.Sleep(12 * time.Second)
	cluster.LogPrintf("TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)

		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}
	return true
}
