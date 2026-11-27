// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
)

func newOnPremiseSSHTestCluster(t *testing.T) *cluster.Cluster {
	t.Helper()
	root := t.TempDir()
	cl := &cluster.Cluster{
		Name:       "onprem",
		WorkingDir: filepath.Join(root, "onprem"),
		Conf: &config.Config{
			WorkingDir:                 root,
			MonitoringSecretVersioning: true,
			ConfRewrite:                true,
			SecretKey:                  []byte("01234567890123456789012345678901"),
			ImmuableFlagMap:            map[string]interface{}{},
			DynamicFlagMap:             map[string]interface{}{},
			DefaultFlagMap:             map[string]interface{}{},
		},
		ConfigManager: manager.NewConfigManager(nil),
	}
	os.MkdirAll(cl.WorkingDir, 0700)
	return cl
}

// The on-premise SSH settings are set through the cluster settings API; the credential is
// masked in the cluster's API JSON, its password may be empty (the private key).
func TestSetOnPremiseSSHSettingsThroughAPI(t *testing.T) {
	cl := newOnPremiseSSHTestCluster(t)
	repman := &ReplicationManager{}

	if err := repman.setClusterSetting(cl, "onpremise-ssh-credential", b64("deploy:MARKERSSH")); err != nil {
		t.Fatalf("credential: %v", err)
	}
	if cl.GetOnPremiseSSHUser() != "deploy" || cl.GetOnPremiseSSHPass() != "MARKERSSH" {
		t.Fatalf("credential not applied")
	}
	if err := repman.setClusterSetting(cl, "onpremise-ssh-credential", b64("deploy:")); err != nil {
		t.Fatalf("credential without a password (key authentication): %v", err)
	}
	if err := repman.setClusterSetting(cl, "onpremise-ssh-credential", b64("deploy:MARKERSSH")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"bm90LWJhc2U2NA", b64("nouser"), b64(":pass")} {
		err := repman.setClusterSetting(cl, "onpremise-ssh-credential", bad)
		if err == nil || setSettingErrorCode(err) != http.StatusBadRequest {
			t.Errorf("credential %q: %v, want a 400", bad, err)
		}
	}

	if err := repman.setClusterSetting(cl, "onpremise-ssh-port", "2222"); err != nil || cl.Conf.OnPremiseSSHPort != 2222 {
		t.Fatalf("port: %v", err)
	}
	script := filepath.Join(t.TempDir(), "start-db.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	if err := repman.setClusterSetting(cl, "onpremise-ssh-start-db-script", b64(script)); err != nil || cl.Conf.OnPremiseSSHStartDbScript != script {
		t.Fatalf("script: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(keyPath, []byte("key"), 0o600)
	if err := repman.setClusterSetting(cl, "onpremise-ssh-private-key", b64(keyPath)); err != nil || cl.Conf.OnPremiseSSHPrivateKey != keyPath {
		t.Fatalf("key path: %v", err)
	}
	if err := repman.setClusterSetting(cl, "onpremise-ssh", "true"); err != nil || !cl.Conf.OnPremiseSSH {
		t.Fatalf("onpremise-ssh on: %v", err)
	}

	doc, err := cl.Conf.MaskSecretsJSON([]byte(fmt.Sprintf(`{"config":{"onpremiseSshCredential":%q}}`, cl.Conf.OnPremiseSSHCredential)), "config.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "MARKER") {
		t.Fatalf("the SSH password is in the API JSON: %s", doc)
	}
	if payload, err := buildClusterAPIPayload(cl); err == nil && strings.Contains(string(payload), "MARKER") {
		t.Fatalf("the SSH password is in the cluster API payload")
	}
}

// The credential and the key are never written in a log line; the scripts are logged decoded.
func TestOnPremiseSSHChangeLogMasked(t *testing.T) {
	for _, name := range []string{"onpremise-ssh-credential", "onpremise-ssh-private-key"} {
		value := b64("MARKER")
		format, args := GetApiChangeLogFormat(name, value)
		if line := fmt.Sprintf(format, args...); strings.Contains(line, value) || strings.Contains(line, "MARKER") {
			t.Errorf("%s logged as %q", name, line)
		}
	}
	format, args := GetApiChangeLogFormat("onpremise-ssh-start-db-script", b64("/opt/start.sh"))
	if line := fmt.Sprintf(format, args...); !strings.Contains(line, "/opt/start.sh") {
		t.Errorf("script logged as %q, want the decoded path", line)
	}
}

// T16: an on-premise SSH setting set in the configuration under /etc is refused by the API (409),
// set and switch; the credential follows the credential chain instead.
func TestOnPremiseSSHImmutableRefused(t *testing.T) {
	cl := newOnPremiseSSHTestCluster(t)
	repman := &ReplicationManager{}
	for _, name := range []string{"onpremise-ssh", "onpremise-ssh-port", "onpremise-ssh-private-key", "onpremise-ssh-db-job-script"} {
		cl.Conf.ImmuableFlagMap[name] = "pinned"
	}
	cl.Conf.OnPremiseSSHPort = 22

	err := repman.setClusterSetting(cl, "onpremise-ssh-port", "2222")
	if !errors.Is(err, errImmutableGlobalSetting) || setSettingErrorCode(err) != http.StatusConflict || cl.Conf.OnPremiseSSHPort != 22 {
		t.Fatalf("pinned port: %v, port %d", err, cl.Conf.OnPremiseSSHPort)
	}
	if err := repman.setClusterSetting(cl, "onpremise-ssh-private-key", b64("/tmp/key")); !errors.Is(err, errImmutableGlobalSetting) {
		t.Fatalf("pinned key: %v", err)
	}
	if err := repman.setClusterSetting(cl, "onpremise-ssh-db-job-script", b64("/tmp/job.sh")); !errors.Is(err, errImmutableGlobalSetting) {
		t.Fatalf("pinned script: %v", err)
	}
	if err := repman.switchClusterSettings(cl, "onpremise-ssh"); !errors.Is(err, errImmutableGlobalSetting) || cl.Conf.OnPremiseSSH {
		t.Fatalf("pinned switch: %v", err)
	}
	// not pinned: still set
	script := filepath.Join(t.TempDir(), "stop.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	if err := repman.setClusterSetting(cl, "onpremise-ssh-stop-proxy-script", b64(script)); err != nil {
		t.Fatalf("a setting not pinned refused: %v", err)
	}
}
