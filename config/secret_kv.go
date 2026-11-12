// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"errors"
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

// The secret_kv plugins. secret_kv-config is built in and always there: the config files and
// the versioned local file (secret_store.json) as one store, holding every secret and its
// versions; it is the base store, never an entry of the list. secret_kv-vault has n entries at
// both levels. secret_kv-opensvc and secret_kv-kubernetes are server entries only: the server
// secrets in the orchestrator's secret object, through a cluster's connection (at the cluster
// level the orchestrator's env secret is the reserved "orchestrator" entry, always on).
const (
	ConstSecretKvPluginConfig     = "secret_kv-config"     // built in: the config and the versioned local file
	ConstSecretKvPluginVault      = "secret_kv-vault"      // a Vault KV, one per entry
	ConstSecretKvPluginOpenSVC    = "secret_kv-opensvc"    // the OpenSVC secret system/sec/repman (server level)
	ConstSecretKvPluginKubernetes = "secret_kv-kubernetes" // a Kubernetes Secret repman-system (server level)
)

// The settings of a server secret_kv-opensvc or secret_kv-kubernetes entry.
const (
	// ConstSecretKvOrchestratorCluster: the cluster whose orchestrator connection (its agent,
	// its certificate, its Kubernetes client) carries the server secrets
	ConstSecretKvOrchestratorCluster = "cluster"
	// ConstSecretKvKubernetesNamespace: the namespace of the repman-system Secret
	ConstSecretKvKubernetesNamespace = "namespace"
)

// The objects the orchestrator server entries write, every instance its own keys in them:
// <instance name, "/" as "_">_<key>.
const (
	ConstSecretKvOpenSVCServerNamespace = "system"
	ConstSecretKvOpenSVCServerObject    = "repman"
	ConstSecretKvKubernetesServerSecret = "repman-system"
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
	// ServerOnly: an entry of the server's list only, never of a cluster's
	ServerOnly bool `json:"serverOnly,omitempty"`
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
	ConstSecretKvPluginOpenSVC: {
		Name:        ConstSecretKvPluginOpenSVC,
		Description: "The OpenSVC secret system/sec/repman, through the agent of a cluster on OpenSVC: keys <instance name>_<key>, readable by the OpenSVC cluster's administrators",
		ServerOnly:  true,
		Settings: PluginSettings{
			{Name: ConstSecretKvOrchestratorCluster, Type: ConstArchiveSettingTypeString},
		},
	},
	ConstSecretKvPluginKubernetes: {
		Name:        ConstSecretKvPluginKubernetes,
		Description: "The Kubernetes Secret repman-system in a namespace, through the client of a cluster on Kubernetes: keys <instance name>_<key>",
		ServerOnly:  true,
		Settings: PluginSettings{
			{Name: ConstSecretKvOrchestratorCluster, Type: ConstArchiveSettingTypeString},
			{Name: ConstSecretKvKubernetesNamespace, Type: ConstArchiveSettingTypeString},
		},
	},
}

// SecretKvPluginAllowedAt: an entry of plugin may be in level's list (secret_kv-config never is).
func SecretKvPluginAllowedAt(level SecretKvLevel, plugin string) bool {
	m, ok := secretKvManifests[plugin]
	if !ok || plugin == ConstSecretKvPluginConfig {
		return false
	}
	return level == SecretKvLevelServer || !m.ServerOnly
}

// IsOrchestratorSecretKvPlugin: plugin writes through a cluster's orchestrator connection.
func IsOrchestratorSecretKvPlugin(plugin string) bool {
	return plugin == ConstSecretKvPluginOpenSVC || plugin == ConstSecretKvPluginKubernetes
}

// SecretKvPlugin is the manifest of a secret_kv plugin.
func SecretKvPlugin(name string) (SecretKvPluginManifest, bool) {
	m, ok := secretKvManifests[name]
	return m, ok
}

