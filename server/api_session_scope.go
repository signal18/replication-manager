// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"net/http"
	"slices"
	"strings"

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
// account of the instance's list with that list's password, else the clusters the password
// authenticated (sorted).
func (repman *ReplicationManager) localSessionScope(user, password string, matched []string) (string, []string) {
	if repman.isInstanceCredential(user, password) {
		return "", nil
	}
	out := slices.Clone(matched)
	slices.Sort(out)
	return sessionScopeClusters, out
}

// sessionClusters reads the cluster restriction of a session's CustomUserInfo: the clusters and
// true for a restricted session, (nil, false) for an instance-wide one. A restricted session
// with an unreadable list is restricted to no cluster.
func sessionClusters(info map[string]interface{}) ([]string, bool) {
	if s, _ := info["scope"].(string); s != sessionScopeClusters {
		return nil, false
	}
	out := []string{}
	switch v := info["clusters"].(type) {
	case []interface{}:
		for _, c := range v {
			if s, ok := c.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out, true
}

// sessionAllowsCluster reports whether a session's CustomUserInfo covers the cluster.
func sessionAllowsCluster(info map[string]interface{}, clusterName string) bool {
	clusters, restricted := sessionClusters(info)
	return !restricted || slices.Contains(clusters, clusterName)
}

// requestSessionClusters is sessionClusters of the request's session JWT: (nil, false) for an
// instance-wide session, an API token or no session.
func requestSessionClusters(r *http.Request) ([]string, bool) {
	info, ok := jwtUserInfo(r)
	if !ok {
		return nil, false
	}
	return sessionClusters(info)
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
