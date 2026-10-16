package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// An explicit main ACL written before the token grants existed (#2083): the grant
// administrator gets them while api-user-tokens is on, a main discard removes them,
// and WARN0251 opens when nobody can issue a token.
func TestAPITokenGrantsWithExplicitACL(t *testing.T) {
	load := func(tokens bool, allow, discard string) *Cluster {
		c := newTestClusterForUsers(t)
		c.ConfigStateMachine = new(state.StateMachine)
		c.ConfigStateMachine.Init()
		c.Conf.APIUserTokens = tokens
		c.Conf.APIUsersACLAllow = allow
		c.Conf.APIUsersACLDiscard = discard
		c.LoadAPIUsers()
		c.checkAPITokenIssuers()
		return c
	}
	explicit := "admin:cluster db proxy app prov global grant show sale extrole terminal"

	c := load(true, explicit, "")
	if g := c.APIUsers["admin"].Grants; !g[config.GrantTokenCreate] || !g[config.GrantTokenManage] {
		t.Fatalf("the grant administrator must hold the token grants: %v %v", g[config.GrantTokenCreate], g[config.GrantTokenManage])
	}
	if c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("WARN0251 while admin can issue tokens")
	}

	c = load(true, explicit, "admin:token-create token-manage")
	if c.APIUsers["admin"].Grants[config.GrantTokenCreate] {
		t.Fatal("a main discard of the token grants must win")
	}
	if !c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("WARN0251 must open when nobody can issue a token")
	}

	c = load(false, explicit, "")
	if c.APIUsers["admin"].Grants[config.GrantTokenCreate] || c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("api-user-tokens off: no implied grant, no state")
	}

	c = load(true, "admin:cluster db show", "")
	if c.APIUsers["admin"].Grants[config.GrantTokenCreate] {
		t.Fatal("a user who does not administer grants gets no implied token grant")
	}
}
