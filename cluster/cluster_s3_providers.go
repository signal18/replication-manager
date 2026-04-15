// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/signal18/replication-manager/config"
)

const s3ProvidersFileName = "s3providers.json"

// s3ProviderOnDisk is a private struct used exclusively for file I/O.
// It bypasses config.S3Provider.MarshalJSON (which always omits credentials)
// so that encrypted AccessKey and SecretKey can be written to and read from disk.
type s3ProviderOnDisk struct {
	Name           string                  `json:"name"`
	ProviderSource config.S3ProviderSource `json:"providerSource"`
	ProviderApp    string                  `json:"providerApp,omitempty"`
	Endpoint       string                  `json:"endpoint,omitempty"`
	Region         string                  `json:"region,omitempty"`
	AccessKey      string                  `json:"accesskey,omitempty"`
	SecretKey      string                  `json:"secretkey,omitempty"`
}

func toOnDisk(p config.S3Provider) s3ProviderOnDisk {
	return s3ProviderOnDisk{
		Name:           p.Name,
		ProviderSource: p.ProviderSource,
		ProviderApp:    p.ProviderApp,
		Endpoint:       p.Endpoint,
		Region:         p.Region,
		AccessKey:      p.AccessKey,
		SecretKey:      p.SecretKey,
	}
}

func fromOnDisk(d s3ProviderOnDisk) config.S3Provider {
	return config.S3Provider{
		Name:           d.Name,
		ProviderSource: d.ProviderSource,
		ProviderApp:    d.ProviderApp,
		Endpoint:       d.Endpoint,
		Region:         d.Region,
		AccessKey:      d.AccessKey,
		SecretKey:      d.SecretKey,
	}
}

// s3ProvidersFilePath returns the deterministic per-cluster path for the S3 providers
// JSON file: filepath.Join(cluster.WorkingDir, "s3providers.json").
// cluster.WorkingDir already contains the cluster name (Conf.WorkingDir + "/" + cluster.Name);
// no additional segment must be appended.
func (cluster *Cluster) s3ProvidersFilePath() string {
	return filepath.Join(cluster.WorkingDir, s3ProvidersFileName)
}

// encryptS3Provider returns a copy of p with AccessKey and SecretKey encrypted
// using the cluster encryption key. If no key is loaded the values are stored
// unchanged (graceful degradation).
func (cluster *Cluster) encryptS3Provider(p config.S3Provider) config.S3Provider {
	out := p
	if p.AccessKey != "" {
		out.AccessKey = cluster.Conf.GetEncryptedString(p.AccessKey)
	}
	if p.SecretKey != "" {
		out.SecretKey = cluster.Conf.GetEncryptedString(p.SecretKey)
	}
	return out
}

// decryptS3Provider decrypts AccessKey and SecretKey in-place.
// Values that were not encrypted (no "hash_" prefix) are returned unchanged.
func (cluster *Cluster) decryptS3Provider(p *config.S3Provider) {
	if p.AccessKey != "" {
		p.AccessKey = cluster.Conf.GetDecryptedPassword("s3provider-accesskey", p.AccessKey)
	}
	if p.SecretKey != "" {
		p.SecretKey = cluster.Conf.GetDecryptedPassword("s3provider-secretkey", p.SecretKey)
	}
}

// LoadS3Providers reads the per-cluster JSON file into ClusterS3Providers under a
// write lock. Secrets are decrypted after reading via s3ProviderOnDisk (which
// bypasses S3Provider.MarshalJSON). If the file is absent the field is set to an
// empty slice without error. Struct parse errors are logged at LvlWarn and yield
// an empty slice. Individual records that fail Validate() are skipped with a
// LvlWarn per record; valid records are still loaded. Startup is never blocked.
func (cluster *Cluster) LoadS3Providers() {
	path := cluster.s3ProvidersFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		cluster.clusterS3ProvidersMu.Lock()
		defer cluster.clusterS3ProvidersMu.Unlock()
		if !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Failed to read S3 providers file %s: %v", path, err)
		}
		cluster.ClusterS3Providers = []config.S3Provider{}
		return
	}

	var raw []s3ProviderOnDisk
	if err := json.Unmarshal(data, &raw); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to parse S3 providers file %s: %v", path, err)
		cluster.clusterS3ProvidersMu.Lock()
		defer cluster.clusterS3ProvidersMu.Unlock()
		cluster.ClusterS3Providers = []config.S3Provider{}
		return
	}

	// Decrypt secrets, validate, and enforce first-wins name uniqueness.
	// Invalid records and duplicate names are skipped with a LvlWarn per entry
	// so a single corrupt entry does not wipe the rest of the provider set.
	seen := make(map[string]struct{}, len(raw))
	valid := make([]config.S3Provider, 0, len(raw))
	for _, d := range raw {
		p := fromOnDisk(d)
		cluster.decryptS3Provider(&p)
		if err := p.Validate(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping invalid S3 provider %q in %s: %v", p.Name, path, err)
			continue
		}
		if _, dup := seen[p.Name]; dup {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping duplicate S3 provider name %q in %s: first occurrence retained", p.Name, path)
			continue
		}
		seen[p.Name] = struct{}{}
		valid = append(valid, p)
	}

	cluster.clusterS3ProvidersMu.Lock()
	defer cluster.clusterS3ProvidersMu.Unlock()
	cluster.ClusterS3Providers = valid
}

