// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// StoreRunner writes a StoreQueue to its stores: the worker every level runs (a cluster for its
// secrets, the server for the server secrets, CREDENTIAL_STORES.md "Two levels"). One pass takes
// the queue, groups it by entry and writes each entry concurrently, so a slow or unreachable
// store never holds up the others nor the monitor (F2); a store that failed keeps its versions
// queued (the newest of each wins) and is tried again, after a full pass, every Retry. The
// owner keeps the flags (Running, Due) and what it shows (Behind, Written).
type StoreRunner struct {
	Queue *StoreQueue
	// Writer is entry's sink, nil when it has none (turned off or removed: its versions stop,
	// what the store holds stays)
	Writer  func(entry string) StoreWriter
	Timeout func() Timeout
	// Retry is the wait before a failed store is tried again (one minute when not positive)
	Retry func() time.Duration
	// Stopped ends the worker (the owner exits)
	Stopped func() bool
	// FullPass, when set, runs when Due: every secret's newest version to every store
	FullPass func()
	// Behind records why entry is behind, "" when it is not (never a value, F10); Written its
	// last successful write
	Behind  func(entry, why string)
	Written func(entry string)
	// WrittenItems, when set, gets the items an entry's successful write held (where they went)
	WrittenItems func(entry string, items []StoreItem)
	// AfterWrite runs after a pass where nothing failed (the owner's drop bookkeeping)
	AfterWrite func()
	Running    *atomic.Bool
	Due        *atomic.Bool
}

// Start runs the worker unless it runs already. Never blocks.
func (r *StoreRunner) Start() {
	if r.Running.CompareAndSwap(false, true) {
		go r.run()
	}
}

// run runs the full pass when one is due, then writes the queue, until both are done.
func (r *StoreRunner) run() {
	defer r.Running.Store(false)
	for !r.Stopped() {
		if r.Due.Swap(false) && r.FullPass != nil {
			r.FullPass()
		}
		if !r.WritePass() {
			if r.Due.Load() {
				continue
			}
			return
		}
		r.Due.Store(true)
		retry := time.Minute
		if r.Retry != nil {
			if d := r.Retry(); d > 0 {
				retry = d
			}
		}
		for end := time.Now().Add(retry); time.Now().Before(end) && !r.Stopped(); {
			time.Sleep(time.Second)
		}
	}
}

// WritePass writes the queue once; true when something failed and is queued again.
func (r *StoreRunner) WritePass() bool {
	items := r.Queue.Take()
	if len(items) == 0 {
		return false
	}
	var order []string
	byEntry := map[string][]StoreItem{}
	for _, it := range items {
		if _, ok := byEntry[it.Entry]; !ok {
			order = append(order, it.Entry)
		}
		byEntry[it.Entry] = append(byEntry[it.Entry], it)
	}
	var timeout Timeout
	if r.Timeout != nil {
		timeout = r.Timeout()
	}
	var retry atomic.Bool
	var wg sync.WaitGroup
	for _, name := range order {
		w := r.Writer(name)
		if w == nil {
			r.Behind(name, "")
			continue
		}
		wg.Add(1)
		go func(name string, w StoreWriter, items []StoreItem) {
			defer wg.Done()
			failed, err := w.WriteAll(context.Background(), items, timeout)
			if err != nil {
				r.Behind(name, err.Error())
				r.Queue.Requeue(failed)
				retry.Store(true)
				return
			}
			r.Behind(name, "")
			if r.Written != nil {
				r.Written(name)
			}
			if r.WrittenItems != nil {
				r.WrittenItems(name, items)
			}
		}(name, w, byEntry[name])
	}
	wg.Wait()
	if !retry.Load() && r.AfterWrite != nil {
		r.AfterWrite()
	}
	return retry.Load()
}
