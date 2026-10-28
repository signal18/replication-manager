// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package credstore puts every place a credential lives behind one interface, chained per
// cluster (and for the server's own config): one read rule, the first store of the chain that
// holds the key, and one write rule, the first store that accepts it (#2075, design in
// doc/implementation/config/CREDENTIAL_STORES.md). Conf.Secrets is only the chain's cache:
// Refresh rebuilds it from the stores, so a reread, a reload or a tag change finds the value
// last written (#2017).
//
// This package holds the interface, the chain and the stores of the internal
// secret_kv-config plugin (PinnedConfig, DynamicConfig). The external plugins
// (secret_kv-localstore, secret_kv-vault) come in later steps of #2075.
package credstore

import (
	"errors"
	"fmt"
	"sync"

	"github.com/signal18/replication-manager/config"
)

// CredentialStore is a place a credential lives. Repman reads and writes credentials only
// through it.
type CredentialStore interface {
	Name() string                                      // the credential's source, shown in the GUI
	Get(key string) (value string, ok bool, err error) // the raw value: encrypted, plain, or a Vault path
	Set(key, value string) error                       // a new current value (a new version if versioned)
	CanSet(key string) error                           // nil, or why not (shown in the GUI)
	Versioned() bool                                   // keeps history: a rotation can be undone
	Generates() bool                                   // makes its own passwords (Vault database engine)
}

// ErrNotHeld is a store's CanSet for a key it does not handle at all (ArchiveStore and a
// key no archive takes): never the reason of a refusal, the next store's is.
var ErrNotHeld = errors.New("not held by this store")

// ErrChangeRefused is what a refusal is (errors.Is); RefusedError carries its reason.
var ErrChangeRefused = errors.New("credential change refused")

// RefusedError is the chain's refusal of a change: Reason is the first store's, shown in the
// GUI.
type RefusedError struct {
	Reason error
}

func (e *RefusedError) Error() string        { return ErrChangeRefused.Error() + ": " + e.Reason.Error() }
func (e *RefusedError) Is(target error) bool { return target == ErrChangeRefused }
func (e *RefusedError) Unwrap() error        { return e.Reason }

// Reason is the reason of a refusal, without the "credential change refused" prefix; any
// other error as it is.
func Reason(err error) string {
	var r *RefusedError
	if errors.As(err, &r) {
		return r.Reason.Error()
	}
	return err.Error()
}

// Store names, the credential's source in the GUI.
const (
	SourcePinned  = "config-file"    // a config file repman never writes (immutable)
	SourceDynamic = "dynamic-config" // <cluster>.toml, written by SaveCallBack (monitoring-save-config on)
	SourceRuntime = "runtime-config" // in memory only (monitoring-save-config off: the config files are the source)
	SourceDefault = "default"        // the flag's default: no store holds the key
	SourceArchive = "archive"        // the connected archive's entry (#2088)
)

// ArchiveStore answers an archive's flat secrets (backup-restic-password,
// backup-restic-aws-access-secret) from the entry of the archive the cluster is connected to,
// when its plugin declares that secret (#2088). The entry holds it encrypted and the save
// writes it with the archive list (T25). No archive connected, or no value in it: the next
// stores answer. A key a config file pins is left to the file, as DynamicConfig does.
type ArchiveStore struct {
	conf *config.Config
	save func()
}

func (s *ArchiveStore) Name() string    { return SourceArchive }
func (s *ArchiveStore) Versioned() bool { return false }
func (s *ArchiveStore) Generates() bool { return false }

func (s *ArchiveStore) Get(key string) (string, bool, error) {
	name, ok := config.ArchiveSecretName(key)
	if !ok {
		return "", false, nil
	}
	if _, pinned := s.conf.GetImmuableFlag(key); pinned {
		return "", false, nil
	}
	v, ok := s.conf.ConnectedArchiveSecret(name)
	return v, ok, nil
}

