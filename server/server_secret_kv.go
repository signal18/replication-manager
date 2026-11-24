// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/state"
	"net"
	"net/url"
	"strings"
)

// The server level's credential stores (CREDENTIAL_STORES.md "Two levels"): the entries of
// [[default.server-secret-kv]] get every version of every server secret: a Vault under
// <path-prefix>/system/repman/<instance name>/<key>, the OpenSVC secret system/sec/repman or a
// Kubernetes Secret repman-system under the keys <instance name, "_" for "/">_<key>. Set by the instance's sysops and extsysops
// only, never copied to a cluster, so no cluster's sponsor or member reaches them. The same
// queue and worker as a cluster's (credstore.StoreRunner).

// serverSecretKv is the server level's queue, its worker's flags and what the states come from.
type serverSecretKv struct {
	once    sync.Once
	queue   *credstore.StoreQueue
	running atomic.Bool
	passDue atomic.Bool

	// wasActive: the monitor was active at the last tick; a standby that becomes active gives
	// the server entries a full pass (a rotation made on the other monitor, versions pulled)
	wasActive atomic.Bool

	// carrierGone: orchestrator store -> when the cluster it writes through was first seen gone
	// (serverKvCarrierGoneLongEnough)
	carrierGone sync.Map

	mu          sync.Mutex
	behind      map[string]string    // entry -> why it is behind (never a value)
	lastWritten map[string]time.Time // entry -> its last successful write
	moves       map[string]*serverKvMove
}

// errServerKvNotNamed: the instance's name is not complete, its folder in a shared store is
// unknown, nothing is written.
const errServerKvNotNamed = "the instance's name is not complete (cloud18-domain, cloud18-sub-domain, cloud18-sub-domain-zone) or a part of it is not valid (1 to 63 letters, digits, '-', '_' or '.', not starting or ending with '.' or '_'): nothing is written"

func (repman *ReplicationManager) serverKvQueue() *credstore.StoreQueue {
	k := &repman.serverSecretKv
	k.once.Do(func() {
		k.queue = credstore.NewStoreQueue(func() int { return repman.Conf.SecretKvQueueLimit })
		k.behind = map[string]string{}
		k.lastWritten = map[string]time.Time{}
	})
	return k.queue
}

// serverKvRunner is the server level's store worker.
func (repman *ReplicationManager) serverKvRunner() *credstore.StoreRunner {
	k := &repman.serverSecretKv
	return &credstore.StoreRunner{
		Queue:  repman.serverKvQueue(),
		Writer: repman.serverKvWriter,
		Timeout: func() credstore.Timeout {
			return credstore.Timeout(time.Duration(repman.Conf.SecretKvWriteTimeout) * time.Second)
		},
		Retry:    func() time.Duration { return time.Duration(repman.Conf.SecretKvRetryInterval) * time.Second },
		Stopped:  repman.exit.Load,
		FullPass: repman.serverKvFullPass,
		Behind:   repman.setServerKvBehind,
		Written: func(name string) {
			k.mu.Lock()
			k.lastWritten[name] = time.Now()
			k.mu.Unlock()
		},
		WrittenItems: repman.recordServerKvWrittenAs,
		Running:      &k.running,
		Due:          &k.passDue,
	}
}

// serverKvWriter is a server entry's sink, nil when it is off or gone: a Vault, or the
// orchestrator's secret object through the cluster the entry names. A cluster missing or not on
// that orchestrator is a writer that fails with the reason, so the entry is behind and says why.
func (repman *ReplicationManager) serverKvWriter(name string) credstore.StoreWriter {
	e, ok := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, name)
	if !ok {
		return nil
	}
	if !config.IsOrchestratorSecretKvPlugin(e.Plugin) {
		return credstore.NewVaultKV(credstore.VaultSettingsFromEntry(repman.Conf, e), nil)
	}
	clusterName := repman.Conf.SecretKvSetting(e, config.ConstSecretKvOrchestratorCluster)
	cl := repman.getClusterByName(clusterName)
	if cl == nil {
		return failingStoreWriter{fmt.Errorf("cluster %q, which carries this store, is not monitored here", clusterName)}
	}
	w, err := cl.ServerOrchestratorStore(e.Plugin, repman.Conf.SecretKvSetting(e, config.ConstSecretKvKubernetesNamespace))
	if err != nil {
		return failingStoreWriter{fmt.Errorf("cluster %s: %w", clusterName, err)}
	}
	return w
}

