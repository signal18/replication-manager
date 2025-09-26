// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
//          Stephane Varoqui  <ahmad@signal18.io>

// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"github.com/signal18/replication-manager/cluster"
)

func (regtest *RegTest) TestRunSysbenchTPCPerMinuteIncreaseThreads(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.RunSysbenchTPCPerMinuteIncreaseThreads()
	return true
}
