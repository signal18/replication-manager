// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

// A secret's level (CREDENTIAL_STORES.md "Two levels"): a server secret is the instance's
// (its setting is tagged scope:"server"), any other is a cluster's. Where a level lands is
// internal to each store plugin: the core hands a store a Scope, never a path.
type Level string

const (
	LevelServer  Level = "server"
	LevelCluster Level = "cluster"
)

// Scope is a level and, for a cluster, its name.
type Scope struct {
	Level Level
	Name  string
}

// ServerScope is the instance's scope.
func ServerScope() Scope { return Scope{Level: LevelServer} }

// ClusterScope is a cluster's scope.
func ClusterScope(name string) Scope { return Scope{Level: LevelCluster, Name: name} }

// localFileName is the versioned local file's name, in the folder of its scope.
const localFileName = "secret_store.json"

// LocalFile is the versioned local file of the built-in secret_kv-config plugin: one file per
// scope under the data directory, the versions encrypted (F9), written atomically. Nothing
// outside the plugin builds its path.
type LocalFile struct {
	DataDir string
}

// Path is the file of scope: <datadir>/secret_store.json for the server,
// <datadir>/<cluster>/secret_store.json for a cluster.
func (f LocalFile) Path(s Scope) string {
	if s.Level == LevelServer {
		return filepath.Join(f.DataDir, localFileName)
	}
	return filepath.Join(f.DataDir, s.Name, localFileName)
}

// ExportPath is where the file of scope is copied in a configuration directory (the export
// next to the cluster files): <conf>/cluster.d/<cluster>_secret_store.json for a cluster,
// <conf>/secret_store.json for the server.
func (f LocalFile) ExportPath(confDir string, s Scope) string {
	if s.Level == LevelServer {
		return filepath.Join(confDir, localFileName)
	}
	return filepath.Join(confDir, "cluster.d", s.Name+"_"+localFileName)
}

// Load reads the file of scope; a missing or empty file is an empty store.
func (f LocalFile) Load(s Scope) (map[string][]FileVersion, error) {
	return LoadVersionFile(f.Path(s))
}

// Write writes the file of scope atomically, owner-only.
func (f LocalFile) Write(s Scope, file map[string][]FileVersion) error {
	return WriteVersionFile(f.Path(s), file)
}

// LoadVersionFile reads a versioned file at path (the tools reading an exported copy use it
// too); a missing or empty file is an empty store.
func LoadVersionFile(path string) (map[string][]FileVersion, error) {
	file := map[string][]FileVersion{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return file, nil
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file == nil {
		file = map[string][]FileVersion{}
	}
	return file, nil
}

// WriteVersionFile writes a versioned file at path through a temp file and a rename, so a
// reader never sees it half written; owner-only (misc.WriteFileDurable).
func WriteVersionFile(path string, file map[string][]FileVersion) error {
	if file == nil {
		file = map[string][]FileVersion{}
	}
	payload, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return misc.WriteFileDurable(path, payload, 0o600, "secret_store-*.tmp")
}

// PruneVersions keeps the last keepLast versions of each key, in new arrays; the value a
// config file held when last seen stays, to tell an edit of the file (rule 9). It returns the
// pruned versions, the number of keys pruned and of versions removed.
func PruneVersions(file map[string][]FileVersion, keepLast int) (map[string][]FileVersion, int, int) {
	pruned := make(map[string][]FileVersion, len(file))
	keysPruned, removed := 0, 0
	for key, versions := range file {
		if len(versions) <= keepLast {
			pruned[key] = append([]FileVersion(nil), versions...)
			continue
		}
		start := len(versions) - keepLast
		copied := append([]FileVersion(nil), versions[start:]...)
		if seen := lastFromConfigFile(versions); seen >= 0 && seen < start {
			copied = append([]FileVersion{versions[seen]}, copied...)
			start--
		}
		pruned[key] = copied
		if start > 0 {
			keysPruned++
		}
		removed += start
	}
	return pruned, keysPruned, removed
}

// lastFromConfigFile is the index of the last version marked from_config_file, else -1.
func lastFromConfigFile(versions []FileVersion) int {
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].FromConfigFile {
			return i
		}
	}
	return -1
}

// AutoPrune is the bound of a versioned file under conf (monitoring-secret-versioning-auto-prune,
// -keep-last), for FileBinding.Save; nil when auto-prune is off.
func AutoPrune(conf *config.Config) func(map[string][]FileVersion) (map[string][]FileVersion, bool) {
	if conf == nil || !conf.MonitoringSecretVersioningAutoPrune || conf.MonitoringSecretVersioningKeepLast < 1 {
		return nil
	}
	keepLast := conf.MonitoringSecretVersioningKeepLast
	return func(file map[string][]FileVersion) (map[string][]FileVersion, bool) {
		pruned, _, removed := PruneVersions(file, keepLast)
		return pruned, removed > 0
	}
}

// SourceServer is the source of a server secret read in a cluster's chain.
const SourceServer = "server"

