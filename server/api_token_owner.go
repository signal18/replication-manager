// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// Owner checks of an API token at authentication (doc/implementation/server/SESSION_CLUSTER_SCOPE.md).
//
// The store record says how the owner logged in when the token was issued:
//   - OwnerScope "instance": a login covering the instance (an account of the instance's list,
//     an SSO identity, repman itself for an app token). The token keeps the owner's current
//     grants on every cluster of its scope, as before.
//   - OwnerScope "clusters": a local login whose password authenticated the owner on some
//     clusters only. The token is bound to them (APIToken.boundToOwnerClusters).
//   - no OwnerScope: a token issued before the field existed. Nothing tells which password
//     its login used, so it is kept only when that cannot matter (apiTokenLegacyOwnerOK).
//
// An SSO owner is then asked to the upstream identity check (apiTokenSSOOwnerActive), when
// one is wired.

// apiTokenOwnerOK reports whether the owner of a verified token may still use it.
func (repman *ReplicationManager) apiTokenOwnerOK(t *APIToken, r *http.Request) bool {
	if t.OwnerScope == "" && !repman.apiTokenLegacyOwnerOK(t) {
		repman.logAPITokenOwnerRefusal(t, r, "issued before logins carried their cluster scope, by an account whose password differs between clusters: log in with the credentials of the cluster and create a new token")
		return false
	}
	if repman.apiTokenOwnerIsSSO(t) && !repman.apiTokenSSOOwnerIsActive(t.User) {
		repman.logAPITokenOwnerRefusal(t, r, "its SSO owner is no longer active")
		return false
	}
	return true
}

// apiTokenLegacyOwnerOK judges a token issued before OwnerScope existed. It keeps today's
// behaviour (the owner's current grants on every cluster of its scope) only when the login
// that issued it necessarily covered all of them:
//   - an SSO owner (OwnerAuthType "SSO"): an SSO identity is one identity for the instance;
//   - a token repman issued itself for an app (CreatedFrom "app ...", never a client
//     address): scoped to the app's cluster, owned by the cluster's admin account;
//   - a local owner whose password is the same on every cluster it exists on: whatever
//     password the login used, it was valid on each of them. An account of the instance's
//     list is such an owner unless a cluster section sets another password for it.
//
// A local owner holding different passwords on different clusters (a shared account with a
// password per cluster) is refused: its token may come from a login valid on one cluster only.
func (repman *ReplicationManager) apiTokenLegacyOwnerOK(t *APIToken) bool {
	if repman.apiTokenOwnerIsSSO(t) {
		return true
	}
	if strings.HasPrefix(t.CreatedFrom, "app ") {
		return true
	}
	return repman.ownerPasswordUniform(t.User)
}

// apiTokenOwnerIsSSO reports whether a token's owner is an SSO identity: recorded so
// (OwnerAuthType "SSO"), or, for a token issued before OwnerScope existed, an owner without a
// local password on every cluster it exists on. Releases of late September 2026 issued tokens
// without OwnerAuthType; a passwordless account can only have logged in through SSO.
func (repman *ReplicationManager) apiTokenOwnerIsSSO(t *APIToken) bool {
	if t.OwnerAuthType == "SSO" {
		return true
	}
	return t.OwnerScope == "" && t.OwnerAuthType == "" && repman.ownerPasswordless(t.User)
}

// ownerPasswordless reports whether user exists on at least one cluster and has no local
// password on any.
func (repman *ReplicationManager) ownerPasswordless(user string) bool {
	found := false
	for _, cl := range repman.clustersSnapshot() {
		u, ok := cl.GetAPIUsers()[user]
		if !ok {
			continue
		}
		if u.Password != "" {
			return false
		}
		found = true
	}
	return found
}

// ownerPasswordUniform reports whether user has one and the same non-empty password on every
// cluster it exists on.
func (repman *ReplicationManager) ownerPasswordUniform(user string) bool {
	password := ""
	for _, cl := range repman.Clusters {
		u, ok := cl.GetAPIUsers()[user]
		if !ok {
			continue
		}
		if u.Password == "" {
			return false
		}
		if password == "" {
			password = u.Password
		} else if subtle.ConstantTimeCompare([]byte(password), []byte(u.Password)) != 1 {
			return false
		}
	}
	return password != ""
}

// apiTokenSSOOwnerIsActive asks whether an SSO token owner is still active upstream (not
// disabled in the Cloud18 realm). apiTokenSSOOwnerActive is wired at start to a cache the
// CRM refreshes off the request path (api_token_owner_upstream.go); nil (tests, or before
// start) means active.
func (repman *ReplicationManager) apiTokenSSOOwnerIsActive(owner string) bool {
	if repman.apiTokenSSOOwnerActive == nil {
		return true
	}
	return repman.apiTokenSSOOwnerActive(owner)
}

// logAPITokenOwnerRefusal writes the refusal of a token to the security log once per token
// (a client retrying would flood it). The set holds ids of the token store and is pruned with
// it (saveAPITokenStoreLocked): F4.
func (repman *ReplicationManager) logAPITokenOwnerRefusal(t *APIToken, r *http.Request, why string) {
	if _, seen := repman.apiTokenOwnerRefusedLogged.LoadOrStore(t.ID, true); seen {
		return
	}
	remote := ""
	if r != nil {
		remote = r.RemoteAddr
	}
	repman.logSecurityEvent("api_token_denied", t.User, remote, fmt.Sprintf("API token %s (%s) refused: %s", t.ID, t.Label, why))
}
