// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
)

// The server credential stores API (CREDENTIAL_STORES.md "Two levels"): the entries (Vault,
// OpenSVC, Kubernetes) of the server level, which hold the server secrets. The instance's sysops and extsysops only:
// the server credentials grant on every cluster (serverCredentialsACL), never a cluster's grant, so
// a sponsor or a member never sees nor switches one. Secrets go in through add and edit and
// never come back.

// serverCredentialsACL: the caller holds the server credentials grant on every cluster of the
// instance. The server stores are the instance's, so an extsysops of one cluster of a shared
// instance does not see or change them.
func (repman *ReplicationManager) serverCredentialsACL(w http.ResponseWriter, r *http.Request) (string, bool) {
	clusters := repman.clustersSnapshot()
	if len(clusters) == 0 {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return "", false
	}
	user := ""
	for _, cl := range clusters {
		valid, u := repman.IsValidClusterACL(r, cl)
		if !valid {
			http.Error(w, "No valid ACL: the server credentials grant is needed on every cluster", http.StatusForbidden)
			return "", false
		}
		user = u
	}
	return user, true
}

// errServerSecretKvsPinned refuses a change of a list a config file sets.
var errServerSecretKvsPinned = errors.New("the server credential stores are set in a config file (server-secret-kv): change them there")

// errServerSecretKvStandby refuses a change on a standby: the active instance's list is pushed.
var errServerSecretKvStandby = errors.New("this instance is a standby: change the server credential stores on the active instance, the standby takes them from the config push")

// serverSecretKvChange runs change on the server list: the owners only, on the active
// instance, the list not set by a config file; then the save, and a full pass.
func (repman *ReplicationManager) serverSecretKvChange(w http.ResponseWriter, r *http.Request, action string, change func() (string, error)) {
	user, ok := repman.serverCredentialsACL(w, r)
	if !ok {
		return
	}
	if repman.Status != ConstMonitorActif {
		http.Error(w, errServerSecretKvStandby.Error(), http.StatusConflict)
		return
	}
	if repman.serverSecretKvsPinned() {
		http.Error(w, errServerSecretKvsPinned.Error(), http.StatusConflict)
		return
	}
	name, err := change()
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, config.ErrSecretKvOfferManaged) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	repman.Save()
	repman.startServerKvPass()
	repman.logSecurityEvent("server_secret_kv_"+action, name, r.RemoteAddr, fmt.Sprintf("Server credential store %s %s by %s", name, action, user))
	writeArchiveJSON(w, map[string]string{"name": name})
}

// checkServerStoreCluster refuses an OpenSVC or Kubernetes server entry naming a cluster that is
// not monitored here or not on that orchestrator: the store would only ever be behind. The
// writer checks it again at each pass (a cluster removed or moved later).
func (repman *ReplicationManager) checkServerStoreCluster(spec config.SecretKvSpec) error {
	plugin, clusterName := spec.Plugin, ""
	for _, e := range repman.Conf.SecretKvListAt(config.SecretKvLevelServer) {
		if e.Name == spec.Name {
			if plugin == "" {
				plugin = e.Plugin
			}
			clusterName = repman.Conf.SecretKvSetting(e, config.ConstSecretKvOrchestratorCluster)
		}
	}
	if v, ok := spec.Settings[config.ConstSecretKvOrchestratorCluster]; ok {
		clusterName = fmt.Sprintf("%v", v)
	}
	if !config.IsOrchestratorSecretKvPlugin(plugin) || clusterName == "" {
		return nil // the plugin's own checks say what is missing
	}
	cl := repman.getClusterByName(clusterName)
	if cl == nil {
		return fmt.Errorf("credential store %s: cluster %q is not monitored here", spec.Name, clusterName)
	}
	if _, err := cl.ServerOrchestratorStore(plugin, ""); err != nil {
		return fmt.Errorf("credential store %s: cluster %s: %w", spec.Name, clusterName, err)
	}
	return nil
}

// handlerMuxServerSecretKvList lists the server credential stores.
// @Summary List the server credential stores
// @Description The entries of the server level, which hold the server secrets (api-credentials, SMTP, GitLab...): a vault under <path-prefix>/system/repman/<instance name>, the OpenSVC secret system/sec/repman or a Kubernetes Secret repman-system under the keys <instance name>_<key>: secrets never returned, only the names of those set; each one's state (last write, behind: GWARN024, WARN0320 on every cluster) and folder, the instance's name (empty while not complete), the server secrets' names, the plugins' manifests and whether a config file sets the list. The instance's sysops and extsysops only: the server credentials grant on every cluster.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} ServerSecretKvListResult
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Router /api/clusters/settings/actions/server-secret-kv [get]
func (repman *ReplicationManager) handlerMuxServerSecretKvList(w http.ResponseWriter, r *http.Request) {
	if _, ok := repman.serverCredentialsACL(w, r); !ok {
		return
	}
	writeArchiveJSON(w, repman.serverSecretKvList())
}

