// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
	"github.com/signal18/replication-manager/utils/state"
)

const testSpace = "c1" // no path-prefix: the cluster at the mount's root

func secretKvTestCluster(t *testing.T, vaults ...*vaultfake.Vault) *Cluster {
	t.Helper()
	conf := &config.Config{
		WorkingDir:           t.TempDir(),
		SecretKey:            []byte("0123456789abcdef"),
		ConfRewrite:          true,
		ImmuableFlagMap:      map[string]interface{}{},
		DynamicFlagMap:       map[string]interface{}{},
		DefaultFlagMap:       map[string]interface{}{},
		SecretKvWriteTimeout: 1,
		SecretKvQueueLimit:   100,
	}
	for i, v := range vaults {
		conf.SecretKvs = append(conf.SecretKvs, config.SecretKvConfig{Name: []string{"vault-a", "vault-b", "vault-c"}[i], Plugin: config.ConstSecretKvPluginVault, Enabled: true,
			Settings: map[string]interface{}{"address": v.URL, "mount": "kv", "role-id": "role", "secret-id": conf.GetEncryptedString("sid")}})
	}
	cl := &Cluster{Name: "c1", Conf: conf, Status: ConstMonitorActif,
		SecurityStateMachine: new(state.StateMachine), ConfigStateMachine: new(state.StateMachine),
		Servers: serverList{&ServerMonitor{Name: "db1", URL: "db1:3306"}, &ServerMonitor{Name: "db2", URL: "db2:3306"}}}
	cl.SecurityStateMachine.Init()
	cl.ConfigStateMachine.Init()
	cl.secretKv.apply = func(string) {}
	// the tests run the passes themselves: no background worker
	cl.secretKv.running.Store(true)
	cl.CredentialChain().Refresh()
	return cl
}

func hasState(sm *state.StateMachine, key string) bool { return sm.CurState.Search(key) }

// #2227: the layout repman computes: a database credential under every database server, an
// archive secret under the attached archive, an instance secret nowhere
func TestSecretFoldersLayout(t *testing.T) {
	cl := secretKvTestCluster(t)
	got := cl.secretFolders("db-servers-credential")
	if strings.Join(got, ",") != "databases/db1,databases/db2" {
		t.Fatalf("database folders %v", got)
	}
	cl.Conf.Archives = []config.ArchiveConfig{{Name: "archive-s3-1", Plugin: config.ConstArchivePluginResticS3, Settings: map[string]interface{}{}}}
	cl.Conf.BackupArchiveName = "archive-s3-1"
	if got := cl.secretFolders("backup-restic-password"); strings.Join(got, ",") != "archives/archive-s3-1" {
		t.Fatalf("archive folders %v", got)
	}
	for _, key := range []string{"git-acces-token", "vault-secret-id", "api-oauth-client-secret"} {
		if f := cl.secretFolders(key); len(f) != 0 {
			t.Fatalf("instance secret %s copied to %v", key, f)
		}
	}
	e := config.SecretKvConfig{Name: "cloud18", Plugin: config.ConstSecretKvPluginVault, Settings: map[string]interface{}{"path-prefix": "/d/s/"}}
	if p := cl.Conf.SecretKvFolder(e, cl.Name) + "/" + cl.secretFolders("backup-restic-password")[0]; p != "d/s/c1/archives/archive-s3-1" {
		t.Fatalf("path %s", p)
	}
}