// failingStoreWriter writes nothing and says why: every item is requeued, the entry is behind.
type failingStoreWriter struct{ err error }

func (w failingStoreWriter) WriteAll(_ context.Context, items []credstore.StoreItem, _ credstore.Timeout) ([]credstore.StoreItem, error) {
	return items, w.err
}

// setServerKvBehind records why entry is behind, "" when it is not (never a value, F10).
func (repman *ReplicationManager) setServerKvBehind(entry, why string) {
	repman.serverKvQueue()
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	if why == "" {
		delete(k.behind, entry)
		return
	}
	k.behind[entry] = config.SecretKvBehindReason(why)
}

// serverKvItems are the items writing ver of key to every active server entry ("" every one,
// else entry only). None, each entry behind, while the instance is not named.
func (repman *ReplicationManager) serverKvItems(entry, key string, ver credstore.Version, compare bool) []credstore.StoreItem {
	active := repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer)
	if len(active) == 0 || ver.Value == "" {
		return nil
	}
	name, named := repman.Conf.InstanceStoreName()
	var items []credstore.StoreItem
	for _, e := range active {
		if entry != "" && e.Name != entry {
			continue
		}
		if !named {
			repman.setServerKvBehind(e.Name, errServerKvNotNamed)
			continue
		}
		if serverKvMoving(e, name) {
			// the move writes every version in order, this one included (checkServerKvMoves)
			continue
		}
		items = append(items, credstore.StoreItem{Entry: e.Name, Path: repman.Conf.ServerSecretKvFolder(e, name), Key: key, Version: ver, Compare: compare})
		// the key the infrastructure documents for the instance's user list in its orchestrator
		// (<api host>_api-credentials, the CRM's SSH page): kept current by the mandatory store
		if e.Offer == config.ConstSecretKvOrchestratorOffer && key == apiCredentialsKey {
			if host, why := aliasHost(repman.Conf.APIPublicURL); why == "" {
				items = append(items, credstore.StoreItem{Entry: e.Name, Path: host, Key: key, Version: ver, Compare: compare, Alias: true})
			} else {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModVault, config.LvlWarn, "Server credential store %s: the user list is not written under <api host>_%s: %s", e.Name, key, why)
			}
		}
	}
	return items
}

// queueServerVersion hands the current version of server secret key to every server entry.
// Only the active monitor writes: a standby takes the server's file from the config pull.
func (repman *ReplicationManager) queueServerVersion(key string) {
	if repman.Status != ConstMonitorActif || !isServerSecretKey(key) {
		return
	}
	items := repman.serverKvItems("", key, repman.serverSecretVersion(key), false)
	if len(items) == 0 {
		return
	}
	repman.serverKvQueue().Push(items...)
	repman.startServerKvWorker()
}

// startServerKvWorker starts the server stores' worker once every cluster is initialised
// (clustersReady). Before, an orchestrator store's carrier is not listed yet: the write failed
// with "not monitored here", the store was behind (GWARN024, WARN0320 on every cluster) and the
// worker waited a whole secret-kv-retry-interval before the next try, at every start. What is
// queued or due meanwhile waits for the first tick, which comes after the clusters are ready.
func (repman *ReplicationManager) startServerKvWorker() {
	if !repman.clustersReady.Load() {
		return
	}
	repman.serverKvRunner().Start()
}

