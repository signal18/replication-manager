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
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
)

const s3ProvidersFileName = "s3providers.json"

// s3ProviderState holds in-memory S3 provider data and its synchronization.
// LOCKING CONTRACT:
//   - providers reads/writes must hold mu (RLock for reads, Lock for writes)
//   - mutate+persist(+rollback) sequences must hold crudMu
type s3ProviderState struct {
	providers []config.S3Provider
	mu        sync.RWMutex
	crudMu    sync.Mutex
}

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
// unchanged (graceful degradation) and a LvlWarn is emitted so operators are
// aware that credentials will be written to disk in plaintext.
func (cluster *Cluster) encryptS3Provider(p config.S3Provider) config.S3Provider {
	out := p
	if p.AccessKey != "" {
		out.AccessKey = cluster.Conf.GetEncryptedString(p.AccessKey)
		if !strings.HasPrefix(out.AccessKey, "hash_") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"S3 provider %q: no encryption key loaded — AccessKey will be stored in plaintext", p.Name)
		}
	}
	if p.SecretKey != "" {
		out.SecretKey = cluster.Conf.GetEncryptedString(p.SecretKey)
		if !strings.HasPrefix(out.SecretKey, "hash_") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"S3 provider %q: no encryption key loaded — SecretKey will be stored in plaintext", p.Name)
		}
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
// empty slice and nil is returned. Struct parse errors are logged at LvlWarn and
// yield an empty slice plus a non-nil error. Individual records that fail
// Validate() or are duplicates are skipped (LvlWarn per record); valid records are
// still loaded and a non-nil error is returned to signal partial data loss so
// callers can decide whether to abort startup or alert. Startup is never blocked.
func (cluster *Cluster) LoadS3Providers() error {
	path := cluster.s3ProvidersFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		cluster.s3Providers.mu.Lock()
		defer cluster.s3Providers.mu.Unlock()
		if !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Failed to read S3 providers file %s: %v", path, err)
			cluster.s3Providers.providers = []config.S3Provider{}
			return fmt.Errorf("read S3 providers file %s: %w", path, err)
		}
		cluster.s3Providers.providers = []config.S3Provider{}
		return nil
	}

	var raw []s3ProviderOnDisk
	if err := json.Unmarshal(data, &raw); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to parse S3 providers file %s: %v", path, err)
		cluster.s3Providers.mu.Lock()
		defer cluster.s3Providers.mu.Unlock()
		cluster.s3Providers.providers = []config.S3Provider{}
		return fmt.Errorf("parse S3 providers file %s: %w", path, err)
	}

	// Decrypt secrets, validate, and enforce first-wins name uniqueness.
	// Invalid records and duplicate names are skipped with a LvlWarn per entry
	// so a single corrupt entry does not wipe the rest of the provider set.
	// Name uniqueness is enforced case-sensitively ("Provider" != "provider");
	// this is intentional and documented in ValidateS3ProviderName.
	seen := make(map[string]struct{}, len(raw))
	valid := make([]config.S3Provider, 0, len(raw))
	skipped := 0
	for _, d := range raw {
		p := fromOnDisk(d)
		cluster.decryptS3Provider(&p)
		if err := p.Validate(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping invalid S3 provider %q in %s: %v", p.Name, path, err)
			skipped++
			continue
		}
		if _, dup := seen[p.Name]; dup {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping duplicate S3 provider name %q in %s: first occurrence retained", p.Name, path)
			skipped++
			continue
		}
		seen[p.Name] = struct{}{}
		valid = append(valid, p)
	}

	cluster.s3Providers.mu.Lock()
	defer cluster.s3Providers.mu.Unlock()
	cluster.s3Providers.providers = valid

	if skipped > 0 {
		return fmt.Errorf("S3 providers file %s: %d record(s) skipped due to validation errors or duplicate names — check logs for details", path, skipped)
	}
	return nil
}

// SaveS3Providers validates all providers, encrypts secrets, and atomically writes
// to the deterministic per-cluster path via a temp-file + rename strategy.
// The private s3ProviderOnDisk struct is used to bypass S3Provider.MarshalJSON
// (which omits credentials) so that encrypted secrets reach the file.
// File permissions are 0600 (owner read/write only).
func (cluster *Cluster) SaveS3Providers() error {
	cluster.s3Providers.mu.RLock()
	snapshot := make([]config.S3Provider, len(cluster.s3Providers.providers))
	copy(snapshot, cluster.s3Providers.providers)
	cluster.s3Providers.mu.RUnlock()

	// Validate all providers and reject duplicate names before touching the file.
	// This guards against state mutated directly via the grouped in-memory state
	// (possible within the cluster package, e.g. in tests or internal helpers).
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

// WithS3ProviderCRUDLock serializes S3 provider mutate+persist(+rollback)
// transactions across concurrent API requests.
func (cluster *Cluster) WithS3ProviderCRUDLock(run func() error) error {
	cluster.s3Providers.crudMu.Lock()
	defer cluster.s3Providers.crudMu.Unlock()
	if run == nil {
		return nil
	}
	return run()
}

// GetS3ProvidersSnapshot returns a deep copy of ClusterS3Providers under the
// read lock. Use this in API response assembly to avoid races with concurrent
// CRUD mutations (AddS3Provider, UpdateS3Provider, RemoveS3Provider).
func (cluster *Cluster) GetS3ProvidersSnapshot() []config.S3Provider {
	cluster.s3Providers.mu.RLock()
	defer cluster.s3Providers.mu.RUnlock()
	snapshot := make([]config.S3Provider, len(cluster.s3Providers.providers))
	copy(snapshot, cluster.s3Providers.providers)
	return snapshot
}

// AddS3Provider validates p, enforces unique name (case-sensitive), then appends
// to ClusterS3Providers under a write lock. In-memory providers hold plaintext
// secrets; encryption happens only at persist time (SaveS3Providers).
func (cluster *Cluster) AddS3Provider(p config.S3Provider) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("invalid S3 provider: %w", err)
	}
	cluster.s3Providers.mu.Lock()
	defer cluster.s3Providers.mu.Unlock()
	for _, existing := range cluster.s3Providers.providers {
		if existing.Name == p.Name {
			// Name matching is case-sensitive: "Provider" and "provider" are distinct.
			return fmt.Errorf("S3 provider with name %q already exists (name comparison is case-sensitive)", p.Name)
		}
	}
	cluster.s3Providers.providers = append(cluster.s3Providers.providers, p)
	return nil
}

// RemoveS3Provider removes the provider whose Name equals name under a write lock.
func (cluster *Cluster) RemoveS3Provider(name string) error {
	cluster.s3Providers.mu.Lock()
	defer cluster.s3Providers.mu.Unlock()
	for i, p := range cluster.s3Providers.providers {
		if p.Name == name {
			cluster.s3Providers.providers = append(
				cluster.s3Providers.providers[:i],
				cluster.s3Providers.providers[i+1:]...,
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
	cluster.s3Providers.mu.Lock()
	defer cluster.s3Providers.mu.Unlock()
	for i, existing := range cluster.s3Providers.providers {
		if existing.Name == p.Name {
			cluster.s3Providers.providers[i] = p
			return nil
		}
	}
	return fmt.Errorf("S3 provider %q not found", p.Name)
}