func (s *ArchiveStore) CanSet(key string) error {
	name, ok := config.ArchiveSecretName(key)
	if !ok {
		return fmt.Errorf("%s is not an archive secret: %w", key, ErrNotHeld)
	}
	if _, pinned := s.conf.GetImmuableFlag(key); pinned {
		return fmt.Errorf("%s is set in a config file (immutable): a new value would be lost at the next restart", key)
	}
	if err := s.conf.CanSetConnectedArchiveSecret(name); err != nil {
		// no archive connected, or its plugin has no such secret: the flat config takes it
		return fmt.Errorf("%s: %w", err, ErrNotHeld)
	}
	return nil
}

func (s *ArchiveStore) Set(key, value string) error {
	if err := s.CanSet(key); err != nil {
		return err
	}
	name, _ := config.ArchiveSecretName(key)
	return s.conf.SetConnectedArchiveSecret(name, s.conf.EncryptSecretValue(value), true)
}

func (s *ArchiveStore) saveEvent() {
	if s.save != nil {
		s.save()
	}
}

func (s *ArchiveStore) snapshot(key string) (interface{}, bool) {
	v, ok, _ := s.Get(key)
	return v, ok
}

func (s *ArchiveStore) restore(key string, raw interface{}, present bool) {
	name, ok := config.ArchiveSecretName(key)
	if !ok {
		return
	}
	v := ""
	if raw != nil {
		v = fmt.Sprintf("%v", raw)
	}
	s.conf.SetConnectedArchiveSecret(name, v, present)
}

// PinnedConfig is the config files repman never writes (config.toml, cluster.d):
// ImmuableFlagMap. It holds a starting value, or a Vault path; a change is refused.
type PinnedConfig struct {
	conf *config.Config
}

func (s *PinnedConfig) Name() string    { return SourcePinned }
func (s *PinnedConfig) Versioned() bool { return false }
func (s *PinnedConfig) Generates() bool { return false }

func (s *PinnedConfig) Get(key string) (string, bool, error) {
	v, ok := s.conf.GetImmuableFlag(key)
	if !ok || v == nil {
		return "", false, nil
	}
	return fmt.Sprintf("%v", v), true, nil
}

func (s *PinnedConfig) CanSet(key string) error {
	return fmt.Errorf("%s is set in a config file (immutable): repman never writes it, a change would be lost at the next restart", key)
}

func (s *PinnedConfig) Set(key, value string) error {
	return s.CanSet(key)
}

// DynamicConfig is the config overlay on top of the files, DynamicFlagMap, in the scope
// monitoring-save-config sets:
//   - on, dynamic mode: <cluster>.toml (for the server: its dynamic config), which
//     SaveCallBack writes (T25) and the next start loads back;
//   - off, static mode: memory only. The config files are the source: a change survives a
//     reread (a tag change) and is gone at the next restart, as that mode intends.
//
// Set stores the encrypted value in DynamicFlagMap; the chain then calls the save event,
// once its lock is released (in static mode the save writes no config file). A key a config
// file pins is refused in both scopes: the file wins at the next restart.
type DynamicConfig struct {
	conf *config.Config
	save func() // the save event: cluster.Save, no I/O
}

func (s *DynamicConfig) Name() string {
	if s.Persistent() {
		return SourceDynamic
	}
	return SourceRuntime
}

// Persistent reports whether a change survives a restart: dynamic mode.
func (s *DynamicConfig) Persistent() bool { return s.conf.ConfRewrite }
func (s *DynamicConfig) Versioned() bool  { return false }
func (s *DynamicConfig) Generates() bool  { return false }

func (s *DynamicConfig) Get(key string) (string, bool, error) {
	v, ok := s.conf.GetDynamicFlag(key)
	if !ok || v == nil {
		return "", false, nil
	}
	return fmt.Sprintf("%v", v), true, nil
}

func (s *DynamicConfig) CanSet(key string) error {
	if _, pinned := s.conf.GetImmuableFlag(key); pinned {
		return fmt.Errorf("%s is set in a config file (immutable): a new value would be lost at the next restart", key)
	}
	return nil
}

func (s *DynamicConfig) Set(key, value string) error {
	if err := s.CanSet(key); err != nil {
		return err
	}
	s.conf.SetDynamicFlag(key, s.conf.EncryptSecretValue(value))
	return nil
}

