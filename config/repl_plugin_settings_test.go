// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The settings of a replication plugin (REPLICATION_INTERFACE.md "Settings: per plugin"): the
// manifest's default, the cluster's [[repl-plugin]] entry, a secret encrypted (F9) and never in
// the JSON (F10); the existing replication-plugin-* keys read as before beside them.

var testReplSettings = PluginSettings{
	{Name: "batch-rows", Type: ConstArchiveSettingTypeInt, Default: "500"},
	{Name: "mode", Type: ConstArchiveSettingTypeString},
	{Name: "token", Type: ConstArchiveSettingTypeSecret, Default: "never-a-default"},
}

func TestReplPluginSettingsLevels(t *testing.T) {
	conf := &Config{SecretKey: []byte("0123456789abcdef")}
	// no entry: the manifest's defaults, never a secret's
	env := strings.Join(conf.ReplPluginEnv(testReplSettings, "repl-mariadb-duckdb-cdc", "duckdb"), "\n")
	if env != "REPMAN_REPL_SETTING_BATCH_ROWS=500" {
		t.Fatalf("defaults: %q", env)
	}
	// the cluster's entry, under an alias of the plugin
	conf.ReplPluginEntries = []ReplPluginEntry{{Plugin: "duckdb", Settings: map[string]interface{}{"batch-rows": int64(2000), "token": conf.GetEncryptedString("s3cr3t"), "undeclared": "x"}}}
	env = strings.Join(conf.ReplPluginEnv(testReplSettings, "repl-mariadb-duckdb-cdc", "duckdb"), "\n")
	if env != "REPMAN_REPL_SECRET_TOKEN=s3cr3t\nREPMAN_REPL_SETTING_BATCH_ROWS=2000" {
		t.Fatalf("cluster level: %q", env)
	}

	// saved encrypted, read back by the config loader beside the existing keys
	out, err := conf.ReplPluginEntriesToml("saved-c1")
	if err != nil || strings.Contains(out, "s3cr3t") || !strings.Contains(out, "[[saved-c1.repl-plugin]]") || !strings.Contains(out, `token = "hash_`) {
		t.Fatalf("saved %v:\n%s", err, out)
	}
	v := viper.New()
	v.SetConfigType("toml")
	existing := "[saved-c1]\ntitle = \"c1\"\nreplication-plugin-destination = \"duckdb\"\nreplication-plugin-tables = \"shop.*\"\nreplication-plugin-merge-interval = 60\nreplication-plugin-checkpoint-interval = 10\nreplication-plugin-source = \"master\"\n"
	if err := v.ReadConfig(strings.NewReader(existing + out)); err != nil {
		t.Fatalf("%s\n%s", err, out)
	}
	var back Config
	if err := v.Sub("saved-c1").Unmarshal(&back); err != nil {
		t.Fatal(err)
	}
	if back.ReplicationPluginDestination != "duckdb" || back.ReplicationPluginTables != "shop.*" || back.ReplicationPluginMergeInterval != 60 || back.ReplicationPluginCheckpointInterval != 10 || back.ReplicationPluginSource != "master" {
		t.Fatalf("the existing keys: %+v", back)
	}
	back.SecretKey = conf.SecretKey
	if env := strings.Join(back.ReplPluginEnv(testReplSettings, "repl-mariadb-duckdb-cdc", "duckdb"), "\n"); env != "REPMAN_REPL_SECRET_TOKEN=s3cr3t\nREPMAN_REPL_SETTING_BATCH_ROWS=2000" {
		t.Fatalf("read back: %q (%+v)", env, back.ReplPluginEntries)
	}

	// the JSON never carries the secret, not even encrypted (F10)
	b, _ := json.Marshal(conf)
	if strings.Contains(string(b), "s3cr3t") || strings.Contains(string(b), "hash_") || !strings.Contains(string(b), `"secretsSet":["token"]`) {
		t.Fatalf("JSON %s", b)
	}

	// a secret held in clear (by hand) never reaches the plugin
	conf.ReplPluginEntries[0].Settings["token"] = "clear"
	if env := strings.Join(conf.ReplPluginEnv(testReplSettings, "duckdb"), "\n"); strings.Contains(env, "clear") {
		t.Fatalf("clear secret passed: %q", env)
	}
	// a value with a line break never reaches it either
	conf.ReplPluginEntries[0].Settings["mode"] = "a\nREPMAN_X=1"
	if env := strings.Join(conf.ReplPluginEnv(testReplSettings, "duckdb"), "\n"); strings.Contains(env, "REPMAN_X") {
		t.Fatalf("a line break passed: %q", env)
	}
}

// no [[repl-plugin]]: a config of before loads as before
func TestReplPluginNoEntry(t *testing.T) {
	conf := &Config{}
	if out, err := conf.ReplPluginEntriesToml("saved-c1"); err != nil || out != "" {
		t.Fatalf("%q %v", out, err)
	}
	if env := conf.ReplPluginEnv(nil, "duckdb"); len(env) != 0 {
		t.Fatalf("%v", env)
	}
}
