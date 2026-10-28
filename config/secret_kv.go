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

// A cluster's credential stores (#2227, doc/implementation/config/CREDENTIAL_STORES.md "Store
// entries"): the built-in secret_kv-config store (the config, whose value is only the seed,
// and the versioned local file, monitoring-secret-versioning), and n secret_kv-vault entries,
// the same model as the archives (archive.go): a TOML array of tables, [[<cluster>.secret-kv]]
// in a config file, [[saved-<cluster>.secret-kv]] in the saved <cluster>.toml, each entry a
// name, its plugin, its settings (secrets encrypted, F9) and whether it is enabled. There is no
// primary: every enabled store is versioned and gets every version of every secret, at a path
// repman computes (<mount>/<space>/databases/<server>/<key>), never typed.
//
// The Vault settings of a cluster (vault-server-addr and the vault-* keys, database_engine
// mode included) are migrated at load into the entry vault-default, enabled, and the Vault*
// fields go back to their defaults: a cluster no longer declares Vault by a bare path in a
// credential (a path left in the config is read once as the seed, cluster_secret_kv.go).

// The secret_kv plugins: two. secret_kv-config is built in and always there: the config
// files and the versioned local file (secret_store.json) as one store, holding every secret
// and its versions; it is the base store, never an entry of the list. secret_kv-vault has n
// entries, the list holds only those.
const (
	ConstSecretKvPluginConfig = "secret_kv-config" // built in: the config and the versioned local file
	ConstSecretKvPluginVault  = "secret_kv-vault"  // a Vault KV, one per entry
)

// ConstSecretKvVaultDefault is the entry the vault-* keys are migrated into.
const ConstSecretKvVaultDefault = "vault-default"

// SecretKvPluginManifest is the config a secret_kv plugin comes with: the typed settings of
// its entries (secrets included). The entry forms are drawn from it.
type SecretKvPluginManifest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tier        string         `json:"tier,omitempty"` // ConstArchiveTierEnterprise, "" for every plan
	Settings    PluginSettings `json:"settings"`
}

// The settings of a secret_kv-vault entry.
const (
	ConstSecretKvVaultAddress = "address"
	ConstSecretKvVaultMount   = "mount"
	// ConstSecretKvVaultPathPrefix: the folder under the mount the cluster's folder goes in
	// (empty: the cluster at the mount's root)
	ConstSecretKvVaultPathPrefix = "path-prefix"
	ConstSecretKvVaultMode       = "mode" // config_store_v2 (KV v2), database_engine (Vault makes the passwords)
	ConstSecretKvVaultAuth       = "auth"
	ConstSecretKvVaultRoleID     = "role-id"
	ConstSecretKvVaultSecretID   = "secret-id"
)

// Vault auth methods an entry signs in with.
const (
	ConstVaultAuthAppRole = "approle"
	ConstVaultAuthJWT     = "jwt" // the instance's Cloud18 identity token (#2159)
)

