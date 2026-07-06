// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"strings"
	"testing"
)

// These tests replay the boot sequence around secret ciphertext stability:
// DecryptSecretsFromConfig seeds the Crypted cache from the raw hash_ values
// found in the flag maps, and GetStableEncryptedValue must then return the
// exact same ciphertext for an unchanged plaintext — otherwise every restart
// re-encrypts every secret (random IV), churning the saved tomls, git and
// the config event log.

func newSeedTestConf(t *testing.T) *Config {
	t.Helper()
	return &Config{
		SecretKey:       []byte("0123456789abcdef0123456789abcdef"),
		DynamicFlagMap:  map[string]interface{}{},
		ImmuableFlagMap: map[string]interface{}{},
		DefaultFlagMap:  map[string]interface{}{},
	}
}

// Simple secret (single ciphertext, no user prefix), raw value present in the
// dynamic map — the standard boot state for a per-cluster saved secret.
func TestBootSeedSimpleSecretStable(t *testing.T) {
	conf := newSeedTestConf(t)
	cipher1 := conf.EncryptSecretValue("smtp-pass")
	if !strings.Contains(cipher1, "hash_") {
		t.Fatalf("encrypt did not produce ciphertext: %s", cipher1)
	}
	conf.DynamicFlagMap["mail-smtp-password"] = cipher1

	conf.DecryptSecretsFromConfig()

	if got := conf.Secrets["mail-smtp-password"].Value; got != "smtp-pass" {
		t.Fatalf("decrypted value = %q, want smtp-pass", got)
	}
	if got := conf.Secrets["mail-smtp-password"].Crypted; got != cipher1 {
		t.Errorf("Crypted cache not seeded at load: %q", got)
	}
	if got := conf.GetStableEncryptedValue("mail-smtp-password", "smtp-pass"); got != cipher1 {
		t.Errorf("first save re-encrypted: got %q want %q", got, cipher1)
	}
}

// Composite credential ("user:hash_...") — db-servers-credential style.
func TestBootSeedCompositeSecretStable(t *testing.T) {
	conf := newSeedTestConf(t)
	cipher1 := conf.EncryptSecretValue("root:dbpass")
	conf.DynamicFlagMap["db-servers-credential"] = cipher1

	conf.DecryptSecretsFromConfig()

	if got := conf.Secrets["db-servers-credential"].Value; got != "root:dbpass" {
		t.Fatalf("decrypted value = %q, want root:dbpass", got)
	}
	if got := conf.GetStableEncryptedValue("db-servers-credential", "root:dbpass"); got != cipher1 {
		t.Errorf("first save re-encrypted composite: got %q want %q", got, cipher1)
	}
}

// Multi-user list ("admin:hash_...,viewer:hash_...") — api-credentials style,
// where the save path recomposes the plaintext from APIUsers.
func TestBootSeedMultiUserSecretStable(t *testing.T) {
	conf := newSeedTestConf(t)
	cipher1 := conf.EncryptSecretValue("admin:adminpw,viewer:viewerpw")
	conf.DynamicFlagMap["api-credentials"] = cipher1

	conf.DecryptSecretsFromConfig()

	if got := conf.Secrets["api-credentials"].Value; got != "admin:adminpw,viewer:viewerpw" {
		t.Fatalf("decrypted value = %q", got)
	}
	if got := conf.GetStableEncryptedValue("api-credentials", "admin:adminpw,viewer:viewerpw"); got != cipher1 {
		t.Errorf("first save re-encrypted api-credentials: got %q want %q", got, cipher1)
	}
}

// Second boot: the ciphertext produced by boot N must be reused at boot N+1.
func TestBootSeedStableAcrossRestarts(t *testing.T) {
	conf := newSeedTestConf(t)
	cipher1 := conf.EncryptSecretValue("root:dbpass")
	conf.DynamicFlagMap["db-servers-credential"] = cipher1
	conf.DecryptSecretsFromConfig()
	saved := conf.GetStableEncryptedValue("db-servers-credential", "root:dbpass")

	// "Restart": fresh Config, maps seeded with what the previous run saved.
	conf2 := newSeedTestConf(t)
	conf2.DynamicFlagMap["db-servers-credential"] = saved
	conf2.DecryptSecretsFromConfig()
	if got := conf2.GetStableEncryptedValue("db-servers-credential", "root:dbpass"); got != saved {
		t.Errorf("restart re-encrypted: got %q want %q", got, saved)
	}
}

// Documents the failure mode: raw hash_ value missing from every flag map at
// DecryptSecretsFromConfig time (only a plaintext default) — the cache cannot
// seed and the first save must produce fresh ciphertext.
func TestBootSeedMissingRawReencrypts(t *testing.T) {
	conf := newSeedTestConf(t)
	conf.DefaultFlagMap["mail-smtp-password"] = "smtp-pass" // plaintext, no hash_
	conf.DecryptSecretsFromConfig()
	if got := conf.Secrets["mail-smtp-password"].Crypted; got != "" {
		t.Fatalf("Crypted unexpectedly seeded: %q", got)
	}
	out := conf.GetStableEncryptedValue("mail-smtp-password", "smtp-pass")
	if !strings.Contains(out, "hash_") {
		t.Errorf("expected fresh ciphertext, got %q", out)
	}
}
