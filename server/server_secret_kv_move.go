// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// The move (CREDENTIAL_STORES.md "The move"): when the instance's name changes (a licence
// loaded, a registration, the name window), each server entry gets every kept version of every
// server secret at the new name, oldest first, from repman's own version store (never read
// from the old place). The old place keeps what it holds (repman never deletes a secret) and
// gets a moved-to key naming the new one. The entry records the name it holds the secrets
// under (SecretKvConfig.WrittenAs): until the move ends it is behind and the ordinary writes
// wait, so the newest version is never written before the older ones.

// ConstServerKvMovedTo is the key left at the old place, its value the new name.
const ConstServerKvMovedTo = "moved-to"

// serverKvMove is an entry's move in progress: where to, and how many versions are written
// (a retry goes on from there, not from the oldest).
type serverKvMove struct {
	to      string
	done    int
	running bool
	lastTry time.Time
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
	for _, e := range repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer) {
		if !serverKvMoving(e, name) {
			continue
		}
		k.mu.Lock()
		if k.moves == nil {
			k.moves = map[string]*serverKvMove{}
		}
		m := k.moves[e.Name]
		if m == nil || m.to != name {
			// a new move, or the name changed again meanwhile: from the oldest
			m = &serverKvMove{to: name}
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
				repman.setServerKvBehind(e.Name, fmt.Sprintf("moving from %s to %s", e.WrittenAs, name))
			}
			go repman.moveServerKv(e, name)
		}
	}
}

// moveServerKv moves entry e from its WrittenAs name to name. Each version is one write, in
// order, the first failure ending the attempt (the next one goes on from it).
func (repman *ReplicationManager) moveServerKv(e config.SecretKvConfig, name string) {
	k := &repman.serverSecretKv
	from := e.WrittenAs
	err := repman.moveServerKvOnce(e, from, name)
	k.mu.Lock()
	if m := k.moves[e.Name]; m != nil {
		m.running = false
		if err == nil && m.to == name {
			delete(k.moves, e.Name)
		}
	}
	k.mu.Unlock()
	if err != nil {
		repman.setServerKvBehind(e.Name, fmt.Sprintf("moving from %s to %s: %s", from, name, err))
		return
	}
	if repman.Conf.SetSecretKvWrittenAsAt(config.SecretKvLevelServer, e.Name, name) {
		repman.Save()
	}
	repman.setServerKvBehind(e.Name, "")
	repman.Logrus.Infof("Server credential store %s: the server secrets moved from %s to %s", e.Name, from, name)
	// what changed during the move, in its place
	repman.startServerKvPass()
}

func (repman *ReplicationManager) moveServerKvOnce(e config.SecretKvConfig, from, name string) error {
	w := repman.serverKvWriter(e.Name)
	if w == nil {
		return fmt.Errorf("the store is off")
	}
	timeout := credstore.Timeout(time.Duration(repman.Conf.SecretKvWriteTimeout) * time.Second)
	items := repman.serverKvMoveItems(e.Name, repman.Conf.ServerSecretKvFolder(e, name))
	k := &repman.serverSecretKv
	k.mu.Lock()
	done := 0
	if m := k.moves[e.Name]; m != nil && m.to == name {
		done = m.done
	}
	k.mu.Unlock()
	for i := done; i < len(items); i++ {
		if repman.exit.Load() {
			return fmt.Errorf("stopping")
		}
		if _, err := w.WriteAll(context.Background(), items[i:i+1], timeout); err != nil {
			return err
		}
		k.mu.Lock()
		if m := k.moves[e.Name]; m != nil && m.to == name {
			m.done = i + 1
		}
		k.mu.Unlock()
	}
	marker := credstore.StoreItem{Entry: e.Name, Path: repman.Conf.ServerSecretKvFolder(e, from), Key: ConstServerKvMovedTo,
		Version: credstore.Version{Value: name, At: time.Now().UTC(), By: repman.Conf.ArbitrationSasUniqueId}}
	if _, err := w.WriteAll(context.Background(), []credstore.StoreItem{marker}, timeout); err != nil {
		return fmt.Errorf("%s at the old place: %w", ConstServerKvMovedTo, err)
	}
	return nil
}

// recordServerKvWrittenAs: an entry's first successful write records the name it holds the
// secrets under.
func (repman *ReplicationManager) recordServerKvWrittenAs(entry string) {
	name, named := repman.Conf.InstanceStoreName()
	if !named {
		return
	}
	e, ok := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, entry)
	if !ok || e.WrittenAs != "" {
		return
	}
	if repman.Conf.SetSecretKvWrittenAsAt(config.SecretKvLevelServer, entry, name) {
		repman.Save()
	}
}
