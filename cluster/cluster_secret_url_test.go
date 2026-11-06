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

var alertURLs = map[string]string{
	"alert-slack-url":         "https://hooks.slack.example/services/T0/B0/MARKERSLACK",
	"cloud18-alert-slack-url": "https://hooks.slack.example/services/T1/B1/MARKERC18",
	"alert-teams-url":         "https://prod.example.logic.azure.com:443/workflows/x/invoke?api-version=2016-06-01&sp=%2Ftriggers&sig=MARKERTEAMS",
	"alert-teams-proxy-url":   "http://user:MARKERPROXY@proxy.example:3128",
}

// withAlertURLDefaults registers the alert URL flags' defaults as a running repman has them: the
// save writes a setting that differs from its default and drops one with none.
func withAlertURLDefaults(c *Cluster) *Cluster {
	for k := range alertURLs {
		c.Conf.DefaultFlagMap[k] = ""
	}
	return c
}

func alertURLFields(c *Cluster) map[string]string {
	return map[string]string{
		"alert-slack-url":         c.Conf.SlackURL,
		"cloud18-alert-slack-url": c.Conf.Cloud18AlertSlackURL,
		"alert-teams-url":         c.Conf.TeamsUrl,
		"alert-teams-proxy-url":   c.Conf.TeamsProxyUrl,
	}
}

// An alert URL is a plain setting: the API JSON shows it (the back office reads it to find why
// an integration does not post) and the save writes it in plain.
func TestAlertURLsPlainSetSave(t *testing.T) {
	setters := map[string]func(*Cluster, string){
		"alert-slack-url":         (*Cluster).SetAlertSlackUrl,
		"cloud18-alert-slack-url": (*Cluster).SetCloud18AlertSlackUrl,
		"alert-teams-url":         (*Cluster).SetAlertTeamsUrl,
		"alert-teams-proxy-url":   (*Cluster).SetAlertTeamsProxyUrl,
	}
	cl := withAlertURLDefaults(newSecretURLTestCluster(t, map[string]interface{}{}))
	for k, u := range alertURLs {
		setters[k](cl, u)
	}
	for k, v := range alertURLFields(cl) {
		if v != alertURLs[k] {
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
	for k, u := range alertURLs {
		if !strings.Contains(string(doc), strings.ReplaceAll(u, "&", `\u0026`)) && !strings.Contains(string(doc), u) {
			t.Fatalf("%s is not in the API JSON", k)
		}
	}

	// the saved file
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	for k, u := range alertURLs {
		if raw, _ := saved.Get(k).(string); raw != u {
			t.Fatalf("%s saved as %q, want the plain URL", k, raw)
		}
	}

}

// A config v3.1.47 or v3.1.48 saved holds the alert URLs encrypted ("https:hash_<hex>"): the
// start reads them back as the URLs, so the alerts post, and the next save writes them in plain.
func TestLegacyEncryptedAlertURLsMigrate(t *testing.T) {
	plain := newSecretURLTestCluster(t, map[string]interface{}{})
	dynamic := map[string]interface{}{}
	for k, u := range alertURLs {
		enc := plain.Conf.EncryptSecretValue(u)
		if !strings.Contains(enc, "hash_") || strings.Contains(enc, "MARKER") {
			t.Fatalf("%s: the fixture is not encrypted: %q", k, enc)
		}
		dynamic[k] = enc
	}
	cl := withAlertURLDefaults(newSecretURLTestCluster(t, dynamic))
	cl.Conf.SecretKey = plain.Conf.SecretKey
	// the fields as the load leaves them: the saved ciphertext
	cl.Conf.SlackURL = dynamic["alert-slack-url"].(string)
	cl.Conf.Cloud18AlertSlackURL = dynamic["cloud18-alert-slack-url"].(string)
	cl.Conf.TeamsUrl = dynamic["alert-teams-url"].(string)
	cl.Conf.TeamsProxyUrl = dynamic["alert-teams-proxy-url"].(string)

	cl.SetClusterCredentialsFromConfig()
	for k, v := range alertURLFields(cl) {
		if v != alertURLs[k] {
			t.Fatalf("%s after the start: %q, want the URL", k, v)
		}
	}

	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if saved == nil {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	for k, u := range alertURLs {
		if raw, _ := saved.Get(k).(string); raw != u {
			t.Fatalf("%s saved as %q after the migration, want the plain URL", k, raw)
		}
	}
}

// Every flat secret added by this change has a save case: a secret without one is saved
// as "" (the default branch of GetEncryptedValueFromMemory).
func TestFlatSecretsHaveSaveCase(t *testing.T) {
	values := map[string]string{
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
