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
		repman.Conf.Cloud18Domain, repman.Conf.Cloud18SubDomain, repman.Conf.Cloud18SubDomainZone = "acme", "repman1", "paris"
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
// other, so serverCredentialsACL (every cluster) refuses the server stores.
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

// The instance's name moves every server secret: changing one of its three settings takes the
// server credentials grant on every cluster, as a server secret does.
func TestInstanceNameSettingNeedsServerGrant(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,half:second")
	c1, c2 := repman.Clusters["c1"], repman.Clusters["c2"]
	c1.Conf.APIUsersACLAllow = serverLevelACL + ",half:cluster-server-credentials global show"
	c1.LoadAPIUsers()
	c2.Conf.APIUsersACLAllow = serverLevelACL + ",half:global show"
	c2.LoadAPIUsers()
	for _, key := range []string{"cloud18-domain", "cloud18-sub-domain", "cloud18-sub-domain-zone"} {
		if err := repman.setServerSetting("half", "", key, "other"); err == nil {
			t.Fatalf("%s changed without the grant on every cluster", key)
		}
		if err := repman.setServerSetting("admin", "", key, "acme"); err != nil {
			t.Fatalf("%s refused to an owner: %v", key, err)
		}
	}
	if n, ok := repman.Conf.InstanceStoreName(); !ok || n != "acme/acme/acme" {
		t.Fatalf("name %q", n)
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
	// A peer's events in the other order: the switch first, on a valid prefix, then the prefix.
	if err := conf.SetSecretKvSetting("ordered", "plugin", config.ConstSecretKvPluginVault, false); err != nil {
		t.Fatal(err)
	}
	if err := conf.SetSecretKvSetting("ordered", "path-prefix", "tenant", false); err != nil {
		t.Fatal(err)
	}
	if err := conf.SetSecretKvSetting("ordered", "enabled", "true", false); err != nil {
		t.Fatalf("switch on a valid prefix: %v", err)
	}
	if err := conf.SetSecretKvSetting("ordered", "path-prefix", "tenant/system", false); !errors.Is(err, config.ErrSecretKvReservedPrefix) {
		t.Fatalf("prefix after the switch: %v", err)
	}
	for _, e := range conf.SecretKvs {
		if e.Name == "ordered" && e.Settings["path-prefix"] != "tenant" {
			t.Fatalf("prefix taken: %v", e.Settings["path-prefix"])
		}
	}
}

// The OpenSVC and Kubernetes stores are the server level's only, with what each needs.
func TestServerKvOrchestratorPluginsServerOnly(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	osvc := config.SecretKvSpec{Name: "osvc", Plugin: config.ConstSecretKvPluginOpenSVC, Settings: map[string]interface{}{"cluster": "c1"}}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelCluster, osvc, true); err == nil {
		t.Fatal("an OpenSVC entry in a cluster's list")
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "nocl", Plugin: config.ConstSecretKvPluginOpenSVC}, true); err == nil {
		t.Fatal("an OpenSVC entry without its cluster")
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, osvc, true); err != nil {
		t.Fatal(err)
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "osvc", Plugin: config.ConstSecretKvPluginVault}, false); err == nil {
		t.Fatal("an entry's plugin changed")
	}
	for ns, ok := range map[string]bool{"infra": true, "Infra": false, "-x": false, "a_b": false, "": false} {
		_, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "k8s-" + map[bool]string{true: "ok", false: "ko"}[ok],
			Plugin: config.ConstSecretKvPluginKubernetes, Settings: map[string]interface{}{"cluster": "c2", "namespace": ns}}, true)
		if (err == nil) != ok {
			t.Fatalf("namespace %q: %v", ns, err)
		}
		if ok {
			conf.DeleteSecretKvAt(config.SecretKvLevelServer, "k8s-ok")
		}
	}
	// read from a file: dropped from a cluster's list, kept in the server's
	conf.SecretKvs = []config.SecretKvConfig{{Name: "osvc", Plugin: config.ConstSecretKvPluginOpenSVC, Enabled: true, Settings: map[string]interface{}{"cluster": "c1"}}}
	conf.MigrateSecretKvs()
	if len(conf.SecretKvList()) != 0 {
		t.Fatal("an OpenSVC entry kept in a cluster's list")
	}
	conf.ServerSecretKvs = []config.SecretKvConfig{{Name: "osvc", Plugin: config.ConstSecretKvPluginOpenSVC, Enabled: true, Settings: map[string]interface{}{"cluster": "c1"}}}
	conf.LoadServerSecretKvs()
	if got := conf.SecretKvActiveAt(config.SecretKvLevelServer); len(got) != 1 || got[0].Plugin != config.ConstSecretKvPluginOpenSVC {
		t.Fatalf("server list: %v", got)
	}
	plugins := map[string]bool{}
	for _, m := range config.SecretKvPlugins() {
		plugins[m.Name] = true
	}
	if plugins[config.ConstSecretKvPluginOpenSVC] || plugins[config.ConstSecretKvPluginKubernetes] {
		t.Fatal("a cluster's form offers a server-only plugin")
	}
	e := conf.SecretKvListAt(config.SecretKvLevelServer)[0]
	if f := conf.ServerSecretKvFolder(e, "acme/repman1/paris"); f != "acme_repman1_paris" {
		t.Fatalf("key prefix %q", f)
	}
}