// SecretKvPlugins lists the cluster level's secret_kv plugin manifests, by name.
func SecretKvPlugins() []SecretKvPluginManifest { return SecretKvPluginsAt(SecretKvLevelCluster) }

// SecretKvPluginsAt lists the manifests of the plugins level's list may hold, and the built-in
// one, by name.
func SecretKvPluginsAt(level SecretKvLevel) []SecretKvPluginManifest {
	out := make([]SecretKvPluginManifest, 0, len(secretKvManifests))
	for _, m := range secretKvManifests {
		if m.Name == ConstSecretKvPluginConfig || SecretKvPluginAllowedAt(level, m.Name) {
			out = append(out, m)
		}
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
	// Offer names the marketplace vault offer the entry comes from ("" for the user's own):
	// the back-office manages its settings, only its switch is the cluster's
	Offer string `mapstructure:"offer" toml:"offer" json:"-"`
	// OfferInfo is the offer as the catalogue describes it, for the API (never saved)
	OfferInfo *SecretKvOffer `mapstructure:"-" toml:"-" json:"offer"`
}

// SecretKvOffer is the marketplace vault offer an entry comes from, as the API gives it.
type SecretKvOffer struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Partner string `json:"partner,omitempty"`
}

// isSecret: the entry's plugin declares name as a secret.
func (e SecretKvConfig) isSecret(name string) bool {
	m, ok := SecretKvPlugin(e.Plugin)
	return ok && m.Settings.HasSecret(name)
}

// MarshalJSON never sends a secret, not even encrypted: secretsSet names the ones held.
func (e SecretKvConfig) MarshalJSON() ([]byte, error) {
	settings, secrets := splitSecretSettings(e.Settings, e.isSecret)
	offer := e.OfferInfo
	if offer == nil && e.Offer != "" {
		offer = &SecretKvOffer{Name: e.Offer}
	}
	return json.Marshal(struct {
		Name       string                 `json:"name"`
		Plugin     string                 `json:"plugin"`
		Enabled    bool                   `json:"enabled"`
		Settings   map[string]interface{} `json:"settings"`
		SecretsSet []string               `json:"secretsSet"`
		Offer      *SecretKvOffer         `json:"offer"`
	}{e.Name, e.Plugin, e.Enabled, settings, secrets, offer})
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

// secretKvMu guards the lists: the save, the peer replay and the store writer read and write
// them.
var secretKvMu sync.Mutex

// SecretKvLevel is the level of a list of credential stores (CREDENTIAL_STORES.md "Two
// levels"): a cluster's entries ([[<cluster>.secret-kv]]) hold its secrets, the server's
// ([[default.server-secret-kv]]) the server secrets. The same code serves both.
type SecretKvLevel string

const (
	SecretKvLevelCluster SecretKvLevel = "cluster"
	SecretKvLevelServer  SecretKvLevel = "server"
)

// secretKvsAt is the list of level.
func (conf *Config) secretKvsAt(level SecretKvLevel) *[]SecretKvConfig {
	if level == SecretKvLevelServer {
		return &conf.ServerSecretKvs
	}
	return &conf.SecretKvs
}

// secretKvTable is the TOML table of level's entries.
func secretKvTable(level SecretKvLevel) string {
	if level == SecretKvLevelServer {
		return "server-secret-kv"
	}
	return "secret-kv"
}

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
	return conf.secretKvByNameAt(SecretKvLevelCluster, name)
}

func (conf *Config) secretKvByNameAt(level SecretKvLevel, name string) *SecretKvConfig {
	list := conf.secretKvsAt(level)
	for i := range *list {
		if (*list)[i].Name == name {
			return &(*list)[i]
		}
	}
	return nil
}

// cleanSecretKvSettings drops from the entries read what their plugin does not declare, and the
// entries of a plugin the level does not take (secret_kv-config is built in, never an entry;
// the orchestrator plugins are the server's).
func (conf *Config) cleanSecretKvSettings() { conf.cleanSecretKvSettingsAt(SecretKvLevelCluster) }

