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

func secretKvTestConf() *Config {
	return &Config{
		DefaultFlagMap: map[string]interface{}{
			"vault-server-addr": "",
			"vault-mount":       "kv",
			"vault-mode":        VaultConfigStoreV2,
			"vault-auth":        ConstVaultAuthAppRole,
			"vault-role-id":     "",
			"vault-secret-id":   "",
		},
		ImmuableFlagMap: map[string]interface{}{},
		DynamicFlagMap:  map[string]interface{}{},
		VaultMount:      "kv",
		VaultMode:       VaultConfigStoreV2,
		VaultAuth:       ConstVaultAuthAppRole,
		SecretKey:       []byte("0123456789abcdef"),
	}
}

// #2227: no Vault, no entry
func TestSecretKvNoMigrationWithoutVault(t *testing.T) {
	conf := secretKvTestConf()
	conf.MigrateSecretKvs()
	if len(conf.SecretKvs) != 0 || conf.IsVaultUsed() {
		t.Fatalf("entries %+v", conf.SecretKvs)
	}
}

// #2227: a cluster using Vault gets vault-default from its vault-* keys, enabled, the secret
// encrypted; the flat Vault fields go back to their defaults: Vault is an entry now
func TestSecretKvMigrationFromVaultKeys(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultMount = "secret"
	conf.VaultRoleId = "role-1"
	conf.VaultSecretId = "s3cr3t-id"
	conf.MigrateSecretKvs()

	if len(conf.SecretKvs) != 1 {
		t.Fatalf("entries %+v", conf.SecretKvs)
	}
	e := conf.SecretKvs[0]
	if e.Name != ConstSecretKvVaultDefault || e.Plugin != ConstSecretKvPluginVault || !e.Enabled {
		t.Fatalf("entry %+v", e)
	}
	if e.Settings["address"] != "https://vault.example:8200" || e.Settings["mount"] != "secret" || e.Settings["role-id"] != "role-1" {
		t.Fatalf("settings %+v", e.Settings)
	}
	if _, ok := e.Settings["mode"]; ok {
		t.Errorf("a default value is stored: %+v", e.Settings)
	}
	sid, _ := e.Settings["secret-id"].(string)
	if !strings.HasPrefix(sid, "hash_") || conf.SecretKvSetting(e, "secret-id") != "s3cr3t-id" {
		t.Fatalf("secret-id held %q", sid)
	}
	if conf.IsVaultUsed() || conf.VaultMount != "kv" || conf.VaultRoleId != "" || conf.VaultSecretId != "" {
		t.Fatalf("flat Vault fields kept: %q %q %q", conf.VaultServerAddr, conf.VaultMount, conf.VaultRoleId)
	}
	if !conf.SecretKvHoldsLegacyKey("vault-server-addr") || !conf.SecretKvHoldsLegacyKey("vault-secret-id") {
		t.Fatal("the save would keep the flat vault keys")
	}
	if a := conf.SecretKvActive(); len(a) != 1 || a[0].Name != ConstSecretKvVaultDefault {
		t.Fatalf("active %+v", a)
	}
	// a second load (the saved list) leaves it as it is
	conf.MigrateSecretKvs()
	if len(conf.SecretKvs) != 1 || conf.SecretKvs[0].Settings["address"] != "https://vault.example:8200" {
		t.Fatalf("second load %+v", conf.SecretKvs)
	}
}

// #2227: a database engine Vault is migrated too, its mode kept
func TestSecretKvMigrationDatabaseEngine(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultMode = VaultDbEngine
	conf.MigrateSecretKvs()
	if len(conf.SecretKvs) != 1 || conf.SecretKvs[0].Settings["mode"] != VaultDbEngine {
		t.Fatalf("entries %+v", conf.SecretKvs)
	}
	if conf.IsVaultUsed() || conf.VaultMode != VaultConfigStoreV2 {
		t.Fatalf("flat fields %q %q", conf.VaultServerAddr, conf.VaultMode)
	}
}

