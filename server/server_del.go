// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"os"
	"strings"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) DeleteCluster(clusterName string) error {
	log.Warnf("Delete Cluster %s \n", clusterName)
	cl := repman.getClusterByName(clusterName)

	// Capture and normalize the gateway before removal.
	// recomputeConflictsForGateway compares against normalized peer values
	// (strings.ToLower + TrimSpace), so prevGateway must match that form.
	var prevGateway string
	if cl != nil {
		prevGateway = strings.ToLower(strings.TrimSpace(cl.Conf.Cloud18GatewayService))
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

	repman.Lock()
	var newClusterList []string
	for i := 0; i < len(repman.ClusterList); i++ {
		if repman.ClusterList[i] != clusterName {
			newClusterList = append(newClusterList, repman.ClusterList[i])
		}
	}
	repman.ClusterList = newClusterList
	delete(repman.Clusters, clusterName)
	repman.Unlock()

	repman.refreshAllPeers()
	// RecomputeGatewayConflicts early-returns when the cluster is already gone
	// from the map, so call the inner function directly with the captured gateway
	// to unblock any peers that were blocked by this cluster.
	repman.recomputeConflictsForGateway(prevGateway)

	err := os.RemoveAll(repman.Conf.WorkingDir + "/" + clusterName)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Delete cluster working directory fail: %s", err)
	}

	if repman.currentCluster == cl {
		repman.currentCluster = nil
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cluster %s is deleted\n", clusterName)
	return nil

}
