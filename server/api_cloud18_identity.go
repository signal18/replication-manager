// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"

	"github.com/signal18/replication-manager/config"
)

// The Cloud18 login of this replication-manager (#2159): made with the account it is registered
// with (config.Cloud18IdentityToken), shown here. Global settings grant, like cloud18-identity-url.

func (repman *ReplicationManager) cloud18IdentityACL(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	mycluster := repman.firstCluster()
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return "", false
	}
	valid, user := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return "", false
	}
	return user, true
}

func writeCloud18IdentityStatus(w http.ResponseWriter, st config.Cloud18IdentityStatus) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

// handlerMuxCloud18Identity godoc
// @Summary The Cloud18 login of this replication-manager
// @Description state none (not registered to Cloud18), registered (no login yet: made with the registered account at the next use or keep-alive), connected, or refused (the realm refused the registered account: register again, reason). Never a token.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} config.Cloud18IdentityStatus
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/settings/actions/cloud18-identity [get]
func (repman *ReplicationManager) handlerMuxCloud18Identity(w http.ResponseWriter, r *http.Request) {
	if _, ok := repman.cloud18IdentityACL(w, r); !ok {
		return
	}
	writeCloud18IdentityStatus(w, repman.Conf.Cloud18IdentityStatus())
}
