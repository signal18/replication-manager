// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// Cluster scope of a local session (doc/implementation/server/SESSION_CLUSTER_SCOPE.md).
//
// The same account name can carry a different password on each cluster (a cluster's own list,
// api-credentials-external). A local login authenticates the password it was given, so it
// authenticates the account on the clusters where that password is the account's password, and
// nowhere else. Two cases:
//   - an account of the instance's list (api-credentials of the server configuration) logged in
//     with that list's password is the instance's account: its session covers the instance (the
//     instance-wide actions included), except the clusters whose section sets another password
//     for the account (claim `excluded`);
//   - any other local login is scoped to the clusters its password opened (claims
//     scope = "clusters", `clusters`, `scope_at`), and reaches no instance-wide action.
//
// Every check that resolves the session's identity by its name (IsValidClusterACL, loginACLUser
// and so UserHasGlobalGrant, the MCP principal, the peer login, the terminal, the API tokens it
// creates) applies it. A session without these claims covers the instance: the RSA key that
// signs the sessions is generated at each start, so every session of a running instance was
// issued by this code, and the issuers that set none are the instance-wide ones (SSO, the
// service logins).

// sessionScopeClusters is the value of the `scope` claim of a session restricted to `clusters`.
const sessionScopeClusters = "clusters"

// sessionScopeClaims are the scope claims of a local session, embedded in its CustomUserInfo.
type sessionScopeClaims struct {
	Scope    string   `json:"scope,omitempty"`
	Clusters []string `json:"clusters,omitempty"`
	Excluded []string `json:"excluded,omitempty"`
	ScopeAt  int64    `json:"scope_at,omitempty"` // unix ms: a cluster deleted since leaves `clusters`
}

// loginScope is what a login (a session, or the login an API token was created from) covers.
type loginScope struct {
	Scoped   bool     // restricted to Clusters, no instance-wide action
	Clusters []string // Scoped: the clusters covered
	Excluded []string // not Scoped: the clusters not covered
}

// Covers reports whether the login covers the cluster.
func (s loginScope) Covers(name string) bool {
	if s.Scoped {
		return slices.Contains(s.Clusters, name)
	}
	return !slices.Contains(s.Excluded, name)
}

// Limited reports whether some cluster is out of the login.
func (s loginScope) Limited() bool {
	return s.Scoped || len(s.Excluded) > 0
}

// String says what the login is limited to, for a refusal.
func (s loginScope) String() string {
	if s.Scoped {
		return "this login is limited to clusters " + strings.Join(s.Clusters, ", ")
	}
	if len(s.Excluded) > 0 {
		return "this login does not cover clusters " + strings.Join(s.Excluded, ", ")
	}
	return "this login covers the instance"
}

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

// localSessionScope returns the scope claims of a local login whose password opened the
// clusters matched. An account of the instance's list with that list's password covers the
// instance, minus the clusters where the account exists with another password (logged); any
// other login is scoped to matched (sorted).
func (repman *ReplicationManager) localSessionScope(user, password string, matched []string) sessionScopeClaims {
	if repman.isInstanceCredential(user, password) {
		var excluded []string
		for _, cl := range repman.clustersSnapshot() {
			if _, ok := cl.GetAPIUsers()[user]; ok && !slices.Contains(matched, cl.Name) {
				excluded = append(excluded, cl.Name)
			}
		}
		if len(excluded) > 0 {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Login of %s with the instance's password: clusters %s excluded from the session, their section sets another password for the account",
				user, strings.Join(excluded, ", "))
		}
		return sessionScopeClaims{Excluded: excluded}
	}
	out := slices.Clone(matched)
	slices.Sort(out)
	return sessionScopeClaims{Scope: sessionScopeClusters, Clusters: out, ScopeAt: time.Now().UnixMilli()}
}

