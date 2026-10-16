// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func archiveTestConf() *Config {
	return &Config{
		BackupArchiveMode: ConstBackupArchiveModeNone,
		DefaultFlagMap: map[string]interface{}{
			"backup-archive-mode":   ConstBackupArchiveModeNone,
			"backup-restic-timeout": 3600,
			"backup-restic-s3-mode": ConstResticS3ModeAuto,
		},
		ImmuableFlagMap:     map[string]interface{}{},
		DynamicFlagMap:      map[string]interface{}{},
		BackupResticTimeout: 3600,
		BackupResticS3Mode:  ConstResticS3ModeAuto,
	}
}

// #2077: every archive setting at its default and no archive: no list
func TestArchiveNoMigrationWhenDefault(t *testing.T) {
	conf := archiveTestConf()
	conf.MigrateArchives()
	if len(conf.Archives) != 0 {
		t.Fatalf("archives %+v, want none", conf.Archives)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeNone || conf.BackupRestic {
		t.Fatalf("mode %s restic %v", conf.BackupArchiveMode, conf.BackupRestic)
	}
}

// #2077: a cluster with an archive and no list gets archive 1 from its settings, and the
// runtime fields keep their values
func TestArchiveMigrationFromLegacy(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticAws
	conf.BackupResticAwsBucket = "bk"
	conf.BackupResticAwsEndpoint = "https://s3.example"
	conf.BackupResticTimeout = 7200
	conf.BackupResticPurgePrune = true
	conf.MigrateArchives()

	if len(conf.Archives) != 1 {
		t.Fatalf("archives %+v, want one", conf.Archives)
	}
	a := conf.Archives[0]
	if a.Name != "s3" || a.Plugin != ConstArchivePluginResticS3 || a.Disabled {
		t.Fatalf("archive 1 %+v", a)
	}
	want := map[string]interface{}{"aws-bucket": "bk", "aws-endpoint": "https://s3.example", "timeout": int64(7200), "purge-prune": true}
	for k, v := range want {
		if a.Settings[k] != v {
			t.Errorf("setting %s = %#v, want %#v", k, a.Settings[k], v)
		}
	}
	if _, ok := a.Settings["s3-mode"]; ok {
		t.Errorf("a default value is stored: %+v", a.Settings)
	}
	if !conf.BackupRestic || !conf.BackupResticAws || conf.BackupResticAwsBucket != "bk" || conf.BackupResticTimeout != 7200 {
		t.Fatalf("runtime fields changed: restic %v aws %v bucket %q timeout %d", conf.BackupRestic, conf.BackupResticAws, conf.BackupResticAwsBucket, conf.BackupResticTimeout)
	}
}

// #2077: archive mode none but customised settings: archive 1 is kept, disabled, so the
// settings survive the legacy keys leaving the saved config
func TestArchiveMigrationDisabledKeepsSettings(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupResticLocalRepository = "sftp:backup@dr:/srv/restic"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || !conf.Archives[0].Disabled || conf.Archives[0].Plugin != ConstArchivePluginResticSftp {
		t.Fatalf("archives %+v, want one disabled sftp archive", conf.Archives)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeNone || conf.BackupRestic {
		t.Fatalf("a disabled archive turned the runtime on: mode %s", conf.BackupArchiveMode)
	}
}

// #2077: dev3's saved config: mode none and a local repository path. The default
// backup-restic-repository is an s3: URL and must not make archive 1 an s3 archive.
func TestArchiveMigrationDisabledLocalNotS3(t *testing.T) {
	conf := archiveTestConf()
	conf.DefaultFlagMap["backup-restic-repository"] = "s3:https://s3.signal18.io/backups"
	conf.BackupResticRepository = "s3:https://s3.signal18.io/backups"
	conf.BackupResticLocalRepository = "/var/lib/replication-manager/backups/archive/dev3"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || !conf.Archives[0].Disabled || conf.Archives[0].Plugin != ConstArchivePluginResticLocal || conf.Archives[0].Name != "local" {
		t.Fatalf("archives %+v, want one disabled local archive", conf.Archives)
	}
	// a customised S3 setting and no local repository: s3
	conf = archiveTestConf()
	conf.DefaultFlagMap["backup-restic-repository"] = "s3:https://s3.signal18.io/backups"
	conf.BackupResticRepository = "s3:https://s3.example/bk"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || conf.Archives[0].Plugin != ConstArchivePluginResticS3 {
		t.Fatalf("archives %+v, want one disabled s3 archive", conf.Archives)
	}
}

// #2077: a list loaded from the saved config sets the runtime fields; a key pinned in a
// config file wins over it
func TestArchiveProjectionAndPin(t *testing.T) {
	conf := archiveTestConf()
	conf.ImmuableFlagMap["backup-restic-timeout"] = 600
	conf.BackupResticTimeout = 600
	conf.Archives = []ArchiveConfig{{Name: "local", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{
		"local-repository": " /data/restic ", "timeout": int64(9999), "purge-prune": true,
	}}}
	conf.MigrateArchives()
	if conf.BackupArchiveMode != ConstBackupArchiveModeResticLocal || !conf.BackupRestic || conf.BackupResticAws {
		t.Fatalf("mode %s restic %v aws %v", conf.BackupArchiveMode, conf.BackupRestic, conf.BackupResticAws)
	}
	if conf.BackupResticLocalRepository != "/data/restic" || !conf.BackupResticPurgePrune {
		t.Fatalf("repository %q prune %v", conf.BackupResticLocalRepository, conf.BackupResticPurgePrune)
	}
	if conf.BackupResticTimeout != 600 {
		t.Fatalf("pinned timeout overwritten by the list: %d", conf.BackupResticTimeout)
	}
	// the save never stores a pinned key in the list
	conf.SyncArchiveFromConfig()
	if _, ok := conf.Archives[0].Settings["timeout"]; ok {
		t.Fatalf("pinned key stored in the list: %+v", conf.Archives[0].Settings)
	}
}

// #2077: a setter changes a field; the save captures it into archive 1, a value back at
// its default leaves the list, and turning the archive off disables archive 1
func TestArchiveSyncFromSetters(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.MigrateArchives()
	conf.BackupResticTags = "nightly"
	conf.SyncArchiveFromConfig()
	if conf.Archives[0].Settings["tags"] != "nightly" {
		t.Fatalf("settings %+v", conf.Archives[0].Settings)
	}
	conf.BackupResticTags = ""
	conf.ApplyBackupArchiveMode(ConstBackupArchiveModeNone)
	conf.SyncArchiveFromConfig()
	if _, ok := conf.Archives[0].Settings["tags"]; ok || !conf.Archives[0].Disabled {
		t.Fatalf("archive 1 %+v", conf.Archives[0])
	}
	conf.ApplyBackupArchiveMode(ConstBackupArchiveModeResticAws)
	conf.SyncArchiveFromConfig()
	if conf.Archives[0].Disabled || conf.Archives[0].Plugin != ConstArchivePluginResticS3 {
		t.Fatalf("archive 1 %+v", conf.Archives[0])
	}
}

// #2077: the rendered tables read back through viper (the config loader) into the same list
func TestArchivesTomlRoundTrip(t *testing.T) {
	conf := archiveTestConf()
	conf.Archives = []ArchiveConfig{
		{Name: "local", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/restic", "purge-prune": true}},
		{Name: "s3-paris", Plugin: ConstArchivePluginResticS3, Disabled: true, Settings: map[string]interface{}{"aws-bucket": "bk", "timeout": int64(7200)}},
	}
	out, err := conf.ArchivesToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("[saved-c1]\ntitle = \"c1\"\n" + out)); err != nil {
		t.Fatalf("%s\n%s", err, out)
	}
	var back Config
	if err := v.Sub("saved-c1").Unmarshal(&back); err != nil {
		t.Fatal(err)
	}
	if len(back.Archives) != 2 {
		t.Fatalf("read back %+v from\n%s", back.Archives, out)
	}
	if back.Archives[0].Name != "local" || back.Archives[0].Settings["local-repository"] != "/data/restic" || back.Archives[0].Settings["purge-prune"] != true {
		t.Fatalf("archive 1 %+v", back.Archives[0])
	}
	if back.Archives[1].Name != "s3-paris" || !back.Archives[1].Disabled || back.Archives[1].Settings["aws-bucket"] != "bk" {
		t.Fatalf("archive 2 %+v", back.Archives[1])
	}
	if _, ok := back.Archives[1].Settings["name"]; ok {
		t.Fatalf("name kept as a setting: %+v", back.Archives[1].Settings)
	}
}

// #2077: a peer's event sets or unsets one archive setting and the runtime follows
func TestArchiveSetSettingFromEvent(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.MigrateArchives()
	if err := conf.SetArchiveSetting("local", "timeout", "1200", false); err != nil {
		t.Fatal(err)
	}
	if conf.BackupResticTimeout != 1200 || conf.Archives[0].Settings["timeout"] != int64(1200) {
		t.Fatalf("timeout %d settings %+v", conf.BackupResticTimeout, conf.Archives[0].Settings)
	}
	if err := conf.SetArchiveSetting("local", "timeout", "", true); err != nil {
		t.Fatal(err)
	}
	if conf.BackupResticTimeout != 3600 {
		t.Fatalf("unset left timeout %d, want the default", conf.BackupResticTimeout)
	}
	if err := conf.SetArchiveSetting("local", "nonsense", "1", false); err == nil {
		t.Fatal("unknown setting accepted")
	}
	if err := conf.SetArchiveSetting("local", "disabled", "true", false); err != nil || conf.BackupArchiveMode != ConstBackupArchiveModeNone {
		t.Fatalf("disabled: %v, mode %s", err, conf.BackupArchiveMode)
	}
}

// #2077: a Config copy never shares its list entries with the original
func TestArchiveMigrationClonesList(t *testing.T) {
	src := archiveTestConf()
	src.Archives = []ArchiveConfig{{Name: "local", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"tags": "a"}}}
	cp := *src
	cp.MigrateArchives()
	cp.Archives[0].Settings["tags"] = "b"
	if src.Archives[0].Settings["tags"] != "a" {
		t.Fatal("the copy's list shares its settings with the original")
	}
}
