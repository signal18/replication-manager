// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// The settings of the external replication plugins (REPLICATION_INTERFACE.md "Settings: per
// plugin"): a plugin's manifest declares them (PluginSetting, as the snapshot and archive
// plugins), the cluster stores each plugin's values in one [[repl-plugin]] entry of its TOML, a
// secret encrypted with the repman key (F9), and the plugin gets them in its environment, a
// secret decrypted only there (REPMAN_REPL_SECRET_<NAME>). Three levels, the most specific
// wins: the manifest's default, the cluster's value, the link's ([[repl-link]], not coded yet).
//
// The existing keys of the core (replication-plugin-*, force-slave-*, replication-*) are not
// settings of an entry: they stay what they are, read by repman as before, and are the cluster
// level of the settings they name (no key removed, renamed or rewritten, T16).

// ReplPluginEntry is the settings of one external replication plugin in a cluster.
type ReplPluginEntry struct {
	Plugin   string                 `mapstructure:"plugin" toml:"plugin" json:"plugin"`
	Settings map[string]interface{} `mapstructure:",remain" toml:"-" json:"settings"`
}

// replPluginEntryMu guards the entries: the save (config-sync goroutine) and the plugin runs
// read them.
var replPluginEntryMu sync.Mutex

// isEncrypted: a value held encrypted, the only form a secret is stored in (F9).
func isEncrypted(v interface{}) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "hash_")
}

// MarshalJSON never sends a secret, not even encrypted: secretsSet names the ones held.
func (e ReplPluginEntry) MarshalJSON() ([]byte, error) {
	settings, secrets := splitSecretSettings(e.Settings, func(k string) bool { return isEncrypted(e.Settings[k]) })
	return json.Marshal(struct {
		Plugin     string                 `json:"plugin"`
		Settings   map[string]interface{} `json:"settings"`
		SecretsSet []string               `json:"secretsSet"`
	}{e.Plugin, settings, secrets})
}

// ReplPluginEntryOf is a copy of the settings of the plugin known under any of names (its
// name and its aliases: an entry written under an alias keeps working); empty when none.
func (conf *Config) ReplPluginEntryOf(names ...string) ReplPluginEntry {
	replPluginEntryMu.Lock()
	defer replPluginEntryMu.Unlock()
	for _, n := range names {
		for _, e := range conf.ReplPluginEntries {
			if e.Plugin == n {
				return ReplPluginEntry{Plugin: e.Plugin, Settings: cloneSettings(e.Settings)}
			}
		}
	}
	first := ""
	if len(names) > 0 {
		first = names[0]
	}
	return ReplPluginEntry{Plugin: first, Settings: map[string]interface{}{}}
}

// ReplPluginEnv is the environment a replication plugin gets for the settings ss its manifest
// declares, by pluginSettingsEnv: the cluster's value, else the manifest's default. A secret
// held in clear (written by hand) never reaches it: a secret is stored encrypted (F9).
func (conf *Config) ReplPluginEnv(ss PluginSettings, names ...string) []string {
	values := conf.ReplPluginEntryOf(names...).Settings
	for k, v := range values {
		if ss.HasSecret(k) && !isEncrypted(v) {
			delete(values, k)
		}
	}
	return conf.pluginSettingsEnv(ss, values, wire.EnvReplSettingPrefix, wire.EnvReplSecretPrefix)
}

// ReplPluginEntriesToml renders the entries as [[<section>.repl-plugin]] tables for the saved
// config: the secrets as held, encrypted.
func (conf *Config) ReplPluginEntriesToml(section string) (string, error) {
	replPluginEntryMu.Lock()
	defer replPluginEntryMu.Unlock()
	entries := append([]ReplPluginEntry(nil), conf.ReplPluginEntries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Plugin < entries[j].Plugin })
	var b strings.Builder
	for _, e := range entries {
		if len(e.Settings) == 0 {
			continue
		}
		if err := writeEntryToml(&b, section, "repl-plugin", map[string]interface{}{"plugin": e.Plugin}, e.Settings); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}