// serverSecretVersion is this instance's current version of server secret key: the version
// store's, else the chain's value now (versioning off: the config is the store).
func (repman *ReplicationManager) serverSecretVersion(key string) credstore.Version {
	if v, ok := repman.serverVersionStore().Latest(key); ok {
		return v
	}
	return credstore.Version{Value: repman.Conf.GetDecryptedValue(key), At: time.Now().UTC(), By: repman.Conf.ArbitrationSasUniqueId}
}

// onServerCredentialChange is the server chain's hook, outside its lock: each server secret
// changed goes to every server entry.
func (repman *ReplicationManager) onServerCredentialChange(changes []credstore.Change) {
	for _, ch := range changes {
		repman.queueServerVersion(ch.Key)
	}
}

// serverKvFullPass writes the current version of every server secret to every active server
// entry, where it differs (compare): at start, for an entry turned on, after a failure, after
// the versions an older release kept were taken over.
func (repman *ReplicationManager) serverKvFullPass() {
	if repman.Status != ConstMonitorActif {
		return
	}
	keys := make([]string, 0, len(serverSecretKeys))
	for key := range serverSecretKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items []credstore.StoreItem
	for _, key := range keys {
		items = append(items, repman.serverKvItems("", key, repman.serverSecretVersion(key), true)...)
	}
	if len(items) > 0 {
		repman.serverKvQueue().Push(items...)
	}
}

// startServerKvPass asks a full pass of the server entries (start, an entry added or turned
// on, versions taken over) and starts the worker. Never blocks.
func (repman *ReplicationManager) startServerKvPass() {
	if repman.Status != ConstMonitorActif || len(repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer)) == 0 {
		return
	}
	repman.serverKvQueue()
	repman.serverSecretKv.passDue.Store(true)
	repman.startServerKvWorker()
}

// ServerKvBehind is why each server entry is behind, for the states of every level.
func (repman *ReplicationManager) ServerKvBehind() map[string]string {
	repman.serverKvQueue()
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	active := map[string]bool{}
	for _, e := range repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer) {
		active[e.Name] = true
	}
	out := map[string]string{}
	for name, why := range k.behind {
		if active[name] {
			out[name] = why
		} else {
			// turned off or removed: its versions stopped, it is not behind
			delete(k.behind, name)
		}
	}
	return out
}

// checkServerSecretKvStores runs at each server tick: a store behind is a state of the server
// level (GWARN024); every cluster raises its own (WARN0320) from ServerKvBehind.
func (repman *ReplicationManager) checkServerSecretKvStores() {
	if repman.Status != ConstMonitorActif {
		repman.serverSecretKv.wasActive.Store(false)
		return
	}
	if !repman.serverSecretKv.wasActive.Swap(true) {
		repman.startServerKvPass()
	}
	if q := repman.serverKvQueue(); q.Len() > 0 || repman.serverSecretKv.passDue.Load() {
		repman.startServerKvWorker()
	}
	repman.ensureServerMarketplaceVaults()
	repman.ensureServerOrchestratorStores()
	repman.checkServerKvMoves()
	repman.checkEtcCredentials()
	for name, why := range repman.ServerKvBehind() {
		repman.SetState("GWARN024@"+name, state.State{ErrType: "WARNING", ErrKey: "GWARN024", ErrFrom: "REPMAN",
			ErrDesc: fmt.Sprintf(config.GlobalError["GWARN024"], name, why)})
	}
}

