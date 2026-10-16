package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newTokenTestCluster(name string) *Cluster {
	c := &Cluster{
		Name:     name,
		Conf:     &config.Config{},
		APIUsers: map[string]APIUser{},
		Grants:   config.GetGrantType(),
	}
	alice := APIUser{User: "alice", Password: "x", Roles: map[string]bool{"dbops": true}}
	c.SetUserGrants(&alice, "db-show cluster-switchover")
	c.APIUsers["alice"] = alice
	return c
}

func TestTokenPrincipalNameRoundTrip(t *testing.T) {
	name := TokenPrincipalName("abc123", "alice@example.com")
	id, user, ok := ParseTokenPrincipal(name)
	if !ok || id != "abc123" || user != "alice@example.com" {
		t.Fatalf("parse %q: id=%q user=%q ok=%v", name, id, user, ok)
	}
	if _, _, ok := ParseTokenPrincipal("alice"); ok {
		t.Fatal("a bare user name must not parse as a token principal")
	}
	if _, _, ok := ParseTokenPrincipal("token:abc"); ok {
		t.Fatal("a principal without user must not parse")
	}
}

func TestGetACLUserIntersectsTokenAndOwnerGrants(t *testing.T) {
	c := newTokenTestCluster("c1")
	tok := APIUser{Grants: map[string]bool{}}
	// Token asks for db-show-* and cluster-failover: failover is not held by alice.
	c.SetUserGrants(&tok, "db-show cluster-failover")
	c.SetTokenPrincipal(TokenPrincipal{ID: "t1", User: "alice", Grants: tok.Grants, Clusters: []string{"*"}})

	eff, ok := c.GetACLUser(TokenPrincipalName("t1", "alice"))
	if !ok {
		t.Fatal("token principal must resolve")
	}
	if !eff.Grants[config.GrantDBShowVariables] {
		t.Error("db-show-variables is held by both owner and token: must be granted")
	}
	if eff.Grants[config.GrantClusterSwitchover] {
		t.Error("cluster-switchover is held by owner but NOT embedded in the token: must be denied")
	}
	if eff.Grants[config.GrantClusterFailover] {
		t.Error("cluster-failover is embedded in the token but NOT held by owner: must be denied")
	}
	if !eff.Roles["dbops"] {
		t.Error("roles are inherited from the owner")
	}
	// Owner loses db-show: the token loses it too, at once.
	alice := c.APIUsers["alice"]
	alice.Grants[config.GrantDBShowVariables] = false
	c.APIUsers["alice"] = alice
	eff, _ = c.GetACLUser(TokenPrincipalName("t1", "alice"))
	if eff.Grants[config.GrantDBShowVariables] {
		t.Error("a grant dropped from the owner must drop from the token")
	}
}

func TestGetACLUserRejectsUnknownMismatchedOrOutOfScope(t *testing.T) {
	c := newTokenTestCluster("c1")
	if _, ok := c.GetACLUser(TokenPrincipalName("nope", "alice")); ok {
		t.Error("unregistered token must not resolve")
	}
	c.SetTokenPrincipal(TokenPrincipal{ID: "t2", User: "bob", Grants: map[string]bool{}, Clusters: []string{"*"}})
	if _, ok := c.GetACLUser(TokenPrincipalName("t2", "alice")); ok {
		t.Error("principal name user must match the registered token owner")
	}
	if _, ok := c.GetACLUser(TokenPrincipalName("t2", "bob")); ok {
		t.Error("owner unknown on this cluster must not resolve")
	}
	c.SetTokenPrincipal(TokenPrincipal{ID: "t3", User: "alice", Grants: map[string]bool{}, Clusters: []string{"other"}})
	if _, ok := c.GetACLUser(TokenPrincipalName("t3", "alice")); ok {
		t.Error("token scoped to another cluster must not resolve here")
	}
	c.DropTokenPrincipal("t3")
	if _, ok := c.GetTokenPrincipal("t3"); ok {
		t.Error("dropped principal must be gone")
	}
	// Bare user names keep resolving through the same call.
	if u, ok := c.GetACLUser("alice"); !ok || u.User != "alice" {
		t.Error("bare user must resolve to the APIUsers entry")
	}
}

func TestIsValidACLTokenMethodRunsURLACLOnly(t *testing.T) {
	c := newTokenTestCluster("c1")
	tok := APIUser{Grants: map[string]bool{}}
	c.SetUserGrants(&tok, "db-show")
	c.SetTokenPrincipal(TokenPrincipal{ID: "t1", User: "alice", Grants: tok.Grants, Clusters: []string{"c1"}})
	principal := TokenPrincipalName("t1", "alice")

	// Public endpoint passes for any resolvable principal.
	if !c.IsValidACL(principal, "", "/api/clusters/c1", "token") {
		t.Error("token principal must pass a public endpoint")
	}
	// A bare user name with the token method is refused (no password check bypass).
	if c.IsValidACL("alice", "", "/api/clusters/c1", "token") {
		t.Error("token auth method must require a token principal name")
	}
	// Password auth with an empty password never passes.
	if c.IsValidACL("alice", "", "/api/clusters/c1", "password") {
		t.Error("blank password must never authenticate")
	}
}

func TestTokenGrantsAllowedFor(t *testing.T) {
	c := newTokenTestCluster("c1")
	allowed, missing := c.TokenGrantsAllowedFor("alice", "db-show cluster-failover")
	if len(missing) != 1 || missing[0] != "cluster-failover" {
		t.Errorf("cluster-failover is not held: missing=%v", missing)
	}
	if !allowed[config.GrantDBShowVariables] || allowed[config.GrantClusterFailover] {
		t.Errorf("allowed set wrong: %v", allowed)
	}
	if _, missing := c.TokenGrantsAllowedFor("nobody", "db"); len(missing) != 1 {
		t.Error("unknown owner holds nothing")
	}
}
