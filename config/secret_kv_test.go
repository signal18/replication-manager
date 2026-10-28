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

// #2227: no Vault, no entry: the config stays the primary
func TestSecretKvNoMigrationWithoutVault(t *testing.T) {
	conf := secretKvTestConf()
	conf.MigrateSecretKvs()
	if len(conf.SecretKvs) != 0 || conf.SecretKvName != "" || conf.IsVaultUsed() {
		t.Fatalf("entries %+v primary %q", conf.SecretKvs, conf.SecretKvName)
	}
}

// #2227: a cluster using Vault gets vault-default from its vault-* keys, the primary, the
// secret encrypted, and the runtime keeps using Vault with the same settings
func TestSecretKvMigrationFromVaultKeys(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultMount = "secret"
	conf.VaultRoleId = "role-1"
	conf.VaultSecretId = "s3cr3t-id"
	conf.MigrateSecretKvs()

	if len(conf.SecretKvs) != 1 || conf.SecretKvName != ConstSecretKvVaultDefault {
		t.Fatalf("entries %+v primary %q", conf.SecretKvs, conf.SecretKvName)
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
	if !conf.IsVaultUsed() || conf.VaultServerAddr != "https://vault.example:8200" || conf.VaultMount != "secret" || conf.VaultRoleId != "role-1" {
		t.Fatalf("runtime changed: %q %q %q", conf.VaultServerAddr, conf.VaultMount, conf.VaultRoleId)
	}
	if conf.GetDecryptedPassword("vault-secret-id", conf.VaultSecretId) != "s3cr3t-id" {
		t.Fatal("runtime secret-id lost")
	}
	if !conf.SecretKvHoldsLegacyKey("vault-server-addr") || !conf.SecretKvHoldsLegacyKey("vault-secret-id") {
		t.Fatal("the save would keep the flat vault keys")
	}
	if len(conf.SecretKvCopies()) != 0 {
		t.Fatal("the primary is a copy")
	}
}

// #2227: the primary back to the config: the Vault* fields go to their defaults, vault-default
// becomes a copy
func TestSecretKvPrimaryToConfig(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.MigrateSecretKvs()
	if err := conf.SetSecretKvName(""); err != nil {
		t.Fatal(err)
	}
	if conf.IsVaultUsed() {
		t.Fatalf("Vault still used: %q", conf.VaultServerAddr)
	}
	if c := conf.SecretKvCopies(); len(c) != 1 || c[0].Name != ConstSecretKvVaultDefault {
		t.Fatalf("copies %+v", c)
	}
	if err := conf.SetSecretKvName("nope"); err == nil {
		t.Fatal("unknown entry accepted as primary")
	}
}

// #2227: a vault-* key pinned in a config file wins over the saved entry
func TestSecretKvPinnedKeyWins(t *testing.T) {
	conf := secretKvTestConf()
	conf.SecretKvs = []SecretKvConfig{{Name: ConstSecretKvVaultDefault, Plugin: ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{"address": "https://old:8200"}}}
	conf.SecretKvName = ConstSecretKvVaultDefault
	conf.ImmuableFlagMap["vault-server-addr"] = "https://new:8200"
	conf.VaultServerAddr = "https://new:8200"
	conf.MigrateSecretKvs()
	if conf.SecretKvs[0].Settings["address"] != "https://new:8200" || conf.VaultServerAddr != "https://new:8200" {
		t.Fatalf("entry %+v runtime %q", conf.SecretKvs[0].Settings, conf.VaultServerAddr)
	}
}

// #2227: the saved list reads back the same, its secret encrypted, never in clear; the API
// never sends it
func TestSecretKvsTomlRoundTrip(t *testing.T) {
	conf := secretKvTestConf()
	conf.VaultServerAddr = "https://vault.example:8200"
	conf.VaultSecretId = "s3cr3t-id"
	conf.MigrateSecretKvs()
	conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: "cloud18", Plugin: ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{"address": "https://c18:8200", "auth": "jwt"}})

	out, err := conf.SecretKvsToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t-id") {
		t.Fatalf("secret in clear in\n%s", out)
	}
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("[saved-c1]\ntitle = \"c1\"\nsecret-kv-name = \"vault-default\"\n" + out)); err != nil {
		t.Fatalf("%s\n%s", err, out)
	}
	var back Config
	if err := v.Sub("saved-c1").Unmarshal(&back); err != nil {
		t.Fatal(err)
	}
	back.SecretKey = conf.SecretKey
	if len(back.SecretKvs) != 2 || back.SecretKvName != ConstSecretKvVaultDefault {
		t.Fatalf("read back %+v primary %q from\n%s", back.SecretKvs, back.SecretKvName, out)
	}
	e := back.SecretKvs[0]
	if e.Name != ConstSecretKvVaultDefault || !e.Enabled || e.Settings["address"] != "https://vault.example:8200" || back.SecretKvSetting(e, "secret-id") != "s3cr3t-id" {
		t.Fatalf("vault-default %+v", e)
	}
	if _, ok := e.Settings["name"]; ok {
		t.Fatalf("name kept as a setting: %+v", e.Settings)
	}
	if back.SecretKvs[1].Name != "cloud18" || back.SecretKvs[1].Settings["auth"] != "jwt" {
		t.Fatalf("cloud18 %+v", back.SecretKvs[1])
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
	if c := conf.SecretKvCopies(); len(c) != 1 || c[0].Settings["address"] != "https://c18:8200" {
		t.Fatalf("copies %+v", c)
	}
	if err := conf.SetSecretKvSetting("cloud18", "nonsense", "1", false); err == nil {
		t.Fatal("unknown setting accepted")
	}
	if err := conf.SetSecretKvSetting("other", "plugin", "secret_kv-localstore", false); err == nil {
		t.Fatal("a non-Vault entry accepted")
	}
	if err := conf.SetSecretKvSetting("cloud18", "plugin", "", true); err != nil || len(conf.SecretKvCopies()) != 0 {
		t.Fatalf("unset plugin: %v %+v", err, conf.SecretKvs)
	}
	if !IsSecretKvInstanceLocalKey("secret-kv.vault-default.secret-id") || IsSecretKvInstanceLocalKey("secret-kv.vault-default.address") {
		t.Fatal("instance-local keys")
	}
}

// #2227: the cluster's space in a shared store
func TestClusterSpace(t *testing.T) {
	conf := &Config{}
	if s := conf.ClusterSpace("c1"); s != "c1" {
		t.Fatalf("space %q", s)
	}
	conf = &Config{Cloud18: true, Cloud18Domain: "d", Cloud18SubDomain: "s", Cloud18SubDomainZone: "fr"}
	if s := conf.ClusterSpace("c1"); s != "d/s-fr/c1" {
		t.Fatalf("space %q", s)
	}
}
