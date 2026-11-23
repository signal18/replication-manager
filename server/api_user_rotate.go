// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
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
	PersistError  string   `json:"persistError,omitempty"`
	// StoresBehind: the server credential stores already known behind when the instance's list
	// was rotated (GWARN024): they may still hold the compromised password. Named for a holder
	// of the server credentials grant on every cluster, counted for anyone else (the entries are
	// the owners'). Empty says no store was known behind, not that every store has the new list:
	// the new version is written by the next pass.
	StoresBehind      []string `json:"storesBehind,omitempty"`
	StoresBehindCount int      `json:"storesBehindCount,omitempty"`
	// EtcFile: the instance's list is set in a configuration file under /etc, and the new list
	// was written there ("written") or not (why, GWARN025): the file keeps the old password
	EtcFile string `json:"etcFile,omitempty"`
	// RevealMailedTo: the new password was mailed as a one-time link to this address, the
	// connected user who rotated it (never the rotated account): Password is then empty. RevealExpires: when the
	// link stops working. RevealError: why no link was mailed, the password is then in Password,
	// shown this once to who rotated it.
	RevealMailedTo string     `json:"revealMailedTo,omitempty"`
	RevealExpires  *time.Time `json:"revealExpires,omitempty"`
	RevealError    string     `json:"revealError,omitempty"`
}

// rotatedPasswordLength: a rotation bans a user who may be compromised, so a long password.
const rotatedPasswordLength = 24