// An OpenSVC or Kubernetes entry is refused when its cluster is not monitored here or not on
// that orchestrator, and one whose cluster went away is behind and says why.
func TestServerKvOrchestratorCluster(t *testing.T) {
	repman := serverKvTestManager(t, true)
	spec := config.SecretKvSpec{Name: "osvc", Plugin: config.ConstSecretKvPluginOpenSVC, Settings: map[string]interface{}{"cluster": "nope"}}
	if err := repman.checkServerStoreCluster(spec); err == nil || !strings.Contains(err.Error(), "not monitored") {
		t.Fatalf("unknown cluster: %v", err)
	}
	spec.Settings["cluster"] = "c1"
	repman.Clusters["c1"].Conf.ProvOrchestrator = config.ConstOrchestratorLocalhost
	if err := repman.checkServerStoreCluster(spec); err == nil {
		t.Fatal("a cluster not on OpenSVC")
	}
	repman.Clusters["c1"].Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	if err := repman.checkServerStoreCluster(spec); err != nil {
		t.Fatal(err)
	}
	if err := repman.checkServerStoreCluster(config.SecretKvSpec{Name: "v", Plugin: config.ConstSecretKvPluginVault}); err != nil {
		t.Fatalf("a vault entry: %v", err)
	}
	// written to the server list, then its cluster goes: behind, with the reason
	repman.Conf.ServerSecretKvs = []config.SecretKvConfig{{Name: "osvc", Plugin: config.ConstSecretKvPluginOpenSVC, Enabled: true, Settings: map[string]interface{}{"cluster": "gone"}}}
	repman.serverKvFullPass()
	items := repman.serverKvQueue().Take()
	if len(items) == 0 || items[0].Path != "acme_repman1_paris" {
		t.Fatalf("items: %v", len(items))
	}
	repman.serverKvQueue().Push(items...)
	if !repman.serverKvRunner().WritePass() || !strings.Contains(repman.ServerKvBehind()["osvc"], "not monitored here") {
		t.Fatalf("behind: %v", repman.ServerKvBehind())
	}
}

// Two instance names never share a key prefix in a flat orchestrator object.
func TestServerKvOrchestratorPrefixNoCollision(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"a_b/c/d", "a/b_c/d", "a/b/c_d", "a__b/c/d", "a/b/c/d"} {
		p := config.ServerOrchestratorKeyPrefix(name)
		if other, ok := seen[p]; ok {
			t.Fatalf("%s and %s share %s", name, other, p)
		}
		seen[p] = name
	}
	if config.ServerOrchestratorKeyPrefix("a_b/c/d") != "a__b_c_d" {
		t.Fatal(config.ServerOrchestratorKeyPrefix("a_b/c/d"))
	}
	for _, part := range []string{"_a", "a_"} {
		if config.ValidInstanceNamePart(part) == nil {
			t.Fatalf("%q accepted", part)
		}
	}
}

// A rotation of the instance's list names the server stores already behind: they may still
// hold the compromised password.
func TestServerKvRotationSaysStoresBehind(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	repman.setServerKvBehind("srv-a", "down")
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if res.StoresBehindCount != 1 || len(res.StoresBehind) != 1 || res.StoresBehind[0] != "srv-a" {
		t.Fatalf("stores behind: %d %v", res.StoresBehindCount, res.StoresBehind)
	}
	// without the server credentials grant on every cluster: counted, never named
	for _, cl := range repman.Clusters {
		cl.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show,ops:show"
		cl.LoadAPIUsers()
	}
	res, err = repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if res.StoresBehindCount != 1 || len(res.StoresBehind) != 0 {
		t.Fatalf("named to a non-owner: %d %v", res.StoresBehindCount, res.StoresBehind)
	}
}

