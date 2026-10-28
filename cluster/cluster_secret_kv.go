// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/state"
)

// The copies of a cluster's secrets to its credential store entries that are enabled and are
// not the primary (#2227, doc/implementation/config/CREDENTIAL_STORES.md "Store entries").
// A copy is never read back and never blocks (F2): the writers queue the plain values
// (CopySecret), a worker writes them, a failing entry is a security state (WARN0269), a full
// queue drops the oldest and says so (WARN0270, T18). Only the active monitor copies.

// databaseCopyKeys are the chain's credentials a database server uses: each is copied under
// every database server of the cluster (databases/<server name>/<key>).
var databaseCopyKeys = []string{"db-servers-credential", "replication-credential", "monitoring-write-heartbeat-credential"}

func isDatabaseCopyKey(key string) bool {
	for _, k := range databaseCopyKeys {
		if k == key {
			return true
		}
	}
	return false
}

// secretKvCopies is the cluster's copy queue, its worker and what the states are built from.
type secretKvCopies struct {
	once    sync.Once
	queue   *credstore.StoreQueue
	running atomic.Bool

	mu              sync.Mutex
	failing         map[string]string // entry -> why its last pass failed (never a value)
	passed          map[string]bool   // entries given their full pass in this run
	dropsSeen       uint64
	dropsUnresolved bool
}

func (cluster *Cluster) copyQueue() *credstore.StoreQueue {
	c := &cluster.secretKv
	c.once.Do(func() {
		c.queue = credstore.NewStoreQueue(func() int { return cluster.Conf.SecretKvStoreQueueLimit })
		c.failing = map[string]string{}
		c.passed = map[string]bool{}
	})
	return c.queue
}

// copyServerName is the name of a database server in a copy's layout.
func copyServerName(srv *ServerMonitor) string {
	if srv.Name != "" {
		return srv.Name
	}
	return strings.ReplaceAll(srv.URL, ":", "_")
}

// CopySecret hands one secret of an object of the cluster to every copy: kind is
// credstore.CopyKind*, object the database server, proxy or app name, key the setting's name,
// value plain. It is the one entry point of the copies: the credential chain calls it now, the
// proxy, app and orchestrator writers (CreateSecretKeyValue*, k8sPatchSecretValues) will.
// It never blocks.
func (cluster *Cluster) CopySecret(kind, object, key, value string) {
	cluster.queueCopies("", []copySecret{{kind, object, key, value}})
}

type copySecret struct{ kind, object, key, value string }

// queueCopies queues secrets for entry ("" every copy) and starts the worker. Nothing on a
// standby monitor, nothing without a copy.
func (cluster *Cluster) queueCopies(entry string, secrets []copySecret) {
	if !cluster.IsActive() || len(secrets) == 0 {
		return
	}
	var items []credstore.StoreItem
	space := cluster.Conf.ClusterSpace(cluster.Name)
	for _, e := range cluster.Conf.SecretKvCopies() {
		if entry != "" && e.Name != entry {
			continue
		}
		for _, s := range secrets {
			if s.value == "" {
				continue
			}
			items = append(items, credstore.StoreItem{Entry: e.Name, Path: credstore.SecretPath(space, s.kind, s.object), Key: s.key, Value: s.value})
		}
	}
	if len(items) == 0 {
		return
	}
	cluster.copyQueue().Push(items...)
	cluster.startSecretCopies()
}

// queueChainCopies is the credential chain's hook: the database credentials it changed, under
// every database server. Called outside the chain's lock.
func (cluster *Cluster) queueChainCopies(changes []credstore.Change) {
	var secrets []copySecret
	for _, ch := range changes {
		if !isDatabaseCopyKey(ch.Key) {
			continue
		}
		for _, srv := range cluster.Servers {
			if srv != nil {
				secrets = append(secrets, copySecret{credstore.KindDatabases, copyServerName(srv), ch.Key, ch.Value})
			}
		}
	}
	cluster.queueCopies("", secrets)
}

// queueFullPass queues every secret the copies hold for entry: what a copy turned on gets.
func (cluster *Cluster) queueFullPass(entry string) {
	var secrets []copySecret
	for _, key := range databaseCopyKeys {
		v := cluster.Conf.Secrets.Get(key).Value
		if v == "" || cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(v) {
			continue // a Vault path not resolved is not a secret
		}
		for _, srv := range cluster.Servers {
			if srv != nil {
				secrets = append(secrets, copySecret{credstore.KindDatabases, copyServerName(srv), key, v})
			}
		}
	}
	cluster.queueCopies(entry, secrets)
}

func (cluster *Cluster) startSecretCopies() {
	if cluster.secretKv.running.CompareAndSwap(false, true) {
		go cluster.runSecretCopies()
	}
}

