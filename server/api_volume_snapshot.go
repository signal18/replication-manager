// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
)

// Volume snapshots of a database server's data volume (#2443,
// doc/implementation/cluster/VOLUME_SNAPSHOTS.md). The ACL rules are in
// cluster/cluster_acl_rules.go (databaseACLRules).
func (repman *ReplicationManager) apiVolumeSnapshotProtectedHandler(router *mux.Router) {
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/volume-snapshots", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVolumeSnapshots)),
	)).Methods("GET")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapshot-take", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVolumeSnapshotTake)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapshot-delete/{snapshotName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVolumeSnapshotDelete)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapback/{snapshotName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVolumeSnapback)),
	)).Methods("POST")
}

// volumeSnapshotServer resolves the cluster and the server of a request and checks its ACL;
// nil when it answered the request with the error.
func (repman *ReplicationManager) volumeSnapshotServer(w http.ResponseWriter, r *http.Request) (*cluster.Cluster, *cluster.ServerMonitor) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return nil, nil
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return nil, nil
	}
	node := mycluster.GetServerFromName(vars["serverName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusNotFound)
		return nil, nil
	}
	return mycluster, node
}

func writeVolumeSnapshotJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handlerMuxServerVolumeSnapshots lists the snapshots of a server's data volume.
// @Summary List the volume snapshots of a server
// @Description Runs the list action of the cluster's snapshot plugin (backup-snapshot-plugin, or backup-snapshot-script when set) and returns the snapshots of the volume holding the server's files, the commands run, the plugins known and the server's last snapshot action. A plugin that can not run is reported in error, with an empty list. Snapshots named repman-* are the ones replication-manager took (managed): only these are pruned and deletable.
// @Description Required grant: cluster-show-backups or db-backup
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} cluster.VolumeSnapshotList "The snapshots"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/volume-snapshots [get]
func (repman *ReplicationManager) handlerMuxServerVolumeSnapshots(w http.ResponseWriter, r *http.Request) {
	mycluster, node := repman.volumeSnapshotServer(w, r)
	if node == nil {
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusOK, mycluster.ListVolumeSnapshots(node))
}

// handlerMuxServerVolumeSnapshotTake takes a snapshot of a server's data volume.
// @Summary Take a volume snapshot of a server
// @Description Starts a snapshot named repman-<UTC time> of the volume holding the server's files (a recursive ZFS snapshot, an LVM snapshot, or the client's script), then deletes the managed snapshots beyond backup-snapshot-keep. The run goes on in the background: its outcome is the server's last action (volume-snapshots) and the states CINF0024 / WARN0330.
// @Description Required grant: db-backup
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 202 {object} map[string]string "The snapshot started: its name"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found" or "Server Not Found"
// @Failure 409 {string} string "Refused: snapshots off, plugin refused or unreachable, an action already running on the server"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapshot-take [post]
func (repman *ReplicationManager) handlerMuxServerVolumeSnapshotTake(w http.ResponseWriter, r *http.Request) {
	mycluster, node := repman.volumeSnapshotServer(w, r)
	if node == nil {
		return
	}
	name, err := mycluster.TakeVolumeSnapshot(node)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusAccepted, map[string]string{"snapshot": name})
}

// handlerMuxServerVolumeSnapshotDelete deletes a volume snapshot replication-manager took.
// @Summary Delete a volume snapshot of a server
// @Description Deletes the snapshot (recursively on ZFS). Only a snapshot replication-manager took (repman-*) can be deleted: a snapshot of another origin is refused. Runs in the background, like the take.
// @Description Required grant: db-backup
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param snapshotName path string true "Snapshot Name"
// @Success 202 {object} map[string]string "The delete started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found" or "Server Not Found"
// @Failure 409 {string} string "Refused: not a repman-* snapshot, invalid name, snapshots off, plugin refused, an action already running"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapshot-delete/{snapshotName} [post]
func (repman *ReplicationManager) handlerMuxServerVolumeSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	mycluster, node := repman.volumeSnapshotServer(w, r)
	if node == nil {
		return
	}
	name := mux.Vars(r)["snapshotName"]
	if err := mycluster.DeleteVolumeSnapshot(node, name); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusAccepted, map[string]string{"snapshot": name})
}

// handlerMuxServerVolumeSnapback puts a server's data volume back to a snapshot.
// @Summary Snapback a server to a volume snapshot
// @Description Destructive, an operator's action only: the server goes into maintenance, is stopped through its orchestrator, its volume is rolled back to the snapshot (on ZFS the snapshots taken after it are destroyed), the server is started and stays in maintenance (WARN0331) until an operator takes it out. Refused on the master and during a failover. A rollback that fails after it changed the volume leaves the server stopped and in maintenance (ERR00123). The replication links of the server are not changed. Runs in the background.
// @Description Required grants: db-restore and db-maintenance
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param snapshotName path string true "Snapshot Name"
// @Success 202 {object} map[string]string "The snapback started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found" or "Server Not Found"
// @Failure 409 {string} string "Refused: the master, a failover running, snapshot not found, snapshots off, plugin refused, an action already running"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/volume-snapback/{snapshotName} [post]
func (repman *ReplicationManager) handlerMuxServerVolumeSnapback(w http.ResponseWriter, r *http.Request) {
	mycluster, node := repman.volumeSnapshotServer(w, r)
	if node == nil {
		return
	}
	name := mux.Vars(r)["snapshotName"]
	if err := mycluster.SnapbackVolume(node, name); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusAccepted, map[string]string{"snapshot": name})
}
