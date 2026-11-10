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
	"sync"
	"unicode"

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
	TokensError   string   `json:"tokensError,omitempty"`
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&form); err != nil || !validUsername(strings.TrimSpace(form.Username)) {
		http.Error(w, "Invalid request: a username is needed, without ',', ':' or control characters", http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(form.Username)
	res, err := repman.rotateAPIUserPassword(mycluster, user, delegator)
	if err != nil {
		repman.logSecurityEvent("api_user_password_rotation_refused", user, r.RemoteAddr,
			fmt.Sprintf("Password rotation of %s by %s refused: %s", user, delegator, err))
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
	tokens := fmt.Sprintf("%d API token(s) revoked", res.TokensRevoked)
	if res.TokensError != "" {
		tokens = res.TokensError
	}
	repman.logSecurityEvent("api_user_password_rotated", res.Username, r.RemoteAddr,
		fmt.Sprintf("Password of %s rotated by %s on %s (%s list), %s, copy: %s",
			res.Username, delegator, strings.Join(res.Clusters, ","), res.Scope, tokens, res.Copy))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}

var errRotateRefused = errors.New("rotation refused")

// rotateMu serializes the rotations: each reads a list, changes one entry and writes it back.
var rotateMu sync.Mutex

// validUsername: a name a user:password list can hold, safe in a log line.
func validUsername(u string) bool {
	if u == "" || strings.ContainsAny(u, ",:") {
		return false
	}
	for _, c := range u {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// clustersSnapshot is the clusters in name order, read under the manager's lock.
func (repman *ReplicationManager) clustersSnapshot() []*cluster.Cluster {
	repman.Lock()
	out := make([]*cluster.Cluster, 0, len(repman.Clusters))
	for _, c := range repman.Clusters {
		if c != nil {
			out = append(out, c)
		}
	}
	repman.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// rotateAPIUserPassword gives user a new generated password: on every cluster holding it in
// the instance's list, or on cl alone for the cluster's own list. Every cluster is checked
// before any change. Then the user's API tokens are revoked and the instance's list is copied
// to OpenSVC. The caller may rotate only its own password or that of a user holding no grant it
// lacks. A failure half-way keeps what was done, tokens revoked: undoing it would give the
// compromised password back.
func (repman *ReplicationManager) rotateAPIUserPassword(cl *cluster.Cluster, user, by string) (*UserRotateResult, error) {
	rotateMu.Lock()
	defer rotateMu.Unlock()
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
		if err := t.CanRotateFor(by, user); err != nil {
			return nil, fmt.Errorf("%w: %s", errRotateRefused, err)
		}
	}
	pass, err := cl.GeneratePasswordLength(rotatedPasswordLength)
	if err != nil {
		return nil, err
	}
	res := &UserRotateResult{Username: user, Password: pass, Scope: scope}
	var setErr error
	for i, t := range targets {
		if err := t.SetAPIUserPassword(user, scope, pass); err != nil {
			var left []string
			for _, o := range targets[i:] {
				left = append(left, o.Name)
			}
			setErr = fmt.Errorf("rotated on %v; failed on %s: %s; %v still accept the old password: rotate again to set one password everywhere",
				res.Clusters, t.Name, err, left)
			break
		}
		res.Clusters = append(res.Clusters, t.Name)
	}
	if len(res.Clusters) > 0 {
		// the ban holds wherever the password changed: the tokens go too
		if res.TokensRevoked, err = repman.revokeUserAPITokens(user, "password rotation by "+by); err != nil {
			// a ban with live tokens is not a ban: said in the answer, not only logged
			res.TokensError = "API tokens NOT revoked, they may still be valid (rotate again to retry): " + err.Error()
			repman.Logrus.Warnf("Password rotation of %s: %s", user, res.TokensError)
		}
	}
	if setErr != nil {
		return nil, setErr
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
	for _, c := range repman.clustersSnapshot() {
		if c.APIUserScope(user) == cluster.APIUserScopeInstance {
			out = append(out, c)
		}
	}
	return out
}

// copyAPICredentialsToOpenSVC writes the instance's list, in clear, to the OpenSVC secret
// system/sec/repman, key <api host>_api-credentials (where the infrastructure index points),
// through the first cluster on OpenSVC. Off with secret-kv-copy-api-credentials. It returns
// what happened, never the value.
func (repman *ReplicationManager) copyAPICredentialsToOpenSVC(src *cluster.Cluster) string {
	if !repman.Conf.SecretKvCopyApiCredentials {
		return "skipped: secret-kv-copy-api-credentials is off"
	}
	host := apiPublicHost(repman.Conf.APIPublicURL)
	if host == "" {
		return "skipped: api-public-url is not set"
	}
	key := host + "_api-credentials"
	for _, c := range repman.clustersSnapshot() {
		if c.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
			continue
		}
		if err := c.WriteOpenSVCSecretKey("system", "repman", key, src.DecryptedAPIUserList()); err != nil {
			return "failed through " + c.Name + ": " + err.Error()
		}
		return "written to system/sec/repman key " + key + " through " + c.Name
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
