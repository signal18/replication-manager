// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
)

// #2041, T25: a secret set through the API reaches secret_store.json by the save callback
// only; the setter marks the store dirty and asks a save, it writes no file.
func TestSetSecretLeavesTheStoreFileToTheSave(t *testing.T) {
	root := t.TempDir()
	cl := &cluster.Cluster{
		Name:       "storesave",
		WorkingDir: filepath.Join(root, "storesave"),
		Conf: &config.Config{
			WorkingDir:                 root,
			MonitoringSecretVersioning: true,
			ConfRewrite:                true,
			SecretKey:                  []byte("01234567890123456789012345678901"),
			ImmuableFlagMap:            map[string]interface{}{},
			DynamicFlagMap:             map[string]interface{}{},
		},
		ConfigManager: manager.NewConfigManager(nil),
	}
	os.MkdirAll(cl.WorkingDir, 0700)
	path := cluster.SecretVersionStorePath(root, cl.Name)

	repman := &ReplicationManager{}
	value := base64.StdEncoding.EncodeToString([]byte("new-restic-password"))
	if err := repman.setClusterSetting(cl, "backup-restic-password", value); err != nil {
		t.Fatalf("setClusterSetting: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("secret_store.json was written by the setter, outside the save callback")
	}
	if !cl.IsNeedConfigSave {
		t.Fatalf("the setter asked no save")
	}

	// the save callback writes the file, with the new secret
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatalf("save callback: %v", err)
	}
	versions, err := cl.SecretVersionsInFile("backup-restic-password")
	if err != nil || len(versions) != 1 || versions[0] != "new-restic-password" {
		t.Fatalf("versions in the file after the save: %v, %v", versions, err)
	}
}