// #2227: a change is written as a version under every server in every active store; a
// failing store never delays the change, and is WARN0269 until it gets the version
func TestFailingVaultNeverBlocksSetAll(t *testing.T) {
	good := vaultfake.New("kv", "role", "sid")
	defer good.Close()
	bad := vaultfake.New("kv", "role", "sid")
	defer bad.Close()
	bad.Delay = 3 * time.Second
	cl := secretKvTestCluster(t, good, bad)

	start := time.Now()
	if err := cl.CredentialChain().Set("db-servers-credential", "u:new"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("SetAll took %s", d)
	}
	if cl.storeQueue().Len() != 4 {
		t.Fatalf("queued %d, want 2 servers x 2 stores", cl.storeQueue().Len())
	}
	if !cl.secretWritePass() {
		t.Fatal("the slow store did not fail")
	}
	for _, srv := range []string{"db1", "db2"} {
		if d := good.Get(testSpace + "/databases/" + srv + "/db-servers-credential"); d["db-servers-credential"] != "u:new" {
			t.Fatalf("%s: %v", srv, d)
		}
	}
	cl.CheckSecretKvStores()
	if !hasState(cl.SecurityStateMachine, "WARN0269@vault-b") || hasState(cl.SecurityStateMachine, "WARN0269@vault-a") {
		t.Fatalf("states %v", cl.SecurityStateMachine.GetStates())
	}
	if cl.storeQueue().Len() != 2 {
		t.Fatalf("the failed versions are not queued again: %d", cl.storeQueue().Len())
	}
	// the store answers again: it gets the version, the state goes
	bad.Delay = 0
	if cl.secretWritePass() {
		t.Fatal("still failing")
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if hasState(cl.SecurityStateMachine, "WARN0269@vault-b") || bad.Get(testSpace+"/databases/db1/db-servers-credential") == nil {
		t.Fatal("the store did not catch up")
	}
}

// #2227: the full pass: the newest version wins across the stores and becomes the current
// one here; a store behind gets it, written only where it differs; a second pass writes
// nothing
func TestFullPassNewestWinsNoChurn(t *testing.T) {
	a := vaultfake.New("kv", "role", "sid")
	defer a.Close()
	b := vaultfake.New("kv", "role", "sid")
	defer b.Close()
	cl := secretKvTestCluster(t, a, b)
	key := "monitoring-write-heartbeat-credential"
	if err := cl.CredentialChain().Set(key, "hb:local"); err != nil {
		t.Fatal(err)
	}
	cl.storeQueue().Take()
	lv, _ := cl.versionStore().Latest(key)
	local := lv.At
	for _, srv := range []string{"db1", "db2"} {
		a.Put(testSpace+"/databases/"+srv+"/"+key, map[string]interface{}{key: "hb:old", "written-at": local.Add(-time.Hour).Format(time.RFC3339Nano), "written-by": "1"})
		b.Put(testSpace+"/databases/"+srv+"/"+key, map[string]interface{}{key: "hb:remote", "written-at": local.Add(time.Hour).Format(time.RFC3339Nano), "written-by": "2"})
	}
	cl.secretFullPass()
	if v := cl.Conf.Secrets.Get(key).Value; v != "hb:remote" {
		t.Fatalf("current %q, want the newest", v)
	}
	if !strings.Contains(cl.secretKv.behind["vault-a"], "another version") || cl.secretKv.behind["vault-b"] != "" {
		t.Fatalf("behind %v", cl.secretKv.behind)
	}
	cl.secretWritePass()
	path := testSpace + "/databases/db1/" + key
	if a.Get(path)[key] != "hb:remote" || a.Versions(path) != 2 || b.Versions(path) != 1 {
		t.Fatalf("a %v (%d versions) b %d versions", a.Get(path), a.Versions(path), b.Versions(path))
	}
	writes := a.Writes + b.Writes
	cl.secretFullPass()
	cl.secretWritePass()
	if a.Writes+b.Writes != writes {
		t.Fatalf("a second pass wrote %d versions", a.Writes+b.Writes-writes)
	}
}

// #2227: a database engine makes the passwords: its value wins over any stored version
func TestFullPassDatabaseEngineFirst(t *testing.T) {
	engine := vaultfake.New("kv", "role", "sid")
	defer engine.Close()
	kv := vaultfake.New("kv", "role", "sid")
	defer kv.Close()
	engine.SetStatic("repl", "from-engine")
	cl := secretKvTestCluster(t, kv, engine)
	cl.Conf.SecretKvs[1].Settings["mode"] = config.VaultDbEngine
	if err := cl.CredentialChain().Set("replication-credential", "repl:old"); err != nil {
		t.Fatal(err)
	}
	cl.storeQueue().Take()
	kv.Put(testSpace+"/databases/db1/replication-credential", map[string]interface{}{"replication-credential": "repl:kv", "written-at": time.Now().Add(time.Hour).Format(time.RFC3339Nano), "written-by": "9"})
	cl.secretFullPass()
	if v := cl.Conf.Secrets.Get("replication-credential").Value; v != "repl:from-engine" {
		t.Fatalf("current %q, want the engine's", v)
	}
	cl.secretWritePass()
	if kv.Get(testSpace + "/databases/db2/replication-credential")["replication-credential"] != "repl:from-engine" {
		t.Fatal("the KV store did not get the engine's value")
	}
}

// #2227: a rotation with a database engine entry: Vault rotates the static roles, the values
// go through the chain and are a version in the other stores
func TestRotateWithVaultEngine(t *testing.T) {
	engine := vaultfake.New("kv", "role", "sid")
	defer engine.Close()
	kv := vaultfake.New("kv", "role", "sid")
	defer kv.Close()
	engine.SetStatic("repman", "p0")
	cl := secretKvTestCluster(t, kv, engine)
	cl.Conf.SecretKvs[1].Settings["mode"] = config.VaultDbEngine
	if err := cl.CredentialChain().Set("db-servers-credential", "repman:p0"); err != nil {
		t.Fatal(err)
	}
	if err := cl.CredentialChain().Set("replication-credential", "repman:p0"); err != nil {
		t.Fatal(err)
	}
	cl.storeQueue().Take()
	e, ok := cl.vaultEngineEntry()
	if !ok {
		t.Fatal("no engine entry")
	}
	if err := cl.rotateWithVaultEngine(e); err != nil {
		t.Fatal(err)
	}
	if engine.Rotations("repman") != 1 || cl.Conf.Secrets.Get("db-servers-credential").Value != "repman:rotated-1" {
		t.Fatalf("rotations %d value %q", engine.Rotations("repman"), cl.Conf.Secrets.Get("db-servers-credential").Value)
	}
	cl.secretWritePass()
	if kv.Get(testSpace + "/databases/db1/db-servers-credential")["db-servers-credential"] != "repman:rotated-1" {
		t.Fatal("the rotated value is not a version in the KV store")
	}
}

// #2227: a credential still declared by a Vault path is read once and stored through the
// chain; a path pinned in a file is read at each start and raises WARN0271
func TestSeedVaultPathSecrets(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	v.Put("legacy/c1/db", map[string]interface{}{"db-servers-credential": "u:seed"})
	v.Put("legacy/c1/repl", map[string]interface{}{"replication-credential": "r:seed"})
	cl := secretKvTestCluster(t, v)
	cl.Conf.SecretKvs[0].Name = config.ConstSecretKvVaultDefault
	cl.Conf.SetDynamicFlag("db-servers-credential", "legacy/c1/db")
	cl.Conf.ImmuableFlagMap["replication-credential"] = "legacy/c1/repl"
	cl.CredentialChain().Refresh()

	cl.seedVaultPathSecrets()
	if got := cl.Conf.Secrets.Get("db-servers-credential").Value; got != "u:seed" {
		t.Fatalf("seed %q", got)
	}
	// rule 1: the config keeps its starting value, the value read is a version
	if raw, _ := cl.Conf.GetDynamicFlag("db-servers-credential"); raw != "legacy/c1/db" {
		t.Fatalf("the config holds %v, want its starting value", raw)
	}
	if v, ok := cl.versionStore().Latest("db-servers-credential"); !ok || v.Value != "u:seed" {
		t.Fatalf("version %+v %v", v, ok)
	}
	if got := cl.Conf.Secrets.Get("replication-credential").Value; got != "r:seed" {
		t.Fatalf("pinned %q", got)
	}
	cl.CheckSecretKvStores()
	if !hasState(cl.ConfigStateMachine, "WARN0271@replication-credential") || hasState(cl.ConfigStateMachine, "WARN0271@db-servers-credential") {
		t.Fatalf("config states %v", cl.ConfigStateMachine.GetStates())
	}
	// the next start: the seeded value is no path any more, only the pin is read again
	logins := v.Logins
	cl.CredentialChain().Refresh()
	cl.seedVaultPathSecrets()
	if v.Logins != logins+1 || cl.Conf.Secrets.Get("db-servers-credential").Value != "u:seed" {
		t.Fatalf("logins %d->%d", logins, v.Logins)
	}
}

// #2227: the drops of a full queue are WARN0270 until the queue drains, then a full pass is due
func TestStoreQueueDropsState(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	cl.Conf.SecretKvQueueLimit = 2
	cl.Servers = append(cl.Servers, &ServerMonitor{Name: "db3"})
	cl.CredentialChain().Set("db-servers-credential", "u:x")
	cl.CheckSecretKvStores()
	if !hasState(cl.SecurityStateMachine, "WARN0270") {
		t.Fatalf("states %v", cl.SecurityStateMachine.GetStates())
	}
	cl.secretKv.passDue.Store(false)
	cl.secretWritePass()
	if !cl.secretKv.passDue.Load() {
		t.Fatal("no full pass after the drops")
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if hasState(cl.SecurityStateMachine, "WARN0270") {
		t.Fatal("WARN0270 after the queue drained")
	}
}

// #2227: a standby records versions in its own file only, never writes the vaults
func TestStandbyWritesNoVault(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	cl.Status = ConstMonitorStandby
	cl.CredentialChain().Set("db-servers-credential", "u:x")
	if v, ok := cl.versionStore().Latest("db-servers-credential"); cl.storeQueue().Len() != 0 || !ok || v.Value != "u:x" {
		t.Fatalf("queued %d, version %+v", cl.storeQueue().Len(), v)
	}
	paths := v.Paths()
	sort.Strings(paths)
	if len(paths) != 0 {
		t.Fatalf("written %v", paths)
	}
}

// #2227 rule 11: a standby takes the versions the active monitor wrote in the file the config
// pull brings, never writes it, and the newest version is its current value
func TestStandbyLoadsPeerVersions(t *testing.T) {
	active := secretKvTestCluster(t)
	active.Conf.MonitoringSecretVersioning = true
	if err := os.MkdirAll(filepath.Join(active.Conf.WorkingDir, active.Name), 0700); err != nil {
		t.Fatal(err)
	}
	standby := secretKvTestCluster(t)
	standby.Conf.MonitoringSecretVersioning = true
	standby.Conf.WorkingDir = active.Conf.WorkingDir // the pull: the same file
	standby.Status = ConstMonitorStandby
	standby.CredentialChain().Refresh()

	if err := active.CredentialChain().Set("replication-credential", "r:pass2"); err != nil {
		t.Fatal(err)
	}
	active.ReconcileSecretVersionStore()
	time.Sleep(10 * time.Millisecond)
	standby.CheckSecretKvStores()
	if v := standby.Conf.Secrets.Get("replication-credential").Value; v != "r:pass2" {
		t.Fatalf("standby current %q", v)
	}
	before, _ := os.ReadFile(SecretVersionStorePath(active.Conf.WorkingDir, active.Name))
	standby.ReconcileSecretVersionStore()
	if after, _ := os.ReadFile(SecretVersionStorePath(active.Conf.WorkingDir, active.Name)); string(after) != string(before) {
		t.Fatal("the standby rewrote the file")
	}
}

// #2227 rule 1b at start: versioning off, the vault holds pass2: the start reads it once and
// runs on it, the config keeps pass1, no new version is written to the vault
func TestReadVaultsAtStartRestores(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	key := "monitoring-write-heartbeat-credential"
	cl.Conf.SetDynamicFlag(key, "hb:pass1")
	cl.CredentialChain().Refresh()
	for _, srv := range []string{"db1", "db2"} {
		v.Put(testSpace+"/databases/"+srv+"/"+key, map[string]interface{}{key: "hb:pass2", "written-at": time.Now().UTC().Format(time.RFC3339Nano), "written-by": "2"})
	}
	writes := v.Writes
	cl.ReadVaultsAtStart()
	if got := cl.Conf.Secrets.Get(key).Value; got != "hb:pass2" {
		t.Fatalf("at start %q, want pass2", got)
	}
	if raw, _ := cl.Conf.GetDynamicFlag(key); raw != "hb:pass1" {
		t.Fatalf("config %v", raw)
	}
	if v.Writes != writes || cl.storeQueue().Len() != 0 {
		t.Fatal("the read at start wrote a version")
	}
}

// #2227 rule 1b at start: a slow vault is bounded by secret-kv-start-timeout: the start goes
// on with what it has and the entry is behind (WARN0269)
func TestReadVaultsAtStartBounded(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	v.Delay = 3 * time.Second
	cl := secretKvTestCluster(t, v)
	cl.Conf.SecretKvStartTimeout = 1
	key := "monitoring-write-heartbeat-credential"
	cl.Conf.SetDynamicFlag(key, "hb:pass1")
	cl.CredentialChain().Refresh()
	start := time.Now()
	cl.ReadVaultsAtStart()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the start waited %s", d)
	}
	if got := cl.Conf.Secrets.Get(key).Value; got != "hb:pass1" {
		t.Fatalf("at start %q", got)
	}
	cl.CheckSecretKvStores()
	if !hasState(cl.SecurityStateMachine, "WARN0269@vault-a") {
		t.Fatalf("states %v", cl.SecurityStateMachine.GetStates())
	}
}

// #2227 rule 2: the prune of a loaded cluster changes its version store, the save writes the
// file; an offline append keeps the file's format
func TestPruneSecretVersionsThroughSave(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.MonitoringSecretVersioning = true
	if err := os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, cl.Name), 0700); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"r:1", "r:2", "r:3"} {
		if err := cl.CredentialChain().Set("replication-credential", v); err != nil {
			t.Fatal(err)
		}
	}
	cl.ReconcileSecretVersionStore()
	path := SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name)
	before, _ := os.ReadFile(path)
	if s := cl.PruneSecretVersions(1, false); s.VersionsRemoved != 2 {
		t.Fatalf("summary %+v", s)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the prune wrote the file: the save callback does")
	}
	cl.ReconcileSecretVersionStore()
	if vs, _ := cl.SecretVersionsInFile("replication-credential"); len(vs) != 1 || vs[0] != "r:3" {
		t.Fatalf("file %v", vs)
	}
	if err := AppendSecretVersionFile(path, "replication-credential", cl.Conf.EncryptSecretValue("r:4"), 7); err != nil {
		t.Fatal(err)
	}
	if vs, _ := cl.SecretVersionsInFile("replication-credential"); len(vs) != 2 || vs[1] != "r:4" {
		t.Fatalf("file after append %v", vs)
	}
}

