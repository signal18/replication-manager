// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// The settings of the external snapshot plugins (#2456, VOLUME_SNAPSHOTS.md "External plugins:
// settings, devices, unmount"): a plugin's manifest declares them (the archive plugins'
// PluginSetting), the cluster stores each plugin's values in one [[snapshot-plugin]] entry of
// its TOML, a secret encrypted with the repman key (F9), and the plugin gets them in its
// environment, a secret decrypted only there (REPMAN_SNAPSHOT_SECRET_<NAME>).

// SnapshotPluginEntry is the settings of one external snapshot plugin in a cluster.
type SnapshotPluginEntry struct {
	Plugin   string                 `mapstructure:"plugin" toml:"plugin" json:"plugin"`
	Settings map[string]interface{} `mapstructure:",remain" toml:"-" json:"settings"`
}

// The environment of an external snapshot plugin beyond the 4.0.1 contract.
const (
	EnvSnapshotSettingPrefix = "REPMAN_SNAPSHOT_SETTING_"
	EnvSnapshotSecretPrefix  = "REPMAN_SNAPSHOT_SECRET_"
)

// snapshotPluginEntryMu guards the entries: the save (config-sync goroutine), the API setter
// and the plugin runs read and replace them.
var snapshotPluginEntryMu sync.Mutex

// isSecret: the plugin declares name a secret, or (its manifest not loaded) the value is one
// held encrypted. Never sent by the API either way.
func (e SnapshotPluginEntry) isSecret(name string) bool {
	if m, ok := SnapshotPlugin(e.Plugin); ok && m.Settings.HasSecret(name) {
		return true
	}
	s, ok := e.Settings[name].(string)
	return ok && strings.HasPrefix(s, "hash_")
}

// MarshalJSON never sends a secret, not even encrypted: secretsSet names the ones held.
func (e SnapshotPluginEntry) MarshalJSON() ([]byte, error) {
	settings, secrets := splitSecretSettings(e.Settings, e.isSecret)
	return json.Marshal(struct {
		Plugin     string                 `json:"plugin"`
		Settings   map[string]interface{} `json:"settings"`
		SecretsSet []string               `json:"secretsSet"`
	}{e.Plugin, settings, secrets})
}

// SnapshotPluginEntryOf is a copy of the entry of plugin; an empty one when it has none.
func (conf *Config) SnapshotPluginEntryOf(plugin string) SnapshotPluginEntry {
	snapshotPluginEntryMu.Lock()
	defer snapshotPluginEntryMu.Unlock()
	for _, e := range conf.SnapshotPluginEntries {
		if e.Plugin == plugin {
			return SnapshotPluginEntry{Plugin: plugin, Settings: cloneSettings(e.Settings)}
		}
	}
	return SnapshotPluginEntry{Plugin: plugin, Settings: map[string]interface{}{}}
}

// SnapshotPluginEntryList is a copy of the entries.
func (conf *Config) SnapshotPluginEntryList() []SnapshotPluginEntry {
	snapshotPluginEntryMu.Lock()
	defer snapshotPluginEntryMu.Unlock()
	out := make([]SnapshotPluginEntry, 0, len(conf.SnapshotPluginEntries))
	for _, e := range conf.SnapshotPluginEntries {
		out = append(out, SnapshotPluginEntry{Plugin: e.Plugin, Settings: cloneSettings(e.Settings)})
	}
	return out
}

// ErrSnapshotSecretNoKey: no repman key to encrypt a secret with; it is never stored in clear.
var ErrSnapshotSecretNoKey = errors.New("no replication-manager encryption key: a snapshot plugin secret is never stored in clear (F9)")

// PutSnapshotPluginSettings applies settings (by name, typed by the manifest; a secret
// encrypted, an empty one keeping the one held) and unset to the entry of an external plugin.
func (conf *Config) PutSnapshotPluginSettings(plugin string, settings map[string]interface{}, unset []string) error {
	m, ok := SnapshotPlugin(plugin)
	if !ok || !m.External {
		return fmt.Errorf("%q is not an external snapshot plugin: only those have settings", plugin)
	}
	// a null value: a secret kept (as an empty one), any other setting refused (unset removes)
	clean := make(map[string]interface{}, len(settings))
	for name, v := range settings {
		if v == nil {
			if m.Settings.HasSecret(name) {
				continue
			}
			return fmt.Errorf("setting %s: null, use unset to remove it", name)
		}
		// the value goes into the plugin's environment: one line, no NUL
		if strings.ContainsAny(fmt.Sprintf("%v", v), "\x00\n\r") {
			return fmt.Errorf("setting %s: a NUL or a line break is not allowed", name)
		}
		clean[name] = v
	}
	settings = clean
	for name, v := range settings {
		if m.Settings.HasSecret(name) && fmt.Sprintf("%v", v) != "" {
			if conf.SecretKey == nil {
				conf.LoadEncrytionKey()
			}
			if conf.SecretKey == nil {
				return ErrSnapshotSecretNoKey
			}
		}
	}
	snapshotPluginEntryMu.Lock()
	defer snapshotPluginEntryMu.Unlock()
	next := make([]SnapshotPluginEntry, 0, len(conf.SnapshotPluginEntries)+1)
	var cur map[string]interface{}
	for _, e := range conf.SnapshotPluginEntries {
		if e.Plugin == plugin {
			cur = cloneSettings(e.Settings)
			continue
		}
		next = append(next, e)
	}
	if cur == nil {
		cur = map[string]interface{}{}
	}
	if err := conf.applyEntrySpec(cur, m.Settings, plugin, settings, unset); err != nil {
		return err
	}
	if len(cur) > 0 {
		next = append(next, SnapshotPluginEntry{Plugin: plugin, Settings: cur})
	}
	sort.Slice(next, func(i, j int) bool { return next[i].Plugin < next[j].Plugin })
	// a new slice: a Config copied by value never shares the entry being changed
	conf.SnapshotPluginEntries = next
	return nil
}