// saveEvent is called by the chain after a change, outside its lock: the save event may
// reach the chain again.
func (s *DynamicConfig) saveEvent() {
	if s.save != nil {
		s.save()
	}
}

// saver is a store whose changes need a save event.
type saver interface{ saveEvent() }

// restorer is a store that can put a key back exactly as it was, absent included: a failed
// SetAll then leaves no trace in it.
type restorer interface {
	snapshot(key string) (raw interface{}, present bool)
	restore(key string, raw interface{}, present bool)
}

func (s *DynamicConfig) snapshot(key string) (interface{}, bool) {
	return s.conf.GetDynamicFlag(key)
}

func (s *DynamicConfig) restore(key string, raw interface{}, present bool) {
	if !present {
		s.conf.DeleteDynamicFlag(key)
		return
	}
	s.conf.SetDynamicFlag(key, raw)
}

// Chain is a cluster's (or the server's) ordered list of credential stores, the config last:
// it is the starting value. Its lock serializes every read of a store and every write, and
// the cache updates that go with them (F2: two setters at once must never write a map
// concurrently).
type Chain struct {
	mu     sync.Mutex
	conf   *config.Config
	stores []CredentialStore
	// copies is told the plain values of each successful SetAll, outside the lock: the
	// copies to the entries that are not the primary (#2227). It must never block.
	copies func([]Change)
}

// OnSet sets the hook told the changes of each successful SetAll, called outside the lock:
// it queues the copies (#2227) and must never block.
func (c *Chain) OnSet(copies func([]Change)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.copies = copies
}

// NewConfigChain returns the chain of the internal secret_kv-config plugin: the connected
// archive's secrets (ArchiveStore, #2088), DynamicConfig, then PinnedConfig, then the flag
// defaults. save is the config save event.
func NewConfigChain(conf *config.Config, save func()) *Chain {
	return NewChain(conf, &ArchiveStore{conf: conf, save: save}, &DynamicConfig{conf: conf, save: save}, &PinnedConfig{conf: conf})
}

// NewVersionedChain is a cluster's chain (#2227): its versioned store first, then the
// internal secret_kv-config stores. The config keeps the starting value of the secrets the
// version store holds.
func NewVersionedChain(conf *config.Config, save func(), versions *VersionStore) *Chain {
	return NewChain(conf, versions, &ArchiveStore{conf: conf, save: save}, &DynamicConfig{conf: conf, save: save}, &PinnedConfig{conf: conf})
}

// NewChain returns a chain of stores, read and written in their order.
func NewChain(conf *config.Config, stores ...CredentialStore) *Chain {
	return &Chain{conf: conf, stores: stores}
}

// Conf is the config the chain reads and writes.
func (c *Chain) Conf() *config.Config { return c.conf }

// raw returns the raw value of key and its source: the first store holding it, else the
// flag default. Call it under the lock.
func (c *Chain) raw(key string) (string, string, bool) {
	for _, s := range c.stores {
		if v, ok, err := s.Get(key); err == nil && ok {
			return v, s.Name(), true
		}
	}
	if v, ok := c.conf.DefaultFlagMap[key]; ok && v != nil {
		return fmt.Sprintf("%v", v), SourceDefault, true
	}
	return "", SourceDefault, false
}

// Source is the name of the store key is read from, SourceDefault when none holds it.
func (c *Chain) Source(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, source, _ := c.raw(key)
	return source
}

// CanSet is nil when a store of the chain takes a change of key, else why none does: the
// first store's reason.
func (c *Chain) CanSet(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.writer(key)
	return err
}

// writer returns the first store that takes a change of key. Call it under the lock.
func (c *Chain) writer(key string) (CredentialStore, error) {
	var first error
	for _, s := range c.stores {
		err := s.CanSet(key)
		if err == nil {
			return s, nil
		}
		if isHeldRefusal(err) {
			// the key is this store's: no later store takes it instead (#2227 rule 1)
			return nil, &RefusedError{Reason: errors.Unwrap(err)}
		}
		if first == nil && !errors.Is(err, ErrNotHeld) {
			first = err
		}
	}
	if first == nil {
		first = errors.New("no credential store")
	}
	return nil, &RefusedError{Reason: first}
}

