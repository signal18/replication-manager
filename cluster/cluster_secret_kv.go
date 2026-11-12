// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/state"
)

// The versioned credential stores of a cluster (#2227, doc/implementation/config/
// CREDENTIAL_STORES.md "Versioned stores"): every write of a secret of the cluster is a
// version in every active store. The local file (secret_kv-config, monitoring-secret-
// versioning) records it at the next tick, as before; each enabled vault entry gets it from
// a bounded queue and a worker, never blocking the chain or the monitor (F2). The worker's
// full pass reads every store and the newest version wins (time, then instance id); a store
// behind is WARN0271 and gets the winner, a dropped version WARN0272. Only the active monitor
// writes the vaults. A value never reaches a log (F10): a line names the store, path and key.

// databaseSecretKeys are the chain's credentials a database server uses: each is written
// under every database server of the cluster.
var databaseSecretKeys = []string{"db-servers-credential", "replication-credential", "monitoring-write-heartbeat-credential"}

// proxySecretKeys are the proxy credentials, by the proxy type that uses them: each is written
// under every proxy of that type.
var proxySecretKeys = map[string]string{
	"proxysql-password":     config.ConstProxySqlproxy,
	"proxysql-user":         config.ConstProxySqlproxy,
	"haproxy-password":      config.ConstProxyHaproxy,
	"maxscale-pass":         config.ConstProxyMaxscale,
	"myproxy-password":      config.ConstProxyMyProxy,
	"shardproxy-credential": config.ConstProxySpider,
	"proxyjanitor-password": config.ConstProxyJanitor,
}

// apiCredentialsKey is the instance's list of local API users (user:password,...): a server
// secret, read from the server's chain (CREDENTIAL_STORES.md "Two levels").
const apiCredentialsKey = "api-credentials"

// engineSecretKeys are the credentials a Vault database engine makes: their user is the
// engine's static role.
var engineSecretKeys = []string{"db-servers-credential", "replication-credential"}

// secretKvStores is the cluster's store queue, its worker and what the states come from.
type secretKvStores struct {
	once    sync.Once
	queue   *credstore.StoreQueue
	running atomic.Bool
	passDue atomic.Bool
	// versions: the in-memory versions of the cluster's secrets, head of its chain; the
	// local file is their copy, written by the save callback only (T25)
	versions *credstore.VersionStore
	// file: the versions' local file (the secret_kv-config plugin's): a standby loads it
	// again when the config pull changes it (rule 11)
	file *credstore.FileBinding
	// apply gives the servers a credential that changed; nil: the cluster's own setters
	apply func(key string)

	mu              sync.Mutex
	behind          map[string]string                       // entry -> why it is behind (never a value)
	passed          map[string]bool                         // entries given their full pass in this run
	pinnedPaths     map[string]bool                         // keys pinned to a Vault path in a config file
	startingCipher  map[string]string                       // starting value -> its ciphertext in the saved files
	lastWritten     map[string]time.Time                    // entry -> its last successful write
	offers          map[string]config.MarketplaceVaultOffer // offer name -> the catalogue's offer
	dropsSeen       uint64
	dropsUnresolved bool
	// orchestratorBehind: why the OpenSVC env secret keeps an old MYSQL_ROOT_PASSWORD (never
	// a value), "" when it has the current one; orchestratorTried: the last write of it;
	// orchestratorWrites numbers the writes, so a write overlapped by a later one does not
	// close the state; orchestratorChecked: the secret was read once since the start
	orchestratorBehind  string
	orchestratorTried   time.Time
	orchestratorWrites  uint64
	orchestratorChecked bool
	orchestratorWriting atomic.Bool
}

func (cluster *Cluster) storeQueue() *credstore.StoreQueue {
	c := &cluster.secretKv
	c.once.Do(func() {
		c.queue = credstore.NewStoreQueue(func() int { return cluster.Conf.SecretKvQueueLimit })
		c.behind = map[string]string{}
		c.passed = map[string]bool{}
		c.pinnedPaths = map[string]bool{}
		c.startingCipher = map[string]string{}
		c.lastWritten = map[string]time.Time{}
		c.offers = map[string]config.MarketplaceVaultOffer{}
	})
	return c.queue
}

// secretServerName is the name of a database server in a store's layout.
func secretServerName(srv *ServerMonitor) string {
	if srv.Name != "" {
		return srv.Name
	}
	return strings.ReplaceAll(srv.URL, ":", "_")
}

// secretFolders are the folders key is written under, <kind>/<object name>: every database
// server for a database credential, every proxy of its type for a proxy credential, the
// attached archive for an archive secret. None for an instance secret: never copied.
func (cluster *Cluster) secretFolders(key string) []string {
	var out []string
	switch {
	case isDatabaseSecretKey(key):
		for _, srv := range cluster.Servers {
			if srv != nil {
				out = append(out, credstore.KindDatabases+"/"+secretServerName(srv))
			}
		}
	case proxySecretKeys[key] != "":
		for _, prx := range cluster.Proxies {
			if prx != nil && prx.GetType() == proxySecretKeys[key] {
				out = append(out, credstore.KindProxies+"/"+prx.GetName())
			}
		}
	default:
		if _, ok := config.ArchiveSecretName(key); ok {
			if a := cluster.Conf.AttachedArchive(); a != "" {
				out = append(out, credstore.KindArchives+"/"+a)
			}
		}
	}
	return out
}

func isDatabaseSecretKey(key string) bool {
	for _, k := range databaseSecretKeys {
		if k == key {
			return true
		}
	}
	return false
}

// secretKeys are the keys the stores hold: every key with a folder.
func (cluster *Cluster) secretKeys() []string {
	var out []string
	for _, key := range config.SecretKeys {
		if len(cluster.secretFolders(key)) > 0 {
			out = append(out, key)
		}
	}
	return out
}

// selfVersion is a version this instance writes now.
func (cluster *Cluster) selfVersion(value string) credstore.Version {
	return credstore.Version{Value: value, At: time.Now().UTC(), By: cluster.Conf.ArbitrationSasUniqueId}
}

// CopySecret hands one version of a secret of an object of the cluster to every active
// store: kind is credstore.Kind*, object the database server, proxy, app or archive name, key
// the setting's name, value plain. The credential chain writes through it; the proxy, app and
// orchestrator writers (CreateSecretKeyValue*, k8sPatchSecretValues) will. Never blocks.
func (cluster *Cluster) CopySecret(kind, object, key, value string) {
	cluster.queueVersions("", key, []string{kind + "/" + object}, cluster.selfVersion(value), false)
}

// queueVersions queues a version of key under folders for entry ("" every active store) and
// starts the worker. Nothing on a standby monitor: it records versions in its own file only.
func (cluster *Cluster) queueVersions(entry, key string, folders []string, ver credstore.Version, compare bool) {
	if !cluster.IsActive() || ver.Value == "" || len(folders) == 0 {
		return
	}
	var items []credstore.StoreItem
	for _, e := range cluster.Conf.SecretKvActive() {
		if entry != "" && e.Name != entry {
			continue
		}
		space := cluster.Conf.SecretKvFolder(e, cluster.Name)
		for _, f := range folders {
			items = append(items, credstore.StoreItem{Entry: e.Name, Path: space + "/" + f, Key: key, Version: ver, Compare: compare})
		}
	}
	if len(items) == 0 {
		return
	}
	cluster.storeQueue().Push(items...)
	cluster.startSecretStores()
}

