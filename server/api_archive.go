// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The archive list API (#2091, doc/implementation/cluster/BACKUP_ARCHIVE_PLUGINS.md).
// Secrets go in through add and edit and never come back: the list gives the names of the
// secrets set.

func writeArchiveJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// handlerMuxArchiveList lists the cluster's archives.
// @Summary List the archives
// @Description The cluster's archives, the attached one, and each archive plugin's typed config (settings, secrets, capabilities). Secrets are never returned, only the names of those set.
// @Tags ClusterArchive
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.ArchiveListResult
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/archive-list [get]
func (repman *ReplicationManager) handlerMuxArchiveList(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		writeArchiveJSON(w, mycluster.ArchiveList())
	})
}

func decodeArchiveSpec(w http.ResponseWriter, r *http.Request) (config.ArchiveSpec, bool) {
	var spec config.ArchiveSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&spec); err != nil {
		http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
		return spec, false
	}
	return spec, true
}

func writeArchivePut(w http.ResponseWriter, name, probe string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeArchiveJSON(w, map[string]string{"name": name, "repository": probe})
}

// handlerMuxArchiveAdd adds an archive.
// @Summary Add an archive
// @Description Adds an archive from its plugin and connection infos (settings and secrets by the names of the plugin's config). The repository is probed first: an existing one is opened (a wrong password is refused), a missing one is initialised; an archive that can't be opened is not added. Without a name, repman names it archive-<type>-<n>.
// @Tags ClusterArchive
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body config.ArchiveSpec true "name (optional), plugin, settings"
// @Success 200 {object} map[string]string "name and repository: opened or initialised"
// @Failure 400 {string} string "Invalid archive, or repository refused"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/add [post]
func (repman *ReplicationManager) handlerMuxArchiveAdd(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		spec, ok := decodeArchiveSpec(w, r)
		if !ok {
			return
		}
		name, probe, err := mycluster.PutArchive(spec, true, false)
		writeArchivePut(w, name, probe, err)
	})
}

// handlerMuxArchiveEdit changes an archive.
// @Summary Edit an archive
// @Description Changes an archive's settings and secrets (an empty secret keeps the secret, unset clears names), then probes its repository again. An archive that can't be opened keeps its former settings.
// @Tags ClusterArchive
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Param body body config.ArchiveSpec true "settings, unset"
// @Success 200 {object} map[string]string "name and repository: opened or initialised"
// @Failure 400 {string} string "Invalid archive, or repository refused"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/edit/{archiveName} [post]
func (repman *ReplicationManager) handlerMuxArchiveEdit(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		spec, ok := decodeArchiveSpec(w, r)
		if !ok {
			return
		}
		spec.Name = vars["archiveName"]
		name, probe, err := mycluster.PutArchive(spec, false, false)
		writeArchivePut(w, name, probe, err)
	})
}

// handlerMuxArchiveRename renames an archive.
// @Summary Rename an archive
// @Description Renames an archive (lowercase letters, digits, - and _). The attached archive stays attached under its new name.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Param newName path string true "New Archive Name"
// @Success 200 {string} string "Archive renamed"
// @Failure 400 {string} string "Invalid or taken name"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/rename/{archiveName}/{newName} [post]
func (repman *ReplicationManager) handlerMuxArchiveRename(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.RenameArchive(vars["archiveName"], vars["newName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("Archive renamed"))
	})
}

// handlerMuxArchiveAttach attaches an archive, or detaches the attached one.
// @Summary Attach or detach an archive
// @Description Attaches the archive (the backups go to it, the archive attached before is detached and stays in the list with its content), or with detach leaves the cluster with no archive.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Success 200 {string} string "Archive attached"
// @Failure 400 {string} string "Unknown archive"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/attach/{archiveName} [post]
// @Router /api/clusters/{clusterName}/archive-actions/detach [post]
func (repman *ReplicationManager) handlerMuxArchiveAttach(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.SetBackupArchiveName(vars["archiveName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mycluster.Save()
		if vars["archiveName"] == "" {
			w.Write([]byte("Archive detached"))
			return
		}
		w.Write([]byte("Archive attached"))
	})
}

// handlerMuxArchiveRemove removes an archive and deletes its content.
// @Summary Remove an archive
// @Description Deletes the archive's content, then takes it out of the list, so nothing is left there to invoice. No check on the backups it holds: a backup whose only copy was there is lost. The attached archive is detached first. Runs in the background: the list shows the archive as removing, then it is gone, or the error stays on it. Refused on a whole bucket (an S3 archive with no prefix).
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Success 202 {string} string "Archive removal started"
// @Failure 400 {string} string "Unknown archive, or content cannot be deleted"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/remove/{archiveName} [post]
func (repman *ReplicationManager) handlerMuxArchiveRemove(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.RemoveArchive(vars["archiveName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Archive removal started"))
	})
}

// handlerMuxArchiveSnapshots lists an archive plugin's snapshots.
// @Summary List an archive's snapshots
// @Description The snapshots of an archive plugin's archive (plakar ...), newest first: id, time, host, tags, paths, size. The plugin is asked at each call. A restic archive's snapshots are listed by /restic/snapshots.
// @Tags ClusterArchive
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Success 200 {array} wire.Snapshot
// @Failure 400 {string} string "Unknown archive, a restic archive, or its snapshots cannot be listed"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-snapshots/{archiveName} [get]
func (repman *ReplicationManager) handlerMuxArchiveSnapshots(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		snaps, err := mycluster.ArchiveSnapshots(vars["archiveName"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeArchiveJSON(w, snaps)
	})
}

