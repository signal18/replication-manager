// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml"
)

// A cluster's archives (#2077, doc/implementation/cluster/BACKUP_ARCHIVE_PLUGINS.md):
// a TOML array of tables, [[<cluster>.archive]] in a config file, [[saved-<cluster>.archive]]
// in the saved <cluster>.toml, each entry named archive-1 to archive-x with its plugin.
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

// ArchiveConfig is one entry of the archive list. Settings holds the plugin's own
// settings; in this step they are the legacy backup-restic-* keys without their prefix.
type ArchiveConfig struct {
	Name     string                 `mapstructure:"name" toml:"name" json:"name"`
	Plugin   string                 `mapstructure:"plugin" toml:"plugin" json:"plugin"`
	Settings map[string]interface{} `mapstructure:",remain" toml:"-" json:"settings"`
}

// archiveLegacyKeys are the backup-restic-* keys archive 1 carries: an archive setting
// named as the key without "backup-restic-". Not here and still flat: the secrets
// (backup-restic-password, backup-restic-aws-access-secret) until the credential store
// chain, and what is not an archive's (binary path, reseed, metadata extractor, the
// server-scope mount dir, retention backup-keep-*, verification).
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
}

// archiveModeKeys are replaced by backup-archive-name and the connected archive's plugin.
var archiveModeKeys = []string{"backup-archive-mode", "backup-restic", "backup-restic-aws"}

const archiveLegacyPrefix = "backup-restic-"

// ArchiveSettingName is the archive setting a legacy key becomes.
func ArchiveSettingName(legacyKey string) string {
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

// nextArchiveName is the first free name of archive-1 to archive-x.
func (conf *Config) nextArchiveName() string {
	for n := 1; ; n++ {
		if name := fmt.Sprintf("archive-%d", n); conf.archiveByName(name) == nil {
			return name
		}
	}
}

func (conf *Config) addArchive(plugin string) *ArchiveConfig {
	conf.Archives = append(conf.Archives, ArchiveConfig{Name: conf.nextArchiveName(), Plugin: plugin, Settings: map[string]interface{}{}})
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

// cleanArchiveSettings drops what is not an archive setting from the entries read.
func (conf *Config) cleanArchiveSettings() {
	known := make(map[string]bool, len(archiveLegacyKeys))
	for _, key := range archiveLegacyKeys {
		known[ArchiveSettingName(key)] = true
	}
	for i := range conf.Archives {
		for k := range conf.Archives[i].Settings {
			if !known[k] {
				delete(conf.Archives[i].Settings, k)
			}
		}
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
// backup-archive-mode is not none, gets archive-1 built from them, connected when the
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
		if connect || conf.archiveSettingsCustomised() {
			plugin := ArchivePluginForMode(conf.BackupArchiveMode)
			if plugin == "" {
				plugin = conf.unconnectedArchivePlugin()
			}
			a := conf.addArchive(plugin)
			conf.captureArchive(a)
			if connect && !conf.isPinned("backup-archive-name") {
				conf.BackupArchiveName = a.Name
			}
		}
	}
	conf.projectArchive()
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

// unconnectedArchivePlugin is the plugin of an archive-1 built with the mode none, from the
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
// back to its default, so a switch never keeps the previous archive's value. No archive
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
	if a != nil {
		for _, key := range archiveLegacyKeys {
			if conf.isPinned(key) {
				continue
			}
			f, ok := configField(conf, key)
			if !ok {
				continue
			}
			if v, ok := a.Settings[ArchiveSettingName(key)]; ok {
				setField(f, v)
			} else if def, ok := conf.DefaultFlagMap[key]; ok {
				setField(f, def)
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
		if !ok || conf.isPinned(key) || conf.isDefaultValue(key, f) {
			delete(a.Settings, name)
			continue
		}
		a.Settings[name] = fieldValue(f)
	}
}

// SyncArchiveFromConfig captures the BackupRestic* fields into the connected archive before
// a save: a setter (API, GUI, MCP) changed the field the runtime reads. No archive
// connected: the fields belong to no archive, except with an empty list, where customised
// settings become an archive-1 not connected (nothing set at runtime is lost).
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
	if !conf.isPinned("backup-archive-mode") {
		if plugin := ArchivePluginForMode(conf.BackupArchiveMode); plugin != "" {
			a.Plugin = plugin
		}
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
// cluster; another mode changes the connected archive's plugin, or connects the first
// archive with that plugin, or a new archive-x (holding the fields when the list was
// empty, so settings set before the mode are kept).
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
	default:
		key := archiveLegacyPrefix + setting
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