var secretKvManifests = map[string]SecretKvPluginManifest{
	ConstSecretKvPluginConfig: {
		Name:        ConstSecretKvPluginConfig,
		Description: "Built in, always there: the config (the seed) and the versioned local file (monitoring-secret-versioning)",
		Settings:    PluginSettings{},
	},
	ConstSecretKvPluginVault: {
		Name:        ConstSecretKvPluginVault,
		Description: "A Vault: the user's own or the Cloud18 vault, KV v2 (every version of every secret) or its database engine (Vault makes the passwords)",
		Settings: PluginSettings{
			{Name: ConstSecretKvVaultAddress, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultMount, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultPathPrefix, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultMode, Type: ConstArchiveSettingTypeEnum, Values: []string{VaultConfigStoreV2, VaultDbEngine}},
			{Name: ConstSecretKvVaultAuth, Type: ConstArchiveSettingTypeEnum, Values: []string{ConstVaultAuthAppRole, ConstVaultAuthJWT}},
			{Name: ConstSecretKvVaultRoleID, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvVaultSecretID, Type: ConstArchiveSettingTypeSecret},
		},
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

// secretKvMu guards the list: the save, the peer replay and the store writer read and write
// it.
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

// MigrateSecretKvs resolves the credential store list at cluster init: a cluster using Vault
// (vault-server-addr set, KV v2 or database engine) gets vault-default from its vault-* keys, enabled, unless the
// list has it; the vault-* keys a config file pins replace its settings (the file wins). Then
// the Vault* fields go back to their defaults: Vault is an entry, no longer the flat config.
func (conf *Config) MigrateSecretKvs() {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	// a Config is copied by value: the list must not share entries with another copy
	conf.SecretKvs = cloneSecretKvs(conf.SecretKvs)
	conf.cleanSecretKvSettings()
	conf.captureVaultDefault()
}

// SyncSecretKvFromConfig moves into vault-default the Vault* fields a setter or inject.toml
// changed since the load (the back-office injects vault-role-id and vault-secret-id), as at
// load.
func (conf *Config) SyncSecretKvFromConfig() {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	conf.captureVaultDefault()
}

// captureVaultDefault stores the non-default vault-* fields into vault-default (created
// enabled when missing, with an address), then resets the fields. Call it under secretKvMu.
func (conf *Config) captureVaultDefault() {
	e := conf.secretKvByName(ConstSecretKvVaultDefault)
	changed := false
	for _, key := range secretKvVaultLegacyKeys {
		if f, ok := configField(conf, key); ok && !conf.isDefaultValue(key, f) {
			changed = true
		}
	}
	if !changed {
		return
	}
	if e == nil {
		if conf.VaultServerAddr == "" {
			return // a login without a Vault: kept flat until the address comes
		}
		conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: ConstSecretKvVaultDefault, Plugin: ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{}})
		e = &conf.SecretKvs[len(conf.SecretKvs)-1]
	}
	for name, key := range secretKvVaultLegacyKeys {
		f, ok := configField(conf, key)
		if !ok || conf.isDefaultValue(key, f) {
			continue
		}
		v := fmt.Sprintf("%v", fieldValue(f))
		if e.isSecret(name) && conf.GetDecryptedPassword(name, v) == conf.GetDecryptedPassword(name, conf.secretKvSetting(e, name)) {
			continue // the same secret: keep its ciphertext, the save stays stable
		}
		conf.storeSecretKvValue(e, name, v)
	}
	conf.resetVaultFields()
}