func (conf *Config) cleanSecretKvSettingsAt(level SecretKvLevel) {
	list := conf.secretKvsAt(level)
	kept := (*list)[:0]
	for _, e := range *list {
		if SecretKvPluginAllowedAt(level, e.Plugin) {
			kept = append(kept, e)
		}
	}
	*list = kept
	for i := range *list {
		e := &(*list)[i]
		if e.Settings == nil {
			e.Settings = map[string]interface{}{}
		}
		if level == SecretKvLevelCluster && ReservedSecretKvPrefix(conf.secretKvSetting(e, ConstSecretKvVaultPathPrefix)) {
			// read from a config file: kept, switched off, never written under a server folder
			e.Enabled = false
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

// LoadServerSecretKvs resolves the server list once read: its own copy, the vault entries
// only, what their plugin does not declare dropped. No migration: the flat vault-* keys are a
// cluster's (MigrateSecretKvs).
func (conf *Config) LoadServerSecretKvs() {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	conf.ServerSecretKvs = cloneSecretKvs(conf.ServerSecretKvs)
	conf.cleanSecretKvSettingsAt(SecretKvLevelServer)
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

// SecretKvsToml renders the cluster list as [[<section>.secret-kv]] tables for the saved config.
func (conf *Config) SecretKvsToml(section string) (string, error) {
	return conf.SecretKvsTomlAt(SecretKvLevelCluster, section)
}

// SecretKvsTomlAt renders level's list as [[<section>.secret-kv]] (cluster) or
// [[<section>.server-secret-kv]] (server) tables. A secret not encrypted (no encryption key) is
// left out: it never reaches the config repository in clear (F9).
func (conf *Config) SecretKvsTomlAt(level SecretKvLevel, section string) (string, error) {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var b strings.Builder
	for _, e := range *conf.secretKvsAt(level) {
		head := map[string]interface{}{"name": e.Name, "plugin": e.Plugin, "enabled": e.Enabled}
		if e.Offer != "" {
			head["offer"] = e.Offer
		}
		settings := make(map[string]interface{}, len(e.Settings))
		for k, v := range e.Settings {
			if e.isSecret(k) && !strings.HasPrefix(fmt.Sprintf("%v", v), "hash_") {
				continue
			}
			settings[k] = v
		}
		if err := writeEntryToml(&b, section, secretKvTable(level), head, settings); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// SecretKvList is a copy of the cluster list.
func (conf *Config) SecretKvList() []SecretKvConfig { return conf.SecretKvListAt(SecretKvLevelCluster) }

// SecretKvListAt is a copy of level's list.
func (conf *Config) SecretKvListAt(level SecretKvLevel) []SecretKvConfig {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	return cloneSecretKvs(*conf.secretKvsAt(level))
}

// SecretKvActive are the cluster list's enabled entries, in the list's order: each gets every
// version of every secret.
func (conf *Config) SecretKvActive() []SecretKvConfig {
	return conf.SecretKvActiveAt(SecretKvLevelCluster)
}

// SecretKvActiveAt are level's enabled entries, in the list's order.
func (conf *Config) SecretKvActiveAt(level SecretKvLevel) []SecretKvConfig {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var out []SecretKvConfig
	for _, e := range *conf.secretKvsAt(level) {
		if e.Enabled && SecretKvPluginAllowedAt(level, e.Plugin) {
			out = append(out, cloneSecretKvs([]SecretKvConfig{e})[0])
		}
	}
	return out
}

// SecretKvActiveEntry is the cluster list's entry name when it is enabled.
func (conf *Config) SecretKvActiveEntry(name string) (SecretKvConfig, bool) {
	return conf.SecretKvActiveEntryAt(SecretKvLevelCluster, name)
}

// SecretKvActiveEntryAt is level's entry name when it is enabled.
func (conf *Config) SecretKvActiveEntryAt(level SecretKvLevel, name string) (SecretKvConfig, bool) {
	for _, e := range conf.SecretKvActiveAt(level) {
		if e.Name == name {
			return e, true
		}
	}
	return SecretKvConfig{}, false
}

// ServerSecretKvFolder is the instance's folder in a server entry, below its mount:
// <path-prefix>/system/repman/<instance name>, or system/repman/<instance name> with no prefix.
// The name is InstanceStoreName: never a URL.
func (conf *Config) ServerSecretKvFolder(e SecretKvConfig, instance string) string {
	if IsOrchestratorSecretKvPlugin(e.Plugin) {
		// a flat secret object: the folder is the keys' prefix
		return ServerOrchestratorKeyPrefix(instance)
	}
	folder := "system/repman/" + instance
	if prefix := strings.Trim(conf.secretKvSetting(&e, ConstSecretKvVaultPathPrefix), "/"); prefix != "" {
		return prefix + "/" + folder
	}
	return folder
}

// ServerOrchestratorKeyPrefix is the instance's keys' prefix in a flat orchestrator secret:
// its name with "_" for "/". The name's parts hold letters, digits, '-', '_' and '.', which
// the OpenSVC and Kubernetes keys both take.
func ServerOrchestratorKeyPrefix(instance string) string {
	return strings.ReplaceAll(instance, "/", "_")
}

// ServerSecretKvLocation says where a server entry puts the instance's secrets, for the API
// and the GUI (no secret).
func (conf *Config) ServerSecretKvLocation(e SecretKvConfig, instance string) string {
	prefix := ServerOrchestratorKeyPrefix(instance)
	switch e.Plugin {
	case ConstSecretKvPluginOpenSVC:
		return ConstSecretKvOpenSVCServerNamespace + "/sec/" + ConstSecretKvOpenSVCServerObject + ", keys " + prefix + "_<key>"
	case ConstSecretKvPluginKubernetes:
		return conf.secretKvSetting(&e, ConstSecretKvKubernetesNamespace) + "/secret/" + ConstSecretKvKubernetesServerSecret + ", keys " + prefix + "_<key>"
	}
	return conf.ServerSecretKvFolder(e, instance)
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
	case "offer":
		if unset {
			e.Offer = ""
		} else {
			e.Offer = value
		}
	case "enabled":
		if unset {
			e.Enabled = false
		} else {
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("enabled: %q is not a bool", value)
			}
			if b && ReservedSecretKvPrefix(conf.secretKvSetting(e, ConstSecretKvVaultPathPrefix)) {
				return fmt.Errorf("credential store %s: %w", entry, ErrSecretKvReservedPrefix)
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
		case setting == ConstSecretKvVaultPathPrefix && ReservedSecretKvPrefix(value):
			return fmt.Errorf("credential store %s: %w", entry, ErrSecretKvReservedPrefix)
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
	return conf.PutSecretKvAt(SecretKvLevelCluster, spec, create)
}

// PutSecretKvAt is PutSecretKv on level's list.
func (conf *Config) PutSecretKvAt(level SecretKvLevel, spec SecretKvSpec, create bool) (string, error) {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	cur := conf.secretKvByNameAt(level, spec.Name)
	switch {
	case create && cur != nil:
		return "", fmt.Errorf("credential store %s already exists", spec.Name)
	case !create && cur == nil:
		return "", fmt.Errorf("unknown credential store %s", spec.Name)
	}
	var e SecretKvConfig
	if cur != nil {
		if cur.Offer != "" {
			return "", fmt.Errorf("credential store %s: %w", spec.Name, ErrSecretKvOfferManaged)
		}
		e = cloneSecretKvs([]SecretKvConfig{*cur})[0]
	} else {
		if err := validateEntryName("credential store", spec.Name); err != nil {
			return "", err
		}
		if strings.HasPrefix(spec.Name, ConstSecretKvMarketplacePrefix) {
			return "", fmt.Errorf("credential store %s: the names %s* are the marketplace's offers", spec.Name, ConstSecretKvMarketplacePrefix)
		}
		e = SecretKvConfig{Name: spec.Name, Plugin: ConstSecretKvPluginVault, Settings: map[string]interface{}{}}
	}
	if spec.Plugin != "" {
		if !SecretKvPluginAllowedAt(level, spec.Plugin) {
			return "", fmt.Errorf("plugin %q: the %s list does not take it", spec.Plugin, level)
		}
		if cur != nil && spec.Plugin != e.Plugin {
			return "", fmt.Errorf("credential store %s: its plugin is %s, remove it and add another", spec.Name, e.Plugin)
		}
		e.Plugin = spec.Plugin
	}
	m, _ := SecretKvPlugin(e.Plugin)
	if err := conf.applyEntrySpec(e.Settings, m.Settings, e.Plugin, spec.Settings, spec.Unset); err != nil {
		return "", err
	}
	if spec.Enabled != nil {
		e.Enabled = *spec.Enabled
	}
	if err := conf.requiredSecretKvSettings(&e); err != nil {
		return "", err
	}
	if level == SecretKvLevelCluster && ReservedSecretKvPrefix(conf.secretKvSetting(&e, ConstSecretKvVaultPathPrefix)) {
		return "", fmt.Errorf("credential store %s: %w", e.Name, ErrSecretKvReservedPrefix)
	}
	if cur != nil {
		*cur = e
	} else {
		list := conf.secretKvsAt(level)
		*list = append(*list, e)
	}
	return e.Name, nil
}

// requiredSecretKvSettings: what an entry of its plugin cannot do without.
func (conf *Config) requiredSecretKvSettings(e *SecretKvConfig) error {
	var required []string
	switch e.Plugin {
	case ConstSecretKvPluginVault:
		required = []string{ConstSecretKvVaultAddress}
	case ConstSecretKvPluginOpenSVC:
		required = []string{ConstSecretKvOrchestratorCluster}
	case ConstSecretKvPluginKubernetes:
		required = []string{ConstSecretKvOrchestratorCluster, ConstSecretKvKubernetesNamespace}
	}
	for _, name := range required {
		if strings.TrimSpace(conf.secretKvSetting(e, name)) == "" {
			return fmt.Errorf("credential store %s: %s is required", e.Name, name)
		}
	}
	if e.Plugin == ConstSecretKvPluginKubernetes && !validKubernetesNamespace(conf.secretKvSetting(e, ConstSecretKvKubernetesNamespace)) {
		return fmt.Errorf("credential store %s: a namespace is 1 to 63 lower-case letters, digits or '-', starting and ending with a letter or digit", e.Name)
	}
	return nil
}

// validKubernetesNamespace: a DNS-1123 label, what Kubernetes takes as a namespace.
func validKubernetesNamespace(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// SetSecretKvEnabled turns an entry on or off. Off stops its versions; what it holds stays.
func (conf *Config) SetSecretKvEnabled(name string, enabled bool) error {
	return conf.SetSecretKvEnabledAt(SecretKvLevelCluster, name, enabled)
}

// SetSecretKvEnabledAt is SetSecretKvEnabled on level's list.
func (conf *Config) SetSecretKvEnabledAt(level SecretKvLevel, name string, enabled bool) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	e := conf.secretKvByNameAt(level, name)
	if e == nil {
		return fmt.Errorf("unknown credential store %s", name)
	}
	if enabled && level == SecretKvLevelCluster && ReservedSecretKvPrefix(conf.secretKvSetting(e, ConstSecretKvVaultPathPrefix)) {
		return fmt.Errorf("credential store %s: %w", name, ErrSecretKvReservedPrefix)
	}
	e.Enabled = enabled
	return nil
}

// DeleteSecretKv takes the entry out of the list; repman never deletes what it holds.
func (conf *Config) DeleteSecretKv(name string) error {
	return conf.DeleteSecretKvAt(SecretKvLevelCluster, name)
}

// DeleteSecretKvAt is DeleteSecretKv on level's list.
func (conf *Config) DeleteSecretKvAt(level SecretKvLevel, name string) error {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	list := conf.secretKvsAt(level)
	for i := range *list {
		if (*list)[i].Name == name {
			if (*list)[i].Offer != "" {
				return fmt.Errorf("credential store %s: %w", name, ErrSecretKvOfferManaged)
			}
			*list = append((*list)[:i], (*list)[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("unknown credential store %s", name)
}

// ErrSecretKvReservedPrefix refuses a cluster entry whose folder would be under the server's:
// system/ is the server level's (system/repman/<instance name>), a cluster never writes there.
var ErrSecretKvReservedPrefix = errors.New("a path-prefix holding a segment system, . or .. is refused at the cluster level: system/repman/<instance name> is the server level's, a cluster's secrets never go there")

// ReservedSecretKvPrefix: a cluster entry's path-prefix that could reach a server folder,
// <prefix>/system/repman/<instance name> whatever the server entry's own prefix: any segment
// system (in any case), or a segment . or .. or empty (a path that moves elsewhere than it says).
func ReservedSecretKvPrefix(prefix string) bool {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return false
	}
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, "system") {
			return true
		}
	}
	return false
}

// ReservedPrefixSecretKvs are the cluster entries switched off because their path-prefix
// reaches a server folder (ReservedSecretKvPrefix): read from a config file, never written.
func (conf *Config) ReservedPrefixSecretKvs() []string {
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	var out []string
	for i := range conf.SecretKvs {
		if ReservedSecretKvPrefix(conf.secretKvSetting(&conf.SecretKvs[i], ConstSecretKvVaultPathPrefix)) {
			out = append(out, conf.SecretKvs[i].Name)
		}
	}
	return out
}

// ErrSecretKvOfferManaged refuses a change of an entry a marketplace offer gives: the
// back-office manages its settings, only its switch is the cluster's.
var ErrSecretKvOfferManaged = errors.New("it comes from a Cloud18 marketplace offer: the back-office manages its settings, only its switch can be changed")

// ConstSecretKvMarketplacePrefix starts the name of the entry a marketplace vault offer gives
// a cluster: cloud18-<offer name>.
const ConstSecretKvMarketplacePrefix = "cloud18-"

// EnsureMarketplaceVault gives the cluster the entry of a marketplace vault offer (#2227):
// added off (the sponsor turns it on), signed in with the instance's Cloud18 identity (jwt),
// at the offer's address, mount and path-prefix; an existing one follows the offer's address,
// mount, role and path-prefix, its switch untouched. True when it changed.
func (conf *Config) EnsureMarketplaceVault(offer MarketplaceVaultOffer, pathPrefix string) (bool, error) {
	name := ConstSecretKvMarketplacePrefix + offer.Name
	if err := validateEntryName("credential store", name); err != nil {
		return false, err
	}
	if strings.TrimSpace(offer.Address) == "" {
		return false, fmt.Errorf("vault offer %s: no address", offer.Name)
	}
	mount := offer.Mount
	if mount == "" {
		mount = "kv"
	}
	want := map[string]interface{}{
		ConstSecretKvVaultAddress:    offer.Address,
		ConstSecretKvVaultMount:      mount,
		ConstSecretKvVaultAuth:       ConstVaultAuthJWT,
		ConstSecretKvVaultPathPrefix: pathPrefix,
	}
	if offer.Role != "" {
		want[ConstSecretKvVaultRoleID] = offer.Role
	}
	secretKvMu.Lock()
	defer secretKvMu.Unlock()
	e := conf.secretKvByName(name)
	if e == nil {
		conf.SecretKvs = append(conf.SecretKvs, SecretKvConfig{Name: name, Plugin: ConstSecretKvPluginVault, Settings: want, Offer: offer.Name})
		return true, nil
	}
	if e.Plugin != ConstSecretKvPluginVault {
		return false, fmt.Errorf("credential store %s is not a vault entry", name)
	}
	changed := false
	if e.Offer != offer.Name {
		e.Offer, changed = offer.Name, true
	}
	for k, v := range want {
		if fmt.Sprintf("%v", e.Settings[k]) != fmt.Sprintf("%v", v) {
			e.Settings[k] = v
			changed = true
		}
	}
	return changed, nil
}

// InstanceStoreName is this instance's name in a shared credential store, the folder of its
// server secrets: <domain>/<subdomain>/<zone>. A registered instance is named by its
// registration, any other by its own name (instance-domain: the company, instance-sub-domain:
// the hostname, instance-sub-domain-zone: the location, an office or a country).
// ok false while the name is not complete (the company or the location not entered yet): the server secrets then
// stay in the local version store.
func (conf *Config) InstanceStoreName() (string, bool) {
	parts := []string{conf.InstanceDomain, conf.InstanceSubDomain, conf.InstanceSubDomainZone}
	if conf.Cloud18 && conf.Cloud18Domain != "" && conf.Cloud18SubDomain != "" && conf.Cloud18SubDomainZone != "" {
		parts = []string{conf.Cloud18Domain, conf.Cloud18SubDomain, conf.Cloud18SubDomainZone}
	}
	// a part that is not a valid folder (a registered one included) never reaches a store path
	for _, p := range parts {
		if ValidInstanceNamePart(p) != nil {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
}

// SeedInstanceName fills the hostname part of the instance's own name at the first start, only
// when empty, never changed afterwards (a container given a new hostname at each start keeps
// its first). The company and the location (an office, a country) are entered by a person.
// True when it set it: the caller saves.
func (conf *Config) SeedInstanceName(hostname string) bool {
	if conf.InstanceSubDomain == "" && hostname != "" {
		conf.InstanceSubDomain = hostname
		return true
	}
	return false
}

// ValidInstanceNamePart: a part of the instance's name is one folder of a store path and one
// part of an OpenSVC key: 1 to 63 letters, digits, '-', '_' or '.', no '..'.
func ValidInstanceNamePart(s string) error {
	if s == "" || len(s) > 63 || strings.Contains(s, "..") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return fmt.Errorf("an instance name part is 1 to 63 characters, no '..', not starting or ending with '.': %q", s)
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("an instance name part holds letters, digits, '-', '_' or '.' only: %q", s)
		}
	}
	return nil
}

// APIUserListWithPassword returns list (user:password,...) with user's password set to pass,
// every other entry as it is; false when user is not in it.
func APIUserListWithPassword(list, user, pass string) (string, bool) {
	entries := strings.Split(list, ",")
	found := false
	for i, e := range entries {
		u, _, _ := strings.Cut(strings.TrimSpace(e), ":")
		if u == user {
			entries[i] = user + ":" + pass
			found = true
		}
	}
	return strings.Join(entries, ","), found
}

// APIUserListHas: list (user:password,...) has an entry for user.
func APIUserListHas(list, user string) bool {
	for _, e := range strings.Split(list, ",") {
		if u, _, _ := strings.Cut(strings.TrimSpace(e), ":"); u == user {
			return true
		}
	}
	return false
}

// DecryptedAPIUserList is the instance's list (api-credentials) with every password in clear.
// Never logged (F10).
func (conf *Config) DecryptedAPIUserList() string {
	entries := strings.Split(conf.GetDecryptedValue("api-credentials"), ",")
	for i, e := range entries {
		u, p, ok := strings.Cut(strings.TrimSpace(e), ":")
		if ok {
			entries[i] = u + ":" + conf.GetDecryptedPassword("api-credentials", p)
		}
	}
	return strings.Join(entries, ",")
}