// onCredentialChange is the credential chain's hook, outside its lock: each version the
// version store just made goes to every vault entry, with its time and writer.
func (cluster *Cluster) onCredentialChange(changes []credstore.Change) {
	vs := cluster.versionStore()
	for _, ch := range changes {
		ver, ok := vs.Latest(ch.Key)
		if !ok || !vs.Holds(ch.Key) {
			ver = cluster.selfVersion(ch.Value)
		}
		cluster.queueVersions("", ch.Key, cluster.secretFolders(ch.Key), ver, false)
	}
}

func (cluster *Cluster) startSecretStores() {
	if cluster.secretKv.running.CompareAndSwap(false, true) {
		go cluster.runSecretStores()
	}
}

func (cluster *Cluster) storeTimeout() credstore.Timeout {
	return credstore.Timeout(time.Duration(cluster.Conf.SecretKvWriteTimeout) * time.Second)
}

func (cluster *Cluster) vaultStore(e config.SecretKvConfig) *credstore.VaultKV {
	return credstore.NewVaultKV(credstore.VaultSettingsFromEntry(cluster.Conf, e), nil)
}

// runSecretStores runs the full pass when one is due, then writes the queue, until both are
// done. A store that failed keeps its versions queued (the latest of each wins) and is tried
// again, with a full pass, after secret-kv-retry-interval.
func (cluster *Cluster) runSecretStores() {
	defer cluster.secretKv.running.Store(false)
	for !cluster.exit.Load() {
		if cluster.secretKv.passDue.Swap(false) {
			cluster.secretFullPass()
		}
		if !cluster.secretWritePass() {
			if cluster.secretKv.passDue.Load() {
				continue
			}
			return
		}
		cluster.secretKv.passDue.Store(true)
		retry := time.Duration(cluster.Conf.SecretKvRetryInterval) * time.Second
		if retry <= 0 {
			retry = time.Minute
		}
		for end := time.Now().Add(retry); time.Now().Before(end) && !cluster.exit.Load(); {
			time.Sleep(time.Second)
		}
	}
}

// secretWritePass writes the queue once; true when something failed and is queued again.
func (cluster *Cluster) secretWritePass() bool {
	c := &cluster.secretKv
	q := cluster.storeQueue()
	items := q.Take()
	if len(items) == 0 {
		return false
	}
	var order []string
	byEntry := map[string][]credstore.StoreItem{}
	for _, it := range items {
		if _, ok := byEntry[it.Entry]; !ok {
			order = append(order, it.Entry)
		}
		byEntry[it.Entry] = append(byEntry[it.Entry], it)
	}
	// Each entry's sink writes concurrently, so a slow or unreachable store (a Vault entry or
	// the orchestrator agent) never blocks the others or holds up the secret worker and the
	// monitoring loop behind it (F2). Each WriteAll bounds its own writes by storeTimeout
	// internally. setStoreBehind/setStoreWritten take the state mutex and the queue has its own
	// mutex, so the only shared scalar is retry, kept atomic.
	var retry atomic.Bool
	var wg sync.WaitGroup
	for _, name := range order {
		var w credstore.StoreWriter
		if name == orchestratorStoreEntry {
			// the reserved orchestrator sink (OpenSVC sec / K8s Secret), not a Vault entry
			if ow := cluster.orchestratorStoreWriter(); ow != nil {
				w = ow
			}
		} else if e, ok := cluster.Conf.SecretKvActiveEntry(name); ok {
			w = cluster.vaultStore(e)
		}
		if w == nil {
			// no sink: an entry turned off or removed, or an orchestrator-less cluster — its
			// versions stop, what it holds stays
			cluster.setStoreBehind(name, "")
			continue
		}
		wg.Add(1)
		go func(name string, w credstore.StoreWriter, items []credstore.StoreItem) {
			defer wg.Done()
			failed, err := w.WriteAll(context.Background(), items, cluster.storeTimeout())
			if err != nil {
				cluster.setStoreBehind(name, err.Error())
				q.Requeue(failed)
				retry.Store(true)
				return
			}
			cluster.setStoreBehind(name, "")
			cluster.setStoreWritten(name)
		}(name, w, byEntry[name])
	}
	wg.Wait()
	if !retry.Load() {
		c.mu.Lock()
		if q.Len() == 0 && c.dropsUnresolved {
			// the queue drained after drops: a full pass writes the newest of every secret
			c.dropsUnresolved = false
			c.passDue.Store(true)
		}
		c.mu.Unlock()
	}
	return retry.Load()
}

// setStoreBehind records why entry is behind, "" when it is not any more. The text names a
// path or a store, never a value (F10).
func (cluster *Cluster) setStoreBehind(entry, why string) {
	cluster.storeQueue()
	c := &cluster.secretKv
	c.mu.Lock()
	defer c.mu.Unlock()
	if why == "" {
		delete(c.behind, entry)
		return
	}
	if i := strings.IndexByte(why, '\n'); i >= 0 {
		why = why[:i]
	}
	c.behind[entry] = why
}

// localVersion is this instance's current version of key: the version store's, else the
// cache's value (the starting value) with no time, any stored version newer.
func (cluster *Cluster) localVersion(key string) credstore.Version {
	if v, ok := cluster.versionStore().Latest(key); ok {
		return v
	}
	return credstore.Version{Value: cluster.Conf.Secrets.Get(key).Value}
}

