// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
)

// The cluster's secrets for their owner (cluster/cluster_secrets.go): a PURE READ. This handler
// changes no credential, generates none and calls no rotation, on any path; a value that cannot
// be read is left out (TestClusterSecretsArePureRead).

// instanceAdminUser is the instance's local administration account, in its list (api-credentials).
const instanceAdminUser = "admin"

// ClusterSecretsResult is the answer of GET /api/clusters/{clusterName}/secrets.
type ClusterSecretsResult struct {
	Cluster string                  `json:"cluster"`
	Secrets []cluster.ClusterSecret `json:"secrets"`
	// InstanceAdmin: the repman administration login of the instance, only for a caller holding
	// cluster-server-credentials on every cluster (the owner, sysops, extsysops)
	InstanceAdmin *cluster.ClusterSecret `json:"instanceAdmin,omitempty"`
}

// handlerMuxClusterSecrets returns the cluster's credentials in clear.
// @Summary Read the cluster's credentials
// @Description Returns, in clear, the credentials replication-manager generated or holds for the cluster: the database administration, replication and heartbeat users, the Cloud18 sponsor and DBA users, the passwords of the proxies that are on, the backup repository password, and the database user of each app. The instance's own secrets (API users, git, registry, Vault, OpenSVC, SSH, mail and alert tokens, cloud keys) are never returned. A caller holding cluster-server-credentials on every cluster of the instance also gets the instance's administration login. A pure read: no credential is ever changed by this call. Each call is a security event naming the caller and the keys returned, never the values. Needs the grant cluster-show-secrets and the setting api-cluster-secrets.
// @Tags ClusterSecrets
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} ClusterSecretsResult "The credentials, in clear"
// @Failure 403 {string} string "No valid ACL (cluster-show-secrets), or api-cluster-secrets is off"
// @Failure 500 {string} string "No valid cluster"
// @Router /api/clusters/{clusterName}/secrets [get]
func (repman *ReplicationManager) handlerMuxClusterSecrets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}
	valid, caller := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		repman.logSecurityEvent("cluster_secrets_refused", caller, r.RemoteAddr,
			fmt.Sprintf("Secrets of cluster %s refused to %s: no cluster-show-secrets", mycluster.Name, caller))
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	if !repman.Conf.APIClusterSecrets {
		http.Error(w, "The cluster secrets are off on this replication-manager (api-cluster-secrets)", http.StatusForbidden)
		return
	}
	res := repman.clusterSecretsFor(mycluster, caller)
	keys := make([]string, 0, len(res.Secrets)+1)
	for _, s := range res.Secrets {
		keys = append(keys, s.Key)
	}
	if res.InstanceAdmin != nil {
		keys = append(keys, "api-credentials:"+instanceAdminUser)
	}
	repman.logSecurityEvent("cluster_secrets_read", caller, r.RemoteAddr,
		fmt.Sprintf("Secrets of cluster %s read by %s: %s", mycluster.Name, caller, strings.Join(keys, ",")))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// clusterSecretsFor gathers what caller may read of cl's secrets; a read only.
func (repman *ReplicationManager) clusterSecretsFor(cl *cluster.Cluster, caller string) ClusterSecretsResult {
	res := ClusterSecretsResult{Cluster: cl.Name, Secrets: cl.GetClusterSecrets()}
	if repman.holdsServerCredentials(caller) == nil {
		if pass := instanceUserPassword(repman.Conf.DecryptedAPIUserList(), instanceAdminUser); pass != "" {
			res.InstanceAdmin = &cluster.ClusterSecret{Key: "api-credentials", Label: "replication-manager administration login",
				User: instanceAdminUser, Password: pass}
		}
	}
	return res
}

// instanceUserPassword is user's password in a decrypted list (user:password,...), "" when absent.
func instanceUserPassword(list, user string) string {
	for _, e := range strings.Split(list, ",") {
		if u, p, ok := strings.Cut(strings.TrimSpace(e), ":"); ok && u == user {
			return p
		}
	}
	return ""
}