// resetVaultFields puts the migrated Vault* fields back to their defaults: Vault is not used
// through the flat config any more (IsVaultUsed false).
func (conf *Config) resetVaultFields() {
	for _, key := range secretKvVaultLegacyKeys {
		f, ok := configField(conf, key)
		if !ok {
			continue
		}
		if def, ok := conf.DefaultFlagMap[key]; ok {
			setField(f, def)
		} else {
			f.Set(reflect.Zero(f.Type()))
		}
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

// SecretKvHoldsLegacyKey: vault-default carries the vault-* key, so the save leaves the flat
// key out. A secret it cannot hold encrypted (no encryption key) stays flat: the entry never
// writes it (F9).
func (conf *Config) SecretKvHoldsLegacyKey(key string) bool {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	e := conf.secretKvByName(ConstSecretKvVaultDefault)
	if e == nil {
		return false
	}
	for name, k := range secretKvVaultLegacyKeys {
		if k != key {
			continue
		}
		if e.isSecret(name) {
			v, ok := e.Settings[name]
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

// SecretKvList is a copy of the list.
func (conf *Config) SecretKvList() []SecretKvConfig {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	return cloneSecretKvs(conf.SecretKvs)
}

// SecretKvActive are the enabled entries, in the list's order: each gets every version of
// every secret.
func (conf *Config) SecretKvActive() []SecretKvConfig {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var out []SecretKvConfig
	for _, e := range conf.SecretKvs {
		if e.Enabled && e.Plugin == ConstSecretKvPluginVault {
			out = append(out, cloneSecretKvs([]SecretKvConfig{e})[0])
		}
	}
	return out
}

// SecretKvActiveEntry is the entry name when it is enabled.
func (conf *Config) SecretKvActiveEntry(name string) (SecretKvConfig, bool) {
	for _, e := range conf.SecretKvActive() {
		if e.Name == name {
			return e, true
		}
	}
	return SecretKvConfig{}, false
}

// SecretKvFolder is the cluster's folder in a vault entry, below its mount:
// <path-prefix>/<cluster>, or <cluster> with no prefix. The client lays its own Vault out with
// the prefix; on Cloud18 the marketplace entry fills it with <domain>/<sub-domain>.
func (conf *Config) SecretKvFolder(e SecretKvConfig, cluster string) string {
	if prefix := strings.Trim(conf.secretKvSetting(&e, ConstSecretKvVaultPathPrefix), "/"); prefix != "" {
		return prefix + "/" + cluster
	}
	return cluster
}

// SetSecretKvSetting applies one entry setting by name (a peer's event
// "secret-kv.<name>.<setting>"), creating the entry when needed. unset removes the setting
// or, for "plugin", the entry. A secret is the peer's ciphertext: peers share the encryption
// key.
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
	return nil
}

// SecretKvSpec is a vault entry to add or change, from the API or the GUI: Settings by the
// names of the plugin's manifest (a secret written only, never returned), Unset the names to
// clear, Enabled when given.
type SecretKvSpec struct {
	Name     string                 `json:"name"`
	Plugin   string                 `json:"plugin"`
	Enabled  *bool                  `json:"enabled,omitempty"`
	Settings map[string]interface{} `json:"settings"`
	Unset    []string               `json:"unset,omitempty"`
}

// PutSecretKv adds (create) or changes the entry spec gives, checked against its plugin's
// manifest; the list holds secret_kv-vault entries only. It returns the entry's name.
func (conf *Config) PutSecretKv(spec SecretKvSpec, create bool) (string, error) {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	cur := conf.secretKvByName(spec.Name)
	switch {
	case create && cur != nil:
		return "", fmt.Errorf("credential store %s already exists", spec.Name)
	case !create && cur == nil:
		return "", fmt.Errorf("unknown credential store %s", spec.Name)
	}
	var e SecretKvConfig
	if cur != nil {
		e = cloneSecretKvs([]SecretKvConfig{*cur})[0]
	} else {
		if err := validateEntryName("credential store", spec.Name); err != nil {
			return "", err
		}
		e = SecretKvConfig{Name: spec.Name, Plugin: ConstSecretKvPluginVault, Settings: map[string]interface{}{}}
	}
	if spec.Plugin != "" && spec.Plugin != ConstSecretKvPluginVault {
		return "", fmt.Errorf("plugin %q: the list holds %s entries only", spec.Plugin, ConstSecretKvPluginVault)
	}
	m, _ := SecretKvPlugin(e.Plugin)
	if err := conf.applyEntrySpec(e.Settings, m.Settings, e.Plugin, spec.Settings, spec.Unset); err != nil {
		return "", err
	}
	if spec.Enabled != nil {
		e.Enabled = *spec.Enabled
	}
	if strings.TrimSpace(conf.secretKvSetting(&e, ConstSecretKvVaultAddress)) == "" {
		return "", fmt.Errorf("credential store %s: an address is required", e.Name)
	}
	if cur != nil {
		*cur = e
	} else {
		conf.SecretKvs = append(conf.SecretKvs, e)
	}
	return e.Name, nil
}

// SetSecretKvEnabled turns an entry on or off. Off stops its versions; what it holds stays.
func (conf *Config) SetSecretKvEnabled(name string, enabled bool) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	e := conf.secretKvByName(name)
	if e == nil {
		return fmt.Errorf("unknown credential store %s", name)
	}
	e.Enabled = enabled
	return nil
}

// DeleteSecretKv takes the entry out of the list; repman never deletes what it holds.
func (conf *Config) DeleteSecretKv(name string) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	for i := range conf.SecretKvs {
		if conf.SecretKvs[i].Name == name {
			conf.SecretKvs = append(conf.SecretKvs[:i], conf.SecretKvs[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("unknown credential store %s", name)
}