// secretFullPass reads the current version of every secret in every active store: a database
// engine's value wins first (it makes the passwords), else the newest version across the
// stores and this instance; a newer value from a store becomes the current one here, and
// every store holding another value is behind and gets the winner (written only where it
// differs). It runs in the worker: never a blocking read at load or on the tick.
func (cluster *Cluster) secretFullPass() {
	if !cluster.IsActive() {
		return
	}
	entries := cluster.Conf.SecretKvActive()
	if len(entries) == 0 {
		return
	}
	// a database engine is read first: it makes the passwords
	sort.SliceStable(entries, func(i, j int) bool {
		return cluster.vaultStore(entries[i]).Generates() && !cluster.vaultStore(entries[j]).Generates()
	})
	ctx := context.Background()
	timeout := time.Duration(cluster.storeTimeout())
	if timeout <= 0 {
		timeout = credstore.DefaultStoreWriteTimeout
	}
	// one sign-in per store and pass
	type conn struct {
		e      config.SecretKvConfig
		kv     *credstore.VaultKV
		client *vault.Client
	}
	var conns []conn
	for _, e := range entries {
		kv := cluster.vaultStore(e)
		cctx, cancel := context.WithTimeout(ctx, timeout)
		client, err := kv.Connect(cctx)
		cancel()
		if err != nil {
			cluster.setStoreBehind(e.Name, "vault login: "+err.Error())
			continue
		}
		conns = append(conns, conn{e, kv, client})
	}
	for _, key := range cluster.secretKeys() {
		local := cluster.localVersion(key)
		if local.Value == "" || cluster.Conf.IsPath(local.Value) {
			continue // no value, or a Vault path not read
		}
		winner, from, engineWon := local, "", false
		type read struct {
			entry  string
			space  string
			folder string
			ver    credstore.Version
			found  bool
		}
		var reads []read
		for _, cn := range conns {
			e, kv, client := cn.e, cn.kv, cn.client
			if kv.Generates() {
				if !isEngineKey(key) {
					continue
				}
				user, _ := splitUser(local.Value)
				rctx, cancel := context.WithTimeout(ctx, timeout)
				value, _, err := kv.StaticCreds(rctx, client, user)
				cancel()
				if err != nil {
					cluster.setStoreBehind(e.Name, fmt.Sprintf("database/static-creds/%s: %s", user, err))
					continue
				}
				// the engine makes the passwords: its value is the current one
				engineWon = true
				if value != local.Value {
					winner, from = cluster.selfVersion(value), e.Name
				}
				continue
			}
			space := cluster.Conf.SecretKvFolder(e, cluster.Name)
			for _, folder := range cluster.secretFolders(key) {
				it := credstore.StoreItem{Entry: e.Name, Path: space + "/" + folder, Key: key}
				rctx, cancel := context.WithTimeout(ctx, timeout)
				ver, found, err := kv.ReadItem(rctx, client, it, "")
				cancel()
				if err != nil {
					cluster.setStoreBehind(e.Name, fmt.Sprintf("%s/%s: %s", it.Path, key, err))
					continue
				}
				reads = append(reads, read{e.Name, space, folder, ver, found})
				if found && !engineWon && ver.Value != winner.Value && ver.Newer(winner) {
					winner, from = ver, e.Name
				}
			}
		}
		if from != "" && winner.Value != local.Value {
			cluster.applyStoreWinner(key, winner, from)
		}
		for _, r := range reads {
			if r.found && r.ver.Value == winner.Value {
				continue
			}
			cluster.setStoreBehind(r.entry, fmt.Sprintf("%s/%s/%s holds another version", r.space, r.folder, key))
			cluster.queueVersions(r.entry, key, []string{r.folder}, winner, true)
		}
	}
	// the app secrets (#2306): never read back (rule 7), each entry gets the current value
	cluster.queueAppSecrets()
}

func isEngineKey(key string) bool {
	for _, k := range engineSecretKeys {
		if k == key {
			return true
		}
	}
	return false
}

func splitUser(credential string) (string, string) {
	if i := strings.IndexByte(credential, ':'); i >= 0 {
		return credential[:i], credential[i+1:]
	}
	return credential, ""
}

// applyStoreWinner makes a newer version read from a store the current one here: the
// version store takes it with its own time and writer (no new version), the chain's cache
// reads it, the save writes the file, and the servers get it. A key pinned to a Vault path
// in a file is left to its path (rule 12).
func (cluster *Cluster) applyStoreWinner(key string, winner credstore.Version, from string) {
	c := &cluster.secretKv
	c.mu.Lock()
	pinnedPath := c.pinnedPaths[key]
	c.mu.Unlock()
	if pinnedPath {
		return
	}
	vs := cluster.versionStore()
	if !vs.Holds(key) {
		return
	}
	vs.Insert(key, winner)
	cluster.CredentialChain().Refresh()
	cluster.Save()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Credential stores: %s takes the newer version held by %s", key, from)
	cluster.applyCredential(key)
}

// applyCredential gives the servers a credential that changed.
func (cluster *Cluster) applyCredential(key string) {
	if cluster.secretKv.apply != nil {
		cluster.secretKv.apply(key)
		return
	}
	switch key {
	case "db-servers-credential":
		cluster.SetClusterMonitorCredentialsFromConfig()
		cluster.ApplyMonitorCredential()
	case "replication-credential":
		cluster.SetClusterReplicationCredentialsFromConfig()
	case apiCredentialsKey:
		cluster.LoadAPIUsers()
	}
}

// seedVaultPathSecrets reads, once, each credential whose config value is still a Vault path
// (the way Vault was declared before #2227) from the first active vault entry, and writes the
// value through the chain: the config holds the value from now on, every store gets it. A path
// pinned in a config file is read at each start, as before, and WARN0273 asks the operator to
// remove the pin.
func (cluster *Cluster) seedVaultPathSecrets() {
	entries := cluster.Conf.SecretKvActive()
	if len(entries) == 0 {
		return
	}
	var keys []string
	for _, key := range config.SecretKeys {
		if v := cluster.Conf.Secrets.Get(key).Value; cluster.Conf.IsPath(v) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	e := entries[0]
	for _, cand := range entries {
		if cand.Name == config.ConstSecretKvVaultDefault {
			e = cand
		}
	}
	kv := cluster.vaultStore(e)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.storeTimeout())+time.Duration(len(keys))*time.Duration(cluster.storeTimeout()))
	defer cancel()
	client, err := kv.Connect(ctx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Credential store %s: sign-in failed, the Vault paths of the config are not read: %s", e.Name, err)
		return
	}
	chain := cluster.CredentialChain()
	cluster.storeQueue()
	c := &cluster.secretKv
	for _, key := range keys {
		path := cluster.Conf.Secrets.Get(key).Value
		value, err := kv.Read(ctx, client, path, key)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlWarn, "Credential store %s: %s at %s not read: %s", e.Name, key, path, err)
			continue
		}
		if _, pinned := cluster.Conf.GetImmuableFlag(key); pinned {
			cluster.Conf.Secrets.Set(key, config.Secret{Value: value})
			c.mu.Lock()
			c.pinnedPaths[key] = true
			c.mu.Unlock()
			continue
		}
		if err := chain.Set(key, value); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlWarn, "Credential store %s: %s read at %s, not stored: %s", e.Name, key, path, credstore.Reason(err))
			cluster.Conf.Secrets.Set(key, config.Secret{Value: value})
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Credential store %s: %s read once at %s, its value now in the credential stores", e.Name, key, path)
	}
}

// vaultEngineEntry is the first active vault entry in database_engine mode.
func (cluster *Cluster) vaultEngineEntry() (config.SecretKvConfig, bool) {
	for _, e := range cluster.Conf.SecretKvActive() {
		if cluster.vaultStore(e).Generates() {
			return e, true
		}
	}
	return config.SecretKvConfig{}, false
}

// activeStoreKeepsVersions: a rotation can be undone, an active store keeps the versions: the
// local file with monitoring-secret-versioning on, or any vault entry.
func (cluster *Cluster) activeStoreKeepsVersions() bool {
	return cluster.Conf.IsMonitoringSecretVersioningEnabled() || len(cluster.Conf.SecretKvActive()) > 0
}

// errNoVersionedStore says no active store keeps the versions: the config is then the store
// (#2269), the version store keeps the previous value in memory only. Never a refusal.
var errNoVersionedStore = errors.New("no credential store keeps versions: monitoring-secret-versioning is off and no vault entry (secret-kv) is enabled, the config is the store")

// versionsKeepError is nil when an active store keeps the versions.
func (cluster *Cluster) versionsKeepError() error {
	if cluster.activeStoreKeepsVersions() {
		return nil
	}
	return errNoVersionedStore
}

// isVersionedKey: a secret of the cluster the version store holds (rule 1): the database and
// proxy credentials. The archive secrets stay in their archive's entry; the server secrets
// (the instance's list of API users among them) are the server's versions.
func isVersionedKey(key string) bool {
	return isDatabaseSecretKey(key) || proxySecretKeys[key] != ""
}

