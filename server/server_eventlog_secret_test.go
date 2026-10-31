// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/sirupsen/logrus"
)

// A webhook URL set on a peer arrives encrypted; its setter takes base64, so it is applied
// generically, which sets the field only. The secret its readers read gets the URL too.
func TestPeerEventWebhookURLSecret(t *testing.T) {
	cl := &cluster.Cluster{
		Name:          "c1",
		Logrus:        logrus.New(),
		ConfigManager: &manager.ConfigManager{},
		Conf: &config.Config{
			SecretKey:       []byte("0123456789abcdef"),
			DefaultFlagMap:  map[string]interface{}{},
			ImmuableFlagMap: map[string]interface{}{},
			DynamicFlagMap:  map[string]interface{}{},
		},
	}
	cl.SetClusterCredentialsFromConfig()
	repman := &ReplicationManager{
		Conf:     &config.Config{},
		Clusters: map[string]*cluster.Cluster{cl.Name: cl},
	}
	const u = "https://example.webhook.office.com/webhookb2/MARKER"
	ev := cluster.ConfigChangeEvent{
		Ts:      time.Now(),
		Cluster: cl.Name,
		Author:  2,
		Action:  "set",
		Key:     "alert-teams-url",
		Value:   cl.Conf.EncryptSecretValue(u),
	}
	if !repman.applyPeerConfigEvent(ev) {
		t.Fatal("the event was not applied")
	}
	if v := cl.Conf.GetDecryptedValue("alert-teams-url"); v != u {
		t.Fatalf("secret %q after the peer event, want the URL", v)
	}
	if s := cl.Conf.Secrets.Get("alert-teams-url"); s.Crypted != ev.Value {
		t.Fatal("the peer's ciphertext is not kept for the next save")
	}
}
