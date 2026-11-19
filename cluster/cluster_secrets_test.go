// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"reflect"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func secretsTestCluster(values map[string]string) *Cluster {
	m := map[string]config.Secret{}
	for k, v := range values {
		m[k] = config.Secret{Value: v}
	}
	return &Cluster{Name: "c1", Conf: &config.Config{Secrets: config.NewSecrets(m)}}
}

func TestGetClusterSecrets(t *testing.T) {
	cl := secretsTestCluster(map[string]string{
		"db-servers-credential":            "root:r00t:with:colons",
		"replication-credential":           "repl:replpass",
		"cloud18-sponsor-user-credentials": "sponsor:sp",
		"haproxy-password":                 "hap",
		"maxscale-pass":                    "mxs",
		"proxysql-user":                    "padmin",
		"proxysql-password":                "ppass",
		// the instance's own secrets are never returned
		"api-credentials":    "admin:adm",
		"git-acces-token":    "tok",
		"vault-secret-id":    "vs",
		"mail-smtp-password": "smtp",
	})
	cl.Conf.HaproxyOn = true
	cl.Conf.ProxysqlOn = true
	// MaxScale off: its password is not listed
	before := cl.Conf.GetSecrets()
	got := map[string]ClusterSecret{}
	for _, s := range cl.GetClusterSecrets() {
		got[s.Key] = s
	}
	want := map[string]ClusterSecret{
		"db-servers-credential":            {Key: "db-servers-credential", User: "root", Password: "r00t:with:colons"},
		"replication-credential":           {Key: "replication-credential", User: "repl", Password: "replpass"},
		"cloud18-sponsor-user-credentials": {Key: "cloud18-sponsor-user-credentials", User: "sponsor", Password: "sp"},
		"haproxy-password":                 {Key: "haproxy-password", Password: "hap"},
		"proxysql-password":                {Key: "proxysql-password", User: "padmin", Password: "ppass"},
	}
	if len(got) != len(want) {
		t.Fatalf("got keys %v, want %v", secretKeysOf(got), secretKeysOf(want))
	}
	for k, w := range want {
		g := got[k]
		if g.User != w.User || g.Password != w.Password || g.Label == "" {
			t.Fatalf("%s: got %+v, want %+v", k, g, w)
		}
	}
	// a read: the secrets are exactly as before
	if after := cl.Conf.GetSecrets(); !reflect.DeepEqual(before, after) {
		t.Fatal("GetClusterSecrets changed the cluster's secrets")
	}
}

func TestClusterSecretKeysExcludeInstanceSecrets(t *testing.T) {
	for _, k := range ClusterSecretKeys() {
		for _, banned := range []string{"api-credentials", "git-", "vault-", "opensvc", "onpremise-ssh", "mail-smtp", "alert-", "aws", "registry", "cloud18-gitlab", "cloud18-domain-secret", "api-oauth"} {
			if strings.HasPrefix(k, banned) || strings.Contains(k, banned) {
				t.Fatalf("%s is an instance secret, not a cluster owner's", k)
			}
		}
	}
}

// The grant goes to the holders of the rotation grant (sysops, extsysops, the Cloud18 owner's
// full cluster family), never to dbops, extdbops or the sponsor.
func TestShowSecretsGrantHolders(t *testing.T) {
	for role, want := range map[string]bool{
		config.RoleSysOps: true, config.RoleExtSysOps: true,
		config.RoleDBOps: false, config.RoleExtDBOps: false, config.RoleSponsor: false,
	} {
		cl := &Cluster{Grants: config.GetGrantType()}
		u := APIUser{User: "u", Roles: map[string]bool{}}
		cl.SetUserGrants(&u, config.GetDefaultGrants(role))
		if got := u.Grants[config.GrantClusterShowSecrets]; got != want {
			t.Fatalf("%s holds %s: %v, want %v", role, config.GrantClusterShowSecrets, got, want)
		}
		if u.Grants[config.GrantClusterRotatePasswords] != want {
			t.Fatalf("%s: %s and %s must have the same holders", role, config.GrantClusterShowSecrets, config.GrantClusterRotatePasswords)
		}
	}
}

func secretKeysOf(m map[string]ClusterSecret) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