// handlerMuxUserRotatePassword rotates one user's password.
// @Summary Rotate a user's password
// @Description Gives the user a new generated password, returned once. A user of the instance's list (api-credentials) gets it on every cluster; a user of the cluster's own list (api-credentials-external) on this cluster. The user's open sessions are refused from the next request and its API tokens are revoked: this is how a compromised user is banned. The new list is written to the versioned store at once, then to every server credential store (Vault, OpenSVC, Kubernetes) at its next pass, and to the configuration file under /etc that sets it, when there is one. The new password is mailed as a one-time link (api-secret-reveal), never to the rotated account: to the connected user who rotated it, at its login's email, when its role covers the level (the instance's list: sysops, extsysops; a cluster's list: also dbops, extdbops, sponsor). The answer then does not carry it (revealMailedTo); otherwise it does, shown once (revealError says why).
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body UserRotateForm true "User"
// @Success 200 {object} UserRotateResult "The new password, shown once; empty when it was mailed to the user as a one-time link (revealMailedTo)"
// @Failure 400 {string} string "Invalid request or unknown user"
// @Failure 403 {string} string "No valid ACL, or another user of the instance's list without cluster-server-credentials on every cluster"
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
	// an account of the instance's list is the instance's: a login scoped to some clusters
	// (api_session_scope.go) never rotates it, not even its own name's (the per-cluster password
	// it logged in with is not that account's instance password)
	if mycluster.APIUserScope(user) == cluster.APIUserScopeInstance && !repman.requestUnrestricted(r) {
		repman.logSecurityEvent("api_user_password_rotation_refused", user, r.RemoteAddr,
			fmt.Sprintf("Password rotation of %s by %s refused: a login scoped to some clusters does not rotate an account of the instance's list", user, delegator))
		http.Error(w, errNoServerCredentials.Error()+": log in with credentials covering the instance", http.StatusForbidden)
		return
	}
	res, err := repman.rotateAPIUserPassword(mycluster, user, delegator)
	if err != nil {
		repman.logSecurityEvent("api_user_password_rotation_refused", user, r.RemoteAddr,
			fmt.Sprintf("Password rotation of %s by %s refused: %s", user, delegator, err))
		switch {
		case errors.Is(err, cluster.ErrAPIUserUnknown):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, errNoServerCredentials):
			http.Error(w, err.Error(), http.StatusForbidden)
		case errors.Is(err, errRotateRefused):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	// out of rotateMu: a slow mail server holds no other rotation
	repman.mailRotatedPassword(res, mycluster, delegator)
	tokens := fmt.Sprintf("%d API token(s) revoked", res.TokensRevoked)
	if res.TokensError != "" {
		tokens = res.TokensError
	}
	if res.PersistError != "" {
		tokens += ", " + res.PersistError
	}
	if res.EtcFile != "" {
		tokens += ", /etc file " + res.EtcFile
	}
	if res.RevealMailedTo != "" {
		tokens += ", new password mailed to " + res.RevealMailedTo + " (who rotated it) as a one-time link"
	} else if res.RevealError != "" {
		tokens += ", new password shown to " + delegator + " (" + res.RevealError + ")"
	}
	if res.StoresBehindCount > 0 {
		tokens += fmt.Sprintf(", %d server credential store(s) behind", res.StoresBehindCount)
		if len(res.StoresBehind) > 0 {
			tokens += ": " + strings.Join(res.StoresBehind, ",")
		}
	}
	repman.logSecurityEvent("api_user_password_rotated", res.Username, r.RemoteAddr,
		fmt.Sprintf("Password of %s rotated by %s on %s (%s list), %s",
			res.Username, delegator, strings.Join(res.Clusters, ","), res.Scope, tokens))
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
	if scope == cluster.APIUserScopeInstance {
		// the instance's list is a server secret: another user's password in it is changed by
		// its owners only (sysops, extsysops); a user may always ban its own password
		if by != user {
			if err := repman.holdsServerCredentials(by); err != nil {
				return nil, fmt.Errorf("%w: %w", errRotateRefused, err)
			}
		}
		if err := repman.CredentialChain().CanSet(apiCredentialsKey); err != nil {
			return nil, fmt.Errorf("%w: %s cannot take a new value: %s", errRotateRefused, apiCredentialsKey, credstore.Reason(err))
		}
	}
	pass, err := cl.GeneratePasswordLength(rotatedPasswordLength)
	if err != nil {
		return nil, err
	}
	res := &UserRotateResult{Username: user, Password: pass, Scope: scope}
	var setErr error
	if scope == cluster.APIUserScopeInstance {
		// a server secret: one version at the server level, every cluster reads it
		setErr = repman.setInstanceUserPassword(user, pass)
		if errors.Is(setErr, errServerSecretNotPersisted) {
			// in force on every cluster, not on disk: said in the answer, not only logged
			res.PersistError = setErr.Error() + " (rotate again to retry)"
			repman.Logrus.Warnf("Password rotation of %s: %s", user, res.PersistError)
			setErr = nil
		}
		if setErr == nil {
			for _, t := range targets {
				res.Clusters = append(res.Clusters, t.Name)
			}
		}
	} else if setErr = cl.SetAPIUserPassword(user, scope, pass); setErr == nil {
		res.Clusters = append(res.Clusters, cl.Name)
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
	// the instance's list is a server secret: its new version goes to every server credential
	// store (Vault, OpenSVC, Kubernetes) through the server chain's hook, a store behind says so
	// (GWARN024); a cluster's own list goes to that cluster's stores
	if scope == cluster.APIUserScopeInstance {
		behind := repman.ServerKvBehind()
		res.StoresBehindCount = len(behind)
		if inFile, why := repman.etcCredentialStatus(apiCredentialsKey); inFile {
			res.EtcFile = "written"
			if why != "" {
				res.EtcFile = "NOT written, the file under /etc keeps the old password: " + why
			}
		}
		if len(behind) > 0 && repman.holdsServerCredentials(by) == nil {
			for name := range behind {
				res.StoresBehind = append(res.StoresBehind, name)
			}
			sort.Strings(res.StoresBehind)
		}
	}
	return res, nil
}

// rotatedPasswordRecipient is who gets a rotated password by mail: the connected user who rotated
// it, at its email (userEmail: the login, or for the local admin the registered account), when its
// role covers the secret's level; never the rotated account
// itself (if it is compromised, its mailbox may be too).
//   - a user of the instance's list is a server secret: sysops or extsysops only;
//   - a user of a cluster's own list is a cluster secret: sysops, extsysops, dbops, extdbops or
//     sponsor of that cluster.
//
// "" with why when the connected user cannot receive it: the password is then shown once to them.
func rotatedPasswordRecipient(scope, delegator, delegatorEmail string, delegatorRoles map[string]bool) (string, string) {
	levelRoles := []string{config.RoleSysOps, config.RoleExtSysOps}
	level := "a server secret (a user of the instance's list) is mailed to a sysops or extsysops only"
	if scope != cluster.APIUserScopeInstance {
		levelRoles = append(levelRoles, config.RoleDBOps, config.RoleExtDBOps, config.RoleSponsor)
		level = "a cluster secret is mailed to a sysops, extsysops, dbops, extdbops or sponsor of the cluster only"
	}
	held := false
	for _, r := range levelRoles {
		held = held || delegatorRoles[r]
	}
	if !held {
		return "", level
	}
	if !strings.Contains(delegatorEmail, "@") {
		return "", delegator + " has no email address"
	}
	return delegatorEmail, ""
}

// userEmail is the email of a login, the one the GUI shows for it (handlerMuxWhoAmI): on a
// registered instance (cloud18), for an admin user (global-admin-show), the account the instance
// is registered with, its owner; otherwise the login itself (an email or not, the caller checks).
func (repman *ReplicationManager) userEmail(login string) string {
	if repman.Conf.Cloud18 && repman.Conf.Cloud18GitUser != "" && repman.isAdminUser(login) {
		return repman.Conf.Cloud18GitUser
	}
	return login
}

// mailRotatedPassword mails the new password as a one-time link to the connected user
// (rotatedPasswordRecipient) when links can be mailed: the answer then no longer carries it.
// Otherwise the answer carries it, shown once, with why it was not mailed.
func (repman *ReplicationManager) mailRotatedPassword(res *UserRotateResult, cl *cluster.Cluster, delegator string) {
	var roles map[string]bool
	if u, ok := cl.GetAPIUsers()[delegator]; ok {
		roles = u.Roles
	}
	repman.mailRotatedPasswordTo(res, cl.Name, delegator, roles)
}

func (repman *ReplicationManager) mailRotatedPasswordTo(res *UserRotateResult, clusterName, delegator string, delegatorRoles map[string]bool) {
	to, why := rotatedPasswordRecipient(res.Scope, delegator, repman.userEmail(delegator), delegatorRoles)
	if to == "" {
		res.RevealError = "not mailed, shown here once: " + why
		return
	}
	where := "on cluster " + clusterName
	if res.Scope == cluster.APIUserScopeInstance {
		where = "on every cluster of this replication-manager (" + strings.Join(res.Clusters, ", ") + ")"
	}
	expires, err := repman.mailSecretReveal(to, clusterName, "New replication-manager password of "+res.Username,
		fmt.Sprintf("You rotated the replication-manager password of %s %s: its sessions were ended and its API tokens revoked. Hand the new password to its owner by a safe channel.", res.Username, where),
		[]SecretRevealItem{{Label: "Login", Value: res.Username}, {Label: "Password", Value: res.Password}})
	if err != nil {
		res.RevealError = "not mailed, shown here once: " + err.Error()
		return
	}
	res.Password = ""
	res.RevealMailedTo = to
	res.RevealExpires = &expires
}

// errNoServerCredentials: the caller lacks the server credentials grant on a cluster.
var errNoServerCredentials = errors.New("a server secret is changed by the instance's sysops and extsysops only")

// holdsServerCredentials: user holds the server credentials grant on every cluster of the
// instance. A server secret is the instance's (CREDENTIAL_STORES.md "Two levels"), so a grant
// on one cluster of a shared instance is not enough; the same rule as the instance's name.
func (repman *ReplicationManager) holdsServerCredentials(user string) error {
	clusters := repman.clustersSnapshot()
	if len(clusters) == 0 {
		return errors.New("no cluster: the server credentials grant cannot be checked")
	}
	for _, cl := range clusters {
		u, ok := cl.GetACLUser(user)
		if !ok || u.Granted(config.GrantClusterServerCredentials) != nil {
			return fmt.Errorf("%w: %s does not hold %s on %s", errNoServerCredentials, user, config.GrantClusterServerCredentials, cl.Name)
		}
	}
	return nil
}

// apiCredentialsKey is the instance's list of API users, a server secret.
const apiCredentialsKey = "api-credentials"

// setInstanceUserPassword writes pass as user's password in the instance's list, a server
// secret: one version, every other user keeps its password, every cluster reloads its users.
func (repman *ReplicationManager) setInstanceUserPassword(user, pass string) error {
	if err := cluster.ValidListPassword(pass); err != nil {
		return err
	}
	// the read and the write under one lock: no other change of the list lands in between
	repman.serverSecrets.setMu.Lock()
	defer repman.serverSecrets.setMu.Unlock()
	list, found := config.APIUserListWithPassword(repman.Conf.GetDecryptedValue(apiCredentialsKey), user, pass)
	if !found {
		return cluster.ErrAPIUserUnknown
	}
	if err := repman.setServerSecretLocked(apiCredentialsKey, list); err != nil {
		if errors.Is(err, errServerSecretNotPersisted) {
			return err
		}
		return fmt.Errorf("%s not changed: %s", apiCredentialsKey, credstore.Reason(err))
	}
	return nil
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
