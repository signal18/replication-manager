// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml"
)

// A cluster's archives (#2077, doc/implementation/cluster/BACKUP_ARCHIVE_PLUGINS.md):
// a TOML array of tables, [[<cluster>.archive]] in a config file, [[saved-<cluster>.archive]]
// in the saved <cluster>.toml, each entry a name and its plugin. The name is a label the
// user chooses (archive-bso-s3); repman names the one the migration creates, and one
// created without a name, archive-<type>-<n> (archive-local-1: restic enabled today).
// The cluster is connected to one of them, backup-archive-name ("" for none). In this
// step the runtime still runs one restic archive from the BackupRestic* fields: the
// connected archive is projected onto them at load, at a switch and at a peer replay, and
// they are captured back into it at each save. The saved config holds the list only,
// never the legacy keys it replaced.

const (
	ConstArchivePluginResticLocal = "archive-restic-local"
	ConstArchivePluginResticSftp  = "archive-restic-sftp"
	ConstArchivePluginResticS3    = "archive-restic-s3"
)

// ArchivePluginManifest is the config an archive plugin comes with: the typed settings an
// entry of that plugin holds (secrets included) and what can be done with it. The add and
// edit forms and their checks are drawn from it. The internal restic plugins have theirs
// below; an external plugin ships it as <bin>.manifest.json.
type ArchivePluginManifest struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Settings     []ArchivePluginSetting `json:"settings"`
	Capabilities []string               `json:"capabilities"`
}

