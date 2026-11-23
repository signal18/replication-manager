// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// Cluster scope of a local session (doc/implementation/server/SESSION_CLUSTER_SCOPE.md).
//
// The same account name can carry a different password on each cluster (a cluster's own list,
// api-credentials-external). A local login authenticates the password it was given, so it
// authenticates the account on the clusters where that password is the account's password, and
// nowhere else. The session records them: CustomUserInfo carries scope = "clusters" and the
// list of those clusters, and every check that resolves the session's identity by its name
// (IsValidClusterACL, loginACLUser and so UserHasGlobalGrant, the MCP principal, the peer login,
// the host terminal, the API tokens it creates) refuses the other clusters.
//
// An account of the instance's list (api-credentials of the server configuration) logged in with
// that list's password is the instance's account: its session carries no scope and covers every
// cluster, as before. A session without the scope claim is instance-wide: the RSA key that signs
// the sessions is generated at each start, so every session of a running instance was issued by
// this code, and the issuers that set no scope are the instance-wide ones (SSO, the instance's
// list, the service and dashboard logins).

// sessionScopeClusters is the value of the `scope` claim of a session restricted to `clusters`.
const sessionScopeClusters = "clusters"

// isInstanceCredential reports whether user:password is an entry of the instance's list
// (api-credentials of the server configuration), compared in constant time.
func (repman *ReplicationManager) isInstanceCredential(user, password string) bool {
	if user == "" || password == "" || repman.Conf == nil {
		return false
	}
	for _, e := range strings.Split(repman.Conf.DecryptedAPIUserList(), ",") {
		u, p, ok := strings.Cut(strings.TrimSpace(e), ":")
		if ok && u == user && p != "" && subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1 {
			return true
		}
	}
	return false
}

// localSessionScope returns the scope claims of a local login: none (instance-wide) for an
// account of the instance's list with that list's password, when that password is also the
// account's password on every cluster where the account exists (matched covers them all: a
// cluster section may set another password for it); else the clusters the password
// authenticated (sorted).
func (repman *ReplicationManager) localSessionScope(user, password string, matched []string) (string, []string) {
	if repman.isInstanceCredential(user, password) {
		everywhere := true
		for _, cl := range repman.clustersSnapshot() {
			if _, ok := cl.GetAPIUsers()[user]; ok && !slices.Contains(matched, cl.Name) {
				everywhere = false
				break
			}
		}
		if everywhere {
			return "", nil
		}
	}
	out := slices.Clone(matched)
	slices.Sort(out)
	return sessionScopeClusters, out
}

