// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/alert/slackman"
)

// newSecretURLTestCluster is a cluster whose config files are written in a temp dir, the
// 8 flat secrets added by this change unset.
func newSecretURLTestCluster(t *testing.T, dynamic map[string]interface{}) *Cluster {
	t.Helper()
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.LogSlack = slackman.NewSlackManager()
	cl.Conf.ConfRewrite = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.DefaultFlagMap = map[string]interface{}{}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.Conf.DynamicFlagMap = dynamic
	cl.SetClusterCredentialsFromConfig()
	if err := os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}
	return cl
}

// A webhook URL set through the API is a secret: the API JSON masks it, the save writes it
// encrypted, and after a restart from the saved file its readers get the URL, not the
// ciphertext.
func TestWebhookURLSecretSetSaveRestart(t *testing.T) {
	urls := map[string]string{
		"alert-slack-url":         "https://hooks.slack.example/services/T0/B0/MARKERSLACK",
		"cloud18-alert-slack-url": "https://hooks.slack.example/services/T1/B1/MARKERC18",
		"alert-teams-url":         "https://prod.example.logic.azure.com:443/workflows/x/invoke?api-version=2016-06-01&sp=%2Ftriggers&sig=MARKERTEAMS",
		"alert-teams-proxy-url":   "http://user:MARKERPROXY@proxy.example:3128",
	}
	setters := map[string]func(*Cluster, string) error{
		"alert-slack-url":         (*Cluster).SetAlertSlackUrl,
		"cloud18-alert-slack-url": (*Cluster).SetCloud18AlertSlackUrl,
		"alert-teams-url":         (*Cluster).SetAlertTeamsUrl,
		"alert-teams-proxy-url":   (*Cluster).SetAlertTeamsProxyUrl,
	}
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	for k, u := range urls {
		if err := setters[k](cl, u); err != nil {
			t.Fatalf("set %s: %s", k, err)
		}
		if v := cl.Conf.GetDecryptedValue(k); v != u {
			t.Fatalf("%s after set: %q", k, v)
		}
	}

	// the API JSON
	doc, err := json.Marshal(cl.Conf)
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = cl.Conf.MaskSecretsJSON(doc, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "MARKER") {
		t.Fatalf("a webhook URL in the API JSON: %s", doc)
	}

	// the saved file
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tree.String(), "MARKER") {
		t.Fatalf("a webhook URL in clear in the saved file:\n%s", tree)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	for k := range urls {
		if raw, _ := saved.Get(k).(string); !strings.Contains(raw, "hash_") {
			t.Fatalf("%s saved as %q, want it encrypted", k, raw)
		}
	}

	// a restart from the saved file
	restarted := newSecretURLTestCluster(t, saved.ToMap())
	restarted.Conf.WorkingDir = cl.Conf.WorkingDir
	restarted.Conf.SecretKey = cl.Conf.SecretKey
	restarted.SetClusterCredentialsFromConfig()
	for k, u := range urls {
		if v := restarted.Conf.GetDecryptedValue(k); v != u {
			t.Fatalf("%s after the restart: %q, want the URL", k, v)
		}
	}
}

// The API sends SecretMask for a secret: a GUI form saved unchanged sends it back, and it
// must not replace the URL.
func TestWebhookURLSecretRefusesMask(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	const u = "https://hooks.slack.example/services/T0/B0/MARKER"
	if err := cl.SetAlertSlackUrl(u); err != nil {
		t.Fatal(err)
	}
	if err := cl.SetAlertSlackUrl(config.SecretMask); err == nil {
		t.Fatal("the mask was accepted")
	}
	if v := cl.Conf.GetDecryptedValue("alert-slack-url"); v != u || cl.Conf.SlackURL != u {
		t.Fatalf("after the mask: secret %q, field %q", v, cl.Conf.SlackURL)
	}
	// an empty value still clears it
	if err := cl.SetAlertSlackUrl(""); err != nil || cl.Conf.GetDecryptedValue("alert-slack-url") != "" {
		t.Fatalf("clear: %v, %q", err, cl.Conf.GetDecryptedValue("alert-slack-url"))
	}
}

// Every flat secret added by this change has a save case: a secret without one is saved
// as "" (the default branch of GetEncryptedValueFromMemory).
func TestFlatSecretsHaveSaveCase(t *testing.T) {
	values := map[string]string{
		"alert-slack-url":              "https://hooks.slack.example/services/T0/B0/X",
		"cloud18-alert-slack-url":      "https://hooks.slack.example/services/T1/B1/X",
		"alert-teams-url":              "https://example.webhook.office.com/webhookb2/X",
		"alert-teams-proxy-url":        "http://user:pass@proxy.example:3128",
		"monitoring-tunnel-credential": "tunnel:pass",
		"registry-consul-credential":   "consul:pass",
		"registry-consul-token":        "token",
		"mysqlrouter-pass":             "routerpass",
	}
	dynamic := map[string]interface{}{}
	for k, v := range values {
		dynamic[k] = v
	}
	cl := newSecretURLTestCluster(t, dynamic)
	for k, v := range values {
		enc := cl.GetEncryptedValueFromMemory(k)
		if !strings.Contains(enc, "hash_") {
			t.Fatalf("%s: saved as %q, want it encrypted", k, enc)
		}
		if got := cl.Conf.DecryptSecretValue(k, enc); got != v {
			t.Fatalf("%s: %q decrypts to %q", k, v, got)
		}
	}
}
