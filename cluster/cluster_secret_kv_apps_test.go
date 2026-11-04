package cluster

import (
	"os"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
)

// appSecretsTestApp is an app holding one of each secret an app can carry (#2306), stored the
// way repman stores them (encrypted), plus values that are not secrets of the cluster.
func appSecretsTestApp(conf *config.Config) *App {
	app := newTestAppForAddApp("web", "8080", "")
	app.AppConfig.AppRandomPassword = conf.GetEncryptedString("pv-rnd-1")
	app.AppConfig.AppDbPass = conf.GetEncryptedString("pv-dbp-1")
	d := app.AppConfig.Deployment
	d.Variables = config.VariableMaps{
		{Name: "APP_SECRET", Type: config.VariableTypeSecret, Value: conf.GetEncryptedString("secret-1"),
			Conditional: config.AVSlice{{Agent: "agent1", Value: conf.GetEncryptedString("agent-secret")}}},
		{Name: "APP_TOKEN", Type: config.VariableTypeSecret, Value: conf.GetEncryptedString("id:token,part")},
		{Name: "DSN", Type: config.VariableTypeSecret, Value: "postgres://app:" + conf.GetEncryptedString("dsn-pass") + "@proxy:5432/app"},
		{Name: "SENSOR_API_KEY", Type: config.VariableTypeSecret, Value: conf.GetEncryptedString("instance-key")},
		{Name: "APP_ENV", Type: config.VariableTypeEnv, Value: "plain-env"},
	}
	d.Storages.GitClones = config.GitClones{{Name: "site", GitPass: conf.GetEncryptedString("git-pass")}}
	d.Storages.S3Mounts = config.S3Mounts{{Name: "assets", SecretKey: conf.GetEncryptedString("s3-secret")}}
	return app
}

func appSecretsWanted(cl *Cluster, app *App) map[string]string {
	gc, s3m := app.AppConfig.Deployment.Storages.GitClones[0], app.AppConfig.Deployment.Storages.S3Mounts[0]
	return map[string]string{
		"apps/web/app-random-password": "pv-rnd-1",
		"apps/web/app-db-pass":         "pv-dbp-1",
		"apps/web/APP_SECRET":          "secret-1",
		"apps/web/APP_SECRET@agent1":   "agent-secret",
		"apps/web/APP_TOKEN":           "id:token,part",
		"apps/web/DSN":                 "postgres://app:dsn-pass@proxy:5432/app",
		"apps/web/" + gc.GetVariablePrefix() + config.GitVarSuffixPass:      "git-pass",
		"apps/web/" + s3m.GetVariablePrefix() + config.S3VarSuffixSecretKey: "s3-secret",
	}
}

