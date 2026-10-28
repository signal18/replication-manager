// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"strings"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
)

// #2075, #2227 rules 1, 1b, 2: a credential changed at runtime (a rotation) is a version in
// the versioned store; a reread, a tag added and dropped, and a restart from the files on disk
// all give the new value, while the config keeps the starting value; the local file is
// written by the save callback only, encrypted.
func TestCredentialSetTagSaveReload(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.Conf.ConfRewrite = true
	cl.Conf.MonitoringSecretVersioning = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	defaults := map[string]interface{}{"replication-credential": "root:mariadb", "backup-verification-credential": ""}
	cl.Conf.DefaultFlagMap = defaults
	cl.Conf.DynamicFlagMap = map[string]interface{}{"replication-credential": cl.Conf.EncryptSecretValue("repl:pass1")}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.SetClusterCredentialsFromConfig()
	if err := os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}
	filePath := SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name)

	// the starting value is version 1 at the first save
	cl.MarkSecretVersionStoreDirty()
	cl.ReconcileSecretVersionStore()

	if err := cl.CredentialChain().Set("replication-credential", "repl:pass2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("the starting value is not in the file: %s", err)
	}
	before, _ := os.ReadFile(filePath)
	check := func(step string) {
		t.Helper()
		if v := cl.Conf.GetDecryptedValue("replication-credential"); v != "repl:pass2" {
			t.Fatalf("%s: %q, want pass2", step, v)
		}
		if raw, _ := cl.Conf.GetDynamicFlag("replication-credential"); cl.Conf.DecryptSecretValue("replication-credential", raw.(string)) != "repl:pass1" {
			t.Fatalf("%s: the config holds another value than the starting one", step)
		}
	}
	check("after the change")
	if after, _ := os.ReadFile(filePath); string(after) != string(before) {
		t.Fatal("the change wrote the file: only the save callback writes it")
	}
	cl.SetClusterCredentialsFromConfig()
	check("reread")
	cl.AddDBTagConfig("localinfile")
	check("tag added")
	cl.DropDBTagConfig("localinfile")
	check("tag dropped")

	// the save callback: the config keeps pass1, the file holds both versions, encrypted
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	cl.ReconcileSecretVersionStore()
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	raw, _ := saved.Get("replication-credential").(string)
	if cl.Conf.DecryptSecretValue("replication-credential", raw) != "repl:pass1" {
		t.Fatalf("the saved config holds %q, want the starting value encrypted", raw)
	}
	data, _ := os.ReadFile(filePath)
	if strings.Contains(string(data), "pass2") || strings.Contains(string(data), "pass1") {
		t.Fatal("a value in clear in the file")
	}
	file, err := loadSecretVersionStore(filePath)
	if err != nil || len(file["replication-credential"]) != 2 {
		t.Fatalf("file versions %+v %v", file["replication-credential"], err)
	}

	// a restart: a new cluster on the same files
	restarted, _ := newTestClusterServer(t)
	restarted.Name = cl.Name
	restarted.ConfigManager = &manager.ConfigManager{}
	restarted.Conf = &config.Config{
		WorkingDir:                 cl.Conf.WorkingDir,
		SecretKey:                  cl.Conf.SecretKey,
		ConfRewrite:                true,
		MonitoringSecretVersioning: true,
		DefaultFlagMap:             defaults,
		ImmuableFlagMap:            map[string]interface{}{},
		DynamicFlagMap:             saved.ToMap(),
	}
	restarted.SetClusterCredentialsFromConfig()
	if v := restarted.Conf.GetDecryptedValue("replication-credential"); v != "repl:pass2" {
		t.Fatalf("after the restart: %q, want pass2", v)
	}
}

// #2227 rule 1: no store keeps versions: a change is refused, the config keeps its value
func TestCredentialChangeRefusedWithoutVersionedStore(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.Conf.ConfRewrite = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.DefaultFlagMap = map[string]interface{}{}
	cl.Conf.DynamicFlagMap = map[string]interface{}{"replication-credential": "repl:pass1"}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.SetClusterCredentialsFromConfig()
	err := cl.CredentialChain().Set("replication-credential", "repl:pass2")
	if err == nil || !strings.Contains(err.Error(), "no credential store keeps versions") {
		t.Fatalf("got %v", err)
	}
	if v, _ := cl.Conf.GetDynamicFlag("replication-credential"); v != "repl:pass1" || cl.Conf.GetDecryptedValue("replication-credential") != "repl:pass1" {
		t.Fatalf("config %v, current %q", v, cl.Conf.GetDecryptedValue("replication-credential"))
	}
	if err := cl.CanRotatePasswords(); err == nil || !strings.Contains(err.Error(), "no credential store keeps versions") {
		t.Fatalf("rotation: %v", err)
	}
}
