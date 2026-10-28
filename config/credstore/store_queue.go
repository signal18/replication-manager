// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"context"
	"sync"
	"time"
)

// Versioned stores (#2227, CREDENTIAL_STORES.md "Versioned stores"): every write of a secret
// of the cluster is a version in every active store. The vault entries are written by a
// worker from a bounded queue (T18) filled after the chain's lock, with a timeout per write:
// a write never blocks the chain, a provisioning or the monitor (F2).

// The kinds of object a secret of the cluster belongs to, the first level of its folder:
// <space>/<cluster>/<kind>/<object name>/<key>.
const (
	KindDatabases = "databases"
	KindProxies   = "proxies"
	KindApps      = "apps"
	KindArchives  = "archives"
)

// Version is a value of a secret and when, by which instance, it was written: the newest wins
// across the stores (the event log's LWW rule: the time, then the instance id).
type Version struct {
	Value string
	At    time.Time
	By    int
}

// Newer: v wins over o.
func (v Version) Newer(o Version) bool {
	if !v.At.Equal(o.At) {
		return v.At.After(o.At)
	}
	return v.By > o.By
}

// StoreItem is one version of a secret for an entry: at <Path>/<Key>, Path being
// <space>/<cluster>/<kind>/<object name>. Compare: written only when the store holds another
// value (a full pass). The value is plain: it never reaches a log (F10).
type StoreItem struct {
	Entry   string
	Path    string
	Key     string
	Version Version
	Compare bool
}

// SecretPath is the folder of an object's secrets: <space>/<cluster>/<kind>/<object name>
// (space being config.SecretSpace's).
func SecretPath(space, kind, object string) string {
	return space + "/" + kind + "/" + object
}

func (it StoreItem) id() string { return it.Entry + "\x00" + it.Path + "\x00" + it.Key }

// StoreWriter is a store the worker writes: items each within timeout; it returns those not
// written and the first error.
type StoreWriter interface {
	WriteAll(ctx context.Context, items []StoreItem, timeout Timeout) ([]StoreItem, error)
}

// Timeout is the time a store's sign-in and each of its writes may take.
type Timeout time.Duration

func (t Timeout) context(ctx context.Context) (context.Context, context.CancelFunc) {
	if t <= 0 {
		t = Timeout(DefaultStoreWriteTimeout)
	}
	return context.WithTimeout(ctx, time.Duration(t))
}

// Defaults when the settings are unset or <= 0: never unbounded, never without a timeout.
const (
	DefaultStoreQueueLimit   = 1000
	DefaultStoreWriteTimeout = 10 * time.Second
)

// StoreQueue holds the versions waiting for the worker: the latest value of each secret of
// each entry wins, at most limit secrets; once full the oldest is dropped, and counted (T18).
type StoreQueue struct {
	mu      sync.Mutex
	limit   func() int
	items   map[string]StoreItem
	order   []string // ids, oldest first
	dropped uint64
}

// NewStoreQueue is a queue of at most limit() secrets (DefaultStoreQueueLimit when <= 0).
func NewStoreQueue(limit func() int) *StoreQueue {
	return &StoreQueue{limit: limit, items: map[string]StoreItem{}}
}

func (q *StoreQueue) max() int {
	if q.limit != nil {
		if n := q.limit(); n > 0 {
			return n
		}
	}
	return DefaultStoreQueueLimit
}

// Push queues items: a secret already queued takes the new value in its place, a new one goes
// last, the oldest dropped when the queue is full.
func (q *StoreQueue) Push(items ...StoreItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, it := range items {
		q.push(it, false)
	}
}

// Requeue puts back items not written, unless a newer value of the same secret was queued
// meanwhile (the latest value wins).
func (q *StoreQueue) Requeue(items []StoreItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, it := range items {
		q.push(it, true)
	}
}

// push: call it under the lock.
func (q *StoreQueue) push(it StoreItem, keepNewer bool) {
	id := it.id()
	if cur, queued := q.items[id]; queued {
		// the latest version wins; a requeued failure never replaces what was queued since
		if !keepNewer && !cur.Version.Newer(it.Version) {
			q.items[id] = it
		}
		return
	}
	max := q.max()
	if len(q.order) >= max {
		drop := len(q.order) - max + 1
		for _, old := range q.order[:drop] {
			delete(q.items, old)
		}
		q.dropped += uint64(drop)
		// a forward reslice would keep the dropped ids in the old array: a fresh one frees them
		q.order = append(make([]string, 0, max), q.order[drop:]...)
	}
	q.items[id] = it
	q.order = append(q.order, id)
}

// Take empties the queue and returns what it held, oldest first.
func (q *StoreQueue) Take() []StoreItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]StoreItem, 0, len(q.order))
	for _, id := range q.order {
		out = append(out, q.items[id])
	}
	q.items = map[string]StoreItem{}
	q.order = nil
	return out
}

// Len is the number of secrets waiting.
func (q *StoreQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}

// Dropped is the number of secrets dropped since the start.
func (q *StoreQueue) Dropped() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}