// Every app secret becomes one version under apps/<app>/<key>, encrypted whole; an unchanged
// value makes none, a change makes one; the file holds ciphertext only; a removed app keeps
// its versions (#2227 rules 2, 6, 10)
func TestVersionAppSecretsRecordsEachSecret(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.MonitoringSecretVersioning = true
	app := appSecretsTestApp(cl.Conf)
	cl.Apps = []*App{app}
	want := appSecretsWanted(cl, app)

	cl.versionAppSecrets()
	vs := cl.versionStore()
	file, _ := vs.Export()
	appKeys := 0
	for key := range file {
		if IsAppSecretVersionKey(key) {
			appKeys++
		}
	}
	if appKeys != len(want) {
		t.Fatalf("%d app keys versioned, want %d: %v", appKeys, len(want), file)
	}
	for key, plain := range want {
		if n := vs.Count(key); n != 1 {
			t.Fatalf("%s: %d versions", key, n)
		}
		stored := file[key][0].HashValue
		if !strings.HasPrefix(stored, "hash_") || strings.ContainsAny(stored, ":,") {
			t.Fatalf("%s not encrypted whole: %q", key, stored)
		}
		if got := cl.Conf.GetDecryptedPassword(key, stored); got != plain {
			t.Fatalf("%s decrypts to %q, want %q", key, got, plain)
		}
	}
	for _, key := range []string{"apps/web/SENSOR_API_KEY", "apps/web/APP_ENV"} {
		if vs.Count(key) != 0 {
			t.Fatalf("%s must not be versioned", key)
		}
	}

	cl.versionAppSecrets()
	if n := vs.Count("apps/web/APP_SECRET"); n != 1 {
		t.Fatalf("an unchanged value made a version: %d", n)
	}
	app.AppConfig.Deployment.Variables[0].Value = cl.Conf.GetEncryptedString("secret-2")
	cl.versionAppSecrets()
	if v, ok := vs.Latest("apps/web/APP_SECRET"); !ok || v.Value != "secret-2" || vs.Count("apps/web/APP_SECRET") != 2 {
		t.Fatalf("change not versioned: %v %d", v, vs.Count("apps/web/APP_SECRET"))
	}

	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"apps/web/APP_SECRET"`) {
		t.Fatalf("the file holds no app version: %s", raw)
	}
	for _, plain := range []string{"secret-1", "secret-2", "agent-secret", "id:token", "dsn-pass", "git-pass", "s3-secret", "pv-rnd-1", "pv-dbp-1"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("the file holds %q in clear", plain)
		}
	}

	cl.Apps = nil
	cl.versionAppSecrets()
	if vs.Count("apps/web/APP_SECRET") != 2 {
		t.Fatalf("a removed app lost its versions")
	}
}

// A changed app secret goes to every vault entry under apps/<app>/<key>; a value the entry
// already holds (provisioning wrote it) makes no new vault version
func TestVersionAppSecretsCopiesToVault(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	cl.Conf.MonitoringSecretVersioning = true
	app := appSecretsTestApp(cl.Conf)
	cl.Apps = []*App{app}
	// provisioning already reported the per-agent value (rule 7)
	v.Put(testSpace+"/apps/web/APP_SECRET@agent1", map[string]interface{}{"APP_SECRET@agent1": "agent-secret"})

	cl.versionAppSecrets()
	if cl.secretWritePass() {
		t.Fatal("write pass failed")
	}
	for key, plain := range appSecretsWanted(cl, app) {
		name := strings.TrimPrefix(key, "apps/web/")
		if d := v.Get(testSpace + "/" + key); d == nil || d[name] != plain {
			t.Fatalf("%s in vault: %v", key, d)
		}
	}
	if n := v.Versions(testSpace + "/apps/web/APP_SECRET@agent1"); n != 1 {
		t.Fatalf("a value the entry held was written again: %d versions", n)
	}
	if v.Get(testSpace+"/apps/web/SENSOR_API_KEY") != nil {
		t.Fatal("the instance's system API key was copied")
	}

	cl.versionAppSecrets()
	if n := cl.storeQueue().Len(); n != 0 {
		t.Fatalf("unchanged values queued: %d", n)
	}
	app.AppConfig.Deployment.Variables[0].Value = cl.Conf.GetEncryptedString("secret-2")
	cl.versionAppSecrets()
	cl.secretWritePass()
	if d := v.Get(testSpace + "/apps/web/APP_SECRET"); d["APP_SECRET"] != "secret-2" || v.Versions(testSpace+"/apps/web/APP_SECRET") != 2 {
		t.Fatalf("change not in vault: %v (%d versions)", d, v.Versions(testSpace+"/apps/web/APP_SECRET"))
	}
}

// An entry turned on after the apps exist gets their secrets at its full pass (rule 4)
func TestSecretFullPassQueuesAppSecrets(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	app := appSecretsTestApp(cl.Conf)
	cl.Apps = []*App{app}

	cl.secretFullPass()
	cl.secretWritePass()
	if d := v.Get(testSpace + "/apps/web/APP_SECRET"); d == nil || d["APP_SECRET"] != "secret-1" {
		t.Fatalf("full pass did not copy the app secret: %v", d)
	}
	// a second pass writes nothing new
	cl.secretFullPass()
	cl.secretWritePass()
	if n := v.Versions(testSpace + "/apps/web/APP_SECRET"); n != 1 {
		t.Fatalf("second full pass wrote again: %d versions", n)
	}
}

// Without the local file a key keeps its last two values in memory (F4), and no file is
// written; without the encryption key nothing is recorded (F9)
func TestVersionAppSecretsBounds(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.MonitoringSecretVersioning = false
	app := appSecretsTestApp(cl.Conf)
	cl.Apps = []*App{app}
	for _, value := range []string{"a", "b", "c", "d"} {
		app.AppConfig.Deployment.Variables[0].Value = cl.Conf.GetEncryptedString(value)
		cl.versionAppSecrets()
	}
	if n := cl.versionStore().Count("apps/web/APP_SECRET"); n != 2 {
		t.Fatalf("%d values kept in memory, want 2", n)
	}
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name)); !os.IsNotExist(err) {
		t.Fatalf("a file was written with versioning off: %v", err)
	}

	nokey := secretKvTestCluster(t)
	nokey.Conf.MonitoringSecretVersioning = true
	nokey.Apps = []*App{appSecretsTestApp(nokey.Conf)}
	nokey.Conf.SecretKey = nil
	nokey.Conf.MonitoringKeyPath = ""
	nokey.versionAppSecrets()
	if n := nokey.versionStore().Count("apps/web/APP_SECRET"); n != 0 {
		t.Fatalf("recorded without the key: %d", n)
	}
}

// A standby merges the file the config pull brings: the app versions come with it (rule 11)
func TestVersionAppSecretsMergeOnStandby(t *testing.T) {
	active := secretKvTestCluster(t)
	active.Conf.MonitoringSecretVersioning = true
	active.Apps = []*App{appSecretsTestApp(active.Conf)}
	active.versionAppSecrets()
	file, _ := active.versionStore().Export()

	standby := secretKvTestCluster(t)
	standby.Conf.MonitoringSecretVersioning = true
	if !standby.versionStore().Merge(file) {
		t.Fatal("nothing merged")
	}
	if v, ok := standby.versionStore().Latest("apps/web/APP_SECRET"); !ok || v.Value != "secret-1" {
		t.Fatalf("app version not merged: %v %v", v, ok)
	}
}

// A provisioning that reports an app secret the entry already holds makes no vault version;
// a changed value makes one (#2306: the save versioned it already, rule 7 reports dedup)
func TestReportOrchestratorAppSecretWritesOnChangeOnly(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	path := testSpace + "/apps/web/APP_SECRET"

	cl.reportOrchestratorSecret("web", "APP_SECRET", "secret-1")
	cl.secretWritePass()
	if n := v.Versions(path); n != 1 {
		t.Fatalf("first report: %d versions", n)
	}
	cl.reportOrchestratorSecret("web", "APP_SECRET", "secret-1")
	cl.secretWritePass()
	if n := v.Versions(path); n != 1 {
		t.Fatalf("an unchanged value reported again made a version: %d", n)
	}
	cl.reportOrchestratorSecret("web", "APP_SECRET", "secret-2")
	cl.secretWritePass()
	if d := v.Get(path); v.Versions(path) != 2 || d["APP_SECRET"] != "secret-2" {
		t.Fatalf("changed value: %d versions, %v", v.Versions(path), d)
	}
}
