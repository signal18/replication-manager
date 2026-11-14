// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// F9 guard: a server secret the global settings fan out (ApplyServerSecret puts its clear value
// in the cluster's config fields) is never written in clear by the cluster's save, nor kept in
// the cluster's versioned file.
func TestApplyServerSecretSavesNoClearValue(t *testing.T) {
	values := map[string]string{
		"mail-smtp-password":      "MARKERSMTP",
		"cloud18-gitlab-password": "MARKERGITLAB",
		"cloud18-domain-secret":   "MARKERDOMAIN",
	}
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.MonitoringSecretVersioning = true
	server := &config.Config{SecretKey: cl.Conf.SecretKey, DynamicFlagMap: map[string]interface{}{}, ImmuableFlagMap: map[string]interface{}{}, DefaultFlagMap: map[string]interface{}{}}
	for k, v := range values {
		server.DynamicFlagMap[k] = server.EncryptSecretValue(v)
	}
	serverChain := credstore.NewConfigChain(server, nil)
	serverChain.Refresh()
	cl.ServerGlobals = &ServerGlobals{ServerSecrets: &credstore.ServerLevel{
		Chain: func() *credstore.Chain { return serverChain },
		Held:  func(k string) bool { _, ok := values[k]; return ok },
	}}
	cl.credChain = nil // built with the server level, as at start
	cl.CredentialChain().Refresh()
	for k, v := range values {
		cl.ApplyServerSecret(k)
		if got := cl.Conf.GetDecryptedValue(k); got != v {
			t.Fatalf("%s: the cluster reads %q", k, got)
		}
	}
	if cl.Conf.MailSMTPPassword != "MARKERSMTP" {
		t.Fatal("the fan-out did not reach the cluster's config")
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	cl.MarkSecretVersionStoreDirty()
	cl.ReconcileSecretVersionStore()
	err := filepath.Walk(cl.Conf.WorkingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(raw), "MARKER") {
			t.Errorf("a server secret in clear in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
