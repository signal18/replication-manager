// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The settings of an external snapshot plugin (#2456): declared by its manifest, stored per
// cluster with the secret encrypted (F9), never sent by the API (F10), passed in its
// environment.

const testSnapPlugin = "storage-test"

func registerTestSnapPlugin(t *testing.T) {
	t.Helper()
	err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: testSnapPlugin, Tier: ConstArchiveTierEnterprise, Settings: PluginSettings{
		{Name: "url", Type: ConstArchiveSettingTypeString},
		{Name: "tls-skip-verify", Type: ConstArchiveSettingTypeBool},
		{Name: "token", Type: ConstArchiveSettingTypeSecret},
	}}, "/opt/snapshotplugin-storage-test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPluginManifestSettingsChecked(t *testing.T) {
	for _, bad := range []PluginSettings{
		{{Name: "Bad Name", Type: ConstArchiveSettingTypeString}},
		{{Name: "a", Type: "blob"}},
		{{Name: "a", Type: ConstArchiveSettingTypeEnum}},
		{{Name: "a", Type: ConstArchiveSettingTypeString}, {Name: "a", Type: ConstArchiveSettingTypeBool}},
	} {
		if err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: "bad-settings", Settings: bad}, "/x"); err == nil {
			t.Errorf("settings %+v accepted", bad)
		}
	}
}

func TestSnapshotPluginSettingsRoundTrip(t *testing.T) {
	registerTestSnapPlugin(t)
	conf := &Config{SecretKey: []byte("0123456789abcdef")}
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"url": "https://storage.example", "tls-skip-verify": "true", "token": "s3cr3t-TOKEN"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"nope": "x"}, nil); err == nil {
		t.Fatal("an undeclared setting accepted")
	}
	if err := conf.PutSnapshotPluginSettings(ConstSnapshotPluginZFS, map[string]interface{}{"url": "x"}, nil); err == nil {
		t.Fatal("settings for a built-in plugin accepted")
	}
	// an empty secret (a form) keeps the one held
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"token": ""}, nil); err != nil {
		t.Fatal(err)
	}

	// saved encrypted (F9)
	out, err := conf.SnapshotPluginEntriesToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t-TOKEN") || !strings.Contains(out, "token = \"hash_") || !strings.Contains(out, "[[saved-c1.snapshot-plugin]]") {
		t.Fatalf("saved:\n%s", out)
	}

	// read back by the config loader
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("[saved-c1]\ntitle = \"c1\"\n" + out)); err != nil {
		t.Fatalf("%s\n%s", err, out)
	}
	var back Config
	if err := v.Sub("saved-c1").Unmarshal(&back); err != nil {
		t.Fatal(err)
	}
	back.SecretKey = conf.SecretKey
	m, _ := SnapshotPlugin(testSnapPlugin)
	env := strings.Join(back.SnapshotPluginEnv(m), "\n")
	for _, want := range []string{"REPMAN_SNAPSHOT_SETTING_URL=https://storage.example", "REPMAN_SNAPSHOT_SETTING_TLS_SKIP_VERIFY=true", "REPMAN_SNAPSHOT_SECRET_TOKEN=s3cr3t-TOKEN"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env %q lacks %s (read back %+v)", env, want, back.SnapshotPluginEntries)
		}
	}

	// the API never gets the secret, not even encrypted (F10)
	b, _ := json.Marshal(conf.SnapshotPluginEntryOf(testSnapPlugin))
	if strings.Contains(string(b), "hash_") || strings.Contains(string(b), "s3cr3t") || !strings.Contains(string(b), `"secretsSet":["token"]`) {
		t.Fatalf("API JSON %s", b)
	}
	b, _ = json.Marshal(conf)
	if strings.Contains(string(b), "s3cr3t") || strings.Contains(string(b), `"token":"hash_`) {
		t.Fatal("the whole config JSON carries the secret")
	}

	// unset removes; an entry left empty goes
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, nil, []string{"url", "tls-skip-verify", "token"}); err != nil {
		t.Fatal(err)
	}
	if len(conf.SnapshotPluginEntryList()) != 0 {
		t.Fatalf("entries left: %+v", conf.SnapshotPluginEntryList())
	}
}

func TestSnapshotPluginSecretNeverInClear(t *testing.T) {
	registerTestSnapPlugin(t)
	conf := &Config{WorkingDir: t.TempDir(), MonitoringKeyPath: "/nonexistent/key"}
	err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"token": "s3cr3t"}, nil)
	if !errors.Is(err, ErrSnapshotSecretNoKey) {
		t.Fatalf("a secret stored without a key: %v", err)
	}
	// a setting that is not a secret needs no key
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"url": "https://x"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPluginEntryHidesUnknownSecrets(t *testing.T) {
	// a plugin not loaded (its binary gone): a value held encrypted is still never sent
	e := SnapshotPluginEntry{Plugin: "not-loaded", Settings: map[string]interface{}{"url": "https://x", "password": "hash_abc"}}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "hash_abc") || !strings.Contains(string(b), `"secretsSet":["password"]`) {
		t.Fatalf("%s", b)
	}
}

func TestSnapshotPluginSettingValuesChecked(t *testing.T) {
	registerTestSnapPlugin(t)
	conf := &Config{SecretKey: []byte("0123456789abcdef")}
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"token": "s3cr3t"}, nil); err != nil {
		t.Fatal(err)
	}
	// a null secret (a JSON null) keeps the one held, never stores "<nil>"
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"token": nil}, nil); err != nil {
		t.Fatal(err)
	}
	m, _ := SnapshotPlugin(testSnapPlugin)
	if env := strings.Join(conf.SnapshotPluginEnv(m), "\n"); !strings.Contains(env, "REPMAN_SNAPSHOT_SECRET_TOKEN=s3cr3t") {
		t.Fatalf("a null secret changed the one held: %s", env)
	}
	if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"url": nil}, nil); err == nil {
		t.Fatal("a null setting accepted")
	}
	for _, bad := range []string{"https://x\nREPMAN_SNAPSHOT_SECRET_TOKEN=forged", "a\x00b", "a\rb"} {
		if err := conf.PutSnapshotPluginSettings(testSnapPlugin, map[string]interface{}{"url": bad}, nil); err == nil {
			t.Fatalf("value %q accepted", bad)
		}
	}
}

// a value written by hand in the TOML with a line break or a NUL never reaches the plugin's
// environment, where it could add a variable; the API refuses it on the way in
func TestSnapshotPluginEnvSkipsLineBreaks(t *testing.T) {
	registerTestSnapPlugin(t)
	m, _ := SnapshotPlugin(testSnapPlugin)
	conf := &Config{SnapshotPluginEntries: []SnapshotPluginEntry{{Plugin: testSnapPlugin, Settings: map[string]interface{}{
		"url": "https://x\nREPMAN_SNAPSHOT_SECRET_TOKEN=forged", "tls-skip-verify": true}}}}
	env := strings.Join(conf.SnapshotPluginEnv(m), "\n")
	if strings.Contains(env, "forged") || strings.Contains(env, "SETTING_URL") || !strings.Contains(env, "REPMAN_SNAPSHOT_SETTING_TLS_SKIP_VERIFY=true") {
		t.Fatalf("env %q", env)
	}
}
