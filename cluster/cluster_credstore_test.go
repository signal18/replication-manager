// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
)

// #2075 step 1: a credential set at runtime goes through the credential chain, survives a
// tag change, is saved by SaveConfigFile although its raw field still holds the default, and
// a config loaded from the saved file (as the start loads [saved-<cluster>] into
// DynamicFlagMap) gets it back.
func TestCredentialSetTagSaveReload(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.Conf.ConfRewrite = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	defaults := map[string]interface{}{"replication-credential": "root:mariadb", "backup-verification-credential": ""}
	cl.Conf.DefaultFlagMap = defaults
	cl.Conf.DynamicFlagMap = map[string]interface{}{}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.Conf.RplUser = "root:mariadb" // the raw field at its default: the save used to skip it
	cl.SetClusterCredentialsFromConfig()
	if err := os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}

	// set through the chain, the raw field left at its default
	if err := cl.CredentialChain().Set("replication-credential", "repl:set-at-runtime"); err != nil {
		t.Fatal(err)
	}
	cl.AddDBTagConfig("localinfile")
	cl.DropDBTagConfig("localinfile")
	if v := cl.Conf.GetDecryptedValue("replication-credential"); v != "repl:set-at-runtime" {
		t.Fatalf("after the tag change: %q", v)
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}

	// a restart: a new config loads the saved section into its dynamic map
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	raw, ok := saved.Get("replication-credential").(string)
	if !ok || raw == "repl:set-at-runtime" {
		t.Fatalf("replication-credential saved as %q, want it saved encrypted", raw)
	}
	reloaded := &config.Config{
		SecretKey:       cl.Conf.SecretKey,
		DefaultFlagMap:  defaults,
		ImmuableFlagMap: map[string]interface{}{},
		DynamicFlagMap:  saved.ToMap(),
	}
	reloaded.DecryptSecretsFromConfig()
	if v := reloaded.GetDecryptedValue("replication-credential"); v != "repl:set-at-runtime" {
		t.Fatalf("after the restart: %q, want the value set at runtime", v)
	}
}
