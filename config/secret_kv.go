// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// A cluster's credential stores are entries (#2227, doc/implementation/config/
// CREDENTIAL_STORES.md "Store entries"), the same model as its archives (archive.go): a TOML
// array of tables, [[<cluster>.secret-kv]] in a config file, [[saved-<cluster>.secret-kv]] in
// the saved <cluster>.toml, each entry a name, its plugin, its settings (secrets encrypted,
// F9) and whether it is enabled.
//
// The primary is the entry secret-kv-name names ("" = the config, the behaviour before the
// entries): it is read, it rotates, it is the reference. Every other enabled entry is a copy:
// the credential chain hands it the plain values after each change, it is never read back.
//
// The Vault settings of a cluster (vault-server-addr and the vault-* keys) are migrated at load
// into the entry vault-default, the primary. In this step the runtime still reads the Vault*
// fields: the primary Vault entry is projected onto them at load, at a peer replay and at a
// change of the primary, and they are captured back into it at each save, as the archive list
// does with the BackupRestic* fields. The saved config holds the entry, never the vault-* keys
// it replaced.

// The secret_kv plugins: two. secret_kv-config is built in and always there: the config
// files and the versioned local file (secret_store.json) as one store, holding every secret
// and its versions; it is the base store, never an entry of the list, and the primary when
// secret-kv-name is "". secret_kv-vault has n entries, the list holds only those.
const (
	ConstSecretKvPluginConfig = "secret_kv-config" // built in: the config and the versioned local file
	ConstSecretKvPluginVault  = "secret_kv-vault"  // a Vault KV, one per entry
)

// ConstSecretKvVaultDefault is the entry the vault-* keys are migrated into.
const ConstSecretKvVaultDefault = "vault-default"

// Credential store plugin capabilities.
const (
	ConstSecretKvCapPrimary = "primary" // it can be the primary
	ConstSecretKvCapCopy    = "copy"    // it takes copies (a writer exists for it)
)

// SecretKvPluginManifest is the config a secret_kv plugin comes with: the typed settings of
// its entries (secrets included) and what it can be. The entry forms are drawn from it.
type SecretKvPluginManifest struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Tier         string         `json:"tier,omitempty"` // ConstArchiveTierEnterprise, "" for every plan
	Settings     PluginSettings `json:"settings"`
	Capabilities []string       `json:"capabilities"`
}

