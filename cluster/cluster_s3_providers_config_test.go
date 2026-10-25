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
// [[saved-<cluster>.s3-provider]] blocks, keys encrypted, and renames the file; the server's
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
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("s3providers.json still there")
	}
	if _, err := os.Stat(jsonPath + ".migrated"); err != nil {
		t.Fatal("s3providers.json.migrated missing")
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
