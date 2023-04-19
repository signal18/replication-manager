// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) AddCluster(clusterName string, clusterHead string) error {
	fmt.Printf("ADD CLUSTER\n")
	var myconf = make(map[string]config.Config)

	myconf[clusterName] = repman.Conf
	repman.Lock()
	repman.ClusterList = append(repman.ClusterList, clusterName)
	//repman.ClusterList = repman.ClusterList
	repman.Confs[clusterName] = repman.Conf

	repman.VersionConfs[clusterName] = new(config.ConfVersion)
	repman.VersionConfs[clusterName].ConfInit = repman.Conf

	repman.ImmuableFlagMaps[clusterName] = repman.ImmuableFlagMaps["default"]
	repman.DynamicFlagMaps[clusterName] = repman.DynamicFlagMaps["default"]

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
	//confs[clusterName] = repman.GetClusterConfig(fistRead, repman.ImmuableFlagMaps["default"], repman.DynamicFlagMaps["default"], clusterName, conf)

	cluster, _ := repman.StartCluster(clusterName)
	fmt.Printf("ADD CLUSTER CONF :\n")
	cluster.Conf.PrintConf()

	cluster.SetClusterHead(clusterHead)
	//cluster.SetClusterHead(clusterName)
	cluster.SetClusterList(repman.Clusters)
	cluster.Save()

	return nil

}