// claimStrings reads a list claim of a decoded JWT.
func claimStrings(v interface{}) []string {
	out := []string{}
	switch l := v.(type) {
	case []interface{}:
		for _, c := range l {
			if s, ok := c.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range l {
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// sessionScope reads the scope of a session's CustomUserInfo. A scoped session with an
// unreadable list covers no cluster. A cluster deleted after the login (clusterDeletedSince)
// leaves a scoped session's list: a cluster created again under the same name is another
// cluster, which the login never authenticated.
func (repman *ReplicationManager) sessionScope(info map[string]interface{}) loginScope {
	if s, _ := info["scope"].(string); s != sessionScopeClusters {
		return loginScope{Excluded: claimStrings(info["excluded"])}
	}
	var at int64
	switch v := info["scope_at"].(type) {
	case float64:
		at = int64(v)
	case int64:
		at = v
	}
	out := []string{}
	for _, c := range claimStrings(info["clusters"]) {
		if !repman.clusterDeletedSince(c, at) {
			out = append(out, c)
		}
	}
	return loginScope{Scoped: true, Clusters: out}
}

// requestSessionScope is the scope of the request's session JWT (zero: no session, or an API
// token, which requestLoginScope reads).
func (repman *ReplicationManager) requestSessionScope(r *http.Request) loginScope {
	info, ok := jwtUserInfo(r)
	if !ok {
		return loginScope{}
	}
	return repman.sessionScope(info)
}

// tokenLoginScope is the scope of the login an API token was created from.
func tokenLoginScope(t *APIToken) loginScope {
	if t.OwnerScope == apiTokenOwnerClusters {
		return loginScope{Scoped: true, Clusters: slices.Clone(t.OwnerClusters)}
	}
	return loginScope{Excluded: slices.Clone(t.OwnerExcluded)}
}

// requestLoginScope is the scope of the request's credential: its session's, or the login an
// API token was created from.
func (repman *ReplicationManager) requestLoginScope(r *http.Request) loginScope {
	if t, ok := repman.parseAPITokenFromRequest(r); ok {
		return tokenLoginScope(t)
	}
	return repman.requestSessionScope(r)
}

// requestUnrestricted reports whether the request's credential covers the instance: what an
// instance-wide action (global settings and views, cluster import, peers, interventions, the
// active/standby switch, Cloud18 registration, rotating an account of the instance's list)
// requires, whatever grant the account holds on the clusters of a scoped login.
func (repman *ReplicationManager) requestUnrestricted(r *http.Request) bool {
	return !repman.requestLoginScope(r).Scoped
}

// requireUnrestricted answers 403 to a scoped credential on an instance-wide action, saying
// what the login is limited to, and logs it; true when the request may go on.
func (repman *ReplicationManager) requireUnrestricted(w http.ResponseWriter, r *http.Request, action string) bool {
	s := repman.requestLoginScope(r)
	if !s.Scoped {
		return true
	}
	repman.logSecurityEvent("api_scope_denied", repman.GetUserFromRequest(r), r.RemoteAddr, action+" refused: "+s.String())
	http.Error(w, fmt.Sprintf("%s: %s needs a login covering the instance", s, action), http.StatusForbidden)
	return false
}

// tokenCovers reports whether token t acts on the cluster.
func tokenCovers(t APIToken, name string) bool {
	if !t.InScope(name) {
		return false
	}
	return t.OwnerScope != apiTokenOwnerClusters || slices.Contains(t.OwnerClusters, name)
}

// tokenVisibleToScope reports whether a caller with login scope s may see, display or revoke
// token t as its owner: a token acting on no cluster out of the caller's login. A caller whose
// login covers every cluster sees every token of its own.
func tokenVisibleToScope(t APIToken, s loginScope) bool {
	if !s.Limited() {
		return true
	}
	if s.Scoped {
		if t.OwnerScope != apiTokenOwnerClusters || len(t.OwnerClusters) == 0 {
			return false
		}
		for _, c := range t.OwnerClusters {
			if !slices.Contains(s.Clusters, c) {
				return false
			}
		}
		return true
	}
	for _, e := range s.Excluded {
		if tokenCovers(t, e) {
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

// markClusterDeleted records that a cluster name stopped naming the cluster it named (deleted,
// renamed, gone from the configuration at a restart) for the scoped sessions
// (clusterDeletedSince), and revokes the API tokens bound to a login on it (OwnerClusters).
// Records older than a session's lifetime are pruned: no session issued before them is still
// valid (F4).
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
	if n, err := repman.revokeAPITokensBoundTo(name, "cluster "+name+" deleted or renamed"); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Revoke the API tokens bound to cluster %s: %s", name, err)
	} else if n > 0 {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Revoked %d API token(s) bound to cluster %s, deleted or renamed", n, name)
	}
}

// revokeTokensOfVanishedClusters revokes the API tokens bound to a login on a cluster that no
// longer exists (dropped from the configuration while repman was stopped): run once the
// clusters are loaded.
func (repman *ReplicationManager) revokeTokensOfVanishedClusters() {
	known := map[string]bool{}
	for _, cl := range repman.clustersSnapshot() {
		known[cl.Name] = true
	}
	for _, name := range repman.apiTokenOwnerClusterNames() {
		if !known[name] {
			repman.markClusterDeleted(name)
		}
	}
}

// userInfoMapScope is the scope GetUserInfoMap exposes (SessionScope, SessionClusters,
// SessionExcluded) for the handlers that read the claims map.
func userInfoMapScope(m map[string]string) loginScope {
	split := func(v string) []string {
		if v == "" {
			return []string{}
		}
		return strings.Split(v, ",")
	}
	if m["SessionScope"] == sessionScopeClusters {
		return loginScope{Scoped: true, Clusters: split(m["SessionClusters"])}
	}
	if m["SessionExcluded"] != "" {
		return loginScope{Excluded: split(m["SessionExcluded"])}
	}
	return loginScope{}
}

// isAdminUserOn is isAdminUser on the clusters the login covers.
func (repman *ReplicationManager) isAdminUserOn(username string, s loginScope) bool {
	for _, cl := range repman.clustersSnapshot() {
		if !s.Covers(cl.Name) {
			continue
		}
		if u, ok := cl.GetAPIUsers()[username]; ok && u.Grants[config.GrantGlobalAdminShow] {
			return true
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

// localSessionInfo is the CustomUserInfo of a local session.
type localSessionInfo struct {
	Name     string
	Role     string
	Password string
	sessionScopeClaims
}

// passwordSessionInfo is the CustomUserInfo of a session opened for user with password without
// a login form (autologin, dashboard, the regtest): scoped like a login (localSessionScope) to
// the clusters where that password is the user's password.
func (repman *ReplicationManager) passwordSessionInfo(user, password string) localSessionInfo {
	var matched []string
	if password != "" {
		for _, cl := range repman.clustersSnapshot() {
			if u, ok := cl.GetAPIUsers()[user]; ok && u.Password != "" && subtle.ConstantTimeCompare([]byte(u.Password), []byte(password)) == 1 {
				matched = append(matched, cl.Name)
			}
		}
	}
	return localSessionInfo{user, "Member", repman.Conf.GetEncryptedString(password), repman.localSessionScope(user, password, matched)}
}

// mayLoginPeersAsInstance reports whether a request may log in to another Cloud18 instance as
// this instance's registered identity (the peer login proxy): an interactive local login
// (never an API token, AuthType Token) covering the instance (never a login scoped to some
// clusters) of an account holding global-admin-show on a cluster it covers, on a registered
// instance.
func (repman *ReplicationManager) mayLoginPeersAsInstance(r *http.Request, uinfomap map[string]string) bool {
	return repman.Conf.Cloud18GitUser != "" && uinfomap["AuthType"] == "Local" && repman.requestUnrestricted(r) &&
		repman.isAdminUserOn(uinfomap["User"], userInfoMapScope(uinfomap))
}

// mcpMayActAsInstance is mayLoginPeersAsInstance for an MCP principal: a password login (never
// an API token, never SSO) covering the instance.
func (repman *ReplicationManager) mcpMayActAsInstance(p *repmanmcp.Principal) bool {
	return p != nil && p.AuthMethod == "password" && !p.SessionScoped &&
		repman.isAdminUserOn(p.User, loginScope{Excluded: p.SessionExcluded})
}
