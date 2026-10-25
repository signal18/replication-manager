// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/spf13/viper"
)

// newS3ConfigTestCluster: a cluster saving its config (ConfRewrite) with an encryption key.
func newS3ConfigTestCluster(t *testing.T) *Cluster {
	t.Helper()
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{}
	cl.Conf.ConfRewrite = true
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.DefaultFlagMap = map[string]interface{}{}
	cl.Conf.DynamicFlagMap = map[string]interface{}{}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.WorkingDir = filepath.Join(cl.Conf.WorkingDir, cl.Name)
	if err := os.MkdirAll(cl.WorkingDir, 0700); err != nil {
		t.Fatal(err)
	}
	return cl
}

func s3TestProviders() []config.S3Provider {
	return []config.S3Provider{
		{Name: "bso-s3", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.bso.example", Region: "fr-par", AccessKey: "AKID", SecretKey: "s3cr3t"},
		{Name: "cloud18", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://minio.example", Region: "eu-west-1", Cloud18SSO: true},
	}
}

// reloadS3Providers reads the saved file the way the server loads it at start: viper, the
// saved-<cluster> section, Unmarshal into the cluster's config.
func reloadS3Providers(t *testing.T, cl *Cluster) []config.S3Provider {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(filepath.Join(cl.WorkingDir, cl.Name+".toml"))
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	sub := v.Sub("saved-" + cl.Name)
	if sub == nil {
		t.Fatal("no saved section")
	}
	var conf config.Config
	if err := sub.Unmarshal(&conf); err != nil {
		t.Fatal(err)
	}
	re := &Cluster{Name: cl.Name, Conf: cl.Conf, WorkingDir: cl.WorkingDir}
	saved := cl.Conf.S3Providers
	cl.Conf.S3Providers = conf.S3Providers
	defer func() { cl.Conf.S3Providers = saved }()
	if err := re.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	return re.GetS3ProvidersSnapshot()
}

// #2155: s3providers.json is read once, the config save writes the providers as
// [[saved-<cluster>.s3-provider]] blocks, keys encrypted, and marks the config as their store
// (the file stays for older releases); the server's
// load path reads them back; a second save writes identical bytes.
func TestS3ProvidersMigrateToConfig(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	var disk []s3ProviderOnDisk
	for _, p := range s3TestProviders() {
		disk = append(disk, toOnDisk(cl.encryptS3Provider(p)))
	}
	data, _ := json.Marshal(disk)
	jsonPath := cl.s3ProvidersFilePath()
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	cl.s3Providers.cipher = nil // a restart: no ciphertext remembered

	if err := cl.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	if !cl.s3Providers.fromFile || len(cl.GetS3ProvidersSnapshot()) != 2 {
		t.Fatalf("loaded from file: %v %+v", cl.s3Providers.fromFile, cl.GetS3ProvidersSnapshot())
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(cl.WorkingDir, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if strings.Count(text, "[[saved-"+cl.Name+".s3-provider]]") != 2 {
		t.Fatalf("blocks not saved:\n%s", text)
	}
	if strings.Contains(text, "AKID") || strings.Contains(text, "s3cr3t") {
		t.Fatalf("keys saved in clear:\n%s", text)
	}
	if kept, err := os.ReadFile(jsonPath); err != nil || string(kept) != string(data) {
		t.Fatalf("s3providers.json must stay as it was for older releases: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cl.WorkingDir, s3ProvidersInConfigMark)); err != nil {
		t.Fatalf("%s missing", s3ProvidersInConfigMark)
	}

	got := reloadS3Providers(t, cl)
	want := s3TestProviders()
	if len(got) != len(want) {
		t.Fatalf("reloaded %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reloaded %+v, want %+v", got[i], want[i])
		}
	}

	if changed, err := cl.SaveConfigFile(); err != nil || changed {
		t.Fatalf("a second save must write the same bytes: changed=%v err=%v", changed, err)
	}
}

// #2155: without the dynamic config the JSON file stays the providers' store
func TestS3ProvidersFileWithoutDynamicConfig(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	cl.Conf.ConfRewrite = false
	if err := cl.AddS3Provider(s3TestProviders()[0]); err != nil {
		t.Fatal(err)
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cl.s3ProvidersFilePath()); err != nil {
		t.Fatalf("s3providers.json not written: %s", err)
	}
}

// #2155: a peer's events create a provider once its keys validate, change one key, and
// remove it when its name is unset
func TestApplyS3ProviderSetting(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	secret := cl.Conf.GetEncryptedString("s3cr3t")
	for _, ev := range [][2]string{{"access-key", cl.Conf.GetEncryptedString("AKID")}, {"endpoint", "https://s3.bso.example"}, {"name", "bso-s3"}, {"provider-source", "custom"}, {"region", "fr-par"}, {"secret-key", secret}} {
		if err := cl.ApplyS3ProviderSetting("bso-s3", ev[0], ev[1], false); err != nil {
			t.Fatalf("%s: %s", ev[0], err)
		}
	}
	ps := cl.GetS3ProvidersSnapshot()
	if len(ps) != 1 || ps[0] != s3TestProviders()[0] {
		t.Fatalf("providers %+v", ps)
	}
	if c := cl.encryptS3Provider(ps[0]).SecretKey; c != secret {
		t.Fatalf("the peer's ciphertext is not reused: %s", c)
	}
	if err := cl.ApplyS3ProviderSetting("bso-s3", "region", "fr-mrs", false); err != nil || cl.GetS3ProvidersSnapshot()[0].Region != "fr-mrs" {
		t.Fatalf("update: %v %+v", err, cl.GetS3ProvidersSnapshot())
	}
	if err := cl.ApplyS3ProviderSetting("bso-s3", "name", "", true); err != nil || len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Fatalf("remove: %v %+v", err, cl.GetS3ProvidersSnapshot())
	}
	if err := cl.ApplyS3ProviderSetting("bso-s3", "region", "", true); err != nil || len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Fatalf("an unset after the removal must be a no-op: %v", err)
	}
}

// #2155 review: a list set by a config file (pinned) is read from its blocks, never written,
// and a peer's event does not change it
func TestS3ProvidersPinned(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	pinned := config.S3ProviderSettings(s3TestProviders()[1])
	cl.Conf.S3Providers = config.S3ProviderBlocks{pinned}
	cl.Conf.ImmuableFlagMap["s3-provider"] = []interface{}{pinned}
	jsonPath := cl.s3ProvidersFilePath()
	if err := os.WriteFile(jsonPath, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	if ps := cl.GetS3ProvidersSnapshot(); len(ps) != 1 || ps[0] != s3TestProviders()[1] {
		t.Fatalf("pinned blocks not read: %+v", ps)
	}
	if err := cl.AddS3Provider(s3TestProviders()[0]); err != nil {
		t.Fatal(err)
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(jsonPath); string(data) != "[]" {
		t.Fatalf("a pinned list must not be written: %s", data)
	}
	if err := cl.ApplyS3ProviderSetting("cloud18", "region", "fr-par", false); err == nil {
		t.Fatal("a peer event on a pinned list must be refused")
	}
}

// #2155 review: without an encryption key the keys never reach the cluster config (pushed to
// the config repository): s3providers.json stays the store, the render refuses a clear key
func TestS3ProvidersWithoutEncryptionKey(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	cl.Conf.SecretKey = nil
	cl.Conf.MonitoringKeyPath = filepath.Join(t.TempDir(), "no.key")
	if cl.s3ProvidersInConfig() {
		t.Fatal("without an encryption key the providers must stay in s3providers.json")
	}
	if err := cl.AddS3Provider(s3TestProviders()[0]); err != nil {
		t.Fatal(err)
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cl.s3ProvidersFilePath()); err != nil {
		t.Fatalf("s3providers.json not written: %s", err)
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(filepath.Join(cl.WorkingDir, cl.Name+".toml"))
	if strings.Contains(string(saved), "s3-provider") || strings.Contains(string(saved), "s3cr3t") {
		t.Fatalf("providers saved in the cluster config without a key:\n%s", saved)
	}
	if _, err := os.Stat(cl.s3ProvidersFilePath()); err != nil {
		t.Fatal("s3providers.json retired without a key")
	}
	if _, err := cl.S3ProvidersToml("saved-" + cl.Name); err == nil {
		t.Fatal("a key in clear must not render")
	}
}

// #2155 review: a peer's events move an existing provider from the SSO to keys and back,
// one key at a time (sorted: access-key, cloud18-sso, secret-key), through invalid steps
func TestApplyS3ProviderSettingSSOAndKeys(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	sso := s3TestProviders()[1]
	if err := cl.AddS3Provider(sso); err != nil {
		t.Fatal(err)
	}
	withKeys := sso
	withKeys.Cloud18SSO, withKeys.AccessKey, withKeys.SecretKey = false, "AKID", "s3cr3t"
	for _, ev := range []struct {
		key, value string
		unset      bool
	}{{"access-key", cl.Conf.GetEncryptedString("AKID"), false}, {"cloud18-sso", "", true}, {"secret-key", cl.Conf.GetEncryptedString("s3cr3t"), false}} {
		if err := cl.ApplyS3ProviderSetting("cloud18", ev.key, ev.value, ev.unset); err != nil {
			t.Fatalf("SSO to keys, %s: %s", ev.key, err)
		}
	}
	if ps := cl.GetS3ProvidersSnapshot(); len(ps) != 1 || ps[0] != withKeys {
		t.Fatalf("SSO to keys: %+v", ps)
	}
	for _, ev := range []struct {
		key, value string
		unset      bool
	}{{"access-key", "", true}, {"cloud18-sso", "true", false}, {"secret-key", "", true}} {
		if err := cl.ApplyS3ProviderSetting("cloud18", ev.key, ev.value, ev.unset); err != nil {
			t.Fatalf("keys to SSO, %s: %s", ev.key, err)
		}
	}
	if ps := cl.GetS3ProvidersSnapshot(); len(ps) != 1 || ps[0] != sso {
		t.Fatalf("keys to SSO: %+v", ps)
	}
	if len(cl.s3Providers.pending) != 0 {
		t.Fatalf("pending left: %+v", cl.s3Providers.pending)
	}
}

// #2155 review: a save renames s3providers.json only when the list was read from it
func TestS3ProvidersFileKeptWhenNotReadFromIt(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	cl.Conf.S3Providers = config.S3ProviderBlocks{config.S3ProviderSettings(cl.encryptS3Provider(s3TestProviders()[0]))}
	jsonPath := cl.s3ProvidersFilePath()
	if err := os.WriteFile(jsonPath, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatal("s3providers.json renamed although the list came from the config")
	}
}

// #2155 review: the ciphertext cache forgets keys no provider holds, and a rotated
// encryption key gets a new ciphertext
func TestS3CipherPruneAndRotation(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	if err := cl.AddS3Provider(s3TestProviders()[0]); err != nil {
		t.Fatal(err)
	}
	old := cl.encryptS3Provider(s3TestProviders()[0]).SecretKey
	cl.stableS3Cipher("gone")
	if _, err := cl.S3ProvidersToml("saved-" + cl.Name); err != nil {
		t.Fatal(err)
	}
	if _, ok := cl.s3Providers.cipher["gone"]; ok {
		t.Fatal("a key no provider holds is still cached")
	}
	cl.Conf.SecretKey = []byte("fedcba9876543210")
	c := cl.stableS3Cipher("s3cr3t")
	if c == old || cl.Conf.GetDecryptedPassword("t", c) != "s3cr3t" {
		t.Fatalf("rotated key: ciphertext %q not renewed", c)
	}
}

// #2155: once the config holds the providers, deleting them all does not bring back the ones
// of s3providers.json, kept for older releases
func TestS3ProvidersAllDeletedNotReadFromFile(t *testing.T) {
	cl := newS3ConfigTestCluster(t)
	data, _ := json.Marshal([]s3ProviderOnDisk{toOnDisk(cl.encryptS3Provider(s3TestProviders()[0]))})
	if err := os.WriteFile(cl.s3ProvidersFilePath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	if err := cl.RemoveS3Provider("bso-s3"); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	cl.Conf.S3Providers = nil // the saved config has no block left
	if err := cl.LoadS3Providers(); err != nil {
		t.Fatal(err)
	}
	if ps := cl.GetS3ProvidersSnapshot(); len(ps) != 0 {
		t.Fatalf("deleted providers came back from s3providers.json: %+v", ps)
	}
}