// SnapshotPluginEnv is the environment an external plugin gets for its settings: each declared
// setting held as REPMAN_SNAPSHOT_SETTING_<NAME>, each secret decrypted as
// REPMAN_SNAPSHOT_SECRET_<NAME> (pluginSettingsEnv).
func (conf *Config) SnapshotPluginEnv(m SnapshotPluginManifest) []string {
	return conf.pluginSettingsEnv(m.Settings, conf.SnapshotPluginEntryOf(m.Name).Settings, EnvSnapshotSettingPrefix, EnvSnapshotSecretPrefix)
}

// pluginSettingsEnv is a plugin's environment for its settings, shared by the snapshot and the
// replication plugins: each declared setting held (else its manifest's default) as
// <settingPrefix><NAME>, each secret held decrypted as <secretPrefix><NAME>. Only for the
// plugin's process, never for a log.
func (conf *Config) pluginSettingsEnv(ss PluginSettings, values map[string]interface{}, settingPrefix, secretPrefix string) []string {
	var env []string
	for _, st := range ss {
		v, ok := values[st.Name]
		if !ok || v == nil {
			if st.Default == "" || st.Type == ConstArchiveSettingTypeSecret {
				continue
			}
			v = st.Default
		}
		raw := fmt.Sprintf("%v", v)
		prefix := settingPrefix
		if st.Type == ConstArchiveSettingTypeSecret {
			prefix, raw = secretPrefix, conf.GetDecryptedPassword(st.Name, raw)
		}
		if strings.ContainsAny(raw, "\x00\n\r") {
			// the API refuses such a value; one written by hand in the TOML never reaches the
			// plugin's environment, where it could add a variable
			continue
		}
		env = append(env, snapshotEnvName(prefix, st.Name)+"="+raw)
	}
	sort.Strings(env)
	return env
}

// snapshotEnvName: api-version -> <prefix>API_VERSION.
func snapshotEnvName(prefix, name string) string {
	return prefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// SnapshotPluginEntriesToml renders the entries as [[<section>.snapshot-plugin]] tables for
// the saved config: the secrets as held, encrypted.
func (conf *Config) SnapshotPluginEntriesToml(section string) (string, error) {
	snapshotPluginEntryMu.Lock()
	defer snapshotPluginEntryMu.Unlock()
	var b strings.Builder
	for _, e := range conf.SnapshotPluginEntries {
		if len(e.Settings) == 0 {
			continue
		}
		if err := writeEntryToml(&b, section, "snapshot-plugin", map[string]interface{}{"plugin": e.Plugin}, e.Settings); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// validSnapshotPluginSettings checks the settings a manifest declares: names the environment
// can carry, the types an entry stores.
func validSnapshotPluginSettings(m SnapshotPluginManifest) error {
	seen := map[string]bool{}
	for _, st := range m.Settings {
		if !snapshotPluginNamePattern.MatchString(st.Name) {
			return fmt.Errorf("snapshot plugin %s: setting name %q: lower case letters, digits and dashes", m.Name, st.Name)
		}
		if seen[st.Name] {
			return fmt.Errorf("snapshot plugin %s: setting %s declared twice", m.Name, st.Name)
		}
		seen[st.Name] = true
		switch st.Type {
		case ConstArchiveSettingTypeString, ConstArchiveSettingTypeBool, ConstArchiveSettingTypeInt, ConstArchiveSettingTypeSecret:
		case ConstArchiveSettingTypeEnum:
			if len(st.Values) == 0 {
				return fmt.Errorf("snapshot plugin %s: enum setting %s without values", m.Name, st.Name)
			}
		default:
			return fmt.Errorf("snapshot plugin %s: setting %s: unknown type %q", m.Name, st.Name, st.Type)
		}
	}
	return nil
}
