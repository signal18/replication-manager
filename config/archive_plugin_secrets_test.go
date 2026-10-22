// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func registerEncryptingPlugin(t *testing.T) {
	t.Helper()
	err := RegisterArchivePlugin(ArchivePluginManifest{Name: "archive-testenc", Settings: []ArchivePluginSetting{
		{Name: "base-url", Type: ConstArchiveSettingTypeString},
		{Name: "secret", Type: ConstArchiveSettingTypeSecret},
		{Name: ConstArchiveSettingEncryptionKey, Type: ConstArchiveSettingTypeSecret},
		{Name: ConstArchiveSettingNoEncrypt, Type: ConstArchiveSettingTypeBool},
	}}, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
}

// A new archive takes in the secrets its plugin created, encrypted; never one its manifest does
// not declare as a secret, never over a secret it holds
func TestTakeArchivePluginSecrets(t *testing.T) {
	registerEncryptingPlugin(t)
	conf := archiveTestConf()
	conf.SecretKey = []byte("0123456789abcdef")
	a := ArchiveConfig{Name: "pbs", Plugin: "archive-testenc", Settings: map[string]interface{}{"base-url": "https://pbs"}}
	if err := conf.TakeArchivePluginSecrets(&a, map[string]string{ConstArchiveSettingEncryptionKey: `{"data":"x"}`}); err != nil {
		t.Fatal(err)
	}
	stored, _ := a.Settings[ConstArchiveSettingEncryptionKey].(string)
	if stored == "" || strings.Contains(stored, `"data"`) {
		t.Fatalf("key stored %q: want it encrypted", stored)
	}
	if err := conf.TakeArchivePluginSecrets(&a, map[string]string{ConstArchiveSettingEncryptionKey: `{"data":"other"}`}); err != nil || a.Settings[ConstArchiveSettingEncryptionKey] != stored {
		t.Fatalf("a held key was replaced: %v", err)
	}
	if err := conf.TakeArchivePluginSecrets(&a, map[string]string{"base-url": "x"}); err == nil {
		t.Fatal("a setting that is not a secret was taken in as one")
	}
}

// An archive of an encrypting plugin without a key and no-encrypt off is listed; with a key or
// no-encrypt it is not. A plugin's secrets never reach the JSON, not even encrypted
func TestUnencryptedArchivesAndSecretsHidden(t *testing.T) {
	registerEncryptingPlugin(t)
	conf := archiveTestConf()
	conf.Archives = []ArchiveConfig{
		{Name: "clear", Plugin: "archive-testenc", Settings: map[string]interface{}{"secret": "hash_s"}},
		{Name: "keyed", Plugin: "archive-testenc", Settings: map[string]interface{}{ConstArchiveSettingEncryptionKey: "hash_k"}},
		{Name: "chosen", Plugin: "archive-testenc", Settings: map[string]interface{}{ConstArchiveSettingNoEncrypt: true}},
		{Name: "restic", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{}},
	}
	got := conf.UnencryptedArchives()
	if len(got) != 1 || got[0].Name != "clear" {
		t.Fatalf("unencrypted %+v, want only clear", got)
	}
	b, err := json.Marshal(conf.Archives[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hash_k") || !strings.Contains(string(b), `"secretsSet":["`+ConstArchiveSettingEncryptionKey+`"]`) {
		t.Fatalf("plugin secret in the JSON: %s", b)
	}
	if b, _ := json.Marshal(conf.Archives[0]); strings.Contains(string(b), "hash_s") {
		t.Fatalf("plugin token secret in the JSON: %s", b)
	}
}
