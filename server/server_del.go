// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"os"

	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) DeleteCluster(clusterName string) error {
	log.Warnf("Delete Cluster %s \n", clusterName)
	cl := repman.getClusterByName(clusterName)
	if cl != nil {
		//if cl.IsProvision {
		err := cl.Unprovision()
		if err != nil {
			log.Errorf("Unprovision cluster fail: %s", err)
		}
		err = cl.WaitClusterStop()
		if err != nil {
			log.Errorf("Wait for stop cluster fail: %s", err)
		}
	}

	i := 0
	var newClusterList []string
	if i < len(repman.ClusterList) {
		if repman.ClusterList[i] != clusterName {
			newClusterList = append(newClusterList, repman.ClusterList[i])
		}
		i++

	}

	repman.ClusterList = newClusterList
	_, ok := repman.Clusters[clusterName]
	if ok {
		delete(repman.Clusters, clusterName)
	}

	err := os.RemoveAll(repman.Conf.WorkingDir + "/" + clusterName)
	if err != nil {
		log.Errorf("Delete cluster working directory fail: %s", err)

	}
	if repman.currentCluster == cl {
		repman.currentCluster = nil
	}
	log.Warnf("Cluster %s is delete\n", clusterName)
	return nil

}
