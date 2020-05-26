// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) CancelRollingRestart() error {
	cluster.LogPrintf(LvlInfo, "API receive cancel rolling restart")
	for _, pr := range cluster.Proxies {
		pr.DelRestartCookie()
	}
	for _, db := range cluster.Servers {
		db.DelRestartCookie()
	}
	return nil
}

func (cluster *Cluster) CancelRollingReprov() error {
	cluster.LogPrintf(LvlInfo, "API receive cancel rolling re-provision")
	for _, pr := range cluster.Proxies {
		pr.DelReprovisionCookie()
	}
	for _, db := range cluster.Servers {
		db.DelReprovisionCookie()
	}
	return nil
}