// ErrServerSecret refuses a change of a server secret through a cluster: it is the
// instance's, changed at the server level (CREDENTIAL_STORES.md "Two levels").
var ErrServerSecret = errors.New("a server secret: it is changed at the server level, for every cluster")

// ServerLevel is the first store of a cluster's chain: it answers the server secrets from the
// server's chain, read only. A cluster never holds its own version of a server secret, so a
// rotation at the server level is the value every cluster reads at its next refresh.
type ServerLevel struct {
	Chain func() *Chain         // the server's chain; nil: no server (tests), the key is not held
	Held  func(key string) bool // the server secrets
}

func (s *ServerLevel) Name() string    { return SourceServer }
func (s *ServerLevel) Versioned() bool { return true }
func (s *ServerLevel) Generates() bool { return false }

func (s *ServerLevel) held(key string) (*Chain, bool) {
	if s == nil || s.Held == nil || s.Chain == nil || !s.Held(key) {
		return nil, false
	}
	c := s.Chain()
	return c, c != nil
}

// Holds: key is a server secret this store answers.
func (s *ServerLevel) Holds(key string) bool {
	_, ok := s.held(key)
	return ok
}

func (s *ServerLevel) Get(key string) (string, bool, error) {
	c, ok := s.held(key)
	if !ok {
		return "", false, nil
	}
	v, ok := c.Raw(key)
	return v, ok, nil
}

func (s *ServerLevel) CanSet(key string) error {
	if _, ok := s.held(key); !ok {
		return fmt.Errorf("%s is not a server secret: %w", key, ErrNotHeld)
	}
	return &heldRefusal{ErrServerSecret}
}

func (s *ServerLevel) Set(key, value string) error { return s.CanSet(key) }

// FileBinding ties a version store to its local file for one scope: loading it at start,
// writing it from the save callback (T25), and, on a standby, merging the file the config pull
// brought (rule 11). The same code for the server and every cluster.
type FileBinding struct {
	// DataDir is read at each use: a reload may move the data directory
	DataDir func() string
	Scope   Scope

	mu      sync.Mutex
	modTime time.Time
}

func (b *FileBinding) file() LocalFile { return LocalFile{DataDir: b.DataDir()} }

// Path is the bound file.
func (b *FileBinding) Path() string { return b.file().Path(b.Scope) }

func (b *FileBinding) noteModTime() {
	if st, err := os.Stat(b.Path()); err == nil {
		b.mu.Lock()
		b.modTime = st.ModTime()
		b.mu.Unlock()
	}
}

// LoadInto loads the file into vs (a missing file is an empty store).
func (b *FileBinding) LoadInto(vs *VersionStore) error {
	file, err := b.file().Load(b.Scope)
	if err != nil {
		return err
	}
	vs.Load(file)
	b.noteModTime()
	return nil
}

// ErrVersionNotEncrypted refuses to write a file holding a value that does not look encrypted
// (F9: the file is in the config push).
var ErrVersionNotEncrypted = errors.New("a credential version is not encrypted and there is no encryption key (monitoring-key-path): the file is not written")

// Save writes vs to the file when a version changed: every value encrypted first (encrypted
// tells an encrypted value), then prune (nil: none) bounds it. The file is left as it was on an
// error, and vs stays dirty for the next save.
func (b *FileBinding) Save(vs *VersionStore, encrypted func(string) bool, prune func(map[string][]FileVersion) (map[string][]FileVersion, bool)) error {
	if !vs.EncryptKeptAsRead(encrypted) {
		vs.MarkDirty()
		return ErrVersionNotEncrypted
	}
	file, changed := vs.Export()
	if prune != nil {
		if pruned, did := prune(file); did {
			file, changed = pruned, true
			vs.Load(pruned)
		}
	}
	if !changed {
		return nil
	}
	if err := b.file().Write(b.Scope, file); err != nil {
		vs.MarkDirty()
		return err
	}
	b.noteModTime()
	return nil
}

// MergeIfChanged merges into vs the file another monitor wrote, when it changed since it was
// last seen; true when a version was added. A standby never writes it back.
func (b *FileBinding) MergeIfChanged(vs *VersionStore) (bool, error) {
	st, err := os.Stat(b.Path())
	if err != nil {
		return false, nil
	}
	b.mu.Lock()
	seen := !st.ModTime().After(b.modTime)
	b.modTime = st.ModTime()
	b.mu.Unlock()
	if seen {
		return false, nil
	}
	file, err := b.file().Load(b.Scope)
	if err != nil {
		return false, err
	}
	if !vs.Merge(file) {
		return false, nil
	}
	vs.Export()
	return true, nil
}

// LooksEncrypted tells a value holding a repman-encrypted token (hash_...), alone or in a
// user:password list: the backstop of F9 before a file is written, not a proof of encryption.
func LooksEncrypted(value string) bool {
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ':', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	}) {
		if strings.HasPrefix(token, "hash_") {
			return true
		}
	}
	return false
}
