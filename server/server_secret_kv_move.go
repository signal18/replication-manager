// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// The move (CREDENTIAL_STORES.md "The move"): when the instance's name changes (one of the
// settings cloud18-domain, cloud18-sub-domain, cloud18-sub-domain-zone; register and unregister
// do not change it), each server entry gets every kept version of every
// server secret at the new name, oldest first, from repman's own version store (never read
// from the old place). The old place keeps what it holds (repman never deletes a secret) and
// gets a moved-to key naming the new one. The entry records the name it holds the secrets
// under (SecretKvConfig.WrittenAs): until the move ends it is behind and the ordinary writes
// wait, so the newest version is never written before the older ones.

// ConstServerKvMovedTo is the key left at the old place, its value the new name.
const ConstServerKvMovedTo = "moved-to"

// serverKvMove is an entry's move in progress: where to, the versions already written there (a
// retry skips them, whatever the list became meanwhile), and whether a newer name made it moot.
// One goroutine at most per entry: a move is never replaced while it runs.
type serverKvMove struct {
	to string
	// place is the entry's place the move writes to (config.SecretKvPlace): a move whose place
	// is not the entry's any more writes nowhere it should, and what it wrote says nothing
	place     string
	written   map[string]bool
	running   bool
	cancelled bool
	lastTry   time.Time
}

// serverKvVersionID names one version of one secret, for the move's progress.
func serverKvVersionID(it credstore.StoreItem) string {
	return it.Key + "\x00" + it.Version.At.UTC().Format(time.RFC3339Nano) + "\x00" + fmt.Sprint(it.Version.By)
}

// serverKvMoving: the entry holds the secrets under another name than the instance's now.
func serverKvMoving(e config.SecretKvConfig, name string) bool {
	return e.WrittenAs != "" && e.WrittenAs != name
}

// serverKvMoveItems is every kept version of every server secret at path, each secret's
// oldest first; the current value alone when the version store keeps none.
func (repman *ReplicationManager) serverKvMoveItems(entry, path string) []credstore.StoreItem {
	keys := make([]string, 0, len(serverSecretKeys))
	for key := range serverSecretKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items []credstore.StoreItem
	for _, key := range keys {
		kept := repman.serverVersionStore().Versions(key)
		if len(kept) == 0 {
			if v := repman.serverSecretVersion(key); v.Value != "" {
				items = append(items, credstore.StoreItem{Entry: entry, Path: path, Key: key, Version: v})
			}
			continue
		}
		for _, fv := range kept {
			value := repman.Conf.DecryptSecretValue(key, fv.HashValue)
			if value == "" {
				continue
			}
			items = append(items, credstore.StoreItem{Entry: entry, Path: path, Key: key, Version: credstore.Version{Value: value, At: fv.At(), By: fv.WrittenBy}})
		}
	}
	return items
}

// checkServerKvMoves starts the move of each active server entry whose name is not the
// instance's, one at a time per entry, a failed one again after the retry interval. Never
// blocks the tick.
func (repman *ReplicationManager) checkServerKvMoves() {
	name, named := repman.Conf.InstanceStoreName()
	if !named || repman.Status != ConstMonitorActif {
		return
	}
	retry := time.Duration(repman.Conf.SecretKvRetryInterval) * time.Second
	if retry <= 0 {
		retry = time.Minute
	}
	k := &repman.serverSecretKv
	active := map[string]bool{}
	for _, e := range repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer) {
		active[e.Name] = true
		if !serverKvMoving(e, name) {
			// renamed back, or its place edited: a move left over must not be reused
			repman.dropServerKvMove(e.Name)
			continue
		}
		k.mu.Lock()
		if k.moves == nil {
			k.moves = map[string]*serverKvMove{}
		}
		m := k.moves[e.Name]
		place := repman.Conf.SecretKvPlace(e)
		if m != nil && (m.to != name || m.place != place) {
			if m.running {
				// renamed again, or its place changed, during the move: it stops at its next
				// write and commits nothing; the next tick starts the move to the newest name
				// and place
				m.cancelled = true
				k.mu.Unlock()
				continue
			}
			m = nil
		}
		if m == nil {
			m = &serverKvMove{to: name, place: place, written: map[string]bool{}}
			k.moves[e.Name] = m
		}
		start := !m.running && time.Since(m.lastTry) >= retry
		if start {
			m.running = true
			m.lastTry = time.Now()
		}
		k.mu.Unlock()
		if start {
			if repman.ServerKvBehind()[e.Name] == "" {
				// behind until moved: the new place does not hold the secrets yet
				repman.setServerKvBehind(e.Name, fmt.Sprintf("%s %s to %s", serverKvMovingFrom, e.WrittenAs, name))
			}
			go repman.moveServerKv(e, m)
		}
	}
	// an entry turned off or removed: its move goes too
	k.mu.Lock()
	var gone []string
	for entry := range k.moves {
		if !active[entry] {
			gone = append(gone, entry)
		}
	}
	k.mu.Unlock()
	for _, entry := range gone {
		repman.dropServerKvMove(entry)
	}
}

// dropServerKvMove forgets entry's move: a running one is cancelled (it removes itself), any
// other deleted with what it had written, which says nothing of another place.
func (repman *ReplicationManager) dropServerKvMove(entry string) {
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	m := k.moves[entry]
	if m == nil {
		return
	}
	if m.running {
		m.cancelled = true
		return
	}
	delete(k.moves, entry)
}

// serverKvMovingFrom starts the behind reason of a move, the only one the move clears.
const serverKvMovingFrom = "moving from"

