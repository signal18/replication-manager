// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "sync"

// SecretMap holds the decrypted secrets of a configuration behind a lock (#2039): API
// handlers, password rotation, Vault and the credential setters write them while the
// monitor reads them, and a Go map written concurrently stops the process (F2). Config
// holds a *SecretMap: copies of a Config (ReloadConfig, a cluster's Config) share it, as
// they shared the map, until their own DecryptSecretsFromConfig gives them their own.
// The methods tolerate a nil receiver (reads see an empty store, writes do nothing).
type SecretMap struct {
	mu sync.RWMutex
	m  map[string]Secret
	// owner is the Config that built the store in DecryptSecretsFromConfig, set once at
	// creation: a decrypt on another Config (a copy) builds its own store
	owner *Config
}

// NewSecrets returns a store holding a copy of m (nil: empty).
func NewSecrets(m map[string]Secret) *SecretMap {
	s := &SecretMap{m: make(map[string]Secret, len(m))}
	for k, v := range m {
		s.m[k] = v
	}
	return s
}

// Get returns the secret of key, the zero Secret when absent.
func (s *SecretMap) Get(key string) Secret {
	v, _ := s.Lookup(key)
	return v
}

// Lookup returns the secret of key and whether it is present.
func (s *SecretMap) Lookup(key string) (Secret, bool) {
	if s == nil {
		return Secret{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	return v, ok
}

// Has reports whether key is present.
func (s *SecretMap) Has(key string) bool {
	_, ok := s.Lookup(key)
	return ok
}

// Set stores the secret of key. On a nil store it does nothing: a write must not crash
// the monitor (F2); a Config gets its store from NewSecrets / DecryptSecretsFromConfig.
func (s *SecretMap) Set(key string, v Secret) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]Secret{}
	}
	s.m[key] = v
}

// Delete removes key.
func (s *SecretMap) Delete(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}

// Snapshot returns a copy of the secrets, to range over without holding the lock.
func (s *SecretMap) Snapshot() map[string]Secret {
	out := map[string]Secret{}
	if s == nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Replace replaces every secret with a copy of m. On a nil store it does nothing, as Set.
func (s *SecretMap) Replace(m map[string]Secret) {
	if s == nil {
		return
	}
	n := make(map[string]Secret, len(m))
	for k, v := range m {
		n[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = n
}

// Len returns the number of secrets.
func (s *SecretMap) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}