// sessionClusters reads the cluster restriction of a session's CustomUserInfo: the clusters and
// true for a restricted session, (nil, false) for an instance-wide one. A restricted session
// with an unreadable list is restricted to no cluster. A cluster deleted after the login
// (clusterDeletedSince) is dropped from the list: a cluster created again under the same name
// is another cluster, which the login never authenticated.
func (repman *ReplicationManager) sessionClusters(info map[string]interface{}) ([]string, bool) {
	if s, _ := info["scope"].(string); s != sessionScopeClusters {
		return nil, false
	}
	var at int64
	switch v := info["scope_at"].(type) {
	case float64:
		at = int64(v)
	case int64:
		at = v
	}
	out := []string{}
	add := func(s string) {
		if s != "" && !repman.clusterDeletedSince(s, at) {
			out = append(out, s)
		}
	}
	switch v := info["clusters"].(type) {
	case []interface{}:
		for _, c := range v {
			if s, ok := c.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range v {
			add(s)
		}
	}
	return out, true
}

// sessionAllowsCluster reports whether a session's CustomUserInfo covers the cluster.
func (repman *ReplicationManager) sessionAllowsCluster(info map[string]interface{}, clusterName string) bool {
	clusters, restricted := repman.sessionClusters(info)
	return !restricted || slices.Contains(clusters, clusterName)
}

// requestSessionClusters is sessionClusters of the request's session JWT: (nil, false) for an
// instance-wide session, an API token or no session.
func (repman *ReplicationManager) requestSessionClusters(r *http.Request) ([]string, bool) {
	info, ok := jwtUserInfo(r)
	if !ok {
		return nil, false
	}
	return repman.sessionClusters(info)
}

// requestOwnerScope is the cluster restriction of the request's credential: a scoped session's
// clusters, or the login clusters of an API token created from a scoped session
// (OwnerScope "clusters", already bound by parseVerifiedAPIToken). (nil, false): unrestricted.
func (repman *ReplicationManager) requestOwnerScope(r *http.Request) ([]string, bool) {
	if t, ok := repman.parseAPITokenFromRequest(r); ok {
		if t.OwnerScope == apiTokenOwnerClusters {
			return slices.Clone(t.OwnerClusters), true
		}
		return nil, false
	}
	return repman.requestSessionClusters(r)
}

// requestUnrestricted reports whether the request's credential covers the instance: what an
// instance-wide action (global settings, cluster import, peers, interventions, the
// active/standby switch, rotating an account of the instance's list) requires, whatever grant
// the account holds on the clusters of a scoped login.
func (repman *ReplicationManager) requestUnrestricted(r *http.Request) bool {
	_, scoped := repman.requestOwnerScope(r)
	return !scoped
}

// tokenVisibleToScope reports whether a scoped caller may see, display or revoke token t as its
// owner: only a token created from a login restricted to clusters all within the caller's.
// An unrestricted caller sees every token of its own.
func tokenVisibleToScope(t APIToken, clusters []string, scoped bool) bool {
	if !scoped {
		return true
	}
	if t.OwnerScope != apiTokenOwnerClusters || len(t.OwnerClusters) == 0 {
		return false
	}
	for _, c := range t.OwnerClusters {
		if !slices.Contains(clusters, c) {
			return false
		}
	}
	return true
}

// clusterDeletedSince reports whether a cluster of this name was deleted at or after the unix
// time in milliseconds at, the scope_at claim (DeleteCluster records it, markClusterDeleted).
func (repman *ReplicationManager) clusterDeletedSince(name string, at int64) bool {
	v, ok := repman.clusterDeletedAt.Load(name)
	if !ok {
		return false
	}
	return v.(time.Time).UnixMilli() >= at
}

// markClusterDeleted records the deletion of a cluster for the scoped sessions
// (clusterDeletedSince) and revokes the API tokens bound to a login on it
// (OwnerClusters). Records older than a session's lifetime are pruned: no session issued
// before them is still valid (F4).
func (repman *ReplicationManager) markClusterDeleted(name string) {
	now := time.Now()
	horizon := now.Add(-time.Hour * time.Duration(repman.Conf.TokenTimeout+1))
	repman.clusterDeletedAt.Range(func(k, v interface{}) bool {
		if v.(time.Time).Before(horizon) {
			repman.clusterDeletedAt.Delete(k)
		}
		return true
	})
	repman.clusterDeletedAt.Store(name, now)
	if n, err := repman.revokeAPITokensBoundTo(name, "deletion of cluster "+name); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Revoke the API tokens bound to deleted cluster %s: %s", name, err)
	} else if n > 0 {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Revoked %d API token(s) bound to deleted cluster %s", n, name)
	}
}

// userInfoMapSessionClusters is the restriction GetUserInfoMap exposes (SessionScope,
// SessionClusters) for the handlers that read the claims map.
func userInfoMapSessionClusters(m map[string]string) ([]string, bool) {
	if m["SessionScope"] != sessionScopeClusters {
		return nil, false
	}
	if m["SessionClusters"] == "" {
		return []string{}, true
	}
	return strings.Split(m["SessionClusters"], ","), true
}

// isAdminUserOn is isAdminUser on the given clusters only (restricted), or every cluster.
func (repman *ReplicationManager) isAdminUserOn(username string, clusters []string, restricted bool) bool {
	if !restricted {
		return repman.isAdminUser(username)
	}
	for _, name := range clusters {
		if cl := repman.getClusterByName(name); cl != nil {
			if u, ok := cl.GetAPIUsers()[username]; ok && u.Grants[config.GrantGlobalAdminShow] {
				return true
			}
		}
	}
	return false
}

// ssoLoginEmail is the login name of an SSO password login when it is an email address (the
// Cloud18 login is by email, and the realm authenticated it), "" otherwise.
func ssoLoginEmail(login string) string {
	if a, err := mail.ParseAddress(login); err == nil && a.Address == login {
		return login
	}
	return ""
}

// passwordSessionInfo is the CustomUserInfo of a session opened for user with password without
// a login form (autologin, dashboard): scoped like a login (localSessionScope) to the clusters
// where that password is the user's password.
func (repman *ReplicationManager) passwordSessionInfo(user, password string) interface{} {
	var matched []string
	if password != "" {
		for _, cl := range repman.clustersSnapshot() {
			if u, ok := cl.GetAPIUsers()[user]; ok && u.Password != "" && subtle.ConstantTimeCompare([]byte(u.Password), []byte(password)) == 1 {
				matched = append(matched, cl.Name)
			}
		}
	}
	scope, clusters := repman.localSessionScope(user, password, matched)
	var at int64
	if scope != "" {
		at = time.Now().UnixMilli()
	}
	return struct {
		Name     string
		Role     string
		Password string
		Scope    string   `json:"scope,omitempty"`
		Clusters []string `json:"clusters,omitempty"`
		ScopeAt  int64    `json:"scope_at,omitempty"`
	}{user, "Member", repman.Conf.GetEncryptedString(password), scope, clusters, at}
}
