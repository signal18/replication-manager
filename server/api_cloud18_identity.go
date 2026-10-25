// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/signal18/replication-manager/config"
)

// The Cloud18 login of this replication-manager (#2159): a person connects it once to the
// Cloud18 identity realm with the device login (config.StartCloud18DeviceLogin). Global settings
// grant, like cloud18-identity-url.

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
// @Description state none (nobody connected it), pending (a device login waits: device holds the code and the page), connected, or reconnect (the login ended: reason). Never a token.
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

// handlerMuxCloud18IdentityConnect godoc
// @Summary Connect this replication-manager to the Cloud18 identity (device login)
// @Description Asks the realm for a code: the person opens the page (device.verificationUri), logs in and types the code (device.userCode); replication-manager then keeps the login. Replaces a login still waiting.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} config.Cloud18IdentityStatus
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The realm or the setting refused the login"
// @Failure 503 {string} string "The realm cannot be reached"
// @Router /api/clusters/settings/actions/cloud18-identity/connect [post]
func (repman *ReplicationManager) handlerMuxCloud18IdentityConnect(w http.ResponseWriter, r *http.Request) {
	user, ok := repman.cloud18IdentityACL(w, r)
	if !ok {
		return
	}
	if _, err := repman.Conf.StartCloud18DeviceLogin(); err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, config.ErrCloud18Refused) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18 identity: device login started by %s", user)
	writeCloud18IdentityStatus(w, repman.Conf.Cloud18IdentityStatus())
}

// handlerMuxCloud18IdentityDisconnect godoc
// @Summary Disconnect this replication-manager from the Cloud18 identity
// @Description Forgets the kept login and ends it at the realm (best effort). The S3 SSO storages and the Cloud18 vault refuse until someone connects it again.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} config.Cloud18IdentityStatus
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "The login file cannot be removed"
// @Router /api/clusters/settings/actions/cloud18-identity/disconnect [post]
func (repman *ReplicationManager) handlerMuxCloud18IdentityDisconnect(w http.ResponseWriter, r *http.Request) {
	user, ok := repman.cloud18IdentityACL(w, r)
	if !ok {
		return
	}
	if err := repman.Conf.DisconnectCloud18Identity(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18 identity: disconnected by %s", user)
	writeCloud18IdentityStatus(w, repman.Conf.Cloud18IdentityStatus())
}