// moveServerKv moves entry e from its WrittenAs name to m.to. Each version is one write, in
// order, the first failure ending the attempt (the next one skips what is written). It commits
// (written-as takes the new name) only when the move was not cancelled by a newer name and the
// entry still says it holds the secrets where the move started (an edit of its place forgets
// that, PutSecretKvAt).
func (repman *ReplicationManager) moveServerKv(e config.SecretKvConfig, m *serverKvMove) {
	k := &repman.serverSecretKv
	from, name := e.WrittenAs, m.to
	err := repman.moveServerKvOnce(e, m, from)
	k.mu.Lock()
	cancelled := m.cancelled
	k.mu.Unlock()
	// the move stays in k.moves, running, until the commit is done: a tick in between sees it
	// and starts no second one
	finish := func(keep bool) {
		k.mu.Lock()
		m.running = false
		if !keep && k.moves[e.Name] == m {
			delete(k.moves, e.Name)
		}
		k.mu.Unlock()
	}
	switch {
	case cancelled:
		finish(false)
		repman.clearServerKvMoving(e.Name)
		repman.Logrus.Infof("Server credential store %s: the move to %s stopped, the instance was renamed or the store changed", e.Name, name)
		return
	case err != nil:
		finish(true) // a retry skips what it wrote
		repman.setServerKvBehind(e.Name, fmt.Sprintf("%s %s to %s: %s", serverKvMovingFrom, from, name, err))
		return
	}
	// compare and set: an edit of the place meanwhile cleared written-as and wins
	committed := repman.Conf.SetSecretKvWrittenAsIfAt(config.SecretKvLevelServer, e.Name, from, name)
	finish(false)
	if !committed {
		repman.Logrus.Infof("Server credential store %s: changed during the move to %s, not recorded as moved", e.Name, name)
		repman.clearServerKvMoving(e.Name)
		return
	}
	repman.Save()
	if repman.serverSecretKvsPinned() {
		repman.Logrus.Warnf("Server credential store %s: moved to %s, but the list is set by a config file (server-secret-kv): where it holds the secrets is not saved, a rename while repman is stopped will not be moved", e.Name, name)
	}
	repman.clearServerKvMoving(e.Name)
	repman.Logrus.Infof("Server credential store %s: the server secrets moved from %s to %s", e.Name, from, name)
	// what changed during the move, in its place
	repman.startServerKvPass()
}

// clearServerKvMoving clears entry's behind state when it is the move's own.
func (repman *ReplicationManager) clearServerKvMoving(entry string) {
	if strings.HasPrefix(repman.ServerKvBehind()[entry], serverKvMovingFrom) {
		repman.setServerKvBehind(entry, "")
	}
}

func (repman *ReplicationManager) moveServerKvOnce(e config.SecretKvConfig, m *serverKvMove, from string) error {
	w := repman.serverKvWriter(e.Name)
	if w == nil {
		return fmt.Errorf("the store is off")
	}
	k := &repman.serverSecretKv
	timeout := credstore.Timeout(time.Duration(repman.Conf.SecretKvWriteTimeout) * time.Second)
	for _, it := range repman.serverKvMoveItems(e.Name, repman.Conf.ServerSecretKvFolder(e, m.to)) {
		id := serverKvVersionID(it)
		k.mu.Lock()
		done, cancelled := m.written[id], m.cancelled
		k.mu.Unlock()
		if cancelled || repman.exit.Load() {
			return fmt.Errorf("stopped")
		}
		if done {
			continue
		}
		if _, err := w.WriteAll(context.Background(), []credstore.StoreItem{it}, timeout); err != nil {
			return err
		}
		k.mu.Lock()
		m.written[id] = true
		k.mu.Unlock()
	}
	k.mu.Lock()
	cancelled := m.cancelled
	k.mu.Unlock()
	if cancelled {
		return fmt.Errorf("stopped")
	}
	if from == config.ConstSecretKvWrittenAsNewPlace {
		return nil // a new place: no old place to leave a marker at
	}
	marker := credstore.StoreItem{Entry: e.Name, Path: repman.Conf.ServerSecretKvFolder(e, from), Key: ConstServerKvMovedTo,
		Version: credstore.Version{Value: m.to, At: time.Now().UTC(), By: repman.Conf.ArbitrationSasUniqueId}}
	if _, err := w.WriteAll(context.Background(), []credstore.StoreItem{marker}, timeout); err != nil {
		// the secrets are at the new place: the marker is information, a read-only old place
		// must not hold the entry behind forever
		repman.Logrus.Warnf("Server credential store %s: the secrets are at %s, but %s could not be written at the old place %s: %s", e.Name, m.to, ConstServerKvMovedTo, from, err)
	}
	return nil
}

// recordServerKvWrittenAs: an entry's first successful write records the name it holds the
// secrets under, from where the write went (the name may have changed since it was queued).
func (repman *ReplicationManager) recordServerKvWrittenAs(entry string, items []credstore.StoreItem) {
	e, ok := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, entry)
	if !ok || e.WrittenAs != "" || len(items) == 0 {
		return
	}
	name, named := repman.Conf.InstanceStoreName()
	if !named {
		return
	}
	at := repman.Conf.ServerSecretKvFolder(e, name)
	for _, it := range items {
		if it.Path != at {
			return // written under another name: the next write records it
		}
	}
	if repman.Conf.SetSecretKvWrittenAsAt(config.SecretKvLevelServer, entry, name) {
		repman.Save()
	}
}