// Persists reports whether the store that would take a change of key keeps it across a
// restart: false in static mode (monitoring-save-config off), and when no store takes it.
func (c *Chain) Persists(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.writer(key)
	if err != nil {
		return false
	}
	if p, ok := s.(interface{ Persistent() bool }); ok {
		return p.Persistent()
	}
	return true
}

// Versioned reports whether the store that would take a change of key keeps history: a
// rotation of key can then be undone.
func (c *Chain) Versioned(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.writer(key)
	return err == nil && s.Versioned()
}

// Change is a new value for a credential: the plain "user:password", or a password.
type Change struct {
	Key, Value string
}

// Set writes value, the plain credential, into the first store that takes it, then updates
// the cache. It is the one write point of a credential.
func (c *Chain) Set(key, value string) error {
	return c.SetAll(Change{Key: key, Value: value})
}

// SetAll writes changes that go together (a rotation's credentials) as one unit: every key
// is checked before any is written, under one lock, and a store failing midway has the
// ones already written put back to their former value; the cache changes only once all are
// written. A store that can (restorer) gets each key back exactly, absent included. Then
// the save events, outside the lock.
//
// A key given twice is written twice, in order: the last value wins, and a rollback still
// puts back the value from before the call (every key is snapshot before any write).
func (c *Chain) SetAll(changes ...Change) error {
	c.mu.Lock()
	writers := make([]CredentialStore, len(changes))
	for i, ch := range changes {
		w, err := c.writer(ch.Key)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		writers[i] = w
	}
	if c.conf.Secrets == nil {
		c.conf.RebuildSecrets(c.rawValue)
	}
	type before struct {
		raw     interface{}
		present bool
	}
	befores := make([]before, len(changes))
	for i, ch := range changes {
		if r, ok := writers[i].(restorer); ok {
			befores[i].raw, befores[i].present = r.snapshot(ch.Key)
		}
	}
	for i, ch := range changes {
		if err := writers[i].Set(ch.Key, ch.Value); err != nil {
			errs := []error{fmt.Errorf("%s: %s: %w", writers[i].Name(), ch.Key, err)}
			for j := i - 1; j >= 0; j-- {
				if r, ok := writers[j].(restorer); ok {
					r.restore(changes[j].Key, befores[j].raw, befores[j].present)
					continue
				}
				// the cache still holds the former value
				if uerr := writers[j].Set(changes[j].Key, c.conf.Secrets.Get(changes[j].Key).Value); uerr != nil {
					errs = append(errs, fmt.Errorf("%s: %s not put back: %w", writers[j].Name(), changes[j].Key, uerr))
				}
			}
			c.mu.Unlock()
			c.saveEvents(writers[:i])
			return errors.Join(errs...)
		}
	}
	for _, ch := range changes {
		old := c.conf.Secrets.Get(ch.Key)
		// the ciphertext is left empty: the save reuses the store's (GetStableEncryptedValue)
		c.conf.Secrets.Set(ch.Key, config.Secret{Value: ch.Value, OldValue: old.Value})
	}
	copies := c.copies
	c.mu.Unlock()
	c.saveEvents(writers)
	if copies != nil {
		copies(changes)
	}
	return nil
}

// saveEvents calls each written store's save event once. Call it without the lock.
func (c *Chain) saveEvents(writers []CredentialStore) {
	seen := map[CredentialStore]bool{}
	for _, w := range writers {
		if sv, ok := w.(saver); ok && !seen[w] {
			seen[w] = true
			sv.saveEvent()
		}
	}
}

// Refresh rebuilds the cache, Conf.Secrets, from the stores. Safe from anywhere at any time:
// every write went into a store, so it finds the value last written.
func (c *Chain) Refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conf.RebuildSecrets(c.rawValue)
}

// rawValue is raw as RebuildSecrets reads it. Call it under the lock.
func (c *Chain) rawValue(key string) (interface{}, bool) {
	v, _, ok := c.raw(key)
	if !ok {
		return nil, false
	}
	return v, true
}
