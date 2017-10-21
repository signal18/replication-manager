// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) IsProvision() bool {
	for _, s := range cluster.servers {
		if s.State == stateFailed || s.State == stateSuspect /*&& misc.Contains(cluster.ignoreList, s.URL) == false*/ {
			return false
		}
	}
	return true

}

func (cluster *Cluster) IsInHostList(host string) bool {
	for _, v := range cluster.hostList {
		if v == host {
			return true
		}
	}
	return false
}

func (cluster *Cluster) IsMasterFailed() bool {
	if cluster.GetMaster().State == stateFailed {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsActive() bool {
	if cluster.runStatus == "A" {
		return true
	} else {
		return false
	}
}