// SaveS3Providers validates all providers, encrypts secrets, and atomically writes
// to the deterministic per-cluster path via a temp-file + rename strategy.
// The private s3ProviderOnDisk struct is used to bypass S3Provider.MarshalJSON
// (which omits credentials) so that encrypted secrets reach the file.
// File permissions are 0600 (owner read/write only).
func (cluster *Cluster) SaveS3Providers() error {
	cluster.clusterS3ProvidersMu.RLock()
	snapshot := make([]config.S3Provider, len(cluster.ClusterS3Providers))
	copy(snapshot, cluster.ClusterS3Providers)
	cluster.clusterS3ProvidersMu.RUnlock()

	// Validate all providers and reject duplicate names before touching the file.
	// This guards against state mutated directly via the public ClusterS3Providers field.
	seen := make(map[string]struct{}, len(snapshot))
	for i := range snapshot {
		if err := snapshot[i].Validate(); err != nil {
			return fmt.Errorf("provider %q failed validation before save: %w", snapshot[i].Name, err)
		}
		if _, dup := seen[snapshot[i].Name]; dup {
			return fmt.Errorf("duplicate S3 provider name %q in snapshot: save aborted", snapshot[i].Name)
		}
		seen[snapshot[i].Name] = struct{}{}
	}

	toWrite := make([]s3ProviderOnDisk, len(snapshot))
	for i, p := range snapshot {
		enc := cluster.encryptS3Provider(p)
		toWrite[i] = toOnDisk(enc)
	}

	data, err := json.MarshalIndent(toWrite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal S3 providers: %w", err)
	}
	path := cluster.s3ProvidersFilePath()
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write S3 providers temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename S3 providers file to %s: %w", path, err)
	}
	return nil
}

// GetS3ProvidersSnapshot returns a deep copy of ClusterS3Providers under the
// read lock. Use this in API response assembly to avoid races with concurrent
// CRUD mutations (AddS3Provider, UpdateS3Provider, RemoveS3Provider).
func (cluster *Cluster) GetS3ProvidersSnapshot() []config.S3Provider {
	cluster.clusterS3ProvidersMu.RLock()
	defer cluster.clusterS3ProvidersMu.RUnlock()
	snapshot := make([]config.S3Provider, len(cluster.ClusterS3Providers))
	copy(snapshot, cluster.ClusterS3Providers)
	return snapshot
}

// AddS3Provider validates p, enforces unique name (case-sensitive), then appends
// to ClusterS3Providers under a write lock. In-memory providers hold plaintext
// secrets; encryption happens only at persist time (SaveS3Providers).
func (cluster *Cluster) AddS3Provider(p config.S3Provider) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("invalid S3 provider: %w", err)
	}
	cluster.clusterS3ProvidersMu.Lock()
	defer cluster.clusterS3ProvidersMu.Unlock()
	for _, existing := range cluster.ClusterS3Providers {
		if existing.Name == p.Name {
			return fmt.Errorf("S3 provider with name %q already exists", p.Name)
		}
	}
	cluster.ClusterS3Providers = append(cluster.ClusterS3Providers, p)
	return nil
}

// RemoveS3Provider removes the provider whose Name equals name under a write lock.
func (cluster *Cluster) RemoveS3Provider(name string) error {
	cluster.clusterS3ProvidersMu.Lock()
	defer cluster.clusterS3ProvidersMu.Unlock()
	for i, p := range cluster.ClusterS3Providers {
		if p.Name == name {
			cluster.ClusterS3Providers = append(
				cluster.ClusterS3Providers[:i],
				cluster.ClusterS3Providers[i+1:]...,
			)
			return nil
		}
	}
	return fmt.Errorf("S3 provider %q not found", name)
}

// UpdateS3Provider validates p then replaces the stored provider whose Name equals
// p.Name under a write lock. In-memory providers hold plaintext secrets; encryption
// happens only at persist time (SaveS3Providers).
func (cluster *Cluster) UpdateS3Provider(p config.S3Provider) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("invalid S3 provider: %w", err)
	}
	cluster.clusterS3ProvidersMu.Lock()
	defer cluster.clusterS3ProvidersMu.Unlock()
	for i, existing := range cluster.ClusterS3Providers {
		if existing.Name == p.Name {
			cluster.ClusterS3Providers[i] = p
			return nil
		}
	}
	return fmt.Errorf("S3 provider %q not found", p.Name)
}
