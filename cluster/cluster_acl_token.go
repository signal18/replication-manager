// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
)

// API-token principals (issue #1835).
//
// An API token is a bearer credential a user issues for themselves, carrying a
// subset of their own grants and a cluster scope. At request time the server
// registers the token's authority on the cluster as a TokenPrincipal and runs the
// ACL under a principal NAME of the form "token:<id>:<user>" instead of the bare
// username. Every grant lookup in the ACL code goes through GetACLUser, which
// resolves that name to a transient APIUser whose grants are the INTERSECTION of
// the token's grants and the owner's CURRENT grants in this cluster: a token can
// never do more than its owner can do right now, and dropping a grant from the
// owner drops it from every token at once. The owner's roles are inherited.
//
// The principal registry is a plain sync.Map on the Cluster, re-populated by the
// server on every token request, so a cluster rebuilt by a config reload simply
// starts empty and is refilled by the next call (no lifecycle coupling).

// TokenPrincipalPrefix marks an ACL user name that denotes an API token.
const TokenPrincipalPrefix = "token:"

// TokenPrincipal is the request-time authority of an API token.
type TokenPrincipal struct {
	ID       string
	User     string
	Grants   map[string]bool // expanded grant map the token carries
	Clusters []string        // cluster scope; "*" means every cluster
}

// InScope reports whether the token may act on clusterName.
func (p TokenPrincipal) InScope(clusterName string) bool {
	for _, c := range p.Clusters {
		if c == "*" || c == clusterName {
			return true
		}
	}
	return false
}

// IsGlobal reports whether the token scope covers every cluster ("*"), which
// endpoints outside a single cluster (global settings, peers) require.
func (p TokenPrincipal) IsGlobal() bool {
	for _, c := range p.Clusters {
		if c == "*" {
			return true
		}
	}
	return false
}

// TokenPrincipalName builds the ACL user name for a token: "token:<id>:<user>".
// The id comes first so a user name containing ':' (an email never does, but a
// custom name might) still parses: everything after the second ':' is the user.
func TokenPrincipalName(id, user string) string {
	return TokenPrincipalPrefix + id + ":" + user
}

// ParseTokenPrincipal splits a principal name back into token id and user name.
func ParseTokenPrincipal(name string) (id string, user string, ok bool) {
	if !strings.HasPrefix(name, TokenPrincipalPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, TokenPrincipalPrefix)
	i := strings.Index(rest, ":")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// IsTokenPrincipal reports whether an ACL user name denotes an API token.
func IsTokenPrincipal(name string) bool {
	_, _, ok := ParseTokenPrincipal(name)
	return ok
}

// SetTokenPrincipal registers (or refreshes) a token's authority on this cluster.
func (cluster *Cluster) SetTokenPrincipal(p TokenPrincipal) {
	cluster.apiTokenPrincipals.Store(p.ID, p)
}

// DropTokenPrincipal forgets a token's authority (revocation, expiry).
func (cluster *Cluster) DropTokenPrincipal(id string) {
	cluster.apiTokenPrincipals.Delete(id)
}

// GetTokenPrincipal returns the registered authority of a token id.
func (cluster *Cluster) GetTokenPrincipal(id string) (TokenPrincipal, bool) {
	v, ok := cluster.apiTokenPrincipals.Load(id)
	if !ok {
		return TokenPrincipal{}, false
	}
	p, ok := v.(TokenPrincipal)
	return p, ok
}

// GetACLUser resolves the APIUser view the ACL checks run against. A bare user
// name resolves to cluster.APIUsers; a token principal name resolves to a transient
// APIUser carrying the intersection of the token's grants and the owner's current
// grants in this cluster. Unknown user, unknown token, owner mismatch or a token
// scoped to other clusters all resolve to (APIUser{}, false).
func (cluster *Cluster) GetACLUser(strUser string) (APIUser, bool) {
	id, user, isToken := ParseTokenPrincipal(strUser)
	if !isToken {
		u, ok := cluster.APIUsers[strUser]
		return u, ok
	}
	p, ok := cluster.GetTokenPrincipal(id)
	if !ok || p.User != user {
		return APIUser{}, false
	}
	owner, ok := cluster.APIUsers[user]
	if !ok {
		return APIUser{}, false
	}
	if !p.InScope(cluster.Name) {
		return APIUser{}, false
	}
	eff := APIUser{
		User:       owner.User,
		IsExternal: owner.IsExternal,
		Roles:      owner.Roles,
		Grants:     make(map[string]bool, len(owner.Grants)),
	}
	for g, v := range owner.Grants {
		eff.Grants[g] = v && p.Grants[g]
	}
	return eff, true
}

// TokenGrantsAllowedFor returns, from a compact grant list a user requests for a
// token (e.g. "db-show proxy cluster-switchover"), the expanded grants the owner
// actually holds in this cluster, and the requested prefixes that matched none of
// the owner's grants here. Used at token creation so a token can only narrow the
// owner's authority, never extend it.
func (cluster *Cluster) TokenGrantsAllowedFor(owner string, requested string) (allowed map[string]bool, missing []string) {
	allowed = map[string]bool{}
	ownerUser, ok := cluster.APIUsers[owner]
	if !ok {
		return allowed, strings.Fields(requested)
	}
	for _, prefix := range strings.Fields(requested) {
		matched := false
		for grant, held := range ownerUser.Grants {
			if strings.HasPrefix(grant, prefix) && held {
				allowed[grant] = true
				matched = true
			}
		}
		if !matched {
			missing = append(missing, prefix)
		}
	}
	return allowed, missing
}
