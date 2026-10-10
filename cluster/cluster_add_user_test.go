package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/sirupsen/logrus"
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

// A user of the main ACL (admin by default) keeps its main grants whatever the
// cluster entry says (strict override), while its roles come from the cluster
// entry: the main entry carries none, so the appended entry must not be skipped.
func TestUpdateUserMainACLUser(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show sale extrole terminal app token"
	c.LoadAPIUsers()

	if err := c.UpdateUser(UserForm{Username: "admin", Roles: "sysops dbops", Grants: "db"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	c.LoadAPIUsers()
	a := c.APIUsers["admin"]
	if !a.Grants[config.GrantClusterSwitchover] {
		t.Fatal("the main ACL grants must survive a narrower cluster entry")
	}
	if a.Roles[config.RoleVisitor] || !a.Roles[config.RoleSysOps] || !a.Roles[config.RoleDBOps] {
		t.Fatalf("the roles of the cluster entry must apply to a main ACL user: %+v", a.Roles)
	}
}

// An update without grants (what EndSubscription can send) writes user::cluster:roles
// and reloads as roles without grants.
func TestUpdateUserEmptyGrants(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Secrets["api-credentials-external"] = config.Secret{Value: "bob:pw2"}
	c.Conf.APIUsersACLAllowExternal = "bob:db:c1:dbops"
	c.LoadAPIUsers()

	if err := c.UpdateUser(UserForm{Username: "bob", Roles: "dbops", Grants: ""}, "admin", false); err != nil {
		t.Fatal(err)
	}
	if got := c.Conf.APIUsersACLAllowExternal; got != "bob::c1:dbops" {
		t.Fatalf("unexpected entry: %q", got)
	}
	c.LoadAPIUsers()
	b := c.APIUsers["bob"]
	if !b.Roles[config.RoleDBOps] {
		t.Fatalf("bob must keep dbops: %+v", b.Roles)
	}
	for g, v := range b.Grants {
		if v {
			t.Fatalf("bob must have no grant, has %s", g)
		}
	}
}

// A delegator below sysops cannot rewrite a sysops account; it still updates the
// others (grants filtered to its own, as before).
func TestUpdateUserNonSysopsDelegator(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Secrets["api-credentials-external"] = config.Secret{Value: "dave:pw3,bob:pw2"}
	c.Conf.APIUsersACLAllowExternal = "admin:cluster db:c1:sysops dbops,dave:db cluster-grant:c1:dbops,bob:db:c1:dbops"
	c.LoadAPIUsers()

	if err := c.UpdateUser(UserForm{Username: "admin", Roles: "dbops", Grants: "db"}, "dave", false); err == nil {
		t.Fatal("a dbops delegator must not update the sysops admin")
	}
	if !strings.Contains(c.Conf.APIUsersACLAllowExternal, "admin:cluster db:c1:sysops dbops") {
		t.Fatalf("admin's entry must be left untouched: %q", c.Conf.APIUsersACLAllowExternal)
	}
	if err := c.UpdateUser(UserForm{Username: "bob", Roles: "dbops", Grants: "db proxy"}, "dave", false); err != nil {
		t.Fatalf("a dbops delegator still updates a dbops user: %v", err)
	}
	c.LoadAPIUsers()
	if c.APIUsers["bob"].Grants[config.GrantProxyConfigCreate] {
		t.Fatal("the delegator's grants must still filter what it gives")
	}
}

// The cluster add handler drops the external accounts, `system` included, then
// calls EnsureSystemServiceUser: `system` must come back with the derived key and
// cluster-resource-sensor (a dbjob secret-login re-created it without the grant).
func TestEnsureSystemServiceUserAfterReset(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	cm := manager.NewConfigManager(config.NewLogrusWrapper(c.Conf, logrus.New()))
	cm.Stop() // saves become no-ops
	c.ConfigManager = cm
	c.LoadAPIUsers()
	c.EnsureSystemServiceUser()

	// the handler's reset
	c.Conf.APIUsersExternal = ""
	c.Conf.APIUsersACLAllowExternal = ""
	c.Conf.APIUsersACLDiscardExternal = ""
	c.Conf.Secrets["api-credentials-external"] = config.Secret{}
	c.LoadAPIUsers()
	if _, ok := c.APIUsers["system"]; ok {
		t.Fatal("fixture: the reset drops system")
	}

	c.EnsureSystemServiceUser()
	c.LoadAPIUsers()
	s, ok := c.APIUsers["system"]
	if !ok {
		t.Fatal("system must be re-created")
	}
	if !s.Grants[config.GrantClusterResourceSensor] {
		t.Fatal("system must carry cluster-resource-sensor")
	}
	if s.Password != c.GetSystemAPIKey() {
		t.Fatal("system must authenticate with the derived key")
	}
}
