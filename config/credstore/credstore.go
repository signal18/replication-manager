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

// ErrChangeRefused wraps the reason no store of the chain takes a change.
var ErrChangeRefused = errors.New("credential change refused")

// Store names, the credential's source in the GUI.
const (
	SourcePinned  = "config-file"    // a config file repman never writes (immutable)
	SourceDynamic = "dynamic-config" // <cluster>.toml, written by SaveCallBack (monitoring-save-config on)
	SourceRuntime = "runtime-config" // in memory only (monitoring-save-config off: the config files are the source)
	SourceDefault = "default"        // the flag's default: no store holds the key
)

// PinnedConfig is the config files repman never writes (config.toml, cluster.d):
// ImmuableFlagMap. It holds a starting value, or a Vault path; a change is refused.
type PinnedConfig struct {
	conf *config.Config
}

func (s *PinnedConfig) Name() string    { return SourcePinned }
func (s *PinnedConfig) Versioned() bool { return false }
func (s *PinnedConfig) Generates() bool { return false }

func (s *PinnedConfig) Get(key string) (string, bool, error) {
	v, ok := s.conf.ImmuableFlagMap[key]
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
// Set stores the encrypted value in DynamicFlagMap and calls the save event (in static mode
// the save writes no config file). A key a config file pins is refused in both scopes: the
// file wins at the next restart.
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
	v, ok := s.conf.DynamicFlagMap[key]
	if !ok || v == nil {
		return "", false, nil
	}
	return fmt.Sprintf("%v", v), true, nil
}

func (s *DynamicConfig) CanSet(key string) error {
	if _, pinned := s.conf.ImmuableFlagMap[key]; pinned {
		return fmt.Errorf("%s is set in a config file (immutable): a new value would be lost at the next restart", key)
	}
	return nil
}

func (s *DynamicConfig) Set(key, value string) error {
	if err := s.CanSet(key); err != nil {
		return err
	}
	if s.conf.DynamicFlagMap == nil {
		s.conf.DynamicFlagMap = make(map[string]interface{})
	}
	s.conf.DynamicFlagMap[key] = s.conf.EncryptSecretValue(value)
	if s.save != nil {
		s.save()
	}
	return nil
}

// Chain is a cluster's (or the server's) ordered list of credential stores, the config last:
// it is the starting value. Its lock serializes every read of a store and every write, and
// the cache updates that go with them (F2: two setters at once must never write a map
// concurrently).
type Chain struct {
	mu     sync.Mutex
	conf   *config.Config
	stores []CredentialStore
}

// NewConfigChain returns the chain of the internal secret_kv-config plugin: DynamicConfig,
// then PinnedConfig, then the flag defaults. save is the config save event.
func NewConfigChain(conf *config.Config, save func()) *Chain {
	return NewChain(conf, &DynamicConfig{conf: conf, save: save}, &PinnedConfig{conf: conf})
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
		if first == nil {
			first = err
		}
	}
	if first == nil {
		first = errors.New("no credential store")
	}
	return nil, fmt.Errorf("%w: %v", ErrChangeRefused, first)
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

// Set writes value, the plain credential ("user:password", or a password), into the first
// store that takes it, then updates the cache. It is the one write point of a credential.
func (c *Chain) Set(key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.writer(key)
	if err != nil {
		return err
	}
	if err := s.Set(key, value); err != nil {
		return fmt.Errorf("%s: %w", s.Name(), err)
	}
	if c.conf.Secrets == nil {
		c.conf.RebuildSecrets(c.rawValue)
	}
	old := c.conf.Secrets.Get(key)
	// the ciphertext is left empty: the save reuses the store's (GetStableEncryptedValue)
	c.conf.Secrets.Set(key, config.Secret{Value: value, OldValue: old.Value})
	return nil
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
