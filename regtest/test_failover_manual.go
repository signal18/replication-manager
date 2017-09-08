// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"

	"github.com/tanji/replication-manager/cluster"
)

func testFailoverManual(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(true)
	cluster.SetRplChecks(true)
	SaveMaster := cluster.GetMaster()
	SaveMasterURL := SaveMaster.URL
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(SaveMaster)
	wg.Wait()
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf("TEST", " Old master %s !=  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
