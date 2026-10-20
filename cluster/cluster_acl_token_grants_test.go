package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// An explicit main ACL written before the token grants existed (#2083): it stays as
// written, WARN0251 says nobody can issue a token, and a discard of the compact name
// token removes both token grants.
func TestAPITokenIssuersWithExplicitACL(t *testing.T) {
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
	if c.APIUsers["admin"].Grants[config.GrantTokenCreate] {
		t.Fatal("an explicit ACL without token stays as written: no implied grant")
	}
	if !c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("WARN0251 must open when nobody holds token-create")
	}

	c = load(true, explicit+" token", "")
	if g := c.APIUsers["admin"].Grants; !g[config.GrantTokenCreate] || !g[config.GrantTokenManage] {
		t.Fatal("token in the allow list gives both token grants")
	}
	if c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("WARN0251 while admin can issue tokens")
	}

	c = load(true, explicit+" token", "admin:token")
	if g := c.APIUsers["admin"].Grants; g[config.GrantTokenCreate] || g[config.GrantTokenManage] {
		t.Fatal("a discard of the compact name token removes both token grants")
	}
	if !c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("WARN0251 when the discard removes the only issuer")
	}

	c = load(false, explicit, "")
	if c.ConfigStateMachine.IsInState("WARN0251") {
		t.Fatal("api-user-tokens off: no state")
	}
}

// A full grant name discards that grant only; a compact name is a prefix.
func TestDiscardUserGrants(t *testing.T) {
	c := newTestClusterForUsers(t)
	u := APIUser{Grants: map[string]bool{}}
	c.SetUserGrants(&u, "cluster db")
	c.DiscardUserGrants(&u, "cluster-checksum db")
	if u.Grants[config.GrantClusterChecksum] || !u.Grants[config.GrantClusterChecksumRepair] {
		t.Fatalf("cluster-checksum is a full name: only it goes (repair %v)", u.Grants[config.GrantClusterChecksumRepair])
	}
	if u.Grants[config.GrantDBStart] || u.Grants[config.GrantDBShowVariables] {
		t.Fatal("db is a prefix: every db-* grant goes")
	}
	if !u.Grants[config.GrantClusterSwitchover] {
		t.Fatal("grants outside the discard stay")
	}
}

// The edges of a discard list (#2084 review): empty or blank, an unknown name, a nil grants
// map; and a prefix that removes held grants says so in the security log, once per entry.
func TestDiscardUserGrantsEdges(t *testing.T) {
	c := newTestClusterForUsers(t)
	// the security log is a fixed-size ring: count the discard lines in it
	discards := func() int {
		n := 0
		for _, m := range c.LogSecurity.Buffer {
			if strings.Contains(m.Text, "discard ") {
				n++
			}
		}
		return n
	}

	u := APIUser{User: "admin", Grants: map[string]bool{}}
	c.SetUserGrants(&u, "cluster db")
	c.DiscardUserGrants(&u, "")
	c.DiscardUserGrants(&u, "   ")
	c.DiscardUserGrants(&u, "no-such-grant")
	if !u.Grants[config.GrantDBStart] || !u.Grants[config.GrantClusterSwitchover] {
		t.Fatal("an empty, blank or unknown discard removes nothing")
	}
	if discards() != 0 {
		t.Fatal("a discard that removes nothing logs nothing")
	}

	nilMap := APIUser{User: "x"}
	c.DiscardUserGrants(&nilMap, "db")
	if nilMap.Grants == nil {
		t.Fatal("a nil grants map is created, not dereferenced")
	}

	c.DiscardUserGrants(&u, "db")
	if u.Grants[config.GrantDBStart] {
		t.Fatal("db removes every db-* grant")
	}
	found := false
	for _, m := range c.LogSecurity.Buffer {
		if strings.Contains(m.Text, `discard "db" removes the grants [`) && strings.Contains(m.Text, config.GrantDBStart) {
			found = true
		}
	}
	if !found || discards() != 1 {
		t.Fatalf("the prefix removal is in the security log, once (%d discard lines)", discards())
	}
}
