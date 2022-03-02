// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import "github.com/signal18/replication-manager/config"

func (repman *ReplicationManager) AddCluster(clusterName string, clusterHead string) error {
	var myconf = make(map[string]config.Config)

	myconf[clusterName] = repman.Conf
	repman.Lock()
	repman.ClusterList = append(repman.ClusterList, clusterName)
	//repman.ClusterList = repman.ClusterList
	repman.Confs[clusterName] = repman.Conf
	repman.Unlock()
	/*file, err := os.OpenFile(repman.Conf.ClusterConfigPath+"/"+clusterName+".toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			log.Errorf("Read file permission denied: %s", repman.Conf.ClusterConfigPath+"/"+clusterName+".toml")
		}
		return err
	}
	defer file.Close()
	err = toml.NewEncoder(file).Encode(myconf)
	if err != nil {
		return err
	}*/

	cluster, _ := repman.StartCluster(clusterName)
	cluster.SetClusterHead(clusterHead)
	cluster.SetClusterList(repman.Clusters)
	cluster.Save()

	return nil

}