// ensureServerMarketplaceVaults gives the server level the marketplace's vault offers (the
// Cloud18 vault) on a marketplace partner that takes clusters from Cloud18 users
// (Cloud18MarketplacePartnerPublic), always on: on the marketplace the copy to the Cloud18 vault
// is not a choice (switched back on at each tick, its disable refused). An on-prem instance never
// gets it. An entry given once stays when the instance stops being a partner: nothing is removed
// or moved silently, and it may then be switched off. The
// path-prefix is the offer's, else the instance's name (InstanceStoreName, three folders), as for
// the clusters' entries; with neither, the offer is not given.
func (repman *ReplicationManager) ensureServerMarketplaceVaults() {
	c := repman.Conf
	offers := repman.MarketplaceCatalogue().Vaults
	if len(offers) == 0 || !c.Cloud18MarketplacePartnerPublic() || repman.serverSecretKvsPinned() {
		return
	}
	instance, named := c.InstanceStoreName()
	changed := false
	for _, o := range offers {
		prefix := o.PathPrefix
		if prefix == "" {
			if !named {
				repman.LogModulePrintf(c.Verbose, config.ConstLogModVault, config.LvlWarn, "Marketplace vault offer %s not given to the server level: the instance has no valid name (cloud18-domain, cloud18-sub-domain, cloud18-sub-domain-zone)", o.Name)
				continue
			}
			prefix = instance
		}
		name := config.ConstSecretKvMarketplacePrefix + o.Name
		before, existed := writtenAsOf(c.SecretKvListAt(config.SecretKvLevelServer), name)
		ok, err := c.EnsureMarketplaceVaultAt(config.SecretKvLevelServer, o, prefix, true)
		if err != nil {
			repman.LogModulePrintf(c.Verbose, config.ConstLogModVault, config.LvlWarn, "Marketplace vault offer not given to the server level: %s", err)
			continue
		}
		if e, found := c.SecretKvEntryAt(config.SecretKvLevelServer, name); found && !e.Enabled {
			// mandatory on the marketplace, whatever another store or an old switch said
			if err := c.SetSecretKvEnabledAt(config.SecretKvLevelServer, name, true); err == nil {
				ok = true
				repman.logSecurityEvent("server_secret_kv_offer", name, "", "Server credential store "+name+" switched on: on a marketplace partner the copy to the Cloud18 vault is mandatory")
			}
		}
		after, _ := writtenAsOf(c.SecretKvListAt(config.SecretKvLevelServer), name)
		if ok && existed && after == config.ConstSecretKvWrittenAsNewPlace && before != after {
			changed = true
			repman.logSecurityEvent("server_secret_kv_offer", name, "", "Server credential store "+name+" follows its marketplace offer to another place: the server secrets are copied there with their history")
			repman.LogModulePrintf(c.Verbose, config.ConstLogModVault, config.LvlWarn, "Server credential store %s follows its marketplace offer to another place: the server secrets are copied there with their history", name)
		} else if ok && !existed {
			changed = true
			repman.logSecurityEvent("server_secret_kv_offer", name, "", "Server credential store "+name+" given by the marketplace offer (marketplace partner)")
		} else if ok {
			changed = true
		}
	}
	if changed {
		repman.Save()
		repman.startServerKvPass()
	}
}

// serverOrchestratorStores are the mandatory orchestrator stores: the entry name, its plugin and
// the orchestrator it writes to.
var serverOrchestratorStores = []struct{ name, plugin, orchestrator string }{
	{config.ConstSecretKvMarketplacePrefix + "opensvc", config.ConstSecretKvPluginOpenSVC, config.ConstOrchestratorOpenSVC},
	{config.ConstSecretKvMarketplacePrefix + "kubernetes", config.ConstSecretKvPluginKubernetes, config.ConstOrchestratorKubernetes},
}

