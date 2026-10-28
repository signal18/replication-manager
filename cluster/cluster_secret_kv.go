// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/state"
)

// The versioned credential stores of a cluster (#2227, doc/implementation/config/
// CREDENTIAL_STORES.md "Versioned stores"): every write of a secret of the cluster is a
// version in every active store. The local file (secret_kv-config, monitoring-secret-
// versioning) records it at the next tick, as before; each enabled vault entry gets it from
// a bounded queue and a worker, never blocking the chain or the monitor (F2). The worker's
// full pass reads every store and the newest version wins (time, then instance id); a store
// behind is WARN0269 and gets the winner, a dropped version WARN0270. Only the active monitor
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
	// applying: a newer version read from a store is becoming the current one here: the
	// chain's hook records it, the full pass writes it where it differs, no new version
	applying atomic.Bool
	// apply gives the servers a credential that changed; nil: the cluster's own setters
	apply func(key string)

	mu              sync.Mutex
	behind          map[string]string    // entry -> why it is behind (never a value)
	passed          map[string]bool      // entries given their full pass in this run
	lastWrite       map[string]time.Time // key -> when this instance last wrote it
	pinnedPaths     map[string]bool      // keys pinned to a Vault path in a config file
	dropsSeen       uint64
	dropsUnresolved bool
}

func (cluster *Cluster) storeQueue() *credstore.StoreQueue {
	c := &cluster.secretKv
	c.once.Do(func() {
		c.queue = credstore.NewStoreQueue(func() int { return cluster.Conf.SecretKvQueueLimit })
		c.behind = map[string]string{}
		c.passed = map[string]bool{}
		c.lastWrite = map[string]time.Time{}
		c.pinnedPaths = map[string]bool{}
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
	space := cluster.Conf.SecretSpace(cluster.Name)
	for _, e := range cluster.Conf.SecretKvActive() {
		if entry != "" && e.Name != entry {
			continue
		}
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

// onCredentialChange is the credential chain's hook, outside its lock: each change is a
// version in the local file (recorded at the next tick) and in every vault entry.
func (cluster *Cluster) onCredentialChange(changes []credstore.Change) {
	cluster.storeQueue()
	c := &cluster.secretKv
	now := time.Now().UTC()
	c.mu.Lock()
	for _, ch := range changes {
		c.lastWrite[ch.Key] = now
	}
	c.mu.Unlock()
	cluster.MarkSecretVersionStoreDirty()
	if c.applying.Load() {
		return
	}
	for _, ch := range changes {
		ver := credstore.Version{Value: ch.Value, At: now, By: cluster.Conf.ArbitrationSasUniqueId}
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

// localVersion is this instance's current version of key: the cache's value, written when
// this instance last wrote it or the local file last recorded it.
func (cluster *Cluster) localVersion(key string, fileAt map[string]time.Time) credstore.Version {
	c := &cluster.secretKv
	c.mu.Lock()
	at := c.lastWrite[key]
	c.mu.Unlock()
	if f, ok := fileAt[key]; ok && f.After(at) {
		at = f
	}
	return credstore.Version{Value: cluster.Conf.Secrets.Get(key).Value, At: at, By: cluster.Conf.ArbitrationSasUniqueId}
}

// fileVersionTimes are the times the local file recorded the latest version of each key.
func (cluster *Cluster) fileVersionTimes() map[string]time.Time {
	out := map[string]time.Time{}
	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return out
	}
	store, err := loadSecretVersionStore(SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name))
	if err != nil {
		return out
	}
	for key, versions := range store {
		if len(versions) == 0 {
			continue
		}
		if at, err := time.Parse(time.RFC3339, versions[len(versions)-1].RotatedAt); err == nil {
			out[key] = at
		}
	}
	return out
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
	space := cluster.Conf.SecretSpace(cluster.Name)
	fileAt := cluster.fileVersionTimes()
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
		local := cluster.localVersion(key, fileAt)
		if local.Value == "" || cluster.Conf.IsPath(local.Value) {
			continue // no value, or a Vault path not read
		}
		winner, from, engineWon := local, "", false
		type read struct {
			entry  string
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
			for _, folder := range cluster.secretFolders(key) {
				it := credstore.StoreItem{Entry: e.Name, Path: space + "/" + folder, Key: key}
				rctx, cancel := context.WithTimeout(ctx, timeout)
				ver, found, err := kv.ReadItem(rctx, client, it, "")
				cancel()
				if err != nil {
					cluster.setStoreBehind(e.Name, fmt.Sprintf("%s/%s: %s", it.Path, key, err))
					continue
				}
				reads = append(reads, read{e.Name, folder, ver, found})
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
			cluster.setStoreBehind(r.entry, fmt.Sprintf("%s/%s/%s holds another version", space, r.folder, key))
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

// applyStoreWinner makes a newer version read from a store the current one here, through the
// chain (a pinned key is left to its file; the local file records it), keeping its time, and
// gives it to the servers.
func (cluster *Cluster) applyStoreWinner(key string, winner credstore.Version, from string) {
	chain := cluster.CredentialChain()
	if err := chain.CanSet(key); err != nil {
		return
	}
	c := &cluster.secretKv
	c.applying.Store(true)
	err := chain.Set(key, winner.Value)
	c.applying.Store(false)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlWarn, "Credential stores: newer %s from %s not applied: %s", key, from, credstore.Reason(err))
		return
	}
	if !winner.At.IsZero() {
		c.mu.Lock()
		c.lastWrite[key] = winner.At
		c.mu.Unlock()
	}
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
// pinned in a config file is read at each start, as before, and WARN0271 asks the operator to
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

// CheckSecretKvStores runs at each tick on the active monitor: the full pass at start, for a
// store turned on and after drops; the worker back when versions wait; the states raised
// again: WARN0269 per store behind, WARN0270 while drops are not repaired, WARN0271 per
// credential pinned to a Vault path. It only queues: never blocks.
func (cluster *Cluster) CheckSecretKvStores() {
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
			cluster.ConfigStateMachine.AddState("WARN0271", state.State{ErrType: "WARNING", ErrFrom: "CONF", ServerUrl: key,
				ErrDesc: fmt.Sprintf(clusterError["WARN0271"], key)})
		}
	}
	if !cluster.IsActive() || cluster.SecurityStateMachine == nil {
		return
	}
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
		cluster.SecurityStateMachine.AddState("WARN0269", state.State{ErrType: "WARNING", ErrFrom: "SECURITY", ServerUrl: name,
			ErrDesc: fmt.Sprintf(clusterError["WARN0269"], name, why)})
	}
	if dropsUnresolved {
		limit := cluster.Conf.SecretKvQueueLimit
		if limit <= 0 {
			limit = credstore.DefaultStoreQueueLimit
		}
		cluster.SecurityStateMachine.AddState("WARN0270", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0270"], drops, limit)})
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
