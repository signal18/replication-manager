// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
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
	router.Handle("/api/clusters/{clusterName}/volume-snapshot-plugins", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxVolumeSnapshotPlugins)),
	)).Methods("GET")
	router.Handle("/api/clusters/{clusterName}/volume-snapshot-plugin-actions/set/{pluginName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxVolumeSnapshotPluginSet)),
	)).Methods("POST")
}

// volumeSnapshotServer resolves the cluster and the server of a request and checks its ACL;
// nil when it answered the request with the error.
func (repman *ReplicationManager) volumeSnapshotServer(w http.ResponseWriter, r *http.Request) (*cluster.Cluster, *cluster.ServerMonitor) {
	// no CORS wildcard on these routes: a snapback is destructive
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

// volumeSnapshotCluster resolves the cluster of a request and checks its ACL; nil when it
// answered the request with the error.
func (repman *ReplicationManager) volumeSnapshotCluster(w http.ResponseWriter, r *http.Request) *cluster.Cluster {
	mycluster := repman.getClusterByName(mux.Vars(r)["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return nil
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return nil
	}
	return mycluster
}

// handlerMuxVolumeSnapshotPlugins lists the snapshot plugins of a cluster with their settings.
// @Summary List the volume snapshot plugins and their settings
// @Description The snapshot plugins known (built-in, the client's script, the external plugins found beside the other plugins), each with the settings its manifest declares, the values the cluster holds for it (a secret never: only its name in secretsSet), whether the plan allows it and whether it says it has been tested on the storage it drives.
// @Description Required grant: cluster-show-backups, db-backup or global-settings
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.VolumeSnapshotPluginList "The plugins"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/volume-snapshot-plugins [get]
func (repman *ReplicationManager) handlerMuxVolumeSnapshotPlugins(w http.ResponseWriter, r *http.Request) {
	mycluster := repman.volumeSnapshotCluster(w, r)
	if mycluster == nil {
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusOK, mycluster.VolumeSnapshotPlugins())
}

// handlerMuxVolumeSnapshotPluginSet sets the settings of an external snapshot plugin.
// @Summary Set the settings of an external snapshot plugin
// @Description Stores the settings its manifest declares, each checked against its type; a secret is encrypted with the replication-manager key before it is saved (refused without a key) and never returned; an empty secret keeps the one held; unset removes settings. The plugin gets them in its environment at its next run: REPMAN_SNAPSHOT_SETTING_<NAME>, and the secret decrypted as REPMAN_SNAPSHOT_SECRET_<NAME>.
// @Description Required grant: global-settings (the settings direct a program on the replication-manager host)
// @Tags DatabaseBackup
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param pluginName path string true "Plugin Name"
// @Param body body cluster.VolumeSnapshotPluginSettingsSpec true "the settings"
// @Success 200 {object} config.SnapshotPluginEntry "The settings held, without the secrets"
// @Failure 400 {string} string "Invalid setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster Not Found"
// @Failure 409 {string} string "The settings are set by a config file"
// @Failure 500 {string} string "No encryption key: a secret is never stored in clear"
// @Router /api/clusters/{clusterName}/volume-snapshot-plugin-actions/set/{pluginName} [post]
func (repman *ReplicationManager) handlerMuxVolumeSnapshotPluginSet(w http.ResponseWriter, r *http.Request) {
	mycluster := repman.volumeSnapshotCluster(w, r)
	if mycluster == nil {
		return
	}
	var spec cluster.VolumeSnapshotPluginSettingsSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&spec); err != nil {
		http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	plugin := mux.Vars(r)["pluginName"]
	if err := mycluster.SetVolumeSnapshotPluginSettings(plugin, spec); err != nil {
		if errors.Is(err, cluster.ErrSnapshotPluginSettingsPinned) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, config.ErrSnapshotSecretNoKey) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeVolumeSnapshotJSON(w, http.StatusOK, mycluster.Conf.SnapshotPluginEntryOf(plugin))
}