// The Cloud18 vault is given to the server level of a marketplace partner that takes clusters
// (registered, partner plan, a public API address), on when it is the only store; never to an
// on-prem instance; off next to the partner's own vault; kept when the instance stops being a
// partner.
func TestServerKvMarketplaceVaultPartnerOnly(t *testing.T) {
	offer := config.MarketplaceVaultOffer{Name: "vault", Partner: "signal18", Address: "https://vault.example"}
	partner := func(repman *ReplicationManager) {
		c := repman.Conf
		c.Cloud18, c.Cloud18GitUser, c.Cloud18SubscriptionPlan = true, "ops@example.com", "partner"
		c.APIPublicURL = "https://repman.example.com"
	}
	entry := func(repman *ReplicationManager) (config.SecretKvConfig, bool) {
		for _, e := range repman.Conf.SecretKvListAt(config.SecretKvLevelServer) {
			if e.Name == config.ConstSecretKvMarketplacePrefix+"vault" {
				return e, true
			}
		}
		return config.SecretKvConfig{}, false
	}

	// on-prem: registered, public, but not on the partner plan
	repman := serverKvTestManager(t, true)
	repman.marketplaceCatalogue.Store(&config.MarketplaceCatalogue{Vaults: []config.MarketplaceVaultOffer{offer}})
	partner(repman)
	repman.Conf.Cloud18SubscriptionPlan = "support"
	repman.ensureServerMarketplaceVaults()
	if _, ok := entry(repman); ok {
		t.Fatal("an on-prem instance got the Cloud18 vault")
	}
	// a partner plan without a public address
	repman.Conf.Cloud18SubscriptionPlan = "partner"
	repman.Conf.APIPublicURL = "https://127.0.0.1:10005"
	repman.ensureServerMarketplaceVaults()
	if _, ok := entry(repman); ok {
		t.Fatal("a partner with no public address got the Cloud18 vault")
	}
	// a public partner with no store of its own: on
	partner(repman)
	repman.ensureServerMarketplaceVaults()
	e, ok := entry(repman)
	if !ok || !e.Enabled || e.Offer != "vault" || repman.Conf.SecretKvSetting(e, config.ConstSecretKvVaultAuth) != config.ConstVaultAuthJWT {
		t.Fatalf("partner entry %+v %v", e, ok)
	}
	// no longer a partner: the entry stays as it is
	repman.Conf.Cloud18SubscriptionPlan = "free"
	repman.ensureServerMarketplaceVaults()
	if e, ok := entry(repman); !ok || !e.Enabled {
		t.Fatal("the entry went when the plan changed")
	}

	// switched off by a sysops: never switched on again by the tick
	if err := repman.Conf.SetSecretKvEnabledAt(config.SecretKvLevelServer, config.ConstSecretKvMarketplacePrefix+"vault", false); err != nil {
		t.Fatal(err)
	}
	partner(repman)
	repman.ensureServerMarketplaceVaults()
	if e, _ := entry(repman); e.Enabled {
		t.Fatal("the tick switched the partner's choice back on")
	}
	// it cannot be removed through the API: the back-office manages it, the sysops switches it
	if err := repman.Conf.DeleteSecretKvAt(config.SecretKvLevelServer, config.ConstSecretKvMarketplacePrefix+"vault"); !errors.Is(err, config.ErrSecretKvOfferManaged) {
		t.Fatalf("delete: %v", err)
	}
	// the offer moves to another address: its place changed, nothing is recorded there yet
	repman.Conf.SetSecretKvWrittenAsAt(config.SecretKvLevelServer, config.ConstSecretKvMarketplacePrefix+"vault", "acme/repman1/paris")
	moved := offer
	moved.Address = "https://vault2.example"
	repman.marketplaceCatalogue.Store(&config.MarketplaceCatalogue{Vaults: []config.MarketplaceVaultOffer{moved}})
	repman.ensureServerMarketplaceVaults()
	if e, _ := entry(repman); e.WrittenAs != "" || repman.Conf.SecretKvSetting(e, config.ConstSecretKvVaultAddress) != moved.Address {
		t.Fatalf("after the offer moved: %+v", e)
	}
	// a catalogue prefix with a ".." folder is refused
	bad := offer
	bad.Name, bad.PathPrefix = "bad", "s18/../x"
	repman.marketplaceCatalogue.Store(&config.MarketplaceCatalogue{Vaults: []config.MarketplaceVaultOffer{bad}})
	repman.ensureServerMarketplaceVaults()
	for _, e := range repman.Conf.SecretKvListAt(config.SecretKvLevelServer) {
		if e.Name == config.ConstSecretKvMarketplacePrefix+"bad" {
			t.Fatal("a prefix with .. was taken")
		}
	}

	// a public partner with its own vault: added off
	own := vaultfake.New("kv", "role", "sid")
	defer own.Close()
	repman2 := serverKvTestManager(t, true, own)
	repman2.marketplaceCatalogue.Store(&config.MarketplaceCatalogue{Vaults: []config.MarketplaceVaultOffer{offer}})
	partner(repman2)
	repman2.ensureServerMarketplaceVaults()
	if e, ok := entry(repman2); !ok || e.Enabled {
		t.Fatalf("next to the partner's own vault: %+v %v", e, ok)
	}
}