// keepCurrentCredentialInConfig: when no store keeps versions (versioning turned off, the vault
// entries off), the config is the store (#2269): the current version the version store still
// holds goes to the dynamic config, so the save writes it and the next start reads it.
func (cluster *Cluster) keepCurrentCredentialInConfig(key string) {
	if cluster.activeStoreKeepsVersions() {
		return
	}
	if _, pinned := cluster.Conf.GetImmuableFlag(key); pinned {
		return
	}
	// an encrypted value only: the config goes to the config repo (F9)
	if v, ok, _ := cluster.versionStore().Get(key); ok && strings.Contains(v, "hash_") {
		cluster.Conf.SetDynamicFlag(key, v)
	}
}

// versionStore is the cluster's version store, made once (a reload keeps it) and filled from
// the local file when versioning is on.
func (cluster *Cluster) versionStore() *credstore.VersionStore {
	cluster.storeQueue()
	c := &cluster.secretKv
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.versions == nil {
		c.versions = credstore.NewVersionStore(func() *config.Config { return cluster.Conf }, isVersionedKey, cluster.versionsKeepError, cluster.Save)
		c.file = &credstore.FileBinding{DataDir: func() string { return cluster.Conf.WorkingDir }, Scope: credstore.ClusterScope(cluster.Name), Loaded: cluster.handOverServerVersions}
		if cluster.Conf != nil && cluster.Conf.IsMonitoringSecretVersioningEnabled() {
			if err := c.file.LoadInto(c.versions); err != nil {
				// never overwritten until read: each save tries again (FileBinding.Save)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Credential versions: %s not read, it is not written until it is: %s", c.file.Path(), err)
			}
		}
	}
	return c.versions
}

// isServerSecret: key is a server secret, read from the server's chain, never versioned here.
func (cluster *Cluster) isServerSecret(key string) bool {
	return cluster.ServerGlobals != nil && cluster.ServerGlobals.ServerSecrets.Holds(key)
}

// handOverServerVersions gives the server the versions of server secrets an older release kept
// in this cluster's file, at each load of the file. The server writes them to its own file
// first; only then is this cluster's file written without them (at the next save). When the
// server could not write them, they stay here: a rotation is never in no file.
func (cluster *Cluster) handOverServerVersions(vs *credstore.VersionStore) {
	g := cluster.ServerGlobals
	if g == nil || g.ServerSecrets == nil || g.ServerSecrets.Held == nil || g.AdoptServerVersions == nil {
		return
	}
	taken := vs.Take(g.ServerSecrets.Held)
	if len(taken) == 0 {
		return
	}
	if err := g.AdoptServerVersions(taken); err != nil {
		vs.Merge(taken)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Credential versions: the server secrets' versions in %s are kept there, the server's file was not written: %s", cluster.secretKv.file.Path(), err)
	}
}

// reloadVersionsFromPeer: on a standby, the local file the config pull brought (written by
// the active monitor, rule 11) is merged into the version store, and the newest versions are
// the current ones here.
func (cluster *Cluster) reloadVersionsFromPeer() {
	if cluster.IsActive() || !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := cluster.versionStore()
	before := map[string]string{}
	for _, key := range databaseSecretKeys {
		before[key] = cluster.Conf.Secrets.Get(key).Value
	}
	merged, err := cluster.secretKv.file.MergeIfChanged(vs)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Credential versions: %s not read: %s", cluster.secretKv.file.Path(), err)
		return
	}
	if !merged {
		return
	}
	cluster.CredentialChain().Refresh()
	for key, old := range before {
		if cluster.Conf.Secrets.Get(key).Value != old {
			cluster.applyCredential(key)
		}
	}
}

// writeSecretVersionFile writes the local file from the version store: called by the save
// callback only (T25), versioning on, when a version changed or the file is missing. The
// values are the encrypted ones (F9); the file is in the config push.
func (cluster *Cluster) writeSecretVersionFile() error {
	// only the active repman runs the save callback (the config-sync gate): a standby reads
	// the active monitor's file from the config pull
	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return nil
	}
	vs := cluster.versionStore()
	// F9 whatever the order of events: a version kept in memory without the key (a vault read
	// with versioning off, versioning then turned on) is encrypted once the key is there, and
	// never written before.
	return cluster.secretKv.file.Save(vs, credstore.LooksEncrypted, credstore.AutoPrune(cluster.Conf))
}

// CheckSecretKvStores runs at each tick on the active monitor: the full pass at start, for a
// store turned on and after drops; the worker back when versions wait; the states raised
// again: WARN0271 per store behind, WARN0272 while drops are not repaired, WARN0273 per
// credential pinned to a Vault path, WARN0275 while the OpenSVC env secret is behind. It only
// queues: never blocks.
func (cluster *Cluster) CheckSecretKvStores() {
	// a standby takes the versions the active monitor pushed (rule 11), the server's too
	cluster.reloadVersionsFromPeer()
	if cluster.ServerGlobals != nil && cluster.ServerGlobals.CheckServerSecrets != nil {
		cluster.ServerGlobals.CheckServerSecrets()
	}
	q := cluster.storeQueue()
	c := &cluster.secretKv
	c.mu.Lock()
	pinned := make([]string, 0, len(c.pinnedPaths))
	for k := range c.pinnedPaths {
		pinned = append(pinned, k)
	}
	c.mu.Unlock()
	if cluster.ConfigStateMachine != nil {
		for _, key := range pinned {
			cluster.ConfigStateMachine.AddState("WARN0273", state.State{ErrType: "WARNING", ErrFrom: "CONF", ServerUrl: key,
				ErrDesc: fmt.Sprintf(clusterError["WARN0273"], key)})
		}
	}
	if !cluster.IsActive() || cluster.SecurityStateMachine == nil {
		return
	}
	cluster.checkOpenSVCRootPassword()
	active := map[string]bool{}
	c.mu.Lock()
	for _, e := range cluster.Conf.SecretKvActive() {
		active[e.Name] = true
		if !c.passed[e.Name] {
			c.passed[e.Name] = true
			c.passDue.Store(true)
		}
	}
	for name := range c.passed {
		if !active[name] {
			delete(c.passed, name) // turned off: turning it on again runs a full pass
		}
	}
	for name := range c.behind {
		if !active[name] {
			delete(c.behind, name)
		}
	}
	if d := q.Dropped(); d > c.dropsSeen {
		c.dropsSeen, c.dropsUnresolved = d, true
	}
	behind := make(map[string]string, len(c.behind))
	for k, v := range c.behind {
		behind[k] = v
	}
	drops, dropsUnresolved := c.dropsSeen, c.dropsUnresolved
	c.mu.Unlock()

	if q.Len() > 0 || c.passDue.Load() {
		cluster.startSecretStores()
	}
	for name, why := range behind {
		cluster.SecurityStateMachine.AddState("WARN0271", state.State{ErrType: "WARNING", ErrFrom: "SECURITY", ServerUrl: name,
			ErrDesc: fmt.Sprintf(clusterError["WARN0271"], name, why)})
	}
	if dropsUnresolved {
		limit := cluster.Conf.SecretKvQueueLimit
		if limit <= 0 {
			limit = credstore.DefaultStoreQueueLimit
		}
		cluster.SecurityStateMachine.AddState("WARN0272", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0272"], drops, limit)})
	}
}

// rotateWithVaultEngine is the rotation with a database engine entry (#2227 rule 8): Vault
// rotates the static roles of the monitoring and replication users and changes their
// passwords on the databases; the values it returns are written through the chain, so they
// are a new version in every other active store, and repman and the replicas take them.
func (cluster *Cluster) rotateWithVaultEngine(e config.SecretKvConfig) error {
	kv := cluster.vaultStore(e)
	ctx := context.Background()
	client, err := kv.Connect(ctx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation: credential store %s sign-in failed: %s", e.Name, err)
		return err
	}
	users := []struct{ key, user string }{{"db-servers-credential", cluster.GetDbUser()}, {"replication-credential", cluster.GetRplUser()}}
	var changes []credstore.Change
	rotated := map[string]bool{}
	for _, u := range users {
		if !rotated[u.user] {
			if err := kv.RotateRole(ctx, client, u.user); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation: credential store %s could not rotate static role %s: %s", e.Name, u.user, err)
				return err
			}
			rotated[u.user] = true
		}
		value, _, err := kv.StaticCreds(ctx, client, u.user)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation: credential store %s: database/static-creds/%s not read: %s", e.Name, u.user, err)
			return err
		}
		changes = append(changes, credstore.Change{Key: u.key, Value: value})
	}
	if err := cluster.CredentialChain().SetAll(changes...); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation: Vault rotated the passwords, repman could not store them: %s", err)
		return err
	}
	cluster.applyCredential("db-servers-credential")
	cluster.applyCredential("replication-credential")
	for _, s := range cluster.slaves {
		for _, ss := range s.Replications {
			if err := s.rejoinSlaveChangePassword(&ss); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Password rotation: replication password of %s not changed: %s", s.URL, err)
			}
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Password rotation by the Vault database engine of credential store %s done", e.Name)
	return nil
}