// Can: the plugin has the capability.
func (m SecretKvPluginManifest) Can(capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// The settings of a secret_kv-vault entry.
const (
	ConstSecretKvVaultAddress  = "address"
	ConstSecretKvVaultMount    = "mount"
	ConstSecretKvVaultMode     = "mode"
	ConstSecretKvVaultAuth     = "auth"
	ConstSecretKvVaultRoleID   = "role-id"
	ConstSecretKvVaultSecretID = "secret-id"
)

// Vault auth methods an entry signs in with.
const (
	ConstVaultAuthAppRole = "approle"
	ConstVaultAuthJWT     = "jwt" // the instance's Cloud18 identity token (#2159)
)

var secretKvManifests = map[string]SecretKvPluginManifest{
	ConstSecretKvPluginConfig: {
		Name:         ConstSecretKvPluginConfig,
		Description:  "Built in, always there: the config and the versioned local file, every secret with its versions",
		Settings:     PluginSettings{},
		Capabilities: []string{ConstSecretKvCapPrimary},
	},
	ConstSecretKvPluginVault: {
		Name:        ConstSecretKvPluginVault,
		Description: "A Vault KV: the user's own, or the Cloud18 vault",
		Settings: PluginSettings{
			{Name: ConstSecretKvVaultAddress, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultMount, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultMode, Type: ConstArchiveSettingTypeEnum, Values: []string{VaultConfigStoreV2, VaultDbEngine}},
			{Name: ConstSecretKvVaultAuth, Type: ConstArchiveSettingTypeEnum, Values: []string{ConstVaultAuthAppRole, ConstVaultAuthJWT}},
			{Name: ConstSecretKvVaultRoleID, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultSecretID, Type: ConstArchiveSettingTypeSecret},
		},
		Capabilities: []string{ConstSecretKvCapPrimary, ConstSecretKvCapCopy},
	},
}

// SecretKvPlugin is the manifest of a secret_kv plugin.
func SecretKvPlugin(name string) (SecretKvPluginManifest, bool) {
	m, ok := secretKvManifests[name]
	return m, ok
}

// SecretKvPlugins lists the secret_kv plugin manifests, by name.
func SecretKvPlugins() []SecretKvPluginManifest {
	out := make([]SecretKvPluginManifest, 0, len(secretKvManifests))
	for _, m := range secretKvManifests {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// secretKvVaultLegacyKeys are the flat keys a secret_kv-vault entry replaces, by setting.
// vault-token stays flat: it is a session token, not a setting.
var secretKvVaultLegacyKeys = map[string]string{
	ConstSecretKvVaultAddress:  "vault-server-addr",
	ConstSecretKvVaultMount:    "vault-mount",
	ConstSecretKvVaultMode:     "vault-mode",
	ConstSecretKvVaultAuth:     "vault-auth",
	ConstSecretKvVaultRoleID:   "vault-role-id",
	ConstSecretKvVaultSecretID: "vault-secret-id",
}

// IsSecretKvLegacyKey: key is one of the vault-* keys an entry replaces.
func IsSecretKvLegacyKey(key string) bool {
	for _, k := range secretKvVaultLegacyKeys {
		if k == key {
			return true
		}
	}
	return false
}

// IsSecretKvInstanceLocalKey: the event key ("secret-kv.<name>.<setting>") is an entry's Vault
// login (role-id, secret-id): each instance owns its own, as vault-role-id and vault-secret-id.
func IsSecretKvInstanceLocalKey(key string) bool {
	rest, ok := strings.CutPrefix(key, "secret-kv.")
	if !ok {
		return false
	}
	return strings.HasSuffix(rest, "."+ConstSecretKvVaultRoleID) || strings.HasSuffix(rest, "."+ConstSecretKvVaultSecretID)
}

// SecretKvConfig is one entry of the credential store list. Settings holds the settings and
// the secrets (encrypted) its plugin's manifest declares.
type SecretKvConfig struct {
	Name     string                 `mapstructure:"name" toml:"name" json:"name"`
	Plugin   string                 `mapstructure:"plugin" toml:"plugin" json:"plugin"`
	Enabled  bool                   `mapstructure:"enabled" toml:"enabled" json:"enabled"`
	Settings map[string]interface{} `mapstructure:",remain" toml:"-" json:"settings"`
}

// isSecret: the entry's plugin declares name as a secret.
func (e SecretKvConfig) isSecret(name string) bool {
	m, ok := SecretKvPlugin(e.Plugin)
	return ok && m.Settings.HasSecret(name)
}

// MarshalJSON never sends a secret, not even encrypted: secretsSet names the ones held.
func (e SecretKvConfig) MarshalJSON() ([]byte, error) {
	settings, secrets := splitSecretSettings(e.Settings, e.isSecret)
	return json.Marshal(struct {
		Name       string                 `json:"name"`
		Plugin     string                 `json:"plugin"`
		Enabled    bool                   `json:"enabled"`
		Settings   map[string]interface{} `json:"settings"`
		SecretsSet []string               `json:"secretsSet"`
	}{e.Name, e.Plugin, e.Enabled, settings, secrets})
}

// Setting is the entry's setting as a string: its value, else the default of the flat key it
// replaced, else "". A secret is returned as held (encrypted).
func (conf *Config) secretKvSetting(e *SecretKvConfig, name string) string {
	if v, ok := e.Settings[name]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	if key, ok := secretKvVaultLegacyKeys[name]; ok && e.Plugin == ConstSecretKvPluginVault {
		if def, ok := conf.DefaultFlagMap[key]; ok && def != nil {
			return fmt.Sprintf("%v", def)
		}
	}
	return ""
}

// SecretKvSetting is the entry's setting as SecretKvConfig.Setting reads it; a secret
// decrypted. For the store's own use, never for a log or the API.
func (conf *Config) SecretKvSetting(e SecretKvConfig, name string) string {
	v := conf.secretKvSetting(&e, name)
	if e.isSecret(name) {
		return conf.GetDecryptedPassword(name, v)
	}
	return v
}

// secretKvMu guards the list and secret-kv-name: the save, the peer replay and the copy
// worker read and write them.
var secretKvMu sync.Mutex

func cloneSecretKvs(in []SecretKvConfig) []SecretKvConfig {
	if in == nil {
		return nil
	}
	out := make([]SecretKvConfig, len(in))
	for i, e := range in {
		out[i] = e
		out[i].Settings = cloneSettings(e.Settings)
	}
	return out
}

func (conf *Config) secretKvByName(name string) *SecretKvConfig {
	for i := range conf.SecretKvs {
		if conf.SecretKvs[i].Name == name {
			return &conf.SecretKvs[i]
		}
	}
	return nil
}

// cleanSecretKvSettings drops from the entries read what their plugin does not declare. The
// list holds secret_kv-vault entries only: secret_kv-config is built in, never an entry.
func (conf *Config) cleanSecretKvSettings() {
	kept := conf.SecretKvs[:0]
	for _, e := range conf.SecretKvs {
		if e.Plugin == ConstSecretKvPluginVault {
			kept = append(kept, e)
		}
	}
	conf.SecretKvs = kept
	for i := range conf.SecretKvs {
		e := &conf.SecretKvs[i]
		if e.Settings == nil {
			e.Settings = map[string]interface{}{}
		}
		m, ok := SecretKvPlugin(e.Plugin)
		if !ok {
			continue
		}
		for k := range e.Settings {
			if _, declared := m.Settings.Setting(k); !declared {
				delete(e.Settings, k)
			}
		}
	}
}

// primaryVaultEntry is the primary when it is an enabled secret_kv-vault entry.
func (conf *Config) primaryVaultEntry() *SecretKvConfig {
	if conf.SecretKvName == "" {
		return nil
	}
	e := conf.secretKvByName(conf.SecretKvName)
	if e == nil || e.Plugin != ConstSecretKvPluginVault || !e.Enabled {
		return nil
	}
	return e
}

// MigrateSecretKvs resolves the credential store list at cluster init: a cluster without a
// list that uses Vault (vault-server-addr set) gets vault-default from its vault-* keys,
// enabled and primary, so a cluster using Vault keeps doing so. With a list, the vault-* keys
// a config file pins are the file's: they replace the settings of vault-default (the file
// wins). Then the primary Vault entry is projected onto the Vault* fields.
func (conf *Config) MigrateSecretKvs() {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	// a Config is copied by value: the list must not share entries with another copy
	conf.SecretKvs = cloneSecretKvs(conf.SecretKvs)
	conf.cleanSecretKvSettings()
	if len(conf.SecretKvs) == 0 {
		conf.createVaultDefault()
	} else if e := conf.secretKvByName(ConstSecretKvVaultDefault); e != nil && e.Plugin == ConstSecretKvPluginVault {
		for name, key := range secretKvVaultLegacyKeys {
			if v, pinned := conf.GetImmuableFlag(key); pinned && v != nil {
				conf.storeSecretKvValue(e, name, fmt.Sprintf("%v", v))
			}
		}
	}
	conf.projectSecretKv()
}

// createVaultDefault adds vault-default from the vault-* keys when Vault is used, the primary
// unless a config file pins secret-kv-name. Call it under secretKvMu with an empty list.
func (conf *Config) createVaultDefault() {
	if conf.VaultServerAddr == "" {
		return
	}
	conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: ConstSecretKvVaultDefault, Plugin: ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{}})
	e := &conf.SecretKvs[len(conf.SecretKvs)-1]
	for name, key := range secretKvVaultLegacyKeys {
		if f, ok := configField(conf, key); ok && !conf.isDefaultValue(key, f) {
			conf.storeSecretKvValue(e, name, fmt.Sprintf("%v", fieldValue(f)))
		}
	}
	if !conf.isPinned("secret-kv-name") {
		conf.SecretKvName = ConstSecretKvVaultDefault
	}
}

// storeSecretKvValue stores a printed value into the entry, typed by its plugin's manifest; a
// secret encrypted (an already encrypted one kept as it is).
func (conf *Config) storeSecretKvValue(e *SecretKvConfig, name, value string) {
	if e.Settings == nil {
		e.Settings = map[string]interface{}{}
	}
	m, ok := SecretKvPlugin(e.Plugin)
	if !ok {
		e.Settings[name] = value
		return
	}
	st, ok := m.Settings.Setting(name)
	if !ok {
		return
	}
	if v, err := conf.typedSettingValue(st, value); err == nil {
		e.Settings[name] = v
	}
}

// projectSecretKv sets the Vault* fields from the primary Vault entry; with no primary Vault
// entry they go back to their default, so Vault is not used. A pinned key keeps its value.
// Call it under secretKvMu.
func (conf *Config) projectSecretKv() {
	p := conf.primaryVaultEntry()
	for name, key := range secretKvVaultLegacyKeys {
		if conf.isPinned(key) {
			continue
		}
		f, ok := configField(conf, key)
		if !ok {
			continue
		}
		if p != nil {
			if v, ok := p.Settings[name]; ok && v != nil {
				setField(f, v)
				continue
			}
		}
		if def, ok := conf.DefaultFlagMap[key]; ok {
			setField(f, def)
		} else {
			f.Set(reflect.Zero(f.Type()))
		}
	}
}

// SyncSecretKvFromConfig captures the Vault* fields into the primary Vault entry before a
// save: a setter or inject.toml changed the field the runtime reads. No list and Vault used:
// vault-default is created from them, as at load.
func (conf *Config) SyncSecretKvFromConfig() {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	if len(conf.SecretKvs) == 0 {
		conf.createVaultDefault()
		return
	}
	p := conf.primaryVaultEntry()
	if p == nil {
		return
	}
	for name, key := range secretKvVaultLegacyKeys {
		f, ok := configField(conf, key)
		if !ok {
			continue
		}
		if conf.isDefaultValue(key, f) && !conf.isPinned(key) {
			delete(p.Settings, name)
			continue
		}
		v := fmt.Sprintf("%v", fieldValue(f))
		if p.isSecret(name) && conf.GetDecryptedPassword(name, v) == conf.GetDecryptedPassword(name, conf.secretKvSetting(p, name)) {
			continue // the same secret: keep its ciphertext, the save stays stable
		}
		conf.storeSecretKvValue(p, name, v)
	}
}

// SecretKvHoldsLegacyKey: the primary Vault entry carries the vault-* key, so the save leaves
// the flat key out. A secret the entry cannot hold encrypted (no encryption key) stays flat:
// the entry never writes it (F9).
func (conf *Config) SecretKvHoldsLegacyKey(key string) bool {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	p := conf.primaryVaultEntry()
	if p == nil {
		return false
	}
	for name, k := range secretKvVaultLegacyKeys {
		if k != key {
			continue
		}
		if p.isSecret(name) {
			v, ok := p.Settings[name]
			return ok && strings.HasPrefix(fmt.Sprintf("%v", v), "hash_")
		}
		return true
	}
	return false
}

// SecretKvsToml renders the list as [[<section>.secret-kv]] tables for the saved config. A
// secret not encrypted (no encryption key) is left out: it never reaches the config
// repository in clear (F9).
func (conf *Config) SecretKvsToml(section string) (string, error) {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var b strings.Builder
	for _, e := range conf.SecretKvs {
		head := map[string]interface{}{"name": e.Name, "plugin": e.Plugin, "enabled": e.Enabled}
		settings := make(map[string]interface{}, len(e.Settings))
		for k, v := range e.Settings {
			if e.isSecret(k) && !strings.HasPrefix(fmt.Sprintf("%v", v), "hash_") {
				continue
			}
			settings[k] = v
		}
		if err := writeEntryToml(&b, section, "secret-kv", head, settings); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// SecretKvList is a copy of the list and the primary's name.
func (conf *Config) SecretKvList() ([]SecretKvConfig, string) {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	return cloneSecretKvs(conf.SecretKvs), conf.SecretKvName
}

// SecretKvCopies are the entries that take copies: enabled Vault entries, not the primary.
func (conf *Config) SecretKvCopies() []SecretKvConfig {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var out []SecretKvConfig
	for _, e := range conf.SecretKvs {
		if !e.Enabled || e.Name == conf.SecretKvName || e.Plugin != ConstSecretKvPluginVault {
			continue
		}
		out = append(out, cloneSecretKvs([]SecretKvConfig{e})[0])
	}
	return out
}

// SecretKvCopy is the entry name when it takes copies now.
func (conf *Config) SecretKvCopy(name string) (SecretKvConfig, bool) {
	for _, e := range conf.SecretKvCopies() {
		if e.Name == name {
			return e, true
		}
	}
	return SecretKvConfig{}, false
}

// SetSecretKvName makes name the primary ("" the config) and projects it.
func (conf *Config) SetSecretKvName(name string) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	if name != "" {
		e := conf.secretKvByName(name)
		if e == nil {
			return fmt.Errorf("unknown credential store %s", name)
		}
		if m, ok := SecretKvPlugin(e.Plugin); ok && !m.Can(ConstSecretKvCapPrimary) {
			return fmt.Errorf("credential store %s: plugin %s cannot be the primary", name, e.Plugin)
		}
	}
	conf.SecretKvName = name
	conf.projectSecretKv()
	return nil
}

// SetSecretKvSetting applies one entry setting by name (a peer's event
// "secret-kv.<name>.<setting>"), creating the entry when needed, then projects the primary.
// unset removes the setting or, for "plugin", the entry. A secret is the peer's ciphertext:
// peers share the encryption key.
func (conf *Config) SetSecretKvSetting(entry, setting, value string, unset bool) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	if err := validateEntryName("credential store", entry); err != nil {
		return err
	}
	e := conf.secretKvByName(entry)
	if e == nil {
		if unset {
			return nil
		}
		conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: entry, Settings: map[string]interface{}{}})
		e = &conf.SecretKvs[len(conf.SecretKvs)-1]
	}
	if e.Settings == nil {
		e.Settings = map[string]interface{}{}
	}
	switch setting {
	case "name":
		return nil
	case "plugin":
		if unset {
			for i := range conf.SecretKvs {
				if conf.SecretKvs[i].Name == entry {
					conf.SecretKvs = append(conf.SecretKvs[:i], conf.SecretKvs[i+1:]...)
					break
				}
			}
		} else if value != ConstSecretKvPluginVault {
			return fmt.Errorf("credential store %s: plugin %q, the list holds %s entries only", entry, value, ConstSecretKvPluginVault)
		} else {
			e.Plugin = value
		}
	case "enabled":
		if unset {
			e.Enabled = false
		} else {
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("enabled: %q is not a bool", value)
			}
			e.Enabled = b
		}
	default:
		m, known := SecretKvPlugin(e.Plugin)
		st, declared := m.Settings.Setting(setting)
		switch {
		case unset:
			delete(e.Settings, setting)
		case known && !declared:
			return fmt.Errorf("plugin %s has no setting %s", e.Plugin, setting)
		case known && st.Type == ConstArchiveSettingTypeSecret:
			e.Settings[setting] = value
		case known:
			v, err := conf.typedSettingValue(st, value)
			if err != nil {
				return err
			}
			e.Settings[setting] = v
		default:
			e.Settings[setting] = value
		}
	}
	conf.projectSecretKv()
	return nil
}