// handlerMuxServerSecretKvAdd adds a server credential store.
// @Summary Add a server credential store
// @Description Adds an entry of the server level: plugin secret_kv-vault (address, mount, path-prefix, mode, auth, role-id, secret-id written only), secret_kv-opensvc (cluster: a cluster on OpenSVC whose agent writes system/sec/repman) or secret_kv-kubernetes (cluster: a cluster on Kubernetes, namespace: where the Secret repman-system is written). A cluster not monitored here or not on that orchestrator is refused. An enabled entry gets every version of every server secret, a full pass first, once the instance is named. Refused on a standby and when a config file sets the list.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param body body config.SecretKvSpec true "name, settings, enabled"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Invalid entry"
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Failure 409 {string} string "A standby, or the list is set by a config file"
// @Router /api/clusters/settings/actions/server-secret-kv/add [post]
func (repman *ReplicationManager) handlerMuxServerSecretKvAdd(w http.ResponseWriter, r *http.Request) {
	spec, ok := decodeSecretKvSpec(w, r)
	if !ok {
		return
	}
	repman.serverSecretKvChange(w, r, "added", func() (string, error) {
		if err := repman.checkServerStoreCluster(spec); err != nil {
			return "", err
		}
		return repman.Conf.PutSecretKvAt(config.SecretKvLevelServer, spec, true)
	})
}

// handlerMuxServerSecretKvEdit changes a server credential store.
// @Summary Edit a server credential store
// @Description Changes a server entry's settings (an empty secret keeps the secret, unset clears names); its plugin does not change.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param entryName path string true "Entry Name"
// @Param body body config.SecretKvSpec true "settings, unset, enabled"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Invalid entry"
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Failure 409 {string} string "A standby, or the list is set by a config file"
// @Router /api/clusters/settings/actions/server-secret-kv/edit/{entryName} [post]
func (repman *ReplicationManager) handlerMuxServerSecretKvEdit(w http.ResponseWriter, r *http.Request) {
	spec, ok := decodeSecretKvSpec(w, r)
	if !ok {
		return
	}
	spec.Name = mux.Vars(r)["entryName"]
	repman.serverSecretKvChange(w, r, "changed", func() (string, error) {
		if err := repman.checkServerStoreCluster(spec); err != nil {
			return "", err
		}
		return repman.Conf.PutSecretKvAt(config.SecretKvLevelServer, spec, false)
	})
}

// handlerMuxServerSecretKvEnable turns a server credential store on.
// @Summary Turn a server credential store on
// @Description The entry gets every version of every server secret, a full pass first.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param entryName path string true "Entry Name"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Failure 409 {string} string "A standby, or the list is set by a config file"
// @Router /api/clusters/settings/actions/server-secret-kv/enable/{entryName} [post]
func (repman *ReplicationManager) handlerMuxServerSecretKvEnable(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["entryName"]
	repman.serverSecretKvChange(w, r, "turned on", func() (string, error) {
		return name, repman.Conf.SetSecretKvEnabledAt(config.SecretKvLevelServer, name, true)
	})
}

// handlerMuxServerSecretKvDisable turns a server credential store off.
// @Summary Turn a server credential store off
// @Description Its versions stop; what it holds stays.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param entryName path string true "Entry Name"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Failure 409 {string} string "A standby, or the list is set by a config file"
// @Router /api/clusters/settings/actions/server-secret-kv/disable/{entryName} [post]
func (repman *ReplicationManager) handlerMuxServerSecretKvDisable(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["entryName"]
	repman.serverSecretKvChange(w, r, "turned off", func() (string, error) {
		return name, repman.Conf.SetSecretKvEnabledAt(config.SecretKvLevelServer, name, false)
	})
}

// handlerMuxServerSecretKvRemove removes a server credential store from the list.
// @Summary Remove a server credential store
// @Description Takes the entry out of the list; repman never deletes what the store holds.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param entryName path string true "Entry Name"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Unknown entry"
// @Failure 403 {string} string "No valid ACL: the server credentials grant is needed on every cluster"
// @Failure 409 {string} string "A standby, or the list is set by a config file"
// @Router /api/clusters/settings/actions/server-secret-kv/remove/{entryName} [post]
func (repman *ReplicationManager) handlerMuxServerSecretKvRemove(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["entryName"]
	repman.serverSecretKvChange(w, r, "removed", func() (string, error) {
		return name, repman.Conf.DeleteSecretKvAt(config.SecretKvLevelServer, name)
	})
}
