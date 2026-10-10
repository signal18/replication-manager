package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

func newTestClusterForUsers(t *testing.T) *Cluster {
	t.Helper()
	c := &Cluster{Name: "c1", Conf: &config.Config{WorkingDir: t.TempDir()}}
	c.Grants = config.GetGrantType()
	c.Roles = config.GetRoleType()
	c.Conf.Secrets = map[string]config.Secret{
		"api-credentials":          {Value: "admin:pw"},
		"api-credentials-external": {},
	}
	c.Log = s18log.NewHttpLog(50)
	c.LogSecurity = s18log.NewHttpLog(50)
	return c
}

// The cluster-add sequence: the external ACL is blanked, then admin (from the main
// credentials, so without an entry of its own) is updated to sysops dbops. The
// update must survive the next LoadAPIUsers instead of leaving admin a visitor.
func TestUpdateUserAddsMissingEntry(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.LoadAPIUsers()
	if !c.APIUsers["admin"].Roles[config.RoleVisitor] {
		t.Fatalf("fixture: admin without an ACL entry starts a visitor: %+v", c.APIUsers["admin"].Roles)
	}

	if err := c.UpdateUser(UserForm{Username: "admin", Roles: "sysops dbops", Grants: "cluster db"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	if c.Conf.APIUsersACLAllowExternal != "admin:cluster db:c1:sysops dbops" {
		t.Fatalf("the entry must be appended, without an empty leading one: %q", c.Conf.APIUsersACLAllowExternal)
	}

	c.LoadAPIUsers()
	roles := c.APIUsers["admin"].Roles
	if roles[config.RoleVisitor] || !roles[config.RoleSysOps] || !roles[config.RoleDBOps] {
		t.Fatalf("admin must stay sysops dbops after a reload: %+v", roles)
	}
	if !c.APIUsers["admin"].Grants[config.GrantClusterSwitchover] {
		t.Fatal("admin must keep the cluster grants after a reload")
	}
}

// An existing entry is rewritten in place, the other entries are kept.
func TestUpdateUserRewritesExistingEntry(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Secrets["api-credentials-external"] = config.Secret{Value: "bob:pw2"}
	c.Conf.APIUsersACLAllowExternal = "admin:db:c1:dbops,bob:db:c1:dbops"
	c.LoadAPIUsers()

	if err := c.UpdateUser(UserForm{Username: "admin", Roles: "sysops", Grants: "cluster"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	if got := c.Conf.APIUsersACLAllowExternal; got != "admin:cluster:c1:sysops,bob:db:c1:dbops" {
		t.Fatalf("rewrite in place expected: %q", got)
	}
	if strings.Count(c.Conf.APIUsersACLAllowExternal, "admin:") != 1 {
		t.Fatal("admin must not get a second entry")
	}
}