// secretKvCopier is the writer of an entry: the list holds secret_kv-vault entries only.
func (cluster *Cluster) secretKvCopier(e config.SecretKvConfig) credstore.StoreWriter {
	return credstore.NewVaultKV(credstore.VaultSettingsFromEntry(cluster.Conf, e), nil)
}

// runSecretCopies writes what the queue holds, entry by entry, until it is empty. A failed
// entry keeps its secrets queued (the latest value of each wins) and is tried again after
// secret-kv-copy-retry-interval.
func (cluster *Cluster) runSecretCopies() {
	c := &cluster.secretKv
	defer c.running.Store(false)
	for !cluster.exit.Load() {
		if !cluster.secretCopyPass() {
			return
		}
		retry := time.Duration(cluster.Conf.SecretKvCopyRetryInterval) * time.Second
		if retry <= 0 {
			retry = time.Minute
		}
		for end := time.Now().Add(retry); time.Now().Before(end) && !cluster.exit.Load(); {
			time.Sleep(time.Second)
		}
	}
}

// secretCopyPass writes the queue once; true when something failed and is queued again.
func (cluster *Cluster) secretCopyPass() bool {
	c := &cluster.secretKv
	q := cluster.copyQueue()
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
	timeout := credstore.Timeout(time.Duration(cluster.Conf.SecretKvCopyTimeout) * time.Second)
	retry := false
	for _, name := range order {
		e, ok := cluster.Conf.SecretKvCopy(name)
		if !ok {
			// turned off, removed or now the primary: its copies stop, what it holds stays
			cluster.setCopyFailing(name, nil)
			continue
		}
		failed, err := cluster.secretKvCopier(e).WriteAll(context.Background(), byEntry[name], timeout)
		cluster.setCopyFailing(name, err)
		if err != nil {
			q.Requeue(failed)
			retry = true
		}
	}
	if !retry {
		c.mu.Lock()
		if q.Len() == 0 && c.dropsUnresolved {
			// the queue drained after drops: the copies get every secret again
			c.dropsUnresolved = false
			c.passed = map[string]bool{}
		}
		c.mu.Unlock()
	}
	return retry
}

// setCopyFailing records why entry's last pass failed, nil when it succeeded. The error
// text names a path, never a value (F10).
func (cluster *Cluster) setCopyFailing(entry string, err error) {
	cluster.copyQueue()
	c := &cluster.secretKv
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.failing, entry)
		return
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	c.failing[entry] = msg
}

// CheckSecretKvCopies runs at each tick on the active monitor: a copy turned on (or met for
// the first time in this run) gets its full pass, a queue left behind gets its worker back,
// and the states are raised again: WARN0269 per failing entry, WARN0270 while drops are not
// repaired. Never blocks: it only queues.
func (cluster *Cluster) CheckSecretKvCopies() {
	if !cluster.IsActive() || cluster.SecurityStateMachine == nil {
		return
	}
	q := cluster.copyQueue()
	c := &cluster.secretKv
	copies := map[string]bool{}
	var due []string
	c.mu.Lock()
	for _, e := range cluster.Conf.SecretKvCopies() {
		copies[e.Name] = true
		if !c.passed[e.Name] {
			c.passed[e.Name] = true
			due = append(due, e.Name)
		}
	}
	for name := range c.passed {
		if !copies[name] {
			delete(c.passed, name) // turned off: turning it on again runs a full pass
		}
	}
	for name := range c.failing {
		if !copies[name] {
			delete(c.failing, name)
		}
	}
	if d := q.Dropped(); d > c.dropsSeen {
		c.dropsSeen, c.dropsUnresolved = d, true
	}
	failing := make(map[string]string, len(c.failing))
	for k, v := range c.failing {
		failing[k] = v
	}
	drops, dropsUnresolved := c.dropsSeen, c.dropsUnresolved
	c.mu.Unlock()

	for _, name := range due {
		cluster.queueFullPass(name)
	}
	if q.Len() > 0 {
		cluster.startSecretCopies()
	}
	for name, why := range failing {
		cluster.SecurityStateMachine.AddState("WARN0269", state.State{ErrType: config.LvlWarn, ErrFrom: "SECURITY", ServerUrl: name,
			ErrDesc: fmt.Sprintf(clusterError["WARN0269"], name, why)})
	}
	if dropsUnresolved {
		limit := cluster.Conf.SecretKvStoreQueueLimit
		if limit <= 0 {
			limit = credstore.DefaultStoreQueueLimit
		}
		cluster.SecurityStateMachine.AddState("WARN0270", state.State{ErrType: config.LvlWarn, ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0270"], drops, limit)})
	}
}
