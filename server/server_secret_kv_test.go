// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
	"github.com/signal18/replication-manager/utils/state"
)

const serverKvFolder = "system/repman/acme/repman1/paris"

// serverKvTestManager is a named instance with its server credential stores on the fake
// vaults; the tests run the passes themselves (no background worker).
func serverKvTestManager(t *testing.T, named bool, vaults ...*vaultfake.Vault) *ReplicationManager {
	t.Helper()
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	repman.Conf.SecretKvWriteTimeout = 1
	repman.Conf.SecretKvQueueLimit = 100
	if named {
		repman.Conf.InstanceDomain, repman.Conf.InstanceSubDomain, repman.Conf.InstanceSubDomainZone = "acme", "repman1", "paris"
	}
	for i, v := range vaults {
		repman.Conf.ServerSecretKvs = append(repman.Conf.ServerSecretKvs, config.SecretKvConfig{Name: []string{"srv-a", "srv-b"}[i], Plugin: config.ConstSecretKvPluginVault, Enabled: true,
			Settings: map[string]interface{}{"address": v.URL, "mount": "kv", "role-id": "role", "secret-id": repman.Conf.GetEncryptedString("sid")}})
	}
	repman.InitAlertStateMachine()
	repman.serverKvQueue()
	repman.serverSecretKv.running.Store(true)
	return repman
}

// A rotation of a user of the instance's list goes to every server credential store, under
// the instance's folder: never to a cluster's store, whose sponsor may read it.
func TestServerKvRotationWritesTheServerStoreOnly(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	clusterVault := vaultfake.New("kv", "role", "sid")
	defer clusterVault.Close()
	repman := serverKvTestManager(t, true, server)
	c1 := repman.Clusters["c1"]
	c1.Conf.SecretKvs = []config.SecretKvConfig{{Name: "client", Plugin: config.ConstSecretKvPluginVault, Enabled: true,
		Settings: map[string]interface{}{"address": clusterVault.URL, "mount": "kv", "role-id": "role", "secret-id": c1.Conf.GetEncryptedString("sid")}}}

	res, err := repman.rotateAPIUserPassword(c1, "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("the write failed: %v", repman.ServerKvBehind())
	}
	got := server.Get(serverKvFolder + "/api-credentials")
	if got == nil || got["api-credentials"] != "admin:first,ops:"+res.Password {
		t.Fatalf("the server store does not hold the new list: %v", server.Paths())
	}
	for _, p := range clusterVault.Paths() {
		if strings.Contains(p, "api-credentials") {
			t.Fatalf("a server secret reached a cluster's store: %s", p)
		}
	}
}

// Not named: the folder in a shared store is unknown, nothing is written and the store is
// behind, on the server level and on every cluster.
func TestServerKvNotNamedIsBehind(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, false, server)
	if _, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin"); err != nil {
		t.Fatal(err)
	}
	if len(server.Paths()) != 0 {
		t.Fatalf("written without a name: %v", server.Paths())
	}
	if why := repman.ServerKvBehind()["srv-a"]; why != errServerKvNotNamed {
		t.Fatalf("behind %q", why)
	}
	repman.checkServerSecretKvStores()
	if !repman.StateMachine.CurState.Search("GWARN024@srv-a") {
		t.Fatal("no server state")
	}
	cl := repman.Clusters["c1"]
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	cl.ConfigStateMachine = new(state.StateMachine)
	cl.ConfigStateMachine.Init()
	cl.ServerGlobals.ServerStoresBehind = repman.ServerKvBehind
	cl.Status = "A"
	cl.CheckSecretKvStores()
	if !cl.SecurityStateMachine.CurState.Search("WARN0320@srv-a") {
		t.Fatal("no cluster state")
	}
}