// ensureServerOrchestratorStores gives an instance whose clusters run on OpenSVC or Kubernetes its
// mandatory server stores there: the OpenSVC secret system/sec/repman, the Kubernetes Secret
// repman-system (in the carrying cluster's namespace, as the cluster level), each written through
// a monitored cluster on that orchestrator, the one it already uses while it still runs there,
// else the first by name. Always on, never removed: when replication-manager runs on an
// orchestrator, the copy of its server secrets to it is not a choice, on-prem or on the
// marketplace. An instance with no cluster on an orchestrator gets none.
func (repman *ReplicationManager) ensureServerOrchestratorStores() {
	c := repman.Conf
	if repman.serverSecretKvsPinned() {
		return
	}
	clusters := repman.clustersSnapshot() // name order
	changed := false
	for _, s := range serverOrchestratorStores {
		var carrier *cluster.Cluster
		if e, ok := c.SecretKvEntryAt(config.SecretKvLevelServer, s.name); ok {
			current := c.SecretKvSetting(e, config.ConstSecretKvOrchestratorCluster)
			if cl := repman.getClusterByName(current); cl != nil && cl.GetOrchestrator() == s.orchestrator {
				carrier = cl
				repman.serverSecretKv.carrierGone.Delete(s.name)
			} else if current != "" && !repman.serverKvCarrierGoneLongEnough(s.name) {
				// the carrier is not listed (a start or a reload) or not on that orchestrator: keep
				// it until it has been gone for serverKvCarrierGrace, a move copies every version
				continue
			}
		}
		for _, cl := range clusters {
			if carrier == nil && cl.GetOrchestrator() == s.orchestrator {
				carrier = cl
			}
		}
		if carrier == nil {
			continue // no cluster on that orchestrator: nothing to write through
		}
		namespace := ""
		if s.plugin == config.ConstSecretKvPluginKubernetes {
			namespace = carrier.Name
		}
		_, existed := c.SecretKvEntryAt(config.SecretKvLevelServer, s.name)
		ok, err := c.EnsureOrchestratorSecretKvAt(config.SecretKvLevelServer, s.name, s.plugin, carrier.Name, namespace)
		if err != nil {
			repman.LogModulePrintf(c.Verbose, config.ConstLogModVault, config.LvlWarn, "Server credential store %s not set: %s", s.name, err)
			continue
		}
		if ok {
			changed = true
			how := "added"
			if existed {
				how = "set"
			}
			repman.logSecurityEvent("server_secret_kv_offer", s.name, "", fmt.Sprintf("Server credential store %s %s through cluster %s: the copy of the server secrets to the orchestrator the clusters run on is mandatory", s.name, how, carrier.Name))
		}
	}
	if changed {
		repman.Save()
		repman.startServerKvPass()
	}
}

// serverKvCarrierGrace is how long the cluster an orchestrator store writes through must be gone
// (not monitored, or no longer on that orchestrator) before the store moves to another cluster.
const serverKvCarrierGrace = 10 * time.Minute

// serverKvCarrierGoneLongEnough records when the carrier of the store name was first seen gone,
// and says whether it has been gone for serverKvCarrierGrace.
func (repman *ReplicationManager) serverKvCarrierGoneLongEnough(name string) bool {
	since, _ := repman.serverSecretKv.carrierGone.LoadOrStore(name, time.Now())
	return time.Since(since.(time.Time)) >= serverKvCarrierGrace
}

// serverStoreMandatory: the server entry name is mandatory (serverEntryMandatory).
func (repman *ReplicationManager) serverStoreMandatory(name string) bool {
	e, ok := repman.Conf.SecretKvEntryAt(config.SecretKvLevelServer, name)
	return ok && repman.serverEntryMandatory(e)
}

// serverEntryMandatory: an entry repman sets and nobody switches off, changes or removes, and only
// these two: a mandatory orchestrator store (offer orchestrator, always), and a marketplace vault
// offer's entry (the vault plugin, from an offer) while the instance is a marketplace partner.
// Any other offer value is not mandatory until someone decides it here.
func (repman *ReplicationManager) serverEntryMandatory(e config.SecretKvConfig) bool {
	switch {
	case e.Offer == config.ConstSecretKvOrchestratorOffer:
		return true
	case e.Offer != "" && e.Plugin == config.ConstSecretKvPluginVault:
		return repman.Conf.Cloud18MarketplacePartnerPublic()
	}
	return false
}

