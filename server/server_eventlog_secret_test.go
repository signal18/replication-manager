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

// An alert URL is a plain setting: a peer's event carries the URL, applied to the field. A peer
// still on v3.1.47 or v3.1.48 sends it encrypted ("https:hash_<hex>"): the next start or reload
// reads that back as the URL.
func TestPeerEventAlertURL(t *testing.T) {
	newCluster := func() (*ReplicationManager, *cluster.Cluster) {
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
		return &ReplicationManager{
			Conf:     &config.Config{},
			Clusters: map[string]*cluster.Cluster{cl.Name: cl},
		}, cl
	}
	const u = "https://example.webhook.office.com/webhookb2/MARKER"
	event := func(cl *cluster.Cluster, value string) cluster.ConfigChangeEvent {
		return cluster.ConfigChangeEvent{Ts: time.Now(), Cluster: cl.Name, Author: 2, Action: "set", Key: "alert-teams-url", Value: value}
	}

	repman, cl := newCluster()
	if !repman.applyPeerConfigEvent(event(cl, u)) {
		t.Fatal("the plain event was not applied")
	}
	if cl.Conf.TeamsUrl != u {
		t.Fatalf("field %q after the plain peer event, want the URL", cl.Conf.TeamsUrl)
	}

	repman, cl = newCluster()
	legacy := cl.Conf.EncryptSecretValue(u)
	if !repman.applyPeerConfigEvent(event(cl, legacy)) {
		t.Fatal("the legacy encrypted event was not applied")
	}
	cl.SetClusterCredentialsFromConfig() // what a start or a reload does
	if cl.Conf.TeamsUrl != u {
		t.Fatalf("field %q after the legacy peer event and a reload, want the URL", cl.Conf.TeamsUrl)
	}
}
