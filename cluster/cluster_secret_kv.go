// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	// fileModTime: the local file as last loaded or written: a standby loads it again when
	// the config pull changes it (rule 11)
	fileModTime time.Time
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
	// orchestratorBehind: why the OpenSVC env secret kept an old MYSQL_ROOT_PASSWORD (never
	// a value), "" when it has the current one; orchestratorTried: the last write of it
	orchestratorBehind  string
	orchestratorTried   time.Time
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
	retry := false
	for _, name := range order {
		e, ok := cluster.Conf.SecretKvActiveEntry(name)
		if !ok {
			// turned off or removed: its versions stop, what it holds stays
			cluster.setStoreBehind(name, "")
			continue
		}
		failed, err := cluster.vaultStore(e).WriteAll(context.Background(), byEntry[name], cluster.storeTimeout())
		if err != nil {
			cluster.setStoreBehind(name, err.Error())
			q.Requeue(failed)
			retry = true
			continue
		}
		cluster.setStoreBehind(name, "")
		cluster.setStoreWritten(name)
	}
	if !retry {
		c.mu.Lock()
		if q.Len() == 0 && c.dropsUnresolved {
			// the queue drained after drops: a full pass writes the newest of every secret
			c.dropsUnresolved = false
			c.passDue.Store(true)
		}
		c.mu.Unlock()
	}
	return retry
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
// proxy credentials. The archive secrets stay in their archive's entry, the instance secrets
// in the config.
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
		if cluster.Conf != nil && cluster.Conf.IsMonitoringSecretVersioningEnabled() {
			path := SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name)
			if file, err := loadSecretVersionStore(path); err == nil {
				c.versions.Load(file)
				if st, err := os.Stat(path); err == nil {
					c.fileModTime = st.ModTime()
				}
			}
		}
	}
	return c.versions
}

// reloadVersionsFromPeer: on a standby, the local file the config pull brought (written by
// the active monitor, rule 11) is merged into the version store, and the newest versions are
// the current ones here.
func (cluster *Cluster) reloadVersionsFromPeer() {
	if cluster.IsActive() || !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := cluster.versionStore()
	path := SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name)
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	c := &cluster.secretKv
	c.mu.Lock()
	seen := !st.ModTime().After(c.fileModTime)
	c.fileModTime = st.ModTime()
	c.mu.Unlock()
	if seen {
		return
	}
	file, err := loadSecretVersionStore(path)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Credential versions: %s not read: %s", path, err)
		return
	}
	before := map[string]string{}
	for _, key := range databaseSecretKeys {
		before[key] = cluster.Conf.Secrets.Get(key).Value
	}
	if !vs.Merge(file) {
		return
	}
	vs.Export() // the standby does not write it back: the active monitor's file is the copy
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
	path := SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name)
	// F9 whatever the order of events: a version kept in memory without the key (a vault read
	// with versioning off, versioning then turned on) is encrypted once the key is there, and
	// never written before. The test is a backstop (a token starting with hash_), not a proof
	// of encryption.
	if !vs.EncryptKeptAsRead(containsHashPrefixToken) {
		vs.MarkDirty()
		return errors.New("a credential version is not encrypted and there is no encryption key (monitoring-key-path): the file is not written")
	}
	file, changed := vs.Export()
	if autoPrune, keepLast := cluster.getSecretVersionStoreAutoPruneSettings(); autoPrune {
		if pruned, _, removed := pruneSecretVersionStore(file, keepLast); removed > 0 {
			file, changed = pruned, true
			vs.Load(pruned)
		}
	}
	if !changed {
		return nil
	}
	if err := writeSecretVersionStoreAtomic(path, file); err != nil {
		vs.MarkDirty()
		return err
	}
	if st, err := os.Stat(path); err == nil {
		c := &cluster.secretKv
		c.mu.Lock()
		c.fileModTime = st.ModTime()
		c.mu.Unlock()
	}
	return nil
}

// CheckSecretKvStores runs at each tick on the active monitor: the full pass at start, for a
// store turned on and after drops; the worker back when versions wait; the states raised
// again: WARN0271 per store behind, WARN0272 while drops are not repaired, WARN0273 per
// credential pinned to a Vault path, WARN0275 while the OpenSVC env secret is behind. It only
// queues: never blocks.
func (cluster *Cluster) CheckSecretKvStores() {
	// a standby takes the versions the active monitor pushed (rule 11)
	cluster.reloadVersionsFromPeer()
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
	it := credstore.StoreItem{Path: cluster.Conf.SecretKvFolder(e, cluster.Name) + "/" + credstore.KindDatabases + "/" + server, Key: key}
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
	if len(versions) > 0 {
		n = versions[len(versions)-1].Version + 1
	}
	store[key] = append(versions, secretVersion{Version: n, HashValue: hashValue, RotatedAt: time.Now().UTC().Format(time.RFC3339Nano), WrittenBy: by})
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
		cluster.CopySecret(credstore.KindApps, object, key, value)
		return
	}
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

// writeOpenSVCRootPassword writes the database root password in the cluster's OpenSVC env
// secret, through the v2 or v3 API the agent speaks, and reports it to the stores. A failure
// keeps WARN0275 open (checkOpenSVCRootPassword writes the key again), a success closes it:
// a reprovision would otherwise start the databases with the old password (#2255).
func (cluster *Cluster) writeOpenSVCRootPassword(svc opensvc.Collector, password string) error {
	err := svc.CreateSecretKeyValue(cluster.Name, "env", "MYSQL_ROOT_PASSWORD", password)
	c := &cluster.secretKv
	c.mu.Lock()
	c.orchestratorTried = time.Now()
	if err != nil {
		c.orchestratorBehind = err.Error()
	} else {
		c.orchestratorBehind = ""
	}
	c.mu.Unlock()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not write MYSQL_ROOT_PASSWORD in the OpenSVC secret %s/sec/env: %s", cluster.Name, err)
		return err
	}
	cluster.reportOrchestratorSecret("env", "MYSQL_ROOT_PASSWORD", password)
	return nil
}

// checkOpenSVCRootPassword runs at each tick on the active monitor: while the OpenSVC env
// secret is behind, WARN0275 is raised and, every secret-kv-retry-interval, the current
// database password is written again in the background. Never blocks the tick.
func (cluster *Cluster) checkOpenSVCRootPassword() {
	c := &cluster.secretKv
	c.mu.Lock()
	why, tried := c.orchestratorBehind, c.orchestratorTried
	c.mu.Unlock()
	if why == "" || cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return
	}
	cluster.SecurityStateMachine.AddState("WARN0275", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0275"], cluster.Name, why)})
	retry := time.Duration(cluster.Conf.SecretKvRetryInterval) * time.Second
	if retry <= 0 {
		retry = time.Minute
	}
	if time.Since(tried) < retry || !c.orchestratorWriting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.orchestratorWriting.Store(false)
		cluster.writeOpenSVCRootPassword(cluster.OpenSVCConnect(), cluster.GetDbPass())
	}()
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
