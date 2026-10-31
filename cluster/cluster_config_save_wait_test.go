// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config/manager"
)

// WaitConfigSaved (#2269) counts only a save that started after the ask and completed, the
// version file included: what the rotation waits for before changing a database.
func TestWaitConfigSaved(t *testing.T) {
	floor := configSaveWaitFloor
	configSaveWaitFloor = time.Second
	defer func() { configSaveWaitFloor = floor }()

	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.initConfigDone.Store(true)
	if err := os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}

	// no gate: a save done before the ask does not count
	if err := cl.SaveCallBack(); err != nil {
		t.Fatal(err)
	}
	if cl.ConfigSavedAsOf().IsZero() {
		t.Fatal("a completed save must be recorded")
	}
	if err := cl.WaitConfigSaved(); err == nil {
		t.Fatal("no save after the ask: the wait must fail")
	}

	// the gate saves once the ask is made
	gate := func() {
		for !cl.IsNeedConfigSave {
			time.Sleep(20 * time.Millisecond)
		}
		cl.IsNeedConfigSave = false
		cl.SaveCallBack()
	}
	go gate()
	if err := cl.WaitConfigSaved(); err != nil {
		t.Fatalf("a save after the ask: %v", err)
	}

	// a version file not written is a save not complete
	cl.Conf.MonitoringSecretVersioning = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.DefaultFlagMap = map[string]interface{}{}
	cl.Conf.DynamicFlagMap = map[string]interface{}{"replication-credential": cl.Conf.EncryptSecretValue("repl:pass1")}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.SetClusterCredentialsFromConfig()
	if err := cl.CredentialChain().Set("replication-credential", "repl:pass2"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}
	go gate()
	if err := cl.WaitConfigSaved(); err == nil {
		t.Fatal("the version file was not written: the save must not count")
	}
}