// #2227 rule 7: the orchestrator writers report the values they set: the shared root password
// under every database server, an app's secret under apps/<app>; the instance's secrets
// (system API key, API auth header) never
func TestOrchestratorSecretsReported(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	cl := secretKvTestCluster(t, v)
	cl.reportOrchestratorSecret("env", "MYSQL_ROOT_PASSWORD", "root-pass")
	cl.reportOrchestratorSecret("env", "SENSOR_API_KEY", "x")
	cl.reportOrchestratorSecret("env", "REPMAN_AUTH_HEADER", "x")
	cl.reportOrchestratorSecret("mattermost", "MM_SQLSETTINGS_DATASOURCE", "dsn-secret")
	cl.ApplyAppRandomPassword(&config.AppConfig{AppHost: "erp:8000"})
	var got []string
	for _, it := range cl.storeQueue().Take() {
		got = append(got, it.Path+"/"+it.Key)
	}
	sort.Strings(got)
	want := []string{
		"c1/apps/erp/app-random-password",
		"c1/apps/mattermost/MM_SQLSETTINGS_DATASOURCE",
		"c1/databases/db1/MYSQL_ROOT_PASSWORD",
		"c1/databases/db2/MYSQL_ROOT_PASSWORD",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("queued %v, want %v", got, want)
	}
}