// startingValueEncrypted is a starting value as the config file holds it: encrypted (F9),
// an already encrypted one or a Vault path as it is.
func (cluster *Cluster) startingValueEncrypted(raw string) string {
	if strings.Contains(raw, "hash_") || cluster.Conf.IsPath(raw) {
		return raw
	}
	cluster.storeQueue()
	c := &cluster.secretKv
	c.mu.Lock()
	defer c.mu.Unlock()
	// encrypted once: each save writes the same bytes
	if enc, ok := c.startingCipher[raw]; ok {
		return enc
	}
	enc := cluster.Conf.EncryptSecretValue(raw)
	c.startingCipher[raw] = enc
	return enc
}

// ChainAtStart is the credential chain a start builds for this cluster from the files on disk
// (#2227 rule 1b): started is the config the start loads, the version store is filled from
// the local file as at a start. It leaves the running cluster as it is.
func (cluster *Cluster) ChainAtStart(started *config.Config) *credstore.Chain {
	vs := credstore.NewVersionStore(func() *config.Config { return started }, isVersionedKey, nil, nil)
	if started.IsMonitoringSecretVersioningEnabled() {
		if file, err := loadSecretVersionStore(SecretVersionStorePath(started.WorkingDir, cluster.Name)); err == nil {
			vs.Load(file)
		}
	}
	return credstore.NewVersionedChain(started, nil, vs)
}

// SecretVersionsInFile are the values (decrypted, for a test's comparison only) the local
// file holds for key, oldest first.
func (cluster *Cluster) SecretVersionsInFile(key string) ([]string, error) {
	file, err := loadSecretVersionStore(SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range file[key] {
		out = append(out, cluster.Conf.DecryptSecretValue(key, v.HashValue))
	}
	return out, nil
}

// SecretKvRead reads key under a database server's folder in an active vault entry: for a
// test's comparison only.
func (cluster *Cluster) SecretKvRead(e config.SecretKvConfig, server, key string) (string, bool, error) {
	return cluster.SecretKvReadObject(e, credstore.KindDatabases, server, key)
}

// SecretKvReadObject reads key under an object's folder (<kind>/<object>: databases/<server>,
// apps/<app name>, ...) in an active vault entry: for a test's comparison only.
func (cluster *Cluster) SecretKvReadObject(e config.SecretKvConfig, kind, object, key string) (string, bool, error) {
	kv := cluster.vaultStore(e)
	timeout := time.Duration(cluster.storeTimeout())
	if timeout <= 0 {
		timeout = credstore.DefaultStoreWriteTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*timeout)
	defer cancel()
	client, err := kv.Connect(ctx)
	if err != nil {
		return "", false, err
	}
	it := credstore.StoreItem{Path: cluster.Conf.SecretKvFolder(e, cluster.Name) + "/" + kind + "/" + object, Key: key}
	v, found, err := kv.ReadItem(ctx, client, it, "")
	return v.Value, found, err
}

// SecretServerName is the name of a database server in the credential stores' layout.
func SecretServerName(srv *ServerMonitor) string { return secretServerName(srv) }

// ReadVaultsAtStart reads, once at the cluster's start and before monitoring begins, the
// newest version the enabled vault entries hold for each versioned secret the local file does
// not version (versioning off, or no version yet): a database engine first, else the newest
// across the entries (rule 1b). The value becomes the current one with its own time, no new
// version. The whole read is bounded by secret-kv-start-timeout: past it, or on a failure,
// the cluster starts on what it has and the entry is behind (WARN0271); the background full
// pass catches up. Never on the monitor tick.
func (cluster *Cluster) ReadVaultsAtStart() {
	entries := cluster.Conf.SecretKvActive()
	if len(entries) == 0 {
		return
	}
	vs := cluster.versionStore()
	var keys []string
	for _, key := range cluster.secretKeys() {
		if !vs.Holds(key) {
			continue
		}
		if _, ok := vs.Latest(key); ok && cluster.Conf.IsMonitoringSecretVersioningEnabled() {
			continue // the local file holds it
		}
		if v := cluster.Conf.Secrets.Get(key).Value; v == "" || cluster.Conf.IsPath(v) {
			continue // no starting value, or a Vault path the seed reads
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return cluster.vaultStore(entries[i]).Generates() && !cluster.vaultStore(entries[j]).Generates()
	})
	timeout := time.Duration(cluster.Conf.SecretKvStartTimeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	newest := map[string]credstore.Version{}
	engine := map[string]bool{}
	from := map[string]string{}
	for _, e := range entries {
		kv := cluster.vaultStore(e)
		client, err := kv.Connect(ctx)
		if err != nil {
			cluster.setStoreBehind(e.Name, "read at start: vault login: "+err.Error())
			continue
		}
		space := cluster.Conf.SecretKvFolder(e, cluster.Name)
		for _, key := range keys {
			if kv.Generates() {
				if !isEngineKey(key) {
					continue
				}
				user, _ := splitUser(cluster.Conf.Secrets.Get(key).Value)
				value, at, err := kv.StaticCreds(ctx, client, user)
				if err != nil {
					cluster.setStoreBehind(e.Name, fmt.Sprintf("read at start: database/static-creds/%s: %s", user, err))
					continue
				}
				newest[key], engine[key], from[key] = credstore.Version{Value: value, At: at}, true, e.Name
				continue
			}
			if engine[key] {
				continue
			}
			for _, folder := range cluster.secretFolders(key) {
				it := credstore.StoreItem{Entry: e.Name, Path: space + "/" + folder, Key: key}
				ver, found, err := kv.ReadItem(ctx, client, it, "")
				if err != nil {
					cluster.setStoreBehind(e.Name, fmt.Sprintf("read at start: %s/%s: %s", it.Path, key, err))
					break
				}
				if found && (newest[key].Value == "" || ver.Newer(newest[key])) {
					newest[key], from[key] = ver, e.Name
				}
			}
		}
	}
	for _, key := range keys {
		ver, ok := newest[key]
		if !ok || ver.Value == "" || ver.Value == cluster.Conf.Secrets.Get(key).Value {
			continue
		}
		vs.Insert(key, ver)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Credential stores: %s at start is the version held by %s", key, from[key])
	}
	cluster.CredentialChain().Refresh()
}

// takeConfigFileCredentials runs at a cluster's start, before the chain is read: a secret set
// in a config file (immutable) is a starting value, the versions win over it, unless an
// operator edited it in the file since the last start: that value is then the current version
// (rule 9). Versioning on only, the value last seen is kept in the local file. A Vault path is
// read at each start instead (rule 12).
func (cluster *Cluster) takeConfigFileCredentials() {
	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := cluster.versionStore()
	for _, key := range cluster.secretKeys() {
		raw, pinned := cluster.Conf.GetImmuableFlag(key)
		if !pinned || raw == nil {
			continue
		}
		v := fmt.Sprintf("%v", raw)
		if v == "" || cluster.Conf.IsPath(v) {
			continue
		}
		if vs.TakeConfigFileValue(key, cluster.Conf.DecryptSecretValue(key, v)) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Credential stores: %s was edited in a config file, its value is the current version", key)
		}
	}
}

// IsVersionedSecretKey: key is a secret the versioned stores hold (#2227 rule 1): its config
// value is the starting value, never overwritten.
func IsVersionedSecretKey(key string) bool { return isVersionedKey(key) }

// AppendSecretVersionFile adds a version to the local versioned file of a cluster that is not
// loaded (the offline secret-store tools, #2227 rule 2): the value as given (encrypted),
// now, by instance by; written atomically in the file's format.
func AppendSecretVersionFile(path, key, hashValue string, by int) error {
	store, err := loadSecretVersionStore(path)
	if err != nil {
		return err
	}
	versions := store[key]
	n := 1
	at := time.Now().UTC()
	if len(versions) > 0 {
		n = versions[len(versions)-1].Version + 1
		at = credstore.AfterLast(at, versions[len(versions)-1].At())
	}
	store[key] = append(versions, secretVersion{Version: n, HashValue: hashValue, RotatedAt: at.UTC().Format(time.RFC3339Nano), WrittenBy: by})
	return writeSecretVersionStoreAtomic(path, store)
}

// PruneSecretVersions keeps the last keepLast versions of each secret of a loaded cluster:
// in the version store, the file written by the next save (#2227 rule 2).
func (cluster *Cluster) PruneSecretVersions(keepLast int, dryRun bool) SecretVersionStorePruneSummary {
	vs := cluster.versionStore()
	file, _ := vs.Export()
	vs.MarkDirty()
	pruned, keys, removed := pruneSecretVersionStore(file, keepLast)
	summary := SecretVersionStorePruneSummary{StorePath: SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name), KeysTotal: len(file), KeysPruned: keys, VersionsRemoved: removed, Changed: removed > 0, DryRun: dryRun}
	if !dryRun && removed > 0 {
		vs.Load(pruned)
		vs.MarkDirty()
		cluster.Save()
	}
	return summary
}

