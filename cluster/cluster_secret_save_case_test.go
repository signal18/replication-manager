// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
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
