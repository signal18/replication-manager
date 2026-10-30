// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
)

// SourceVersions is a credential read from the versioned store, shown in the GUI.
const SourceVersions = "versions"

// FileVersion is one version of a secret as the local versioned file (secret_store.json)
// holds it: the value encrypted (F9), when and by which instance it was written.
type FileVersion struct {
	Version   int    `json:"version"`
	HashValue string `json:"hash_value"`
	RotatedAt string `json:"rotated_at"`
	WrittenBy int    `json:"written_by,omitempty"`
	// FromConfigFile: the value a config file (immutable) held when it was seen (rule 9)
	FromConfigFile bool `json:"from_config_file,omitempty"`
}

// At is when the version was written.
func (v FileVersion) At() time.Time {
	at, _ := time.Parse(time.RFC3339Nano, v.RotatedAt)
	return at
}

// VersionStore is the versions of the cluster's secrets in memory (#2227 rules 1, 1b, 2): the
// head of the cluster's chain for the secrets it holds. A change is a new version made here,
// with its time and writing instance; the config keeps the starting value forever. The
// local file (secret_store.json) is its copy on disk, written by the cluster's save callback
// only (T25) and pushed with the config; the vault entries get each version from the queue.
// A change is refused when no active store keeps versions, so the config never holds
// anything else than the starting value. A value set in a config file (immutable) is a
// starting value too: the versions win over it until an operator edits it there
// (TakeConfigFileValue).
type VersionStore struct {
	mu       sync.Mutex
	conf     func() *config.Config
	held     func(key string) bool // the secrets it holds
	keeps    func() error          // nil when an active store keeps the versions, else why not
	save     func()
	versions map[string][]FileVersion
	dirty    bool
}

// NewVersionStore is an empty version store; held names its secrets, keeps says whether an
// active store keeps the versions, save is the cluster's save event.
func NewVersionStore(conf func() *config.Config, held func(string) bool, keeps func() error, save func()) *VersionStore {
	return &VersionStore{conf: conf, held: held, keeps: keeps, save: save, versions: map[string][]FileVersion{}}
}

// heldRefusal is a refusal no later store of the chain may override: the key is this store's
// and the config must not take it instead (rule 1).
type heldRefusal struct{ err error }

func (h *heldRefusal) Error() string { return h.err.Error() }
func (h *heldRefusal) Unwrap() error { return h.err }

func (s *VersionStore) Name() string    { return SourceVersions }
func (s *VersionStore) Versioned() bool { return true }
func (s *VersionStore) Generates() bool { return false }

// Persistent: a change survives a restart when an active store keeps it.
func (s *VersionStore) Persistent() bool { return s.keeps == nil || s.keeps() == nil }

// Holds: key is one of the secrets this store versions.
func (s *VersionStore) Holds(key string) bool { return s.held != nil && s.held(key) }

func (s *VersionStore) Get(key string) (string, bool, error) {
	if !s.Holds(key) {
		return "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	if len(vs) == 0 {
		return "", false, nil
	}
	return vs[len(vs)-1].HashValue, true, nil
}

func (s *VersionStore) CanSet(key string) error {
	if !s.Holds(key) {
		return fmt.Errorf("%s is not versioned: %w", key, ErrNotHeld)
	}
	if s.keeps != nil {
		if err := s.keeps(); err != nil {
			return &heldRefusal{err}
		}
	}
	if !canEncrypt(s.conf()) {
		return &heldRefusal{ErrNoEncryptionKey}
	}
	return nil
}

// ErrNoEncryptionKey refuses a version the local file could not keep encrypted: without the
// key, EncryptSecretValue returns the value as it is, and the file goes to the config repo (F9).
var ErrNoEncryptionKey = errors.New("no encryption key: the local versioned file (monitoring-secret-versioning) keeps a credential encrypted only, the new value cannot be stored; create the key (monitoring-key-path)")

// canEncrypt: the versions can be kept encrypted. Only the local file needs the key: with
// versioning off nothing is written to it, and a vault entry keeps its values server side.
// The key is loaded, or loads now, as GetEncryptedString loads it.
func canEncrypt(conf *config.Config) bool {
	if !conf.IsMonitoringSecretVersioningEnabled() {
		return true
	}
	if conf.SecretKey == nil {
		conf.LoadEncrytionKey()
	}
	return conf.SecretKey != nil
}

// Set makes a new version of key, now, by this instance.
func (s *VersionStore) Set(key, value string) error {
	if err := s.CanSet(key); err != nil {
		return err
	}
	conf := s.conf()
	s.add(key, conf.EncryptSecretValue(value), time.Now().UTC(), conf.ArbitrationSasUniqueId)
	return nil
}

func (s *VersionStore) add(key, stored string, at time.Time, by int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	n := 1
	if len(vs) > 0 {
		n = vs[len(vs)-1].Version + 1
	}
	s.versions[key] = append(vs, FileVersion{Version: n, HashValue: stored, RotatedAt: at.UTC().Format(time.RFC3339Nano), WrittenBy: by})
	s.dirty = true
}

