// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// UserRotateForm names the user whose password is rotated.
type UserRotateForm struct {
	Username string `json:"username"`
}

// UserRotateResult is the rotation's outcome. Password is the new one, shown this once.
type UserRotateResult struct {
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	Scope         string   `json:"scope"`
	Clusters      []string `json:"clusters"`
	TokensRevoked int      `json:"tokensRevoked"`
	Copy          string   `json:"copy"`
}

// rotatedPasswordLength: a rotation bans a user who may be compromised, so a long password.
const rotatedPasswordLength = 24

// handlerMuxUserRotatePassword rotates one user's password.
// @Summary Rotate a user's password
// @Description Gives the user a new generated password, returned once. A user of the instance's list (api-credentials) gets it on every cluster; a user of the cluster's own list (api-credentials-external) on this cluster. The user's open sessions are refused from the next request and its API tokens are revoked: this is how a compromised user is banned. The instance's list is copied to the OpenSVC secret system/sec/repman when a cluster is on OpenSVC.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body UserRotateForm true "User"
// @Success 200 {object} UserRotateResult "The new password, shown once"
// @Failure 400 {string} string "Invalid request or unknown user"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "This user's password cannot be rotated"
// @Failure 500 {string} string "Rotation failed"
// @Router /api/clusters/{clusterName}/users/rotate-password [post]
func (repman *ReplicationManager) handlerMuxUserRotatePassword(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}
	valid, delegator := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	var form UserRotateForm
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil || strings.TrimSpace(form.Username) == "" {
		http.Error(w, "Invalid request: a username is needed", http.StatusBadRequest)
		return
	}
	res, err := repman.rotateAPIUserPassword(mycluster, strings.TrimSpace(form.Username), delegator)
	if err != nil {
		repman.logSecurityEvent("api_user_password_rotation_refused", form.Username, r.RemoteAddr,
			fmt.Sprintf("Password rotation of %s by %s refused: %s", form.Username, delegator, err))
		switch {
		case errors.Is(err, cluster.ErrAPIUserUnknown):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, errRotateRefused):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	repman.logSecurityEvent("api_user_password_rotated", res.Username, r.RemoteAddr,
		fmt.Sprintf("Password of %s rotated by %s on %s (%s list), %d API token(s) revoked, copy: %s",
			res.Username, delegator, strings.Join(res.Clusters, ","), res.Scope, res.TokensRevoked, res.Copy))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}

var errRotateRefused = errors.New("rotation refused")

// rotateAPIUserPassword gives user a new generated password: on every cluster holding it in
// the instance's list, or on cl alone for the cluster's own list. Every cluster is checked
// before any change. Then the user's API tokens are revoked and the instance's list is copied
// to OpenSVC.
func (repman *ReplicationManager) rotateAPIUserPassword(cl *cluster.Cluster, user, by string) (*UserRotateResult, error) {
	scope := cl.APIUserScope(user)
	if scope == "" {
		return nil, cluster.ErrAPIUserUnknown
	}
	targets := []*cluster.Cluster{cl}
	if scope == cluster.APIUserScopeInstance {
		targets = repman.clustersWithInstanceUser(user)
	}
	for _, t := range targets {
		if err := t.CanRotateAPIUserPassword(user); err != nil {
			if errors.Is(err, cluster.ErrAPIUserUnknown) {
				return nil, err
			}
			return nil, fmt.Errorf("%w on %s: %s", errRotateRefused, t.Name, err)
		}
	}
	pass, err := cl.GeneratePasswordLength(rotatedPasswordLength)
	if err != nil {
		return nil, err
	}
	res := &UserRotateResult{Username: user, Password: pass, Scope: scope}
	for _, t := range targets {
		if err := t.SetAPIUserPassword(user, scope, pass); err != nil {
			// what changed is said: the clusters already rotated keep the new password
			return nil, fmt.Errorf("rotated on %v, failed on %s: %s", res.Clusters, t.Name, err)
		}
		res.Clusters = append(res.Clusters, t.Name)
	}
	res.TokensRevoked, err = repman.revokeUserAPITokens(user, "password rotation by "+by)
	if err != nil {
		repman.Logrus.Warnf("Password rotation of %s: API tokens not revoked: %s", user, err)
	}
	res.Copy = "not applicable: a cluster's own list is not copied"
	if scope == cluster.APIUserScopeInstance {
		res.Copy = repman.copyAPICredentialsToOpenSVC(targets[0])
	}
	return res, nil
}

// clustersWithInstanceUser are the clusters holding user in the instance's list, in name order.
func (repman *ReplicationManager) clustersWithInstanceUser(user string) []*cluster.Cluster {
	var out []*cluster.Cluster
	for _, c := range repman.Clusters {
		if c != nil && c.APIUserScope(user) == cluster.APIUserScopeInstance {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// copyAPICredentialsToOpenSVC writes the instance's list, in clear, to the OpenSVC secret
// system/sec/repman, key <api host>_api-credentials (the place jbond007 indexes), through
// the first cluster on OpenSVC. It returns what happened, never the value.
func (repman *ReplicationManager) copyAPICredentialsToOpenSVC(src *cluster.Cluster) string {
	host := apiPublicHost(repman.Conf.APIPublicURL)
	if host == "" {
		return "skipped: api-public-url is not set"
	}
	names := make([]string, 0, len(repman.Clusters))
	for name := range repman.Clusters {
		names = append(names, name)
	}
	sort.Strings(names)
	key := host + "_api-credentials"
	for _, name := range names {
		c := repman.Clusters[name]
		if c == nil || c.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
			continue
		}
		if err := c.WriteOpenSVCSecretKey("system", "repman", key, src.DecryptedAPIUserList()); err != nil {
			return "failed through " + name + ": " + err.Error()
		}
		return "written to system/sec/repman key " + key + " through " + name
	}
	return "skipped: no cluster on OpenSVC"
}

// apiPublicHost is the host of api-public-url, "" when unset or not a URL.
func apiPublicHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