// #2227: a vault-* key pinned in a config file wins over the saved entry
func TestSecretKvPinnedKeyWins(t *testing.T) {
	conf := secretKvTestConf()
	conf.SecretKvs = []SecretKvConfig{{Name: ConstSecretKvVaultDefault, Plugin: ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{"address": "https://old:8200"}}}
	conf.ImmuableFlagMap["vault-server-addr"] = "https://new:8200"
	conf.VaultServerAddr = "https://new:8200"
	conf.MigrateSecretKvs()
	if conf.SecretKvs[0].Settings["address"] != "https://new:8200" || conf.IsVaultUsed() {
		t.Fatalf("entry %+v runtime %q", conf.SecretKvs[0].Settings, conf.VaultServerAddr)
	}
}

// #2227: a login injected after the load (the back-office) goes into vault-default
func TestSecretKvSyncInjectedLogin(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.MigrateSecretKvs()
	conf.VaultRoleId, conf.VaultSecretId = "role-2", "id-2"
	conf.SyncSecretKvFromConfig()
	e := conf.SecretKvs[0]
	if e.Settings["role-id"] != "role-2" || conf.SecretKvSetting(e, "secret-id") != "id-2" || conf.VaultRoleId != "" {
		t.Fatalf("entry %+v field %q", e.Settings, conf.VaultRoleId)
	}
}

// #2227: the saved list reads back the same, its secret encrypted, never in clear; the API
// never sends it
func TestSecretKvsTomlRoundTrip(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultSecretId = "s3cr3t-id"
	conf.MigrateSecretKvs()
	conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: "cloud18", Plugin: ConstSecretKvPluginVault, Enabled: false, Settings: map[string]interface{}{"address": "https://c18:8200", "auth": "jwt"}})

	out, err := conf.SecretKvsToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t-id") {
		t.Fatalf("secret in clear in\n%s", out)
	}
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
	if len(back.SecretKvs) != 2 {
		t.Fatalf("read back %+v from\n%s", back.SecretKvs, out)
	}
	e := back.SecretKvs[0]
	if e.Name != ConstSecretKvVaultDefault || !e.Enabled || e.Settings["address"] != "https://vault.example:8200" || back.SecretKvSetting(e, "secret-id") != "s3cr3t-id" {
		t.Fatalf("vault-default %+v", e)
	}
	if _, ok := e.Settings["name"]; ok {
		t.Fatalf("name kept as a setting: %+v", e.Settings)
	}
	if back.SecretKvs[1].Name != "cloud18" || back.SecretKvs[1].Enabled || back.SecretKvs[1].Settings["auth"] != "jwt" {
		t.Fatalf("cloud18 %+v", back.SecretKvs[1])
	}
	if a := back.SecretKvActive(); len(a) != 1 {
		t.Fatalf("active %+v", a)
	}
	js, _ := json.Marshal(back.SecretKvs[0])
	if strings.Contains(string(js), "hash_") || !strings.Contains(string(js), `"secretsSet":["secret-id"]`) {
		t.Fatalf("json %s", js)
	}
}

// #2227: without an encryption key the secret is left out of the saved entry, and the flat
// vault-secret-id is kept by the save as before (F9)
func TestSecretKvNoKeyNoClearSecret(t *testing.T) {
	conf := secretKvTestConf()
	conf.SecretKey = nil
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultSecretId = "s3cr3t-id"
	conf.MigrateSecretKvs()
	out, err := conf.SecretKvsToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t-id") || strings.Contains(out, "secret-id") {
		t.Fatalf("secret written without a key:\n%s", out)
	}
	if conf.SecretKvHoldsLegacyKey("vault-secret-id") {
		t.Fatal("the flat secret would be dropped with nowhere to go")
	}
}