// TakeConfigFileValue is the start's check of key set in a config file (immutable, rule 9),
// plain its value there. The value last seen in the file is kept as a version marked
// from_config_file: the same value changes nothing, the versions made since win over it; a
// different value is an operator's edit of the file and becomes the current version, now.
// Seen for the first time while versions exist, it is kept as seen before them, not made
// current. True when the file's value became the current one.
func (s *VersionStore) TakeConfigFileValue(key, plain string) bool {
	conf := s.conf()
	if !s.Holds(key) || plain == "" || !canEncrypt(conf) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	seen := -1
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].FromConfigFile {
			seen = i
			break
		}
	}
	if seen >= 0 && conf.DecryptSecretValue(key, vs[seen].HashValue) == plain {
		return false
	}
	v := FileVersion{HashValue: conf.EncryptSecretValue(plain), WrittenBy: conf.ArbitrationSasUniqueId, FromConfigFile: true}
	if seen < 0 && len(vs) > 0 {
		v.RotatedAt = vs[0].At().Add(-time.Nanosecond).UTC().Format(time.RFC3339Nano)
		vs = append([]FileVersion{v}, vs...)
		for i := range vs {
			vs[i].Version = i + 1
		}
		s.versions[key] = vs
		s.dirty = true
		return false
	}
	v.Version = 1
	if len(vs) > 0 {
		v.Version = vs[len(vs)-1].Version + 1
	}
	v.RotatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.versions[key] = append(vs, v)
	s.dirty = true
	return true
}

// Insert adds a version read from another store, with its own time and writer: a newer
// value becomes the current one here without a new version of it (rule 3).
// Without the key nothing is added: the value stays where it was read.
func (s *VersionStore) Insert(key string, v Version) {
	if !canEncrypt(s.conf()) {
		return
	}
	s.add(key, s.conf().EncryptSecretValue(v.Value), v.At, v.By)
}

// Record adds the current value of a key this store does not version as a new version when
// it differs from the last one (the file's history of the other secrets, as before #2227).
func (s *VersionStore) Record(key, stored, plain string) bool {
	conf := s.conf()
	s.mu.Lock()
	vs := s.versions[key]
	if len(vs) > 0 && conf.DecryptSecretValue(key, vs[len(vs)-1].HashValue) == plain {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	s.add(key, stored, time.Now().UTC(), conf.ArbitrationSasUniqueId)
	return true
}

// Latest is the current version of key, its value decrypted.
func (s *VersionStore) Latest(key string) (Version, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	if len(vs) == 0 {
		return Version{}, false
	}
	last := vs[len(vs)-1]
	return Version{Value: s.conf().DecryptSecretValue(key, last.HashValue), At: last.At(), By: last.WrittenBy}, true
}

// Load replaces the versions with the file's.
func (s *VersionStore) Load(file map[string][]FileVersion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = map[string][]FileVersion{}
	for k, vs := range file {
		s.versions[k] = append([]FileVersion(nil), vs...)
	}
	s.dirty = false
}

// Merge adds the versions of a file written by the pair's other monitor (rule 11): a version
// is known by its time and writer; the merged list is in time order, renumbered. True when
// it added one.
func (s *VersionStore) Merge(file map[string][]FileVersion) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := false
	for k, theirs := range file {
		seen := map[string]bool{}
		for _, v := range s.versions[k] {
			seen[v.RotatedAt+"/"+fmt.Sprint(v.WrittenBy)] = true
		}
		merged := append([]FileVersion(nil), s.versions[k]...)
		for _, v := range theirs {
			if !seen[v.RotatedAt+"/"+fmt.Sprint(v.WrittenBy)] {
				merged = append(merged, v)
				added = true
			}
		}
		sort.SliceStable(merged, func(i, j int) bool {
			ai, aj := merged[i].At(), merged[j].At()
			if !ai.Equal(aj) {
				return ai.Before(aj)
			}
			return merged[i].WrittenBy < merged[j].WrittenBy
		})
		for i := range merged {
			merged[i].Version = i + 1
		}
		s.versions[k] = merged
	}
	if added {
		s.dirty = true
	}
	return added
}

// Export is a copy of the versions for the file, and whether they changed since the last
// export.
func (s *VersionStore) Export() (map[string][]FileVersion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]FileVersion, len(s.versions))
	for k, vs := range s.versions {
		out[k] = append([]FileVersion(nil), vs...)
	}
	dirty := s.dirty
	s.dirty = false
	return out, dirty
}

// MarkDirty asks the next save to write the file.
func (s *VersionStore) MarkDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

func (s *VersionStore) saveEvent() {
	if s.save != nil {
		s.save()
	}
}

func (s *VersionStore) snapshot(key string) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.versions[key]), true
}

func (s *VersionStore) restore(key string, raw interface{}, present bool) {
	n, _ := raw.(int)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.versions[key]) > n {
		s.versions[key] = s.versions[key][:n]
	}
}

// isHeldRefusal: err is a refusal the chain must not look past.
func isHeldRefusal(err error) bool {
	var h *heldRefusal
	return errors.As(err, &h)
}

// Count is how many versions key has.
func (s *VersionStore) Count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.versions[key])
}