// orchestratorSecretKind is the place, in the stores' layout, of a key the orchestrator layer
// sets in the cluster's shared secret object ("env": the OpenSVC sec object, the Kubernetes
// Secret): the database root password under every database server, the shard proxy password
// under every shard proxy. The others are the instance's (the API admin password, the system
// API key, the API auth header): never copied (rule 6).
var orchestratorSecretKind = map[string]string{
	"MYSQL_ROOT_PASSWORD":      credstore.KindDatabases,
	"SHARDPROXY_ROOT_PASSWORD": credstore.KindProxies,
}

// reportOrchestratorSecret is what an orchestrator writer calls once it set a secret (rule 7):
// the value goes to the stores as a version, the orchestrator is never read back. object is
// "env", the cluster's shared secret object, or an app's name (apps/<app>/<key>).
func (cluster *Cluster) reportOrchestratorSecret(object, key, value string) {
	if value == "" || key == "SENSOR_API_KEY" {
		return
	}
	if object != "env" {
		// an app secret is written where the entry holds another value only (#2306): the save
		// already versioned it, and a provisioning that sets an unchanged value makes no version
		cluster.queueVersions("", key, []string{credstore.KindApps + "/" + object}, cluster.selfVersion(value), true)
		return
	}
	// keep the orchestrator secret current through the version queue (#2356), with its one-deep
	// _OLD history, instead of only the bespoke write: the DB root and shard proxy passwords.
	cluster.queueOrchestrator(key, value)
	switch orchestratorSecretKind[key] {
	case credstore.KindDatabases:
		for _, srv := range cluster.Servers {
			if srv != nil {
				cluster.CopySecret(credstore.KindDatabases, secretServerName(srv), key, value)
			}
		}
	case credstore.KindProxies:
		for _, prx := range cluster.Proxies {
			if prx != nil && prx.GetType() == config.ConstProxySpider {
				cluster.CopySecret(credstore.KindProxies, prx.GetName(), key, value)
			}
		}
	}
}

// IsAppSecretVersionKey reports whether key is an app secret's key in the version store
// (apps/<app name>/<key>, #2306), not a setting of the cluster's config.
func IsAppSecretVersionKey(key string) bool {
	return strings.HasPrefix(key, credstore.KindApps+"/")
}

// appSecretValues are an app's secrets (rule 6) by their key in the stores, each value in
// clear: the names provisioning writes to the orchestrator and reports (rule 7), so both land
// on apps/<app name>/<key>, plus the app's secret settings (tagged secret:"true") under their
// setting's name. Every value is decrypted with GetDecryptedEmbedded: a secret variable as
// provisioning hands it to the container (its embedded ciphertexts too: a DSN), the others
// whole. It decrypts only a token of a valid ciphertext's length; a value that starts with
// hash_ without being one is kept as it is, where GetDecryptedPassword would panic on it (a
// too short ciphertext), every save. The instance's system API key is never copied (rule 6,
// as reportOrchestratorSecret).
func (cluster *Cluster) appSecretValues(app *App) map[string]string {
	out := make(map[string]string)
	if app == nil || app.AppConfig == nil {
		return out
	}
	conf := cluster.Conf
	put := func(key, value string) {
		if value != "" && key != "SENSOR_API_KEY" {
			out[key] = value
		}
	}
	for key, stored := range app.AppConfig.SecretValues() {
		put(key, conf.GetDecryptedEmbedded(key, stored))
	}
	d := app.AppConfig.Deployment
	if d == nil {
		return out
	}
	for _, v := range d.Variables {
		if v.Type != config.VariableTypeSecret {
			continue
		}
		put(v.Name, conf.GetDecryptedEmbedded(v.Name, v.Value))
		for _, cd := range v.Conditional {
			name := config.AgentVariableName(v.Name, cd.Agent)
			put(name, conf.GetDecryptedEmbedded(name, cd.Value))
		}
	}
	for _, gc := range d.Storages.GitClones {
		if gc != nil {
			put(gc.GetVariablePrefix()+config.GitVarSuffixPass, conf.GetDecryptedEmbedded(gc.Name, gc.GitPass))
		}
	}
	for _, s3m := range d.Storages.S3Mounts {
		if s3m != nil {
			put(s3m.GetVariablePrefix()+config.S3VarSuffixSecretKey, conf.GetDecryptedEmbedded(s3m.Name, s3m.SecretKey))
		}
	}
	return out
}

