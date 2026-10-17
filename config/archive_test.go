// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"fmt"
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
	if len(conf.Archives) != 0 || conf.BackupArchiveName != "" {
		t.Fatalf("archives %+v connected %q, want none", conf.Archives, conf.BackupArchiveName)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeNone || conf.BackupRestic {
		t.Fatalf("mode %s restic %v", conf.BackupArchiveMode, conf.BackupRestic)
	}
}

// #2077: a cluster with an archive and no list gets archive-local-1 from its settings, connected,
// and the runtime fields keep their values
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
	if a.Name != "archive-s3-1" || a.Plugin != ConstArchivePluginResticS3 || conf.BackupArchiveName != "archive-s3-1" {
		t.Fatalf("archive %+v connected %q", a, conf.BackupArchiveName)
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

// #2077: archive mode none but customised settings: archive-local-1 is kept, not connected, so
// the settings survive the legacy keys leaving the saved config
func TestArchiveMigrationNotConnectedKeepsSettings(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupResticLocalRepository = "sftp:backup@dr:/srv/restic"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || conf.Archives[0].Name != "archive-sftp-1" || conf.Archives[0].Plugin != ConstArchivePluginResticSftp || conf.BackupArchiveName != "" {
		t.Fatalf("archives %+v connected %q, want one sftp archive not connected", conf.Archives, conf.BackupArchiveName)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeNone || conf.BackupRestic {
		t.Fatalf("an archive not connected turned the runtime on: mode %s", conf.BackupArchiveMode)
	}
}

// #2077: dev3's saved config: mode none and a local repository path. The default
// backup-restic-repository is an s3: URL and must not make archive-local-1 an s3 archive.
func TestArchiveMigrationNotConnectedLocalNotS3(t *testing.T) {
	conf := archiveTestConf()
	conf.DefaultFlagMap["backup-restic-repository"] = "s3:https://s3.signal18.io/backups"
	conf.BackupResticRepository = "s3:https://s3.signal18.io/backups"
	conf.BackupResticLocalRepository = "/var/lib/replication-manager/backups/archive/dev3"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || conf.Archives[0].Plugin != ConstArchivePluginResticLocal || conf.BackupArchiveName != "" {
		t.Fatalf("archives %+v, want one local archive not connected", conf.Archives)
	}
	// a customised S3 setting and no local repository: s3
	conf = archiveTestConf()
	conf.DefaultFlagMap["backup-restic-repository"] = "s3:https://s3.signal18.io/backups"
	conf.BackupResticRepository = "s3:https://s3.example/bk"
	conf.MigrateArchives()
	if len(conf.Archives) != 1 || conf.Archives[0].Plugin != ConstArchivePluginResticS3 {
		t.Fatalf("archives %+v, want one s3 archive", conf.Archives)
	}
}

// #2077: the connected archive sets the runtime fields; a key pinned in a config file wins
// over it; an entry read with an unknown setting loses it
func TestArchiveProjectionAndPin(t *testing.T) {
	conf := archiveTestConf()
	conf.ImmuableFlagMap["backup-restic-timeout"] = 600
	conf.BackupResticTimeout = 600
	conf.BackupArchiveName = "archive-local-1"
	conf.Archives = []ArchiveConfig{{Name: "archive-local-1", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{
		"local-repository": " /data/restic ", "timeout": int64(9999), "purge-prune": true, "disabled": true,
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
	if _, ok := conf.Archives[0].Settings["disabled"]; ok {
		t.Fatalf("unknown setting kept: %+v", conf.Archives[0].Settings)
	}
	// the save never stores a pinned key in the list
	conf.SyncArchiveFromConfig()
	if _, ok := conf.Archives[0].Settings["timeout"]; ok {
		t.Fatalf("pinned key stored in the list: %+v", conf.Archives[0].Settings)
	}
}

// #2077: a setter changes a field; the save captures it into the connected archive, a
// value back at its default leaves the list; the mode setter disconnects, connects a new
// archive of another type, then changes the connected archive's plugin
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
	if err := conf.ApplyArchiveMode(ConstBackupArchiveModeNone); err != nil {
		t.Fatal(err)
	}
	if _, ok := conf.Archives[0].Settings["tags"]; ok || conf.BackupArchiveName != "" || conf.BackupRestic {
		t.Fatalf("archive %+v connected %q", conf.Archives[0], conf.BackupArchiveName)
	}
	if err := conf.ApplyArchiveMode(ConstBackupArchiveModeResticAws); err != nil {
		t.Fatal(err)
	}
	if len(conf.Archives) != 2 || conf.BackupArchiveName != "archive-s3-1" || conf.Archives[1].Plugin != ConstArchivePluginResticS3 || !conf.BackupResticAws {
		t.Fatalf("archives %+v connected %q, want a new archive-s3-1", conf.Archives, conf.BackupArchiveName)
	}
	if err := conf.ApplyArchiveMode(ConstBackupArchiveModeResticLocal); err != nil {
		t.Fatal(err)
	}
	if len(conf.Archives) != 2 || conf.BackupArchiveName != "archive-s3-1" || conf.Archives[1].Plugin != ConstArchivePluginResticLocal || conf.BackupResticAws {
		t.Fatalf("archives %+v connected %q, want archive-s3-1 changed to local", conf.Archives, conf.BackupArchiveName)
	}
}

// #2077: settings set while no archive exists are kept by the archive the mode setter
// creates (the GUI sets the repository, then the mode)
func TestArchiveModeKeepsSettingsSetBefore(t *testing.T) {
	conf := archiveTestConf()
	conf.MigrateArchives()
	conf.BackupResticLocalRepository = "/data/restic"
	if err := conf.ApplyArchiveMode(ConstBackupArchiveModeResticLocal); err != nil {
		t.Fatal(err)
	}
	if conf.BackupArchiveName != "archive-local-1" || conf.Archives[0].Settings["local-repository"] != "/data/restic" || conf.BackupResticLocalRepository != "/data/restic" {
		t.Fatalf("archives %+v connected %q repository %q", conf.Archives, conf.BackupArchiveName, conf.BackupResticLocalRepository)
	}
}

// #2077: switching backup-archive-name keeps the archive left as it was run and runs the
// new one with its own settings only: nothing of the previous archive leaks
func TestArchiveSwitch(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveName = "archive-local-1"
	conf.Archives = []ArchiveConfig{
		{Name: "archive-local-1", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/restic"}},
		{Name: "archive-s3-1", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{"aws-bucket": "migration"}},
	}
	conf.MigrateArchives()
	conf.BackupResticTimeout = 1800 // set at runtime on archive-local-1
	if err := conf.SwitchArchive("archive-s3-1"); err != nil {
		t.Fatal(err)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeResticAws || conf.BackupResticAwsBucket != "migration" {
		t.Fatalf("mode %s bucket %q", conf.BackupArchiveMode, conf.BackupResticAwsBucket)
	}
	if conf.BackupResticTimeout != 3600 || conf.BackupResticLocalRepository != "" {
		t.Fatalf("archive-local-1 leaked: timeout %d repository %q", conf.BackupResticTimeout, conf.BackupResticLocalRepository)
	}
	if conf.Archives[0].Settings["timeout"] != int64(1800) {
		t.Fatalf("archive-local-1 lost its runtime change: %+v", conf.Archives[0].Settings)
	}
	if err := conf.SwitchArchive("archive-9"); err == nil {
		t.Fatal("unknown archive accepted")
	}
	if err := conf.SwitchArchive(""); err != nil || conf.BackupArchiveMode != ConstBackupArchiveModeNone || conf.BackupRestic {
		t.Fatalf("disconnect: %v mode %s", err, conf.BackupArchiveMode)
	}
}

// #2077: the rendered tables read back through viper (the config loader) into the same list
func TestArchivesTomlRoundTrip(t *testing.T) {
	conf := archiveTestConf()
	conf.Archives = []ArchiveConfig{
		{Name: "archive-local-1", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/restic", "purge-prune": true}},
		{Name: "archive-s3-1", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{"aws-bucket": "bk", "timeout": int64(7200)}},
	}
	out, err := conf.ArchivesToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("[saved-c1]\ntitle = \"c1\"\nbackup-archive-name = \"archive-s3-1\"\n" + out)); err != nil {
		t.Fatalf("%s\n%s", err, out)
	}
	var back Config
	if err := v.Sub("saved-c1").Unmarshal(&back); err != nil {
		t.Fatal(err)
	}
	if len(back.Archives) != 2 || back.BackupArchiveName != "archive-s3-1" {
		t.Fatalf("read back %+v connected %q from\n%s", back.Archives, back.BackupArchiveName, out)
	}
	if back.Archives[0].Name != "archive-local-1" || back.Archives[0].Settings["local-repository"] != "/data/restic" || back.Archives[0].Settings["purge-prune"] != true {
		t.Fatalf("archive-local-1 %+v", back.Archives[0])
	}
	if back.Archives[1].Name != "archive-s3-1" || back.Archives[1].Settings["aws-bucket"] != "bk" {
		t.Fatalf("archive-s3-1 %+v", back.Archives[1])
	}
	if _, ok := back.Archives[1].Settings["name"]; ok {
		t.Fatalf("name kept as a setting: %+v", back.Archives[1].Settings)
	}
}

// #2077: a peer's event sets or unsets one setting of the connected archive and the runtime
// follows; a setting of another archive leaves the runtime alone
func TestArchiveSetSettingFromEvent(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.MigrateArchives()
	if err := conf.SetArchiveSetting("archive-local-1", "timeout", "1200", false); err != nil {
		t.Fatal(err)
	}
	if conf.BackupResticTimeout != 1200 || conf.Archives[0].Settings["timeout"] != int64(1200) {
		t.Fatalf("timeout %d settings %+v", conf.BackupResticTimeout, conf.Archives[0].Settings)
	}
	if err := conf.SetArchiveSetting("archive-s3-1", "timeout", "60", false); err != nil {
		t.Fatal(err)
	}
	if conf.BackupResticTimeout != 1200 {
		t.Fatalf("another archive's setting changed the runtime: %d", conf.BackupResticTimeout)
	}
	if err := conf.SetArchiveSetting("archive-local-1", "timeout", "", true); err != nil {
		t.Fatal(err)
	}
	if conf.BackupResticTimeout != 3600 {
		t.Fatalf("unset left timeout %d, want the default", conf.BackupResticTimeout)
	}
	if err := conf.SetArchiveSetting("archive-local-1", "nonsense", "1", false); err == nil {
		t.Fatal("unknown setting accepted")
	}
}

// #2077: a Config copy never shares its list entries with the original
func TestArchiveMigrationClonesList(t *testing.T) {
	src := archiveTestConf()
	src.Archives = []ArchiveConfig{{Name: "archive-local-1", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"tags": "a"}}}
	cp := *src
	cp.MigrateArchives()
	cp.Archives[0].Settings["tags"] = "b"
	if src.Archives[0].Settings["tags"] != "a" {
		t.Fatal("the copy's list shares its settings with the original")
	}
}

// #2088: each plugin comes with its typed config: settings typed from the fields they
// replace, an enum with its values, secrets as type secret
func TestArchivePluginManifests(t *testing.T) {
	s3, ok := ArchivePlugin(ConstArchivePluginResticS3)
	if !ok {
		t.Fatal("no s3 manifest")
	}
	cases := map[string]string{"aws-bucket": ConstArchiveSettingTypeString, "timeout": ConstArchiveSettingTypeInt, "purge-prune": ConstArchiveSettingTypeBool, "s3-mode": ConstArchiveSettingTypeEnum, "password": ConstArchiveSettingTypeSecret, "aws-access-secret": ConstArchiveSettingTypeSecret}
	for name, typ := range cases {
		if st, ok := s3.Setting(name); !ok || st.Type != typ {
			t.Errorf("%s: %+v, want type %s", name, st, typ)
		}
	}
	if st, _ := s3.Setting("s3-mode"); st.Validate("new") != nil || st.Validate("bogus") == nil {
		t.Errorf("s3-mode validation")
	}
	local, _ := ArchivePlugin(ConstArchivePluginResticLocal)
	if local.HasSetting("aws-bucket") || local.HasSecret("aws-access-secret") || !local.HasSecret("password") || !local.Can(ConstArchiveCapProbe) {
		t.Errorf("local manifest %+v", local)
	}
	if len(ArchivePlugins()) != 3 {
		t.Errorf("plugins %+v", ArchivePlugins())
	}
}

// #2088: the migration moves the restic password and S3 secret set at runtime into
// archive-local-1, encrypted, out of the flat config; a pinned one stays in its file
func TestArchiveMigrationMovesSecrets(t *testing.T) {
	conf := archiveTestConf()
	conf.SecretKey = []byte("0123456789abcdef")
	conf.DefaultFlagMap["backup-restic-password"] = "secret"
	conf.BackupArchiveMode = ConstBackupArchiveModeResticAws
	conf.DynamicFlagMap["backup-restic-password"] = conf.EncryptSecretValue("repo-pass")
	conf.DynamicFlagMap["backup-restic-aws-access-secret"] = "plain-s3"
	conf.MigrateArchives()
	a := conf.Archives[0]
	for _, name := range []string{"password", "aws-access-secret"} {
		raw := fmt.Sprintf("%v", a.Settings[name])
		if !strings.Contains(raw, "hash_") {
			t.Errorf("%s not moved encrypted: %q", name, raw)
		}
	}
	if conf.DecryptSecretValue("backup-restic-aws-access-secret", fmt.Sprintf("%v", a.Settings["aws-access-secret"])) != "plain-s3" {
		t.Error("aws secret does not decrypt back")
	}
	if _, ok := conf.DynamicFlagMap["backup-restic-password"]; ok {
		t.Error("the flat password stayed")
	}

	pinned := archiveTestConf()
	pinned.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	pinned.ImmuableFlagMap["backup-restic-password"] = "file"
	pinned.DynamicFlagMap["backup-restic-password"] = "dyn"
	pinned.MigrateArchives()
	if _, ok := pinned.Archives[0].Settings["password"]; ok {
		t.Error("a pinned password moved into the list")
	}
}

// #2088: the API never shows a secret, only that it is set
func TestArchiveJSONHidesSecrets(t *testing.T) {
	a := ArchiveConfig{Name: "archive-s3-1", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{"aws-bucket": "bk", "password": "hash_x", "aws-access-secret": "hash_y"}}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, "hash_") || !strings.Contains(out, `"secretsSet":["aws-access-secret","password"]`) || !strings.Contains(out, `"aws-bucket":"bk"`) {
		t.Fatalf("json %s", out)
	}
}

// #2088: a mode change to another plugin changes the connected archive's plugin and drops
// what the new plugin does not declare
func TestArchiveModeChangeCleansEntry(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticAws
	conf.BackupResticAwsBucket = "bk"
	conf.MigrateArchives()
	conf.Archives[0].Settings["aws-access-secret"] = "hash_y"
	if err := conf.ApplyArchiveMode(ConstBackupArchiveModeResticLocal); err != nil {
		t.Fatal(err)
	}
	if len(conf.Archives) != 1 || conf.BackupArchiveName != "archive-s3-1" || conf.Archives[0].Plugin != ConstArchivePluginResticLocal {
		t.Fatalf("archives %+v connected %q", conf.Archives, conf.BackupArchiveName)
	}
	if _, ok := conf.Archives[0].Settings["aws-bucket"]; ok {
		t.Errorf("s3 setting kept on a local archive: %+v", conf.Archives[0].Settings)
	}
	if _, ok := conf.Archives[0].Settings["aws-access-secret"]; ok {
		t.Errorf("s3 secret kept on a local archive")
	}
}

// #2077: a generated name says the type and counts per type
func TestArchiveNames(t *testing.T) {
	conf := archiveTestConf()
	for _, p := range []string{ConstArchivePluginResticS3, ConstArchivePluginResticLocal, ConstArchivePluginResticS3, ConstArchivePluginResticSftp} {
		conf.addArchive(p)
	}
	got := []string{}
	for _, a := range conf.Archives {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != "archive-s3-1,archive-local-1,archive-s3-2,archive-sftp-1" {
		t.Fatalf("names %v", got)
	}
}

// #2091: an archive built from a spec is checked against its plugin's typed config: the
// values are stored typed, a secret encrypted, an unknown setting or a bad value refused,
// a name kept to the rule and generated when missing
func TestArchiveBuildFromSpec(t *testing.T) {
	conf := archiveTestConf()
	conf.SecretKey = []byte("0123456789abcdef")
	a, isNew, err := conf.BuildArchive(ArchiveSpec{Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{
		"aws-bucket": "backups", "timeout": "120", "purge-prune": "true", "s3-mode": "new", "aws-access-secret": "s3-secret",
	}}, true, false)
	if err != nil || !isNew {
		t.Fatalf("build: %v new %v", err, isNew)
	}
	if a.Name != "archive-s3-1" || a.Settings["timeout"] != int64(120) || a.Settings["purge-prune"] != true || a.Settings["aws-bucket"] != "backups" {
		t.Fatalf("archive %+v", a)
	}
	if raw := fmt.Sprintf("%v", a.Settings["aws-access-secret"]); !strings.HasPrefix(raw, "hash_") {
		t.Fatalf("secret stored in clear: %q", raw)
	}
	for _, bad := range []ArchiveSpec{
		{Name: "Archive BSO", Plugin: ConstArchivePluginResticS3},
		{Name: "x", Plugin: "archive-nope"},
		{Name: "x", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"aws-bucket": "b"}},
		{Name: "x", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{"s3-mode": "sideways"}},
		{Name: "x", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{"timeout": "soon"}},
	} {
		if _, _, err := conf.BuildArchive(bad, true, false); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	conf.StoreArchive(a)
	if _, _, err := conf.BuildArchive(ArchiveSpec{Name: "archive-s3-1", Plugin: ConstArchivePluginResticS3}, true, false); err == nil {
		t.Error("a second archive-s3-1 accepted")
	}
	if _, _, err := conf.BuildArchive(ArchiveSpec{Name: "archive-bso-s3-uk1", Settings: map[string]interface{}{"aws-bucket": "b"}}, false, false); err == nil {
		t.Error("edit of an unknown archive accepted")
	}
	// edit: an empty secret keeps it, unset clears a setting
	b, _, err := conf.BuildArchive(ArchiveSpec{Name: "archive-s3-1", Settings: map[string]interface{}{"aws-access-secret": "", "aws-prefix": "c1"}, Unset: []string{"timeout"}}, false, false)
	if err != nil || b.Settings["aws-access-secret"] != a.Settings["aws-access-secret"] || b.Settings["aws-prefix"] != "c1" {
		t.Fatalf("edit %v %+v", err, b.Settings)
	}
	if _, ok := b.Settings["timeout"]; ok {
		t.Fatal("unset kept timeout")
	}
}

// #2091: rename follows the attached archive; delete detaches it
func TestArchiveRenameAndDelete(t *testing.T) {
	conf := archiveTestConf()
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.MigrateArchives()
	if err := conf.RenameArchive("archive-local-1", "archive-bso-s3-uk1"); err != nil {
		t.Fatal(err)
	}
	if conf.BackupArchiveName != "archive-bso-s3-uk1" || !conf.ArchiveExists("archive-bso-s3-uk1") {
		t.Fatalf("attached %q", conf.BackupArchiveName)
	}
	if err := conf.RenameArchive("archive-bso-s3-uk1", "Bad Name"); err == nil {
		t.Fatal("bad name accepted")
	}
	conf.DeleteArchiveEntry("archive-bso-s3-uk1")
	if len(conf.Archives) != 0 || conf.BackupArchiveName != "" || conf.BackupRestic {
		t.Fatalf("after delete: %+v attached %q restic %v", conf.Archives, conf.BackupArchiveName, conf.BackupRestic)
	}
}

// #2091: the copy for an archive that is not attached runs that archive, with its own
// secrets, and leaves the cluster's config as it was
func TestArchiveConfigForArchive(t *testing.T) {
	conf := archiveTestConf()
	conf.SecretKey = []byte("0123456789abcdef")
	conf.DefaultFlagMap["backup-restic-password"] = "secret"
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.BackupResticLocalRepository = "/data/restic"
	conf.MigrateArchives()
	conf.RebuildSecrets(func(k string) (interface{}, bool) { v, ok := conf.DefaultFlagMap[k]; return v, ok })
	other := ArchiveConfig{Name: "archive-bso-s3-uk1", Plugin: ConstArchivePluginResticS3, Settings: map[string]interface{}{
		"aws-bucket": "uk1", "password": conf.EncryptSecretValue("uk1-pass"),
	}}
	view := conf.ConfigForArchive(other)
	if !view.BackupResticAws || view.BackupResticAwsBucket != "uk1" || view.BackupResticLocalRepository != "" {
		t.Fatalf("view aws %v bucket %q repo %q", view.BackupResticAws, view.BackupResticAwsBucket, view.BackupResticLocalRepository)
	}
	if p := view.GetDecryptedValue("backup-restic-password"); p != "uk1-pass" {
		t.Fatalf("view password %q", p)
	}
	if conf.BackupResticAws || conf.BackupResticLocalRepository != "/data/restic" || conf.GetDecryptedValue("backup-restic-password") != "secret" || len(conf.Archives) != 1 {
		t.Fatalf("the cluster's config changed: aws %v repo %q archives %d", conf.BackupResticAws, conf.BackupResticLocalRepository, len(conf.Archives))
	}
}

// #2091: the retention is each archive's own. A cluster with no list moves its retention
// into archive-local-1; a list saved before keeps the flat value, adopted by the attached
// archive; a switch carries each archive's value; nothing attached goes back to the default.
func TestArchiveRetentionPerArchive(t *testing.T) {
	conf := archiveTestConf()
	conf.DefaultFlagMap["backup-keep-last"] = 10
	conf.BackupArchiveMode = ConstBackupArchiveModeResticLocal
	conf.BackupResticLocalRepository = "/data/restic"
	conf.BackupKeepLast = 3
	conf.BackupArchiveRemoveOriginal = true
	conf.MigrateArchives()
	a := conf.Archives[0]
	if a.Settings["keep-last"] != int64(3) || a.Settings["remove-original"] != true {
		t.Fatalf("archive settings %+v, want keep-last 3 and remove-original", a.Settings)
	}
	if !IsArchiveLegacyKey("backup-keep-last") || !IsArchiveLegacyKey("backup-archive-remove-original") {
		t.Fatal("the save must leave the flat retention keys out")
	}

	// a list saved before the retention moved: the flat value is adopted
	old := archiveTestConf()
	old.DefaultFlagMap["backup-keep-last"] = 10
	old.Archives = []ArchiveConfig{{Name: "archive-local-1", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/restic"}}}
	old.BackupArchiveName = "archive-local-1"
	old.BackupKeepLast = 4
	old.MigrateArchives()
	if old.Archives[0].Settings["keep-last"] != int64(4) || old.BackupKeepLast != 4 {
		t.Fatalf("adopted %+v field %d, want 4", old.Archives[0].Settings, old.BackupKeepLast)
	}

	// each archive keeps its own, nothing attached is the default
	old.Archives = append(old.Archives, ArchiveConfig{Name: "archive-local-2", Plugin: ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/r2", "keep-last": int64(7)}})
	if err := old.SwitchArchive("archive-local-2"); err != nil || old.BackupKeepLast != 7 {
		t.Fatalf("switch: %v keep-last %d, want 7", err, old.BackupKeepLast)
	}
	if err := old.SwitchArchive(""); err != nil || old.BackupKeepLast != 10 {
		t.Fatalf("detach: %v keep-last %d, want the default 10", err, old.BackupKeepLast)
	}
	if old.Archives[0].Settings["keep-last"] != int64(4) {
		t.Fatalf("archive-local-1 lost its keep-last: %+v", old.Archives[0].Settings)
	}

	// the manifest groups and checks them
	m, _ := ArchivePlugin(ConstArchivePluginResticS3)
	st, ok := m.Setting("keep-within")
	if !ok || st.Group != ConstArchiveSettingGroupRetention || st.Validate("2y5m") != nil || st.Validate("two years") == nil {
		t.Fatalf("keep-within %+v", st)
	}
	if st, _ := m.Setting("keep-last"); st.Type != ConstArchiveSettingTypeInt {
		t.Fatalf("keep-last %+v, want an int", st)
	}
	if st, _ := m.Setting("remove-original"); st.Type != ConstArchiveSettingTypeBool {
		t.Fatalf("remove-original %+v, want a bool", st)
	}
	if st, _ := m.Setting("mount-host"); st.Group != ConstArchiveSettingGroupAdvanced {
		t.Fatalf("mount-host %+v, want advanced", st)
	}
}
