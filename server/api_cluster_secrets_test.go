// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"regexp"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// A reveal never changes a credential: the read's two files call nothing that rotates, saves,
// generates, provisions, encrypts or sets a secret, on any path.
func TestClusterSecretsArePureRead(t *testing.T) {
	banned := regexp.MustCompile(`(?i)rotat|save|generat|provision|encrypt|apply|write|update|^Set[A-Z]|^Add|^Drop|^Delete|^Revoke|^Mail|^Switch`)
	for _, f := range []string{"api_cluster_secrets.go", "../cluster/cluster_secrets.go"} {
		file, err := parser.ParseFile(gotoken.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
				// the response headers are the handler's own, not a credential
				if inner, ok := fn.X.(*ast.CallExpr); ok && name == "Set" {
					if sel, ok := inner.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Header" {
						return true
					}
				}
			}
			if banned.MatchString(name) {
				t.Errorf("%s calls %s: the cluster secrets read must not change anything", f, name)
			}
			return true
		})
	}
}

func TestClusterSecretsACLRule(t *testing.T) {
	_, cl := newTokenTestManager(t)
	owner := cluster.APIUser{User: "owner@example.com", Roles: map[string]bool{}}
	cl.SetUserGrants(&owner, config.GetDefaultGrants(config.RoleSysOps))
	cl.APIUsers[owner.User] = owner
	sponsor := cluster.APIUser{User: "sponsor@example.com", Roles: map[string]bool{}}
	cl.SetUserGrants(&sponsor, config.GetDefaultGrants(config.RoleSponsor))
	cl.APIUsers[sponsor.User] = sponsor
	if !cl.IsURLPassACL(owner.User, "/api/clusters/c1/secrets", false) {
		t.Fatal("a sysops is refused the cluster secrets")
	}
	if cl.IsURLPassACL(sponsor.User, "/api/clusters/c1/secrets", false) {
		t.Fatal("a sponsor reads the cluster secrets")
	}
}

func TestClusterSecretsInstanceAdmin(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"replication-credential": {Value: "repl:rp"}})
	repman.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"api-credentials": {Value: "admin:adm1n,alice:x"}})
	owner := cluster.APIUser{User: "owner@example.com", Roles: map[string]bool{}}
	cl.SetUserGrants(&owner, config.GetDefaultGrants(config.RoleSysOps))
	cl.APIUsers[owner.User] = owner

	res := repman.clusterSecretsFor(cl, owner.User)
	if res.InstanceAdmin == nil || res.InstanceAdmin.User != "admin" || res.InstanceAdmin.Password != "adm1n" {
		t.Fatalf("the owner does not get the instance admin login: %+v", res.InstanceAdmin)
	}
	if len(res.Secrets) != 1 || res.Secrets[0].Password != "rp" {
		t.Fatalf("secrets: %+v", res.Secrets)
	}
	// alice holds no server credentials grant: the cluster's secrets only
	if res := repman.clusterSecretsFor(cl, "alice"); res.InstanceAdmin != nil {
		t.Fatal("a user without cluster-server-credentials gets the instance admin login")
	}
}