// versionAppSecrets makes each app secret whose value changed since its last version a new
// version (#2227 rules 2 and 6, #2306): in the version store under apps/<app name>/<key>,
// which this save writes to the local file, and queued to every active vault entry (written
// there only when it holds another value). The save callback runs it, so it covers every
// writer of an app (API, template, reset, peer import, provisioning): the version is made at
// the save that follows the write. The value is encrypted whole: an app secret is any string,
// never a user:password list, which EncryptSecretValue would leave partly in clear. Without the
// key nothing is recorded (F9). Without the file, memory keeps the last two values of a key
// (F4). A removed app or secret keeps its versions (rule 10).
func (cluster *Cluster) versionAppSecrets() {
	if cluster.Conf == nil || cluster.Conf.SecretKey == nil {
		return
	}
	vs := cluster.versionStore()
	fileKeeps := cluster.Conf.IsMonitoringSecretVersioningEnabled()
	for _, app := range cluster.GetAppsCopy() {
		if app == nil {
			continue
		}
		folder := credstore.KindApps + "/" + app.Name
		for key, plain := range cluster.appSecretValues(app) {
			vkey := folder + "/" + key
			// Latest decrypts with DecryptSecretValue, which splits a stored value on ',' and
			// ':': the comparison holds because every app version is stored as one hash_<hex>
			// token (GetEncryptedString below), never as a user:password list
			if last, ok := vs.Latest(vkey); ok && last.Value == plain {
				continue
			}
			stored := cluster.Conf.GetEncryptedString(plain)
			if !strings.HasPrefix(stored, "hash_") || !vs.Record(vkey, stored, plain) {
				continue
			}
			if !fileKeeps {
				vs.KeepConfigValues(vkey)
			}
			cluster.queueVersions("", key, []string{folder}, cluster.selfVersion(plain), true)
		}
	}
}

// queueAppSecrets queues the current value of every app secret to every active vault entry,
// written only where it holds another value: the full pass's part for the apps (rule 4), so an
// entry turned on later gets them.
func (cluster *Cluster) queueAppSecrets() {
	vs := cluster.versionStore()
	for _, app := range cluster.GetAppsCopy() {
		if app == nil {
			continue
		}
		folder := credstore.KindApps + "/" + app.Name
		for key, plain := range cluster.appSecretValues(app) {
			ver := cluster.selfVersion(plain)
			if last, ok := vs.Latest(folder + "/" + key); ok && last.Value == plain {
				ver = last
			}
			cluster.queueVersions("", key, []string{folder}, ver, true)
		}
	}
}

// writeOpenSVCRootPassword writes the database root password in the cluster's OpenSVC env
// secret, through the v2 or v3 API the agent speaks, and reports it to the stores. A failure
// keeps WARN0275 open (checkOpenSVCRootPassword writes the key again), a success closes it:
// a reprovision would otherwise start the databases with the old password (#2255). A write
// overlapped by a later one (a rotation during a retry) leaves the state open: which value
// the agent kept is unknown, the retry writes the current one.
func (cluster *Cluster) writeOpenSVCRootPassword(svc opensvc.Collector, password string) error {
	c := &cluster.secretKv
	c.mu.Lock()
	c.orchestratorWrites++
	write := c.orchestratorWrites
	c.mu.Unlock()
	// through the store writer: it saves the superseded value to MYSQL_ROOT_PASSWORD_OLD before
	// it overwrites the key, within one bounded budget (#2356). A direct write here would leave the
	// queue nothing to save: the old password would be lost.
	_, err := cluster.openSVCStore().writeWith(context.Background(), svc, []credstore.StoreItem{{
		Entry: orchestratorStoreEntry, Key: "MYSQL_ROOT_PASSWORD", Version: cluster.selfVersion(password),
	}}, cluster.storeTimeout())
	c.mu.Lock()
	wasBehind := c.orchestratorBehind != ""
	c.orchestratorTried = time.Now()
	switch {
	case err != nil:
		c.orchestratorBehind = err.Error()
	case write != c.orchestratorWrites:
		c.orchestratorBehind = "a later write overlapped this one"
	default:
		c.orchestratorBehind = ""
	}
	c.mu.Unlock()
	if err != nil {
		// once per state: the retries of an open WARN0275 log at debug
		level := config.LvlErr
		if wasBehind {
			level = config.LvlDbg
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, level, "Can not write MYSQL_ROOT_PASSWORD in the OpenSVC secret %s/sec/env: %s", cluster.Name, err)
		return err
	}
	cluster.reportOrchestratorSecret("env", "MYSQL_ROOT_PASSWORD", password)
	return nil
}

// checkOpenSVCRootPassword runs at each tick on the active monitor. After the start it reads
// the env secret (v3 agents), every secret-kv-retry-interval until the agent answers, and
// opens WARN0275 when it holds another password than the current one: a state lost at a
// restart or a failover is found again. While the secret is
// behind, WARN0275 is raised and, every secret-kv-retry-interval, the current database
// password is written again. Both run in the background: never blocks the tick.
func (cluster *Cluster) checkOpenSVCRootPassword() {
	c := &cluster.secretKv
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		c.mu.Lock()
		c.orchestratorBehind, c.orchestratorChecked = "", false
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	why, tried, checked := c.orchestratorBehind, c.orchestratorTried, c.orchestratorChecked
	c.mu.Unlock()
	retry := time.Duration(cluster.Conf.SecretKvRetryInterval) * time.Second
	if retry <= 0 {
		retry = time.Minute
	}
	if !checked {
		// read again every interval until the agent answers: after a failover it may not yet
		if time.Since(tried) >= retry && c.orchestratorWriting.CompareAndSwap(false, true) {
			go func() {
				defer c.orchestratorWriting.Store(false)
				answered := cluster.verifyOpenSVCRootPassword(cluster.OpenSVCConnect())
				c.mu.Lock()
				if answered {
					c.orchestratorChecked = true
				} else if c.orchestratorBehind == "" {
					c.orchestratorTried = time.Now()
				}
				c.mu.Unlock()
			}()
		}
	}
	if why == "" {
		return
	}
	cluster.SecurityStateMachine.AddState("WARN0275", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0275"], cluster.Name, why)})
	if time.Since(tried) < retry || !c.orchestratorWriting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.orchestratorWriting.Store(false)
		cluster.writeOpenSVCRootPassword(cluster.OpenSVCConnect(), cluster.GetDbPass())
	}()
}

