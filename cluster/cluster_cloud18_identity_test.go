// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The instance's Cloud18 identity is passwordless in every cluster: a password a list still
// holds for it is dropped at load and never authenticates a local login.
func TestCloud18IdentityIsPasswordless(t *testing.T) {
	c := &Cluster{
		Name: "c1",
		Conf: &config.Config{
			Cloud18GitUser: "owner@x.io",
			Secrets: config.NewSecrets(map[string]config.Secret{
				"api-credentials-external": {Value: "owner@x.io:generated-pw,dba:dba-pw"},
			}),
		},
		Grants: config.GetGrantType(),
		Roles:  config.GetRoleType(),
	}
	if err := c.LoadAPIUsers(); err != nil {
		t.Fatal(err)
	}
	owner, ok := c.GetAPIUsers()["owner@x.io"]
	if !ok {
		t.Fatal("the Cloud18 identity stays an account of the cluster")
	}
	if owner.Password != "" {
		t.Fatal("the Cloud18 identity's stored password must be dropped at load")
	}
	if c.IsValidACL("owner@x.io", "generated-pw", "/api/monitor", AuthMethodSubmittedPassword) {
		t.Fatal("a local password login under the Cloud18 identity must be refused")
	}
	if _, err := c.GetAPIUser("owner@x.io", "generated-pw"); err == nil {
		t.Fatal("GetAPIUser must refuse the Cloud18 identity's local password")
	}
	if !c.IsValidACL("owner@x.io", "", "/api/monitor", "oidc") {
		t.Fatal("the Cloud18 identity signs in with SSO")
	}
	if !c.IsValidACL("dba", "dba-pw", "/api/monitor", AuthMethodSubmittedPassword) {
		t.Fatal("another local account keeps its password")
	}

	got := c.stripCloud18IdentityPassword("a:1,owner@x.io:secret,b:2")
	if got != "a:1,owner@x.io:,b:2" {
		t.Fatalf("the stored list loses the identity's password only, got %q", got)
	}
	c.Conf.Cloud18GitUser = ""
	if got := c.stripCloud18IdentityPassword("owner@x.io:secret"); got != "owner@x.io:secret" {
		t.Fatal("no Cloud18 identity: the list is unchanged")
	}
}

// AddUser never stores a password for the Cloud18 identity, given or generated, and a later
// add strips one an older release stored.
func TestAddUserCloud18IdentityStoresNoPassword(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.Cloud18 = true
	cl.Conf.Cloud18GitUser = "owner@x.io"
	if err := cl.CredentialChain().Set("api-credentials-external", "old@x.io:keep,owner@x.io:generated-by-old-release"); err != nil {
		t.Skipf("no writable credential store in this fixture: %v", err)
	}
	cl.Conf.Secrets.Set("api-credentials-external", config.Secret{Value: "old@x.io:keep,owner@x.io:generated-by-old-release"})
	if err := cl.AddUser(UserForm{Username: "dba", Password: "dba-pw", Grants: "cluster show"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	stored := cl.Conf.GetDecryptedValue("api-credentials-external")
	if stored != "old@x.io:keep,owner@x.io:,dba:dba-pw" {
		t.Fatalf("the stored list must lose the identity's password only, got %q", stored)
	}

	cl2 := newSecretURLTestCluster(t, map[string]interface{}{})
	cl2.Conf.Cloud18 = true
	cl2.Conf.Cloud18GitUser = "owner@x.io"
	if err := cl2.AddUser(UserForm{Username: "owner@x.io", Password: "given", Grants: "cluster show"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	if got := cl2.Conf.GetDecryptedValue("api-credentials-external"); got != "owner@x.io:" {
		t.Fatalf("the Cloud18 identity is added without password, got %q", got)
	}
}
