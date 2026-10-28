// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The credential store entries API (#2227, doc/implementation/config/CREDENTIAL_STORES.md):
// the cluster's vault entries, as the archive list API. Secrets go in through add and edit
// and never come back: the list gives the names of the secrets set (secretsSet).

func decodeSecretKvSpec(w http.ResponseWriter, r *http.Request) (config.SecretKvSpec, bool) {
	var spec config.SecretKvSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&spec); err != nil {
		http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
		return spec, false
	}
	return spec, true
}

func writeSecretKvError(w http.ResponseWriter, err error) {
	if errors.Is(err, cluster.ErrSecretKvsPinned) || errors.Is(err, config.ErrSecretKvOfferManaged) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handlerMuxSecretKvList lists the cluster's vault entries.
// @Summary List the credential store entries
// @Description The cluster's vault entries (secrets never returned, only the names of those set; offer: the Cloud18 marketplace offer an entry comes from, name, title and partner, null for the user's own), each one's state (last write, behind: WARN0269), the queue's drops (WARN0270), the plugins' manifests, the copy settings, and the database credentials' starting value source, current source, version count and refusal.
// @Tags ClusterCredentials
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.SecretKvListResult
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/secret-kv-list [get]
func (repman *ReplicationManager) handlerMuxSecretKvList(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		writeArchiveJSON(w, mycluster.SecretKvList())
	})
}

// handlerMuxSecretKvAdd adds a vault entry.
// @Summary Add a credential store entry
// @Description Adds a vault entry (plugin secret_kv-vault) from the settings of its manifest: address, mount, path-prefix, mode, auth, role-id, secret-id (written only). An enabled entry gets every version of every secret of the cluster, a full pass first.
// @Tags ClusterCredentials
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body config.SecretKvSpec true "name, settings, enabled"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Invalid entry"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file"
// @Router /api/clusters/{clusterName}/secret-kv-actions/add [post]
func (repman *ReplicationManager) handlerMuxSecretKvAdd(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		spec, ok := decodeSecretKvSpec(w, r)
		if !ok {
			return
		}
		name, err := mycluster.PutSecretKv(spec, true)
		if err != nil {
			writeSecretKvError(w, err)
			return
		}
		writeArchiveJSON(w, map[string]string{"name": name})
	})
}

// handlerMuxSecretKvEdit changes a vault entry.
// @Summary Edit a credential store entry
// @Description Changes a vault entry's settings (an empty secret keeps the secret, unset clears names).
// @Tags ClusterCredentials
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param entryName path string true "Entry Name"
// @Param body body config.SecretKvSpec true "settings, unset, enabled"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Invalid entry"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file, or the entry comes from a Cloud18 marketplace offer (its settings are the back-office's)"
// @Router /api/clusters/{clusterName}/secret-kv-actions/edit/{entryName} [post]
func (repman *ReplicationManager) handlerMuxSecretKvEdit(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		spec, ok := decodeSecretKvSpec(w, r)
		if !ok {
			return
		}
		spec.Name = vars["entryName"]
		name, err := mycluster.PutSecretKv(spec, false)
		if err != nil {
			writeSecretKvError(w, err)
			return
		}
		writeArchiveJSON(w, map[string]string{"name": name})
	})
}

// handlerMuxSecretKvEnable turns a vault entry on.
// @Summary Turn a credential store entry on
// @Description The entry gets every version of every secret of the cluster, a full pass first. The cluster's sponsor may do it.
// @Tags ClusterCredentials
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param entryName path string true "Entry Name"
// @Success 200 {string} string "Credential store on"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file"
// @Router /api/clusters/{clusterName}/secret-kv-actions/enable/{entryName} [post]
func (repman *ReplicationManager) handlerMuxSecretKvEnable(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.SetSecretKvEnabled(vars["entryName"], true); err != nil {
			writeSecretKvError(w, err)
			return
		}
		w.Write([]byte("Credential store on"))
	})
}

// handlerMuxSecretKvDisable turns a vault entry off.
// @Summary Turn a credential store entry off
// @Description The entry gets no more versions; what it holds stays. The cluster's sponsor may do it.
// @Tags ClusterCredentials
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param entryName path string true "Entry Name"
// @Success 200 {string} string "Credential store off"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file"
// @Router /api/clusters/{clusterName}/secret-kv-actions/disable/{entryName} [post]
func (repman *ReplicationManager) handlerMuxSecretKvDisable(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.SetSecretKvEnabled(vars["entryName"], false); err != nil {
			writeSecretKvError(w, err)
			return
		}
		w.Write([]byte("Credential store off"))
	})
}

// handlerMuxSecretKvRemove removes a vault entry.
// @Summary Remove a credential store entry
// @Description Takes the entry out of the list. repman never deletes a secret: what the vault holds stays.
// @Tags ClusterCredentials
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param entryName path string true "Entry Name"
// @Success 200 {string} string "Credential store removed"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file, or the entry comes from a Cloud18 marketplace offer (its settings are the back-office's)"
// @Router /api/clusters/{clusterName}/secret-kv-actions/remove/{entryName} [post]
func (repman *ReplicationManager) handlerMuxSecretKvRemove(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.RemoveSecretKv(vars["entryName"]); err != nil {
			writeSecretKvError(w, err)
			return
		}
		w.Write([]byte("Credential store removed"))
	})
}