// #2227: an entry from a marketplace offer: the list carries its offer (name, title, partner),
// its settings are the back-office's (edit and remove refused), its switch is the cluster's;
// the user's own entries carry no offer and cannot take a cloud18- name
func TestMarketplaceVaultEntryManaged(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.Cloud18, cl.Conf.Cloud18GitUser = true, "u"
	cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = "d", "s", "fr"
	cl.EnsureMarketplaceVaults([]config.MarketplaceVaultOffer{{Name: "vault-fr", Title: "Cloud18 vault", Partner: "partner-a", Address: "https://v:8200"}})
	if _, err := cl.PutSecretKv(config.SecretKvSpec{Name: "own", Settings: map[string]interface{}{"address": "https://own:8200"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.PutSecretKv(config.SecretKvSpec{Name: "cloud18-mine", Settings: map[string]interface{}{"address": "https://x"}}, true); err == nil {
		t.Fatal("a cloud18- name accepted for the user's own entry")
	}
	if _, err := cl.PutSecretKv(config.SecretKvSpec{Name: "cloud18-vault-fr", Settings: map[string]interface{}{"mount": "other"}}, false); !errors.Is(err, config.ErrSecretKvOfferManaged) {
		t.Fatalf("edit: %v", err)
	}
	if err := cl.RemoveSecretKv("cloud18-vault-fr"); !errors.Is(err, config.ErrSecretKvOfferManaged) {
		t.Fatalf("remove: %v", err)
	}
	if err := cl.SetSecretKvEnabled("cloud18-vault-fr", true); err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(cl.SecretKvList())
	var got struct {
		Entries []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Offer   *struct {
				Name, Title, Partner string
			} `json:"offer"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(js, &got); err != nil {
		t.Fatal(err)
	}
	offers := map[string]bool{}
	for _, e := range got.Entries {
		switch e.Name {
		case "cloud18-vault-fr":
			if e.Offer == nil || e.Offer.Name != "vault-fr" || e.Offer.Title != "Cloud18 vault" || e.Offer.Partner != "partner-a" || !e.Enabled {
				t.Fatalf("offer entry %+v", e)
			}
			offers[e.Name] = true
		case "own":
			if e.Offer != nil {
				t.Fatalf("own entry carries an offer: %+v", e.Offer)
			}
			offers[e.Name] = true
		}
	}
	if len(offers) != 2 {
		t.Fatalf("entries %s", js)
	}
	out, err := cl.Conf.SecretKvsToml("saved-c1")
	if err != nil || !strings.Contains(out, `offer = "vault-fr"`) {
		t.Fatalf("saved %s %v", out, err)
	}
}