// The full pass (start, an entry turned on) writes every server secret's current value; a
// store that fails is behind and keeps its versions queued.
func TestServerKvFullPassAndFailure(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	if err := repman.CredentialChain().Set("mail-smtp-password", "smtp-1"); err != nil {
		t.Fatal(err)
	}
	repman.serverKvQueue().Take()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("the full pass failed: %v", repman.ServerKvBehind())
	}
	for key, want := range map[string]string{"api-credentials": "admin:first,ops:second", "mail-smtp-password": "smtp-1"} {
		if got := server.Get(serverKvFolder + "/" + key); got == nil || got[key] != want {
			t.Fatalf("%s not written by the full pass", key)
		}
	}
	server.SetFail(true)
	if err := repman.CredentialChain().Set("mail-smtp-password", "smtp-2"); err != nil {
		t.Fatal(err)
	}
	if !repman.serverKvRunner().WritePass() || repman.ServerKvBehind()["srv-a"] == "" || repman.serverKvQueue().Len() == 0 {
		t.Fatal("a failing store is not behind with its version queued")
	}
	server.SetFail(false)
	if repman.serverKvRunner().WritePass() || repman.ServerKvBehind()["srv-a"] != "" {
		t.Fatal("the store does not catch up")
	}
	if got := server.Get(serverKvFolder + "/mail-smtp-password"); got["mail-smtp-password"] != "smtp-2" {
		t.Fatal("the queued version was not written")
	}
}

// The server credential stores are the instance's owners' only: the server credentials grant,
// never a cluster's settings grant.
func TestServerKvACL(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second,dba:third")
	cl := repman.Clusters["c1"]
	cl.Conf.APIUsersACLAllow = serverLevelACL + ",dba:cluster-settings db-config-flag show"
	cl.LoadAPIUsers()
	for _, url := range []string{"/api/clusters/settings/actions/server-secret-kv", "/api/clusters/settings/actions/server-secret-kv/add", "/api/clusters/settings/actions/server-secret-kv/enable/x"} {
		if !cl.IsURLPassACL("admin", url, false) {
			t.Fatalf("an owner is refused %s", url)
		}
		if cl.IsURLPassACL("dba", url, false) {
			t.Fatalf("the cluster settings grant reaches %s", url)
		}
	}
}

// The server list is saved in its own table, never the cluster's.
func TestServerKvToml(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	conf.ServerSecretKvs = []config.SecretKvConfig{{Name: "srv", Plugin: config.ConstSecretKvPluginVault, Enabled: true,
		Settings: map[string]interface{}{"address": "https://vault.example", "secret-id": "clear"}}}
	out, err := conf.SecretKvsTomlAt(config.SecretKvLevelServer, "saved-default")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[[saved-default.server-secret-kv]]") || strings.Contains(out, "clear") {
		t.Fatalf("server table:\n%s", out)
	}
	if cl, _ := conf.SecretKvsTomlAt(config.SecretKvLevelCluster, "saved-default"); cl != "" {
		t.Fatalf("the server list in the cluster table:\n%s", cl)
	}
}