// verifyOpenSVCRootPassword compares the env secret's MYSQL_ROOT_PASSWORD with the current
// database password and marks the secret behind when they differ (the next tick writes it).
// It returns whether the agent answered: false on a v2 agent until the version is known, or
// when the agent is unreachable, so the caller reads again. A v2 agent has no read of a key:
// nothing is checked. A missing secret raises nothing: a cluster monitored, not provisioned,
// on OpenSVC has none.
func (cluster *Cluster) verifyOpenSVCRootPassword(svc opensvc.Collector) bool {
	if !svc.IsV3() {
		return cluster.GetOrchestratorVersion() == "v2" || svc.UseCollectorAPI
	}
	cur, err := svc.GetSecretKeyValueV3(cluster.Name, "env", "MYSQL_ROOT_PASSWORD")
	var se *opensvc.StatusError
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Can not read MYSQL_ROOT_PASSWORD of the OpenSVC secret %s/sec/env: %s", cluster.Name, err)
		return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
	}
	if string(cur) == cluster.GetDbPass() {
		return true
	}
	c := &cluster.secretKv
	c.mu.Lock()
	if c.orchestratorBehind == "" {
		c.orchestratorBehind = "found at start: it differs from the current password"
		c.orchestratorTried = time.Time{}
	}
	c.mu.Unlock()
	return true
}

func (cluster *Cluster) setStoreWritten(entry string) {
	c := &cluster.secretKv
	c.mu.Lock()
	c.lastWritten[entry] = time.Now()
	c.mu.Unlock()
}

// SecretKvState is a vault entry's state for the API and the GUI: never a value.
type SecretKvState struct {
	LastWrite time.Time `json:"lastWrite,omitempty"` // its last successful write in this run
	Behind    string    `json:"behind,omitempty"`    // why it is behind (WARN0271), empty when not
}

// SecretKvListResult is the vault entry list: the entries (secrets never returned, their
// names in secretsSet), each one's state, the plugins' manifests, the queue's drops (WARN0272)
// and the settings of the copies.
type SecretKvListResult struct {
	Entries      []config.SecretKvConfig         `json:"entries"`
	States       map[string]SecretKvState        `json:"states"`
	Plugins      []config.SecretKvPluginManifest `json:"plugins"`
	Pinned       bool                            `json:"pinned"` // a config file sets the list: change it there
	Queued       int                             `json:"queued"`
	Dropped      uint64                          `json:"dropped"`
	DropsPending bool                            `json:"dropsPending"` // WARN0272
	Settings     map[string]int                  `json:"settings"`
	Credentials  []CredentialInfo                `json:"credentials"`
}

// SecretKvList is the vault entry list for the API.
func (cluster *Cluster) SecretKvList() SecretKvListResult {
	q := cluster.storeQueue()
	c := &cluster.secretKv
	out := SecretKvListResult{
		Entries: cluster.Conf.SecretKvList(),
		States:  map[string]SecretKvState{},
		Plugins: config.SecretKvPlugins(),
		Pinned:  cluster.SecretKvsPinned(),
		Queued:  q.Len(),
		Dropped: q.Dropped(),
		Settings: map[string]int{
			"secret-kv-queue-limit":    cluster.Conf.SecretKvQueueLimit,
			"secret-kv-write-timeout":  cluster.Conf.SecretKvWriteTimeout,
			"secret-kv-retry-interval": cluster.Conf.SecretKvRetryInterval,
			"secret-kv-start-timeout":  cluster.Conf.SecretKvStartTimeout,
		},
		Credentials: cluster.GetCredentialsInfo(),
	}
	if out.Entries == nil {
		out.Entries = []config.SecretKvConfig{}
	}
	c.mu.Lock()
	out.DropsPending = c.dropsUnresolved
	for i, e := range out.Entries {
		out.States[e.Name] = SecretKvState{LastWrite: c.lastWritten[e.Name], Behind: c.behind[e.Name]}
		if e.Offer != "" {
			// the offer as the catalogue describes it; gone from it, its name only
			info := &config.SecretKvOffer{Name: e.Offer}
			if o, ok := c.offers[e.Offer]; ok {
				info.Title, info.Partner = o.Title, o.Partner
			}
			out.Entries[i].OfferInfo = info
		}
	}
	c.mu.Unlock()
	return out
}

// SecretKvsPinned: a config file sets the list (secret-kv): the API cannot change it.
func (cluster *Cluster) SecretKvsPinned() bool {
	_, pinned := cluster.Conf.GetImmuableFlag("secret-kv")
	return pinned
}

// ErrSecretKvsPinned is the refusal of a change of a list a config file sets.
var ErrSecretKvsPinned = errors.New("the credential stores are set by a config file (secret-kv): change them there")

// PutSecretKv adds (create) or changes a vault entry; the save writes it, a peer replays it,
// an entry turned on gets its full pass at the next tick.
func (cluster *Cluster) PutSecretKv(spec config.SecretKvSpec, create bool) (string, error) {
	if cluster.SecretKvsPinned() {
		return "", ErrSecretKvsPinned
	}
	name, err := cluster.Conf.PutSecretKv(spec, create)
	if err != nil {
		return "", err
	}
	cluster.Save()
	return name, nil
}

// SetSecretKvEnabled turns a vault entry on or off.
func (cluster *Cluster) SetSecretKvEnabled(name string, enabled bool) error {
	if cluster.SecretKvsPinned() {
		return ErrSecretKvsPinned
	}
	if err := cluster.Conf.SetSecretKvEnabled(name, enabled); err != nil {
		return err
	}
	cluster.Save()
	return nil
}

// RemoveSecretKv takes a vault entry out of the list; what it holds stays (rule 10).
func (cluster *Cluster) RemoveSecretKv(name string) error {
	if cluster.SecretKvsPinned() {
		return ErrSecretKvsPinned
	}
	if err := cluster.Conf.DeleteSecretKv(name); err != nil {
		return err
	}
	cluster.setStoreBehind(name, "")
	cluster.Save()
	return nil
}

// initialSource is where key's starting value is: a config file, the dynamic config, or the
// default (#2227 rule 1).
func (cluster *Cluster) initialSource(key string) string {
	if _, ok := cluster.Conf.GetImmuableFlag(key); ok {
		return credstore.SourcePinned
	}
	if _, ok := cluster.Conf.GetDynamicFlag(key); ok {
		return credstore.SourceDynamic
	}
	return credstore.SourceDefault
}

// EnsureMarketplaceVaults gives a Cloud18-registered cluster the entries of the marketplace's
// vault offers, off until its sponsor turns them on (#2227): the Cloud18 vault, as the Cloud18
// storages for the archives. The path-prefix is the offer's, else the instance's URI.
func (cluster *Cluster) EnsureMarketplaceVaults(offers []config.MarketplaceVaultOffer) {
	if !cluster.cloud18Registered() || len(offers) == 0 || cluster.SecretKvsPinned() {
		return
	}
	cluster.storeQueue()
	cluster.secretKv.mu.Lock()
	for _, o := range offers {
		cluster.secretKv.offers[o.Name] = o
	}
	cluster.secretKv.mu.Unlock()
	c := cluster.Conf
	uri := c.Cloud18Domain + "/" + c.Cloud18SubDomain + "-" + c.Cloud18SubDomainZone
	changed := false
	for _, o := range offers {
		prefix := o.PathPrefix
		if prefix == "" {
			prefix = uri
		}
		ok, err := c.EnsureMarketplaceVault(o, prefix)
		if err != nil {
			cluster.LogModulePrintf(c.Verbose, config.ConstLogModVault, config.LvlWarn, "Marketplace vault offer not given to the cluster: %s", err)
			continue
		}
		changed = changed || ok
	}
	if changed {
		cluster.Save()
	}
}
