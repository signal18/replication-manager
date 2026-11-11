// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"encoding/json"
	"os"
	"path/filepath"

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
