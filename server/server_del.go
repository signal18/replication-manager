// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) DeleteCluster(clusterName string) error {

	fmt.Printf("COUCOU\n")
	cl := repman.getClusterByName(clusterName)
	//if cl.IsProvision {
	err := cl.Unprovision()
	if err != nil {
		log.Errorf("Fail to unprovision cluster : %s", err)
	}
	err = cl.WaitClusterStop()
	if err != nil {
		log.Errorf("Fail to wait for stop cluster : %s", err)
	}
	//}
	cl.Stop()
	i := 0
	var newClusterList []string
	if i < len(repman.ClusterList) {
		if repman.ClusterList[i] != clusterName {
			newClusterList = append(newClusterList, repman.ClusterList[i])
		}

	}

	repman.ClusterList = newClusterList
	delete(repman.Clusters, clusterName)
	err = os.RemoveAll(cl.WorkingDir)
	if err != nil {
		log.Errorf("Fail to delete cluster working directory : %s", err)
	}
	if repman.currentCluster == cl {
		repman.currentCluster = nil
	}
	return nil

}