// aliasHost is the host the user list's documented key is named after (<api host>_api-credentials),
// with why there is none: api-public-url has no host, or a loopback or unspecified one (the
// default https://127.0.0.1:10005), a name other instances would share in the same store.
func aliasHost(raw string) (string, string) {
	host := apiPublicHost(raw)
	if host == "" {
		return "", "api-public-url has no host"
	}
	if strings.EqualFold(host, "localhost") {
		return "", "api-public-url names localhost, set the instance's public address"
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return "", "api-public-url names " + host + ", set the instance's public address"
	}
	return host, ""
}

// apiPublicHost is the host of api-public-url, "" when it has none.
func apiPublicHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// writtenAsOf is the written-as of the entry named name in list, and whether it is there.
func writtenAsOf(list []config.SecretKvConfig, name string) (string, bool) {
	for _, e := range list {
		if e.Name == name {
			return e.WrittenAs, true
		}
	}
	return "", false
}

// ServerSecretKvState is a server entry's state for the API and the GUI: never a value.
type ServerSecretKvState struct {
	LastWrite time.Time `json:"lastWrite,omitempty"`
	Behind    string    `json:"behind,omitempty"`
	// Path is the entry's folder for this instance, empty while the instance is not named
	Path string `json:"path,omitempty"`
	// AliasPath is the second key of the user list in an orchestrator store, the one named
	// after the API host (<api host>_api-credentials, kept equal to the one under Path)
	AliasPath string `json:"aliasPath,omitempty"`
	// Mandatory: the entry cannot be switched off (serverStoreMandatory); the GUI shows it locked
	Mandatory bool `json:"mandatory,omitempty"`
}

// ServerSecretKvListResult is the server entries for the API: never a secret.
type ServerSecretKvListResult struct {
	Entries []config.SecretKvConfig         `json:"entries"`
	States  map[string]ServerSecretKvState  `json:"states"`
	Plugins []config.SecretKvPluginManifest `json:"plugins"`
	// Pinned: a config file sets the list, the API does not change it
	Pinned bool `json:"pinned"`
	// InstanceName is the folder's name, empty while not complete
	InstanceName string   `json:"instanceName"`
	Keys         []string `json:"keys"`
}

// serverSecretKvList is the server entries and their states.
func (repman *ReplicationManager) serverSecretKvList() ServerSecretKvListResult {
	repman.serverKvQueue()
	name, named := repman.Conf.InstanceStoreName()
	res := ServerSecretKvListResult{
		Entries: repman.Conf.SecretKvListAt(config.SecretKvLevelServer),
		States:  map[string]ServerSecretKvState{},
		Plugins: config.SecretKvPluginsAt(config.SecretKvLevelServer),
		Pinned:  repman.serverSecretKvsPinned(),
	}
	if named {
		res.InstanceName = name
	}
	for key := range serverSecretKeys {
		res.Keys = append(res.Keys, key)
	}
	sort.Strings(res.Keys)
	behind := repman.ServerKvBehind()
	mandatory := map[string]bool{}
	for _, e := range res.Entries {
		mandatory[e.Name] = repman.serverEntryMandatory(e)
	}
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, e := range res.Entries {
		st := ServerSecretKvState{LastWrite: k.lastWritten[e.Name], Behind: behind[e.Name], Mandatory: mandatory[e.Name]}
		if named {
			st.Path = repman.Conf.ServerSecretKvLocation(e, name)
			if e.Offer == config.ConstSecretKvOrchestratorOffer {
				// the second key of the same secret: two prefixes in the object, said here
				if host, why := aliasHost(repman.Conf.APIPublicURL); why == "" {
					st.AliasPath = credstore.StoreItem{Path: host, Key: apiCredentialsKey}.PrefixedKey()
				}
			}
		}
		res.States[e.Name] = st
	}
	return res
}

// serverSecretKvsPinned: a config file sets the server entries, the API does not change them.
func (repman *ReplicationManager) serverSecretKvsPinned() bool {
	_, pinned := repman.Conf.GetImmuableFlag("server-secret-kv")
	return pinned
}