// #2227: a peer's event sets one setting, enables an entry, removes it; the list holds Vault
// entries only; an entry's Vault login is instance-local
func TestSecretKvSetSettingFromEvent(t *testing.T) {
	conf := secretKvTestConf()
	for _, ev := range [][2]string{{"plugin", ConstSecretKvPluginVault}, {"address", "https://c18:8200"}, {"enabled", "true"}} {
		if err := conf.SetSecretKvSetting("cloud18", ev[0], ev[1], false); err != nil {
			t.Fatal(err)
		}
	}
	if a := conf.SecretKvActive(); len(a) != 1 || a[0].Settings["address"] != "https://c18:8200" {
		t.Fatalf("active %+v", a)
	}
	if err := conf.SetSecretKvSetting("cloud18", "nonsense", "1", false); err == nil {
		t.Fatal("unknown setting accepted")
	}
	if err := conf.SetSecretKvSetting("other", "plugin", "secret_kv-localstore", false); err == nil {
		t.Fatal("a non-Vault entry accepted")
	}
	if err := conf.SetSecretKvSetting("cloud18", "plugin", "", true); err != nil || len(conf.SecretKvActive()) != 0 {
		t.Fatalf("unset plugin: %v %+v", err, conf.SecretKvs)
	}
	if !IsSecretKvInstanceLocalKey("secret-kv.vault-default.secret-id") || IsSecretKvInstanceLocalKey("secret-kv.vault-default.address") {
		t.Fatal("instance-local keys")
	}
}

// #2227: the cluster's folder in a vault entry: <path-prefix>/<cluster>, or the cluster at the
// mount's root
func TestSecretKvFolder(t *testing.T) {
	conf := &Config{}
	e := SecretKvConfig{Name: "v", Plugin: ConstSecretKvPluginVault, Settings: map[string]interface{}{}}
	if f := conf.SecretKvFolder(e, "c1"); f != "c1" {
		t.Fatalf("folder %q", f)
	}
	e.Settings["path-prefix"] = "d/s"
	if f := conf.SecretKvFolder(e, "c1"); f != "d/s/c1" {
		t.Fatalf("folder %q", f)
	}
	if s := conf.ClusterSpace("c1"); s != "c1" {
		t.Fatalf("bucket space %q", s)
	}
}

// #2227: an entry added and changed from its manifest's settings: the secret encrypted and
// kept by an empty field, an unknown setting or another plugin refused, an address required
func TestPutSecretKv(t *testing.T) {
	conf := secretKvTestConf()
	on := true
	if _, err := conf.PutSecretKv(SecretKvSpec{Name: "own", Enabled: &on, Settings: map[string]interface{}{"address": "https://v:8200", "role-id": "r", "secret-id": "s1"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := conf.PutSecretKv(SecretKvSpec{Name: "own", Settings: map[string]interface{}{"address": "https://v:8200"}}, true); err == nil {
		t.Fatal("added twice")
	}
	if _, err := conf.PutSecretKv(SecretKvSpec{Name: "own", Settings: map[string]interface{}{"secret-id": "", "path-prefix": "d/s"}}, false); err != nil {
		t.Fatal(err)
	}
	e := conf.SecretKvs[0]
	if !e.Enabled || e.Settings["path-prefix"] != "d/s" || conf.SecretKvSetting(e, "secret-id") != "s1" || !strings.HasPrefix(e.Settings["secret-id"].(string), "hash_") {
		t.Fatalf("entry %+v", e)
	}
	for _, bad := range []SecretKvSpec{
		{Name: "x", Settings: map[string]interface{}{"address": "https://v", "nonsense": 1}},
		{Name: "y", Plugin: "secret_kv-localstore", Settings: map[string]interface{}{"address": "https://v"}},
		{Name: "z", Settings: map[string]interface{}{"mount": "kv"}},
		{Name: "Bad Name", Settings: map[string]interface{}{"address": "https://v"}},
	} {
		if _, err := conf.PutSecretKv(bad, true); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := conf.SetSecretKvEnabled("own", false); err != nil || conf.SecretKvs[0].Enabled {
		t.Fatal("not turned off")
	}
	if err := conf.DeleteSecretKv("own"); err != nil || len(conf.SecretKvs) != 0 {
		t.Fatal("not removed")
	}
}
