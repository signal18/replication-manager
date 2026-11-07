// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"hash/crc64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// secretKeysWithoutSaveCase are the config.SecretKeys GetEncryptedValueFromMemory has no case
// for, each with why: any other key without a case is saved as "" and lost at the next restart.
var secretKeysWithoutSaveCase = map[string]string{
	"meet-token": "no config field: the meet token is fetched at each login and kept in memory",
}

// Every secret key has a save case: with an encryption key, its value is saved encrypted.
func TestSecretKeysHaveSaveCase(t *testing.T) {
	dynamic := map[string]interface{}{}
	for _, k := range config.SecretKeys {
		dynamic[k] = "someuser:somepass"
	}
	cl := newSecretURLTestCluster(t, dynamic)
	for _, k := range config.SecretKeys {
		if _, excluded := secretKeysWithoutSaveCase[k]; excluded {
			continue
		}
		if enc := cl.GetEncryptedValueFromMemory(k); !strings.Contains(enc, "hash_") {
			t.Errorf("%s has no save case: GetEncryptedValueFromMemory gives %q", k, enc)
		}
	}
}

// A secret set at runtime is saved encrypted, and a restart from the saved file gets it back.
func TestRuntimeSecretsSaveRestart(t *testing.T) {
	values := map[string]string{
		"cloud18-domain-secret":            "MARKERDOMAIN",
		"prov-app-template-repo-password":  "MARKERTEMPLATE",
		"prov-docker-registry-credentials": "registry.example:5000:reguser:MARKERREGISTRY",
	}
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	for k, v := range values {
		if err := cl.CredentialChain().Set(k, v); err != nil {
			t.Fatalf("set %s: %s", k, err)
		}
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tree.String(), "MARKER") {
		t.Fatalf("a secret in clear in the saved file:\n%s", tree)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	for k := range values {
		if raw, _ := saved.Get(k).(string); !strings.Contains(raw, "hash_") {
			t.Fatalf("%s saved as %q, want it encrypted", k, raw)
		}
	}

	restarted := newSecretURLTestCluster(t, saved.ToMap())
	restarted.Conf.WorkingDir = cl.Conf.WorkingDir
	restarted.Conf.SecretKey = cl.Conf.SecretKey
	restarted.SetClusterCredentialsFromConfig()
	for k, v := range values {
		if got := restarted.Conf.GetDecryptedValue(k); got != v {
			t.Fatalf("%s after the restart: %q, want %q", k, got, v)
		}
	}
}

// The ProxySQL login is read where its password is: a user set at runtime (the proxysql-user
// secret) is the one a new proxy logs in with, the configured user only when the secret is empty.
func TestProxysqlUserFollowsTheSecret(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.ProxysqlUser = "admin"

	cl.Conf.Secrets.Set("proxysql-user", config.Secret{})
	if got := cl.GetProxysqlUser(); got != "admin" {
		t.Fatalf("no secret: user %q, want the configured admin", got)
	}

	if err := cl.CredentialChain().Set("proxysql-user", "external"); err != nil {
		t.Fatal(err)
	}
	if got := cl.GetProxysqlUser(); got != "external" {
		t.Fatalf("user set at runtime: %q, want external", got)
	}
	if prx := NewProxySQLProxy(0, cl, "proxysql1"); prx.User != "external" {
		t.Fatalf("a new proxy logs in as %q, want external", prx.User)
	}
}

// A ProxySQL added with its login (service plan, proxies copied from a cluster head, gRPC) logs
// in with it: the login is kept where the proxy reads it, not only in Conf.ProxysqlUser.
func TestAddedProxysqlKeepsItsLogin(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.crcTable = crc64.MakeTable(crc64.ECMA) // as Cluster.Init sets it: the proxy ID is a CRC
	// the defaults a running replication-manager has: proxysql-user and proxysql-password admin
	cl.Conf.ProxysqlUser = "admin"
	cl.Conf.Secrets.Set("proxysql-user", config.Secret{Value: "admin"})
	cl.Conf.Secrets.Set("proxysql-password", config.Secret{Value: "admin"})
	if err := cl.AddSeededProxy(config.ConstProxySqlproxy, "proxysql1", "6033", "external", "proxypass"); err != nil {
		t.Fatal(err)
	}
	if got := cl.GetProxysqlUser(); got != "external" {
		t.Fatalf("after the add: user %q, want external", got)
	}
	if got := cl.Conf.Secrets.Get("proxysql-password").Value; got != "proxypass" {
		t.Fatalf("after the add: password %q, want the one given", got)
	}
	found := false
	for _, p := range cl.Proxies {
		if prx, ok := p.(*ProxySQLProxy); ok {
			found = true
			if prx.User != "external" || prx.Pass != "proxypass" {
				t.Fatalf("the added proxy logs in as %s:%s, want external:proxypass", prx.User, prx.Pass)
			}
		}
	}
	if !found {
		t.Fatal("no ProxySQL proxy after the add")
	}
}

// An SSO-only account added (AddSSOOnlyUser) is kept with secret versioning on: the add marks
// the version store dirty, so the save callback records the new api-credentials-external and the
// next start reads it back. Before, the add changed the credential chain in memory only: the start
// read the store's older version, the account was no user any more, and its SSO session was
// refused with "No valid ACL" on every cluster call.
func TestAddedUserSurvivesTheVersionStore(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.MonitoringSecretVersioning = true
	// a running cluster: its store recorded at its start (Run marks it dirty), then clean
	cl.MarkSecretVersionStoreDirty()
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	cl.IsNeedConfigSave = false

	if err := cl.AddSSOOnlyUser(UserForm{Username: "sso@x.io", Roles: "extsysops", Grants: "cluster show"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	cl.LoadAPIUsers()
	if !cl.IsValidACL("sso@x.io", "", "/api/clusters/"+cl.Name, "oidc") {
		t.Fatal("right after the add, the account's SSO session must pass the cluster ACL")
	}
	cl.secretVersionStoreMu.Lock()
	dirty := cl.secretVersionStoreDirty
	cl.secretVersionStoreMu.Unlock()
	if !dirty || !cl.IsNeedConfigSave {
		t.Fatalf("the add must mark the version store dirty and ask a save (dirty %v, save asked %v)", dirty, cl.IsNeedConfigSave)
	}

	// the save callback records it: the newest version is what the next start reads
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	versions, err := cl.SecretVersionsInFile("api-credentials-external")
	if err != nil || len(versions) == 0 || !strings.Contains(versions[len(versions)-1], "sso@x.io") {
		t.Fatalf("the newest api-credentials-external in secret_store.json must hold the added account: %d versions, %v", len(versions), err)
	}
}

// A removed account is recorded too (#2358): DropUser writes the credentials through the chain and
// marks the store, so the next start does not read the account back from an older version.
func TestDroppedUserStaysDroppedInTheVersionStore(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.MonitoringSecretVersioning = true
	for _, u := range []string{"stays@x.io", "gone@x.io"} {
		if err := cl.AddSSOOnlyUser(UserForm{Username: u, Roles: "extdbops", Grants: "db show"}, "admin", false); err != nil {
			t.Fatal(err)
		}
	}
	cl.LoadAPIUsers()
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	if err := cl.DropUser(UserForm{Username: "gone@x.io"}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cl.Conf.GetDecryptedValue("api-credentials-external"), "gone@x.io") {
		t.Fatal("the removed account must leave the credentials the chain holds")
	}
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	versions, err := cl.SecretVersionsInFile("api-credentials-external")
	if err != nil || len(versions) == 0 || strings.Contains(versions[len(versions)-1], "gone@x.io") || !strings.Contains(versions[len(versions)-1], "stays@x.io") {
		t.Fatalf("the newest api-credentials-external in secret_store.json must hold the kept account, not the removed one: %d versions, %v", len(versions), err)
	}
}

// users/add (AddAPIUser, #2359): on a Cloud18 instance an email added without a password is a
// Cloud18 identity, added SSO-only, so its SSO session passes the cluster ACL; a non-email user, an
// email on an instance that is not Cloud18, keep the random password users/send-credentials sends;
// a given password is kept.
func TestAddAPIUserSSOOnlyForCloud18Identities(t *testing.T) {
	cases := []struct {
		name, user, password string
		cloud18, ssoOnly     bool
	}{
		{"Cloud18 email, no password", "ops@x.io", "", true, true},
		{"Cloud18 email, a password", "pw@x.io", "given-pass", true, false},
		{"Cloud18, not an email", "localops", "", true, false},
		{"not Cloud18, an email", "mail@x.io", "", false, false},
	}
	for _, c := range cases {
		cl := newSecretURLTestCluster(t, map[string]interface{}{})
		cl.Conf.Cloud18 = c.cloud18
		if err := cl.AddAPIUser(UserForm{Username: c.user, Password: c.password, Roles: "extsysops", Grants: "cluster show"}, "admin", false); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		cl.LoadAPIUsers()
		u, ok := cl.APIUsers[c.user]
		if !ok {
			t.Fatalf("%s: the account was not added", c.name)
		}
		if got := u.Password == ""; got != c.ssoOnly {
			t.Errorf("%s: SSO-only %v, want %v", c.name, got, c.ssoOnly)
		}
		if c.ssoOnly && !cl.IsValidACL(c.user, "", "/api/clusters/"+cl.Name, "oidc") {
			t.Errorf("%s: the SSO session must pass the cluster ACL", c.name)
		}
		if c.password != "" && u.Password != c.password {
			t.Errorf("%s: the given password must be kept", c.name)
		}
	}
}
