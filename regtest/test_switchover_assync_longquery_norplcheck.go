// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/dbhelper"
)

func testSwitchoverLongQueryNoRplCheckNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(8)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	SaveMasterURL := cluster.GetMaster().URL
	go dbhelper.InjectLongTrx(cluster.GetMaster().Conn, 20)

	cluster.LogPrintf("INFO :  Master is %s", cluster.GetMaster().URL)
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.GetMaster().URL)

	time.Sleep(20 * time.Second)
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	cluster.CloseTestCluster(conf, test)
	return true
}