// handlerMuxArchiveForget deletes one snapshot of an archive plugin's archive.
// @Summary Delete an archive's snapshot
// @Description Deletes one snapshot of an archive plugin's archive (plakar ...), at once; the backups that recorded it lose it, and one whose only copy it was leaves the catalogue. A restic archive's snapshot is purged by /restic/purge/{snapshotID}.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Param snapshotID path string true "Snapshot ID"
// @Success 200 {string} string "Snapshot deleted"
// @Failure 400 {string} string "Unknown archive, a restic archive, or the snapshot cannot be deleted"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/forget/{archiveName}/{snapshotID} [post]
func (repman *ReplicationManager) handlerMuxArchiveForget(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.ForgetArchiveSnapshot(vars["archiveName"], vars["snapshotID"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("Snapshot deleted"))
	})
}

// handlerMuxArchiveSnapback puts a server's backup directory back as it was in a snapshot.
// @Summary Snapback a backup directory
// @Description Puts the backup directory a snapshot of the attached archive holds back as it was in it, in the background: the directory as it is now is snapshotted first (its latest snapshot), then emptied, then the snapshot is restored into it, and the server's backups are read again. Never twice its size on disk. Until a snapback to its latest snapshot or a release, the server's backups and archive pushes are locked (WARN0255). Refused while a backup is written, the backups folder moves, a snapback of the server runs, or without room for the snapshot.
// @Tags ClusterArchive
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param snapshotID path string true "Snapshot ID (or its first 8 characters)"
// @Success 202 {object} map[string]string
// @Failure 400 {string} string "Refused: no archive attached, unknown snapshot, not a server's backup directory, a backup running, no room"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/snapback/{snapshotID} [post]
func (repman *ReplicationManager) handlerMuxArchiveSnapback(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		server, err := mycluster.Snapback(vars["snapshotID"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeArchiveJSON(w, map[string]string{"server": server, "snapshot": vars["snapshotID"], "message": "Snapback started"})
	})
}

// handlerMuxArchiveSnapbackRelease keeps a server's backup directory as a snapback left it.
// @Summary Release a snapback
// @Description Keeps the server's backup directory as the snapback left it and unlocks its backups (WARN0255 resolved): its next backup's snapshot is its new latest one. A running snapback is not released.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Snapback released"
// @Failure 400 {string} string "No snapback, or one running"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/snapback-release/{serverName} [post]
func (repman *ReplicationManager) handlerMuxArchiveSnapbackRelease(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.ReleaseSnapback(vars["serverName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("Snapback released"))
	})
}

// handlerMuxArchiveSize reads an archive's size.
// @Summary Read an archive's size
// @Description Reads the bytes the archive's content takes, in the background: the list shows the archive as sizing, then its size, or the error. A detached archive keeps the size it had when it was detached; this reads it again.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Success 202 {string} string "Archive size read started"
// @Failure 400 {string} string "Unknown archive, or its size cannot be read"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/size/{archiveName} [post]
func (repman *ReplicationManager) handlerMuxArchiveSize(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.SizeArchive(vars["archiveName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Archive size read started"))
	})
}

// handlerMuxArchiveRebuildCatalogue rebuilds the backup catalogue from an archive, or from all.
// @Summary Rebuild the backup catalogue from the archives
// @Description Reads, in the background, the backup records in each snapshot of the archive (every archive replication-manager can read when no archive is named) that no backup of the catalogue records: a backup the catalogue lacks joins it, only in the archive; a backup it has gets the snapshot when it records none of that archive. A snapshot the catalogue records that the archive no longer lists, or a snapshot whose content its plugin cannot list (no contents operation), raises WARN0260; CINF0021 while it runs. It also runs at the first start with no catalogue and when a standby becomes active. One at a time.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string false "Archive Name (all when none)"
// @Success 202 {string} string "Backup catalogue rebuild started"
// @Failure 400 {string} string "Unknown or unreadable archive, or a rebuild runs"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/rebuild-catalogue/{archiveName} [post]
func (repman *ReplicationManager) handlerMuxArchiveRebuildCatalogue(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.RebuildBackupCatalogue(vars["archiveName"]); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Backup catalogue rebuild started"))
	})
}

// handlerMuxArchiveDropMissingSnapshots takes the snapshots gone from an archive out of the catalogue.
// @Summary Drop the snapshots gone from an archive from the backup catalogue
// @Description Takes out of the backup catalogue the snapshots the last rebuild from the archive found recorded but no longer in it (WARN0260), each one confirmed gone by a new listing of the archive: the backups recording it lose it, a backup whose only copy it was leaves the catalogue. A snapshot listed again is kept.
// @Tags ClusterArchive
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param archiveName path string true "Archive Name"
// @Success 200 {string} string "Snapshots dropped"
// @Failure 400 {string} string "Unknown or unreadable archive, no snapshot missing, or a rebuild runs"
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/{clusterName}/archive-actions/drop-missing-snapshots/{archiveName} [post]
func (repman *ReplicationManager) handlerMuxArchiveDropMissingSnapshots(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		n, err := mycluster.DropMissingCatalogueSnapshots(vars["archiveName"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte(fmt.Sprintf("%d snapshot(s) dropped from the backup catalogue", n)))
	})
}