// A cluster entry never writes under the server's folder: the path-prefix system is refused at
// the cluster level, allowed at the server level.
func TestServerKvFolderReservedForTheServer(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	for _, prefix := range []string{"system", "system/repman", "/system/repman/acme/", "tenant/system/repman", "System", "./system", "a/../b", "a//system"} {
		_, err := conf.PutSecretKvAt(config.SecretKvLevelCluster, config.SecretKvSpec{Name: "client", Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": prefix}}, true)
		if !errors.Is(err, config.ErrSecretKvReservedPrefix) {
			t.Fatalf("cluster prefix %q: %v", prefix, err)
		}
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelCluster, config.SecretKvSpec{Name: "client", Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": "systems"}}, true); err != nil {
		t.Fatalf("an ordinary prefix refused: %v", err)
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv", Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": "tenant"}}, true); err != nil {
		t.Fatal(err)
	}
}

// A standby that becomes active gives the server entries a full pass: a rotation made on the
// other monitor reaches them.
func TestServerKvPassWhenBecomingActive(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	repman.Status = ConstMonitorStandby
	repman.checkServerSecretKvStores()
	if repman.serverSecretKv.passDue.Load() {
		t.Fatal("a standby asks a pass")
	}
	repman.Status = ConstMonitorActif
	repman.checkServerSecretKvStores()
	if !repman.serverSecretKv.passDue.Load() {
		t.Fatal("no pass when becoming active")
	}
	repman.serverSecretKv.passDue.Store(false)
	repman.checkServerSecretKvStores()
	if repman.serverSecretKv.passDue.Load() {
		t.Fatal("a pass at every tick")
	}
}

// A name part that is not a valid folder, a registered one included, never reaches a path.
func TestServerKvInvalidNameIsNotNamed(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	conf.Cloud18, conf.Cloud18Domain, conf.Cloud18SubDomain, conf.Cloud18SubDomainZone = true, "acme", "../x", "paris"
	if _, ok := conf.InstanceStoreName(); ok {
		t.Fatal("an invalid registered name is used")
	}
	conf.Cloud18SubDomain = "repman1"
	if name, ok := conf.InstanceStoreName(); !ok || name != "acme/repman1/paris" {
		t.Fatalf("name %q", name)
	}
}

// The server credentials grant is checked on each cluster: held on one of two, refused on the
// other, so instanceNameACL (every cluster) refuses the server stores.
func TestServerKvGrantOnEveryCluster(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,half:second")
	url := "/api/clusters/settings/actions/server-secret-kv"
	c1, c2 := repman.Clusters["c1"], repman.Clusters["c2"]
	c1.Conf.APIUsersACLAllow = serverLevelACL + ",half:cluster-server-credentials show"
	c1.LoadAPIUsers()
	c2.Conf.APIUsersACLAllow = serverLevelACL + ",half:show"
	c2.LoadAPIUsers()
	if !c1.IsURLPassACL("half", url, false) || c2.IsURLPassACL("half", url, false) {
		t.Fatal("the grant is not checked per cluster")
	}
	if err := repman.holdsServerCredentials("half"); err == nil {
		t.Fatal("the grant on one cluster of two is enough")
	}
}

// A cluster's config never holds the server credential stores, whatever its section says.
func TestServerKvNeverInAClusterConfig(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first")
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("[c9]\ntitle = \"c9\"\n\n[[c9.server-secret-kv]]\nname = \"sneak\"\nplugin = \"secret_kv-vault\"\nenabled = true\naddress = \"https://vault.example\"\n")); err != nil {
		t.Fatal(err)
	}
	base := *repman.Conf
	base.ServerSecretKvs = []config.SecretKvConfig{{Name: "srv", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
	if repman.ImmuableFlagMaps == nil {
		repman.ImmuableFlagMaps = map[string]map[string]interface{}{}
	}
	if repman.DynamicFlagMaps == nil {
		repman.DynamicFlagMaps = map[string]map[string]interface{}{}
	}
	if repman.VersionConfs == nil {
		repman.VersionConfs = map[string]*config.ConfVersion{}
	}
	got := repman.GetClusterConfig(v, map[string]interface{}{}, map[string]interface{}{}, "c9", base)
	if got.ServerSecretKvs != nil {
		t.Fatalf("a cluster holds server stores: %+v", got.ServerSecretKvs)
	}
}

// The same rule on a peer's event and on an entry read from a config file.
func TestServerKvReservedPrefixEveryPath(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	if err := conf.SetSecretKvSetting("client", "path-prefix", "tenant/system/repman", false); !errors.Is(err, config.ErrSecretKvReservedPrefix) {
		t.Fatalf("peer event: %v", err)
	}
	conf.SecretKvs = []config.SecretKvConfig{{Name: "fromfile", Plugin: config.ConstSecretKvPluginVault, Enabled: true,
		Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": "p/system"}}}
	conf.MigrateSecretKvs()
	if len(conf.SecretKvActive()) != 0 {
		t.Fatal("a file entry under a server folder is active")
	}
	if got := conf.ReservedPrefixSecretKvs(); len(got) != 1 || got[0] != "fromfile" {
		t.Fatalf("not listed for its state: %v", got)
	}
	if err := conf.SetSecretKvSetting("fromfile", "enabled", "true", false); !errors.Is(err, config.ErrSecretKvReservedPrefix) {
		t.Fatalf("a peer event turns it on: %v", err)
	}
	if err := conf.SetSecretKvEnabled("fromfile", true); !errors.Is(err, config.ErrSecretKvReservedPrefix) {
		t.Fatalf("the API turns it on: %v", err)
	}
}