// ArchivePluginSetting is one setting of a plugin's config.
type ArchivePluginSetting struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`             // ConstArchiveSettingType*
	Values []string `json:"values,omitempty"` // the allowed values of an enum
	Group  string   `json:"group,omitempty"`  // ConstArchiveSettingGroup*, "" for the main settings
}

// Archive plugin setting types. A secret is held encrypted in the entry, read and written
// through the credential chain, never shown (#2088).
const (
	ConstArchiveSettingTypeString = "string"
	ConstArchiveSettingTypeInt    = "int"
	ConstArchiveSettingTypeBool   = "bool"
	ConstArchiveSettingTypeEnum   = "enum"
	ConstArchiveSettingTypeSecret = "secret"
)

// Archive plugin capabilities.
const (
	ConstArchiveCapEdit = "edit" // its settings and secrets can be changed
	// its content is deleted, then it leaves the list: content left behind would still be
	// invoiced and no longer visible. An archive the cluster left (detached) stays listed.
	ConstArchiveCapRemove = "remove"
	ConstArchiveCapProbe  = "probe" // opens an existing repository, or initialises a new one
	// the bytes its content takes, what an archive billed on its content is invoiced on:
	// shown in the list, and in remove's confirmation
	ConstArchiveCapSize = "size"
)

// the retention every restic plugin holds: how many snapshots the purge keeps, and whether
// the original leaves the backup folder once archived. Each archive keeps its own (#2091).
var archiveResticRetentionSettings = []string{
	"keep-last", "keep-hourly", "keep-daily", "keep-weekly", "keep-monthly", "keep-yearly",
	"keep-within", "keep-within-hourly", "keep-within-daily", "keep-within-weekly", "keep-within-monthly", "keep-within-yearly",
	"remove-original",
}

// settings every restic plugin holds: retention, tags, timeouts, purge options, mount
var archiveResticCommonSettings = append(append([]string{}, archiveResticRetentionSettings...),
	"repo-append-cluster", "additional-env", "tags", "host", "timeout", "dump-timeout", "auto-init",
	"purge-group-by", "purge-keep-tag", "purge-host", "purge-tag", "purge-path",
	"purge-prune", "purge-prune-compact", "purge-prune-max-unused", "purge-prune-max-repack-size",
	"purge-prune-repack-cacheable-only", "purge-prune-repack-small", "purge-prune-repack-uncompressed",
	"mount-target-dir", "mount-host", "mount-tag", "mount-path", "mount-path-template", "mount-time-template",
	"mount-allow-other", "mount-no-default-permissions", "mount-owner-root", "mount-no-lock",
	"mount-verbose", "mount-quiet", "mount-recovery-enabled",
)

// archiveFlatKeys are the archive settings whose flat key is not backup-restic-<setting>:
// the retention, moved from the cluster into each archive (#2091).
var archiveFlatKeys = map[string]string{
	"keep-last":           "backup-keep-last",
	"keep-hourly":         "backup-keep-hourly",
	"keep-daily":          "backup-keep-daily",
	"keep-weekly":         "backup-keep-weekly",
	"keep-monthly":        "backup-keep-monthly",
	"keep-yearly":         "backup-keep-yearly",
	"keep-within":         "backup-keep-within",
	"keep-within-hourly":  "backup-keep-within-hourly",
	"keep-within-daily":   "backup-keep-within-daily",
	"keep-within-weekly":  "backup-keep-within-weekly",
	"keep-within-monthly": "backup-keep-within-monthly",
	"keep-within-yearly":  "backup-keep-within-yearly",
	"remove-original":     "backup-archive-remove-original",
}

// archiveFlatKey is the flat key an archive setting replaces.
func archiveFlatKey(setting string) string {
	if k, ok := archiveFlatKeys[setting]; ok {
		return k
	}
	return archiveLegacyPrefix + setting
}

// Archive setting groups, how a form lays a plugin's settings out: the location and
// credentials first (no group), then the retention, then the advanced settings.
const (
	ConstArchiveSettingGroupRetention = "retention"
	ConstArchiveSettingGroupAdvanced  = "advanced"
)

// archiveSettingGroup is the group of a restic setting.
func archiveSettingGroup(name string) string {
	for _, r := range archiveResticRetentionSettings {
		if r == name {
			return ConstArchiveSettingGroupRetention
		}
	}
	switch {
	case strings.HasPrefix(name, "purge-"):
		return ConstArchiveSettingGroupRetention
	case name == "repo-append-cluster", name == "additional-env", name == "tags", name == "host",
		name == "timeout", name == "dump-timeout", name == "auto-init", strings.HasPrefix(name, "mount-"):
		return ConstArchiveSettingGroupAdvanced
	}
	return ""
}

// enums of the restic settings
var archiveResticEnums = map[string][]string{
	"s3-mode": {ConstResticS3ModeAuto, ConstResticS3ModeNew, ConstResticS3ModeLegacy},
}

var archiveResticCaps = []string{ConstArchiveCapEdit, ConstArchiveCapRemove, ConstArchiveCapProbe, ConstArchiveCapSize}

// resticSettings types each setting from the backup-restic-* field it replaces, so a type
// is never written twice.
func resticSettings(names []string, secrets ...string) []ArchivePluginSetting {
	t := reflect.TypeOf(Config{})
	byKey := map[string]reflect.Kind{}
	for i := 0; i < t.NumField(); i++ {
		if tag := strings.Split(t.Field(i).Tag.Get("mapstructure"), ",")[0]; tag != "" {
			byKey[tag] = t.Field(i).Type.Kind()
		}
	}
	out := make([]ArchivePluginSetting, 0, len(names)+len(secrets))
	for _, name := range names {
		st := ArchivePluginSetting{Name: name, Type: ConstArchiveSettingTypeString, Group: archiveSettingGroup(name)}
		switch byKey[archiveFlatKey(name)] {
		case reflect.Bool:
			st.Type = ConstArchiveSettingTypeBool
		case reflect.Int, reflect.Int32, reflect.Int64:
			st.Type = ConstArchiveSettingTypeInt
		}
		if values, ok := archiveResticEnums[name]; ok {
			st.Type, st.Values = ConstArchiveSettingTypeEnum, values
		}
		out = append(out, st)
	}
	for _, name := range secrets {
		out = append(out, ArchivePluginSetting{Name: name, Type: ConstArchiveSettingTypeSecret})
	}
	return out
}

var (
	archivePluginsOnce     sync.Once
	archivePluginManifests map[string]ArchivePluginManifest
)

func archiveManifests() map[string]ArchivePluginManifest {
	archivePluginsOnce.Do(func() {
		archivePluginManifests = map[string]ArchivePluginManifest{
			ConstArchivePluginResticLocal: {
				Name:         ConstArchivePluginResticLocal,
				Description:  "Restic repository on a local path",
				Settings:     resticSettings(append([]string{"local-repository", "dir-mode", "file-mode", "purge-oldest-on-disk-space", "purge-oldest-on-disk-threshold"}, archiveResticCommonSettings...), "password"),
				Capabilities: archiveResticCaps,
			},
			ConstArchivePluginResticSftp: {
				Name:         ConstArchivePluginResticSftp,
				Description:  "Restic repository over sftp (local-repository sftp:user@host:/path)",
				Settings:     resticSettings(append([]string{"local-repository"}, archiveResticCommonSettings...), "password"),
				Capabilities: archiveResticCaps,
			},
			ConstArchivePluginResticS3: {
				Name:         ConstArchivePluginResticS3,
				Description:  "Restic repository in an S3 bucket",
				Settings:     resticSettings(append([]string{"repository", "aws-access-key-id", "aws-region", "aws-endpoint", "aws-bucket", "aws-prefix", "s3-mode"}, archiveResticCommonSettings...), "password", "aws-access-secret"),
				Capabilities: archiveResticCaps,
			},
		}
	})
	return archivePluginManifests
}

// ArchivePlugin is the manifest of an archive plugin.
func ArchivePlugin(name string) (ArchivePluginManifest, bool) {
	m, ok := archiveManifests()[name]
	return m, ok
}

// ArchivePlugins lists the archive plugin manifests, by name.
func ArchivePlugins() []ArchivePluginManifest {
	out := make([]ArchivePluginManifest, 0, len(archiveManifests()))
	for _, m := range archiveManifests() {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Setting is the declared setting name.
func (m ArchivePluginManifest) Setting(name string) (ArchivePluginSetting, bool) {
	for _, st := range m.Settings {
		if st.Name == name {
			return st, true
		}
	}
	return ArchivePluginSetting{}, false
}

// HasSetting: the plugin declares name, a setting that is not a secret.
func (m ArchivePluginManifest) HasSetting(name string) bool {
	st, ok := m.Setting(name)
	return ok && st.Type != ConstArchiveSettingTypeSecret
}

// HasSecret: the plugin declares name, a secret.
func (m ArchivePluginManifest) HasSecret(name string) bool {
	st, ok := m.Setting(name)
	return ok && st.Type == ConstArchiveSettingTypeSecret
}

func (m ArchivePluginManifest) Can(capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// Validate checks value against the setting's type (an enum's allowed values included).
func (st ArchivePluginSetting) Validate(value string) error {
	switch st.Type {
	case ConstArchiveSettingTypeBool:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s: %q is not a bool", st.Name, value)
		}
	case ConstArchiveSettingTypeInt:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("%s: %q is not an integer", st.Name, value)
		}
	case ConstArchiveSettingTypeEnum:
		for _, v := range st.Values {
			if v == value {
				return nil
			}
		}
		return fmt.Errorf("%s: %q is not one of %s", st.Name, value, strings.Join(st.Values, ", "))
	}
	if strings.HasPrefix(st.Name, "keep-within") && !ResticDurationChecker(value) {
		return fmt.Errorf("%s: %q is not a duration like 2y5m7d3h", st.Name, value)
	}
	return nil
}

// archiveSecretKeys are the flat secrets an archive's secret replaces, by secret name: the
// runtime reads them, the credential chain answers them from the connected archive (#2088).
var archiveSecretKeys = map[string]string{
	"password":          "backup-restic-password",
	"aws-access-secret": "backup-restic-aws-access-secret",
}

// ArchiveSecretName is the archive secret a flat key is, when it is one.
func ArchiveSecretName(key string) (string, bool) {
	for name, k := range archiveSecretKeys {
		if k == key {
			return name, true
		}
	}
	return "", false
}

// ArchiveConfig is one entry of the archive list. Settings holds the settings and the
// secrets (encrypted) its plugin's manifest declares; in this step the settings are the
// legacy backup-restic-* keys without their prefix.
type ArchiveConfig struct {
	Name     string                 `mapstructure:"name" toml:"name" json:"name"`
	Plugin   string                 `mapstructure:"plugin" toml:"plugin" json:"plugin"`
	Settings map[string]interface{} `mapstructure:",remain" toml:"-" json:"settings"`
}

// archiveLegacyKeys are the flat keys archive 1 carries: an archive setting named as the
// key without "backup-restic-", or as archiveFlatKeys names it (the retention). Not here
// and still flat: the secrets (backup-restic-password, backup-restic-aws-access-secret)
// until the credential store chain, and what is not an archive's (binary path, reseed,
// metadata extractor, the server-scope mount dir, unsafe mount, verification).
var archiveLegacyKeys = []string{
	"backup-restic-local-repository",
	"backup-restic-repository",
	"backup-restic-aws-access-key-id",
	"backup-restic-aws-region",
	"backup-restic-aws-endpoint",
	"backup-restic-aws-bucket",
	"backup-restic-aws-prefix",
	"backup-restic-s3-mode",
	"backup-restic-repo-append-cluster",
	"backup-restic-additional-env",
	"backup-restic-tags",
	"backup-restic-host",
	"backup-restic-timeout",
	"backup-restic-dump-timeout",
	"backup-restic-auto-init",
	"backup-restic-dir-mode",
	"backup-restic-file-mode",
	"backup-restic-purge-group-by",
	"backup-restic-purge-keep-tag",
	"backup-restic-purge-host",
	"backup-restic-purge-tag",
	"backup-restic-purge-path",
	"backup-restic-purge-prune",
	"backup-restic-purge-prune-compact",
	"backup-restic-purge-prune-max-unused",
	"backup-restic-purge-prune-max-repack-size",
	"backup-restic-purge-prune-repack-cacheable-only",
	"backup-restic-purge-prune-repack-small",
	"backup-restic-purge-prune-repack-uncompressed",
	"backup-restic-purge-oldest-on-disk-space",
	"backup-restic-purge-oldest-on-disk-threshold",
	"backup-restic-mount-target-dir",
	"backup-restic-mount-host",
	"backup-restic-mount-tag",
	"backup-restic-mount-path",
	"backup-restic-mount-path-template",
	"backup-restic-mount-time-template",
	"backup-restic-mount-allow-other",
	"backup-restic-mount-no-default-permissions",
	"backup-restic-mount-owner-root",
	"backup-restic-mount-no-lock",
	"backup-restic-mount-verbose",
	"backup-restic-mount-quiet",
	"backup-restic-mount-recovery-enabled",
	// the retention (#2091)
	"backup-keep-last",
	"backup-keep-hourly",
	"backup-keep-daily",
	"backup-keep-weekly",
	"backup-keep-monthly",
	"backup-keep-yearly",
	"backup-keep-within",
	"backup-keep-within-hourly",
	"backup-keep-within-daily",
	"backup-keep-within-weekly",
	"backup-keep-within-monthly",
	"backup-keep-within-yearly",
	"backup-archive-remove-original",
}

// archiveModeKeys are replaced by backup-archive-name and the connected archive's plugin.
var archiveModeKeys = []string{"backup-archive-mode", "backup-restic", "backup-restic-aws"}

const archiveLegacyPrefix = "backup-restic-"

// ArchiveSettingName is the archive setting a legacy key becomes.
func ArchiveSettingName(legacyKey string) string {
	for name, k := range archiveFlatKeys {
		if k == legacyKey {
			return name
		}
	}
	return strings.TrimPrefix(legacyKey, archiveLegacyPrefix)
}

// IsArchiveLegacyKey says whether the save leaves key out: archive 1 carries it.
func IsArchiveLegacyKey(key string) bool {
	for _, k := range archiveModeKeys {
		if k == key {
			return true
		}
	}
	for _, k := range archiveLegacyKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ArchivePluginForMode is the plugin of a backup-archive-mode ("" for none).
func ArchivePluginForMode(mode string) string {
	switch mode {
	case ConstBackupArchiveModeResticLocal:
		return ConstArchivePluginResticLocal
	case ConstBackupArchiveModeResticSftp:
		return ConstArchivePluginResticSftp
	case ConstBackupArchiveModeResticAws:
		return ConstArchivePluginResticS3
	}
	return ""
}

// ArchiveModeForPlugin is the backup-archive-mode the runtime runs for a restic plugin.
func ArchiveModeForPlugin(plugin string) (string, bool) {
	switch plugin {
	case ConstArchivePluginResticLocal:
		return ConstBackupArchiveModeResticLocal, true
	case ConstArchivePluginResticSftp:
		return ConstBackupArchiveModeResticSftp, true
	case ConstArchivePluginResticS3:
		return ConstBackupArchiveModeResticAws, true
	}
	return "", false
}

// MarshalJSON never shows a secret: the API gets the names of the secrets set.
func (a ArchiveConfig) MarshalJSON() ([]byte, error) {
	settings := make(map[string]interface{}, len(a.Settings))
	secrets := []string{}
	for k, v := range a.Settings {
		if _, secret := archiveSecretKeys[k]; secret {
			secrets = append(secrets, k)
			continue
		}
		settings[k] = v
	}
	sort.Strings(secrets)
	return json.Marshal(struct {
		Name       string                 `json:"name"`
		Plugin     string                 `json:"plugin"`
		Settings   map[string]interface{} `json:"settings"`
		SecretsSet []string               `json:"secretsSet"`
	}{a.Name, a.Plugin, settings, secrets})
}

// archiveMu guards the archive list: the save (config-sync goroutine), the API setters and
// the peer replay all write it, and a concurrent map write stops repman (F2).
var archiveMu sync.Mutex

func (conf *Config) archiveByName(name string) *ArchiveConfig {
	for i := range conf.Archives {
		if conf.Archives[i].Name == name {
			return &conf.Archives[i]
		}
	}
	return nil
}

// archiveType is the type part of a plugin's archive names: local, sftp, s3 for the restic
// plugins, the plugin name without "archive-" for another one.
func archiveType(plugin string) string {
	if t, ok := strings.CutPrefix(plugin, "archive-restic-"); ok {
		return t
	}
	return strings.TrimPrefix(plugin, "archive-")
}

// nextArchiveName is the first free name of the plugin's type: archive-<type>-1, -2 ...
func (conf *Config) nextArchiveName(plugin string) string {
	for n := 1; ; n++ {
		if name := fmt.Sprintf("archive-%s-%d", archiveType(plugin), n); conf.archiveByName(name) == nil {
			return name
		}
	}
}

func (conf *Config) addArchive(plugin string) *ArchiveConfig {
	conf.Archives = append(conf.Archives, ArchiveConfig{Name: conf.nextArchiveName(plugin), Plugin: plugin, Settings: map[string]interface{}{}})
	return &conf.Archives[len(conf.Archives)-1]
}

// connectedArchive is the archive backup-archive-name names, when its plugin is one the
// runtime runs.
func (conf *Config) connectedArchive() *ArchiveConfig {
	if conf.BackupArchiveName == "" {
		return nil
	}
	a := conf.archiveByName(conf.BackupArchiveName)
	if a == nil {
		return nil
	}
	if _, ok := ArchiveModeForPlugin(a.Plugin); !ok {
		return nil
	}
	return a
}

// archiveEntryHolds: the entry's plugin manifest declares setting or secret name; an entry
// of an unknown plugin keeps every name an archive can hold.
func archiveEntryHolds(a *ArchiveConfig, name string) bool {
	if m, ok := ArchivePlugin(a.Plugin); ok {
		return m.HasSetting(name) || m.HasSecret(name)
	}
	if _, ok := archiveSecretKeys[name]; ok {
		return true
	}
	return IsArchiveLegacyKey(archiveFlatKey(name))
}

func cleanArchiveEntry(a *ArchiveConfig) {
	for k := range a.Settings {
		if !archiveEntryHolds(a, k) {
			delete(a.Settings, k)
		}
	}
}

// cleanArchiveSettings drops from the entries read what their plugin does not declare.
func (conf *Config) cleanArchiveSettings() {
	for i := range conf.Archives {
		cleanArchiveEntry(&conf.Archives[i])
	}
}

// configFieldsByKey maps a mapstructure key to its Config field index.
var (
	configFieldsOnce  sync.Once
	configFieldsByKey map[string]int
)

func configField(conf *Config, key string) (reflect.Value, bool) {
	configFieldsOnce.Do(func() {
		configFieldsByKey = make(map[string]int)
		t := reflect.TypeOf(Config{})
		for i := 0; i < t.NumField(); i++ {
			tag := strings.Split(t.Field(i).Tag.Get("mapstructure"), ",")[0]
			if tag != "" {
				configFieldsByKey[tag] = i
			}
		}
	})
	i, ok := configFieldsByKey[key]
	if !ok {
		return reflect.Value{}, false
	}
	return reflect.ValueOf(conf).Elem().Field(i), true
}

// fieldValue is a field's value as stored in an archive entry (TOML types).
func fieldValue(f reflect.Value) interface{} {
	switch f.Kind() {
	case reflect.Bool:
		return f.Bool()
	case reflect.Int, reflect.Int64, reflect.Int32:
		return f.Int()
	default:
		return f.String()
	}
}

// setField sets a field from any printed form (TOML value, pflag default, event string).
func setField(f reflect.Value, v interface{}) error {
	s := fmt.Sprintf("%v", v)
	switch f.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int64, reflect.Int32:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		f.SetInt(n)
	case reflect.String:
		f.SetString(s)
	default:
		return fmt.Errorf("unsupported field kind %s", f.Kind())
	}
	return nil
}

// isDefaultValue compares a field with the key's default (no default: the zero value).
func (conf *Config) isDefaultValue(key string, f reflect.Value) bool {
	def, ok := conf.DefaultFlagMap[key]
	if !ok {
		return f.IsZero()
	}
	return fmt.Sprintf("%v", fieldValue(f)) == fmt.Sprintf("%v", def)
}

// isPinned: a key set in a config file (or on the command line) wins over the list.
func (conf *Config) isPinned(key string) bool {
	_, ok := conf.GetImmuableFlag(key)
	return ok
}

func cloneArchives(in []ArchiveConfig) []ArchiveConfig {
	if in == nil {
		return nil
	}
	out := make([]ArchiveConfig, len(in))
	for i, a := range in {
		out[i] = a
		out[i].Settings = make(map[string]interface{}, len(a.Settings))
		for k, v := range a.Settings {
			out[i].Settings[k] = v
		}
	}
	return out
}

// MigrateArchives resolves the archive list at cluster init, by inspecting the config: a
// cluster without a list whose archive settings differ from their defaults, or whose
// backup-archive-mode is not none, gets archive-<type>-1 built from them, connected when the
// mode is not none (not connected, its settings survive the legacy keys leaving the saved
// config). A cluster with every archive setting at its default gets no list. Then the
// connected archive is projected onto the BackupRestic* fields the runtime reads.
func (conf *Config) MigrateArchives() {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	conf.NormalizeBackupArchiveMode()
	// a Config is copied by value: the list must not share entries with another copy
	conf.Archives = cloneArchives(conf.Archives)
	conf.cleanArchiveSettings()

	if len(conf.Archives) == 0 {
		connect := conf.BackupArchiveMode != ConstBackupArchiveModeNone
		if connect || conf.archiveSettingsCustomised() || conf.archiveSecretsCustomised() {
			plugin := ArchivePluginForMode(conf.BackupArchiveMode)
			if plugin == "" {
				plugin = conf.unconnectedArchivePlugin()
			}
			a := conf.addArchive(plugin)
			conf.captureArchive(a)
			conf.moveFlatSecrets(a)
			if connect && !conf.isPinned("backup-archive-name") {
				conf.BackupArchiveName = a.Name
			}
		}
	} else if a := conf.legacyArchive(); a != nil {
		conf.adoptFlatSettings(a)
	}
	conf.projectArchive()
}

// legacyArchive is the archive the flat settings belong to: the attached one, else the
// first of the list.
func (conf *Config) legacyArchive() *ArchiveConfig {
	if a := conf.archiveByName(conf.BackupArchiveName); a != nil {
		return a
	}
	if len(conf.Archives) > 0 {
		return &conf.Archives[0]
	}
	return nil
}

// adoptFlatSettings moves into a the archive settings still customised as flat keys and
// that a does not hold yet: a list saved before a setting became an archive's (the
// retention, #2091) keeps its value, and the save then leaves the flat key out.
func (conf *Config) adoptFlatSettings(a *ArchiveConfig) {
	if a.Settings == nil {
		a.Settings = map[string]interface{}{}
	}
	for _, key := range archiveLegacyKeys {
		name := ArchiveSettingName(key)
		if _, held := a.Settings[name]; held || !archiveEntryHolds(a, name) || conf.isPinned(key) {
			continue
		}
		if f, ok := configField(conf, key); ok && !conf.isDefaultValue(key, f) {
			a.Settings[name] = fieldValue(f)
		}
	}
}

// moveFlatSecrets moves the archive secrets set at runtime (DynamicFlagMap: the saved config,
// a setter) into a, encrypted, and out of the flat config (#2088). A pinned secret stays in
// its file; a secret a's plugin does not declare stays flat.
func (conf *Config) moveFlatSecrets(a *ArchiveConfig) {
	for name, key := range archiveSecretKeys {
		if !archiveEntryHolds(a, name) || conf.isPinned(key) {
			continue
		}
		v, ok := conf.GetDynamicFlag(key)
		if !ok || v == nil || fmt.Sprintf("%v", v) == "" {
			continue
		}
		raw := fmt.Sprintf("%v", v)
		if !strings.Contains(raw, "hash_") {
			raw = conf.EncryptSecretValue(raw)
		}
		a.Settings[name] = raw
		conf.DeleteDynamicFlag(key)
		if f, ok := configField(conf, key); ok {
			if def, ok := conf.DefaultFlagMap[key]; ok {
				setField(f, def)
			}
		}
	}
}

// archiveSecretsCustomised: a flat archive secret was set at runtime.
func (conf *Config) archiveSecretsCustomised() bool {
	for _, key := range archiveSecretKeys {
		if v, ok := conf.GetDynamicFlag(key); ok && v != nil && fmt.Sprintf("%v", v) != "" && !conf.isPinned(key) {
			return true
		}
	}
	return false
}

// archiveSettingsCustomised: an archive setting differs from its default, a pinned one
// excepted (the config file keeps it).
func (conf *Config) archiveSettingsCustomised() bool {
	for _, key := range archiveLegacyKeys {
		if f, ok := configField(conf, key); ok && !conf.isPinned(key) && !conf.isDefaultValue(key, f) {
			return true
		}
	}
	return false
}

// unconnectedArchivePlugin is the plugin of an archive-<type>-1 built with the mode none, from the
// settings the cluster customised: a local repository (sftp: prefix for sftp), else an S3
// setting, else local. The default backup-restic-repository is an s3: URL, so it never
// decides alone.
func (conf *Config) unconnectedArchivePlugin() string {
	if repo := strings.TrimSpace(conf.BackupResticLocalRepository); repo != "" && !conf.isPinned("backup-restic-local-repository") {
		if IsSftpResticRepository(repo) {
			return ConstArchivePluginResticSftp
		}
		return ConstArchivePluginResticLocal
	}
	for _, key := range archiveLegacyKeys {
		if key != "backup-restic-repository" && !strings.HasPrefix(key, "backup-restic-aws") {
			continue
		}
		if f, ok := configField(conf, key); ok && !conf.isPinned(key) && !conf.isDefaultValue(key, f) {
			return ConstArchivePluginResticS3
		}
	}
	return ConstArchivePluginResticLocal
}

// ProjectArchive sets the BackupRestic* fields from the connected archive.
func (conf *Config) ProjectArchive() {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	conf.projectArchive()
}

// projectArchive: a pinned key keeps its value; a setting the archive does not hold goes
// back to its default (no default: its zero value), so a switch never keeps the previous
// archive's value. No archive
// connected: the mode is none.
func (conf *Config) projectArchive() {
	a := conf.connectedArchive()
	modePinned := false
	for _, k := range archiveModeKeys {
		if conf.isPinned(k) {
			modePinned = true
		}
	}
	if !modePinned {
		if a == nil {
			conf.BackupArchiveMode = ConstBackupArchiveModeNone
		} else {
			conf.BackupArchiveMode, _ = ArchiveModeForPlugin(a.Plugin)
		}
	}
	// a list with nothing attached: the settings go back to their defaults too, so the
	// archive left never lends its values to the next one. An empty list keeps the fields:
	// set before any archive, the save captures them into a new one.
	if a != nil || len(conf.Archives) > 0 {
		for _, key := range archiveLegacyKeys {
			if conf.isPinned(key) {
				continue
			}
			f, ok := configField(conf, key)
			if !ok {
				continue
			}
			var v interface{}
			held := false
			if a != nil {
				v, held = a.Settings[ArchiveSettingName(key)]
			}
			if held {
				setField(f, v)
			} else if def, ok := conf.DefaultFlagMap[key]; ok {
				setField(f, def)
			} else {
				f.Set(reflect.Zero(f.Type()))
			}
		}
	}
	conf.BackupResticLocalRepository = strings.TrimSpace(conf.BackupResticLocalRepository)
	conf.applyBackupArchiveModeFlags()
	conf.NormalizeResticS3Mode()
}

// captureArchive stores the BackupRestic* fields into a: a pinned key or a default value
// is not stored, as the flat save does for any key.
func (conf *Config) captureArchive(a *ArchiveConfig) {
	if a.Settings == nil {
		a.Settings = map[string]interface{}{}
	}
	for _, key := range archiveLegacyKeys {
		name := ArchiveSettingName(key)
		f, ok := configField(conf, key)
		if !ok || !archiveEntryHolds(a, name) || conf.isPinned(key) || conf.isDefaultValue(key, f) {
			delete(a.Settings, name)
			continue
		}
		a.Settings[name] = fieldValue(f)
	}
}

// ConnectedArchiveSecret is the raw (encrypted) secret name of the connected archive.
func (conf *Config) ConnectedArchiveSecret(name string) (string, bool) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a := conf.connectedArchive()
	if a == nil || !archiveEntryHolds(a, name) {
		return "", false
	}
	v, ok := a.Settings[name]
	if !ok || v == nil {
		return "", false
	}
	return fmt.Sprintf("%v", v), true
}

// CanSetConnectedArchiveSecret is nil when the connected archive takes secret name.
func (conf *Config) CanSetConnectedArchiveSecret(name string) error {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a := conf.connectedArchive()
	if a == nil {
		return fmt.Errorf("no archive connected")
	}
	if !archiveEntryHolds(a, name) {
		return fmt.Errorf("archive %s (%s) has no secret %s", a.Name, a.Plugin, name)
	}
	return nil
}

// SetConnectedArchiveSecret stores the raw (encrypted) secret name in the connected archive;
// present false removes it.
func (conf *Config) SetConnectedArchiveSecret(name, raw string, present bool) error {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a := conf.connectedArchive()
	if a == nil {
		return fmt.Errorf("no archive connected")
	}
	if !archiveEntryHolds(a, name) {
		return fmt.Errorf("archive %s (%s) has no secret %s", a.Name, a.Plugin, name)
	}
	if a.Settings == nil {
		a.Settings = map[string]interface{}{}
	}
	if present {
		a.Settings[name] = raw
	} else {
		delete(a.Settings, name)
	}
	return nil
}

// ArchiveHoldsSecret: the connected archive holds the flat secret key, so the save leaves the
// flat key out.
func (conf *Config) ArchiveHoldsSecret(key string) bool {
	name, ok := ArchiveSecretName(key)
	if !ok {
		return false
	}
	_, held := conf.ConnectedArchiveSecret(name)
	return held
}

// SyncArchiveFromConfig captures the BackupRestic* fields into the connected archive before
// a save: a setter (API, GUI, MCP) changed the field the runtime reads. No archive
// connected: the fields belong to no archive, except with an empty list, where customised
// settings become an archive-<type>-1 not connected (nothing set at runtime is lost).
func (conf *Config) SyncArchiveFromConfig() {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a := conf.connectedArchive()
	if a == nil {
		if len(conf.Archives) == 0 && conf.archiveSettingsCustomised() {
			conf.captureArchive(conf.addArchive(conf.unconnectedArchivePlugin()))
		}
		return
	}
	conf.captureArchive(a)
}

// SwitchArchive connects the cluster to the archive named name ("" for none): the fields
// are captured into the archive left, then the new one is projected.
func (conf *Config) SwitchArchive(name string) error {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	if name != "" {
		a := conf.archiveByName(name)
		if a == nil {
			return fmt.Errorf("unknown archive %s", name)
		}
		if _, ok := ArchiveModeForPlugin(a.Plugin); !ok {
			return fmt.Errorf("archive %s: plugin %s is not supported", name, a.Plugin)
		}
	}
	if cur := conf.connectedArchive(); cur != nil {
		conf.captureArchive(cur)
	}
	conf.BackupArchiveName = name
	conf.projectArchive()
	return nil
}

// ApplyArchiveMode is the backup-archive-mode setter over the list: none disconnects the
// cluster; another mode changes the connected archive's plugin (its name is only a label),
// or connects the first archive with that plugin, or a new archive-<type>-1 (holding the
// fields when the list was empty, so settings set before the mode are kept).
func (conf *Config) ApplyArchiveMode(mode string) error {
	if err := conf.ValidateBackupArchiveMode(mode); err != nil {
		return err
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	cur := conf.connectedArchive()
	if cur != nil {
		conf.captureArchive(cur)
	}
	plugin := ArchivePluginForMode(mode)
	switch {
	case plugin == "":
		conf.BackupArchiveName = ""
	case cur != nil:
		cur.Plugin = plugin
		cleanArchiveEntry(cur)
	default:
		var a *ArchiveConfig
		for i := range conf.Archives {
			if conf.Archives[i].Plugin == plugin {
				a = &conf.Archives[i]
				break
			}
		}
		if a == nil {
			empty := len(conf.Archives) == 0
			a = conf.addArchive(plugin)
			if empty {
				conf.captureArchive(a)
			}
		}
		conf.BackupArchiveName = a.Name
	}
	conf.BackupArchiveMode = mode
	conf.projectArchive()
	return nil
}

// SetArchiveSetting applies one archive setting by name (a peer's event
// "archive.<name>.<setting>"), creating the entry when needed, then projects the connected
// archive. unset removes the setting (its field goes back to its default) or, for
// "plugin", the entry.
func (conf *Config) SetArchiveSetting(archive, setting, value string, unset bool) error {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	idx := -1
	for i := range conf.Archives {
		if conf.Archives[i].Name == archive {
			idx = i
		}
	}
	if idx < 0 {
		if unset {
			return nil
		}
		conf.Archives = append(conf.Archives, ArchiveConfig{Name: archive, Settings: map[string]interface{}{}})
		idx = len(conf.Archives) - 1
	}
	a := &conf.Archives[idx]
	if a.Settings == nil {
		a.Settings = map[string]interface{}{}
	}
	switch setting {
	case "name":
		return nil
	case "plugin":
		if unset {
			conf.Archives = append(conf.Archives[:idx], conf.Archives[idx+1:]...)
		} else {
			a.Plugin = value
		}
	case "password", "aws-access-secret":
		// the peer's ciphertext: peers share the encryption key, the chain reads it at its
		// next refresh
		if unset {
			delete(a.Settings, setting)
		} else {
			a.Settings[setting] = value
		}
	default:
		key := archiveFlatKey(setting)
		f, ok := configField(conf, key)
		if !ok {
			return fmt.Errorf("unknown archive setting %s", setting)
		}
		if unset {
			delete(a.Settings, setting)
			if def, ok := conf.DefaultFlagMap[key]; ok && !conf.isPinned(key) && conf.connectedArchive() == a {
				setField(f, def)
			}
		} else {
			probe := reflect.New(f.Type()).Elem()
			if err := setField(probe, value); err != nil {
				return err
			}
			a.Settings[setting] = fieldValue(probe)
		}
	}
	conf.projectArchive()
	return nil
}

// ArchivesToml renders the list as [[<section>.archive]] tables for the saved config.
func (conf *Config) ArchivesToml(section string) (string, error) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	var b strings.Builder
	for _, a := range conf.Archives {
		b.WriteString("\n[[" + section + ".archive]]\n")
		head := map[string]interface{}{"name": a.Name, "plugin": a.Plugin}
		for _, part := range []map[string]interface{}{head, a.Settings} {
			if len(part) == 0 {
				continue
			}
			keys := make([]string, 0, len(part))
			for k := range part {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				t, err := toml.TreeFromMap(map[string]interface{}{k: part[k]})
				if err != nil {
					return "", err
				}
				line, err := t.ToTomlString()
				if err != nil {
					return "", err
				}
				b.WriteString(line)
			}
		}
	}
	return b.String(), nil
}

// ArchiveSpec is an archive to add or change, from the API, the GUI or a pushed
// inject.toml (#2091): Settings holds settings and secrets by the names of the plugin's
// config, Unset the names to clear.
type ArchiveSpec struct {
	Name     string                 `json:"name" mapstructure:"name"`
	Plugin   string                 `json:"plugin" mapstructure:"plugin"`
	Settings map[string]interface{} `json:"settings" mapstructure:",remain"`
	Unset    []string               `json:"unset,omitempty" mapstructure:"unset"`
}

var archiveNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateArchiveName: lowercase letters, digits, - and _, starting with a letter or a
// digit, 64 characters at most. The name goes into the config file and the event keys.
func ValidateArchiveName(name string) error {
	if !archiveNameRe.MatchString(name) {
		return fmt.Errorf("archive name %q: lowercase letters, digits, - and _ only, 64 characters at most", name)
	}
	return nil
}

// ArchiveExists: the list holds name.
func (conf *Config) ArchiveExists(name string) bool {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	return conf.archiveByName(name) != nil
}

// ArchiveList is a copy of the list and the attached archive's name.
func (conf *Config) ArchiveList() ([]ArchiveConfig, string) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	return cloneArchives(conf.Archives), conf.BackupArchiveName
}

// AttachedArchive is the attached archive's name ("" none, or no config).
func (conf *Config) AttachedArchive() string {
	if conf == nil {
		return ""
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	return conf.BackupArchiveName
}

// LegacyArchiveName is the archive a snapshot recorded before the archive list belongs to:
// the archive the legacy settings became, attached by the migration, else the first of the
// list. "" when the cluster has no archive.
func (conf *Config) LegacyArchiveName() string {
	if conf == nil {
		return ""
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	if conf.BackupArchiveName != "" {
		return conf.BackupArchiveName
	}
	if len(conf.Archives) > 0 {
		return conf.Archives[0].Name
	}
	return ""
}

// typedArchiveValue checks v against the plugin's setting and returns it as stored: a
// bool, an int64, a string, or an encrypted secret.
func (conf *Config) typedArchiveValue(st ArchivePluginSetting, v interface{}) (interface{}, error) {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if err := st.Validate(s); err != nil {
		return nil, err
	}
	switch st.Type {
	case ConstArchiveSettingTypeBool:
		b, _ := strconv.ParseBool(s)
		return b, nil
	case ConstArchiveSettingTypeInt:
		n, _ := strconv.ParseInt(s, 10, 64)
		return n, nil
	case ConstArchiveSettingTypeSecret:
		if strings.HasPrefix(s, "hash_") {
			return s, nil
		}
		return conf.EncryptSecretValue(s), nil
	}
	return s, nil
}

// BuildArchive returns the entry spec gives, checked against its plugin's config, without
// storing it: create adds a new archive (named archive-<type>-<n> when spec has no name),
// else spec changes an existing one, or adds it when upsert.
func (conf *Config) BuildArchive(spec ArchiveSpec, create, upsert bool) (ArchiveConfig, bool, error) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	var a ArchiveConfig
	existing := conf.archiveByName(spec.Name)
	isNew := existing == nil
	switch {
	case create && existing != nil:
		return a, false, fmt.Errorf("archive %s already exists", spec.Name)
	case !create && existing == nil && !upsert:
		return a, false, fmt.Errorf("unknown archive %s", spec.Name)
	}
	if existing != nil {
		a = cloneArchives([]ArchiveConfig{*existing})[0]
	} else {
		a = ArchiveConfig{Name: spec.Name, Settings: map[string]interface{}{}}
	}
	if spec.Plugin != "" {
		a.Plugin = spec.Plugin
	}
	m, ok := ArchivePlugin(a.Plugin)
	if !ok {
		return a, false, fmt.Errorf("unknown archive plugin %q", a.Plugin)
	}
	if isNew {
		if a.Name == "" {
			a.Name = conf.nextArchiveName(a.Plugin)
		}
		if err := ValidateArchiveName(a.Name); err != nil {
			return a, false, err
		}
	} else if !m.Can(ConstArchiveCapEdit) && len(spec.Settings)+len(spec.Unset) > 0 {
		return a, false, fmt.Errorf("archive %s: plugin %s cannot be edited", a.Name, a.Plugin)
	}
	cleanArchiveEntry(&a)
	for name, v := range spec.Settings {
		st, ok := m.Setting(name)
		if !ok {
			return a, false, fmt.Errorf("plugin %s has no setting %s", a.Plugin, name)
		}
		if st.Type == ConstArchiveSettingTypeSecret && fmt.Sprintf("%v", v) == "" {
			continue // an empty secret field in a form keeps the secret
		}
		tv, err := conf.typedArchiveValue(st, v)
		if err != nil {
			return a, false, err
		}
		a.Settings[name] = tv
	}
	for _, name := range spec.Unset {
		delete(a.Settings, name)
	}
	return a, isNew, nil
}

// StoreArchive adds a or replaces the entry of its name; the attached archive is projected
// again.
func (conf *Config) StoreArchive(a ArchiveConfig) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a = cloneArchives([]ArchiveConfig{a})[0]
	if cur := conf.archiveByName(a.Name); cur != nil {
		*cur = a
	} else {
		conf.Archives = append(conf.Archives, a)
	}
	conf.projectArchive()
}

// RenameArchive renames an archive; backup-archive-name follows.
func (conf *Config) RenameArchive(name, newName string) error {
	if err := ValidateArchiveName(newName); err != nil {
		return err
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	a := conf.archiveByName(name)
	if a == nil {
		return fmt.Errorf("unknown archive %s", name)
	}
	if name == newName {
		return nil
	}
	if conf.archiveByName(newName) != nil {
		return fmt.Errorf("archive %s already exists", newName)
	}
	a.Name = newName
	if conf.BackupArchiveName == name {
		conf.BackupArchiveName = newName
	}
	return nil
}

// DeleteArchiveEntry takes name out of the list (its content is the caller's: remove
// deletes it first). The attached archive is detached.
func (conf *Config) DeleteArchiveEntry(name string) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	for i := range conf.Archives {
		if conf.Archives[i].Name == name {
			conf.Archives = append(conf.Archives[:i], conf.Archives[i+1:]...)
			break
		}
	}
	if conf.BackupArchiveName == name {
		conf.BackupArchiveName = ""
		conf.projectArchive()
	}
}

// ConfigForArchive is a copy of the config running archive a, for an operation on an
// archive that is not, or not yet, attached (probe, remove): its BackupRestic* fields and
// its restic password and S3 secret are a's. The copy never writes the config maps.
func (conf *Config) ConfigForArchive(a ArchiveConfig) *Config {
	archiveMu.Lock()
	cp := *conf
	cp.Archives = cloneArchives(conf.Archives)
	found := false
	for i := range cp.Archives {
		if cp.Archives[i].Name == a.Name {
			cp.Archives[i] = cloneArchives([]ArchiveConfig{a})[0]
			found = true
		}
	}
	if !found {
		cp.Archives = append(cp.Archives, cloneArchives([]ArchiveConfig{a})[0])
	}
	cp.BackupArchiveName = a.Name
	cp.projectArchive()
	archiveMu.Unlock()

	// the copy's own secret store: the archive's secret, else the flat value (pinned,
	// dynamic, default), as the credential chain reads it
	cp.RebuildSecrets(func(key string) (interface{}, bool) {
		if name, ok := ArchiveSecretName(key); ok {
			if v, ok := a.Settings[name]; ok && v != nil && archiveEntryHolds(&a, name) {
				return v, true
			}
		}
		if v, ok := conf.GetImmuableFlag(key); ok && v != nil {
			return v, true
		}
		if v, ok := conf.GetDynamicFlag(key); ok && v != nil {
			return v, true
		}
		if v, ok := conf.DefaultFlagMap[key]; ok && v != nil {
			return v, true
		}
		return nil, false
	})
	return &cp
}
