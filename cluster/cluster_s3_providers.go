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

// s3ProvidersInConfigMark, next to s3providers.json, tells that the cluster config holds the
// providers (#2155): the file is left as it was for a release before #2155 (a downgrade finds
// its providers), never read again by this one.
const s3ProvidersInConfigMark = s3ProvidersFileName + ".in-config"

// s3ProviderState holds in-memory S3 provider data and its synchronization.
// LOCKING CONTRACT:
//   - providers reads/writes must hold mu (RLock for reads, Lock for writes)
//   - mutate+persist(+rollback) sequences must hold crudMu
type s3ProviderState struct {
	providers []config.S3Provider
	mu        sync.RWMutex
	crudMu    sync.Mutex
	// fromFile: the list was read from s3providers.json, not from the config's
	// [[s3-provider]] blocks: the next config save migrates it (#2155)
	fromFile bool
	// cipher: the ciphertext a key was read or replayed with, by plaintext, reused while it
	// encodes the same key: a save rewrites identical bytes (no git change, no event)
	cipher   map[string]string
	cipherMu sync.Mutex
	// pending: a provider a peer's events are creating, until its keys validate (#2155)
	pending map[string]map[string]interface{}
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
	Cloud18SSO     bool                    `json:"cloud18Sso,omitempty"`
	RoleArn        string                  `json:"roleArn,omitempty"`
	Bucket         string                  `json:"bucket,omitempty"`
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
		Cloud18SSO:     p.Cloud18SSO,
		RoleArn:        p.RoleArn,
		Bucket:         p.Bucket,
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
		Cloud18SSO:     d.Cloud18SSO,
		RoleArn:        d.RoleArn,
		Bucket:         d.Bucket,
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
		out.AccessKey = cluster.stableS3Cipher(p.AccessKey)
		if !strings.HasPrefix(out.AccessKey, "hash_") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"S3 provider %q: no encryption key loaded — AccessKey will be stored in plaintext", p.Name)
		}
	}
	if p.SecretKey != "" {
		out.SecretKey = cluster.stableS3Cipher(p.SecretKey)
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
		p.AccessKey = cluster.decryptS3Key("s3provider-accesskey", p.AccessKey)
	}
	if p.SecretKey != "" {
		p.SecretKey = cluster.decryptS3Key("s3provider-secretkey", p.SecretKey)
	}
}

// decryptS3Key decrypts one key and remembers its ciphertext for stableS3Cipher.
func (cluster *Cluster) decryptS3Key(name string, value string) string {
	plain := cluster.Conf.GetDecryptedPassword(name, value)
	if strings.HasPrefix(value, "hash_") && plain != value {
		cluster.s3Providers.cipherMu.Lock()
		if cluster.s3Providers.cipher == nil {
			cluster.s3Providers.cipher = map[string]string{}
		}
		cluster.s3Providers.cipher[plain] = value
		cluster.s3Providers.cipherMu.Unlock()
	}
	return plain
}

// stableS3Cipher encrypts a key, reusing the ciphertext it was read with: encryption uses a
// random IV, a new one at each save would change the saved config every time (#2155). A
// remembered ciphertext the current key does not decrypt to the same key (rotated key) is
// encrypted again.
func (cluster *Cluster) stableS3Cipher(plain string) string {
	cluster.s3Providers.cipherMu.Lock()
	defer cluster.s3Providers.cipherMu.Unlock()
	if c, ok := cluster.s3Providers.cipher[plain]; ok {
		if cluster.Conf.GetDecryptedPassword("s3provider-key", c) == plain {
			return c
		}
		delete(cluster.s3Providers.cipher, plain)
	}
	c := cluster.Conf.GetEncryptedString(plain)
	if strings.HasPrefix(c, "hash_") {
		if cluster.s3Providers.cipher == nil {
			cluster.s3Providers.cipher = map[string]string{}
		}
		cluster.s3Providers.cipher[plain] = c
	}
	return c
}

// pruneS3Cipher forgets the ciphertexts of keys no provider holds any more: the cache keeps
// plaintext keys as map keys, it must not outlive them.
func (cluster *Cluster) pruneS3Cipher(providers []config.S3Provider) {
	keep := make(map[string]struct{}, 2*len(providers))
	for _, p := range providers {
		keep[p.AccessKey] = struct{}{}
		keep[p.SecretKey] = struct{}{}
	}
	cluster.s3Providers.cipherMu.Lock()
	defer cluster.s3Providers.cipherMu.Unlock()
	for plain := range cluster.s3Providers.cipher {
		if _, ok := keep[plain]; !ok {
			delete(cluster.s3Providers.cipher, plain)
		}
	}
}

// S3ProvidersPinned tells whether a config file sets the provider list (s3-provider): the list
// is read from it and the API, the peers' events cannot change it.
func (cluster *Cluster) S3ProvidersPinned() bool {
	_, pinned := cluster.Conf.GetImmuableFlag("s3-provider")
	return pinned
}

// s3ProvidersInConfig tells whether the providers are kept in the cluster's config as
// [[s3-provider]] blocks (#2155): with the dynamic config and an encryption key, unless a
// config file sets the list (pinned). Without a key the keys would reach the config
// repository in clear: s3providers.json (local, 0600) stays their store, as without the
// dynamic config.
func (cluster *Cluster) s3ProvidersInConfig() bool {
	if !cluster.Conf.ConfRewrite || cluster.S3ProvidersPinned() {
		return false
	}
	if cluster.Conf.SecretKey == nil {
		cluster.Conf.LoadEncrytionKey()
	}
	return cluster.Conf.SecretKey != nil
}

// LoadS3Providers loads the cluster's providers into ClusterS3Providers under a write lock:
// from the config's [[s3-provider]] blocks (#2155) when the config keeps them or a config file
// sets them (pinned), else from the per-cluster JSON file they replace, which the next config
// save then migrates. Blocks without a JSON file are read in any case, and so is the config
// once a save marked it as holding the providers (s3ProvidersInConfigMark). Secrets are
// decrypted after reading. If neither is there the list is empty and nil is returned. Struct
// parse errors are logged at LvlWarn and yield an empty slice plus a non-nil error.
// Individual records that fail Validate() or are duplicates are skipped (LvlWarn per
// record); valid records are still loaded and a non-nil error is returned to signal partial
// data loss so callers can decide whether to abort startup or alert. Startup is never blocked.
func (cluster *Cluster) LoadS3Providers() error {
	pinned := cluster.S3ProvidersPinned()
	path := cluster.s3ProvidersFilePath()
	if len(cluster.Conf.S3Providers) > 0 {
		_, noFile := os.Stat(path)
		if pinned || cluster.s3ProvidersInConfig() || os.IsNotExist(noFile) {
			records := make([]config.S3Provider, 0, len(cluster.Conf.S3Providers))
			for _, m := range cluster.Conf.S3Providers {
				records = append(records, config.S3ProviderFromSettings(m))
			}
			return cluster.setLoadedS3Providers(records, "the config", false)
		}
	}
	if _, err := os.Stat(filepath.Join(cluster.WorkingDir, s3ProvidersInConfigMark)); err == nil && cluster.s3ProvidersInConfig() {
		// the config holds the list, here empty: the file left for older releases is not read
		return cluster.setLoadedS3Providers(nil, "the config", false)
	}
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

	records := make([]config.S3Provider, 0, len(raw))
	for _, d := range raw {
		records = append(records, fromOnDisk(d))
	}
	err = cluster.setLoadedS3Providers(records, path, true)
	if cluster.s3ProvidersInConfig() && len(records) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"S3 providers read from %s: the next config save moves them to the cluster config (#2155)", path)
		cluster.Save()
	}
	return err
}

// setLoadedS3Providers decrypts, validates and dedups the loaded records, then sets them as
// the cluster's list; from names where they come from, for the logs.
func (cluster *Cluster) setLoadedS3Providers(records []config.S3Provider, from string, fromFile bool) error {
	// Decrypt secrets, validate, and enforce first-wins name uniqueness.
	// Invalid records and duplicate names are skipped with a LvlWarn per entry
	// so a single corrupt entry does not wipe the rest of the provider set.
	// Name uniqueness is enforced case-sensitively ("Provider" != "provider");
	// this is intentional and documented in ValidateS3ProviderName.
	seen := make(map[string]struct{}, len(records))
	valid := make([]config.S3Provider, 0, len(records))
	skipped := 0
	for _, p := range records {
		cluster.decryptS3Provider(&p)
		if err := p.Validate(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping invalid S3 provider %q in %s: %v", p.Name, from, err)
			skipped++
			continue
		}
		if _, dup := seen[p.Name]; dup {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping duplicate S3 provider name %q in %s: first occurrence retained", p.Name, from)
			skipped++
			continue
		}
		seen[p.Name] = struct{}{}
		valid = append(valid, p)
	}

	cluster.s3Providers.mu.Lock()
	defer cluster.s3Providers.mu.Unlock()
	cluster.s3Providers.providers = valid
	cluster.s3Providers.fromFile = fromFile && len(valid) > 0

	if skipped > 0 {
		return fmt.Errorf("S3 providers in %s: %d record(s) skipped due to validation errors or duplicate names — check logs for details", from, skipped)
	}
	return nil
}

// SaveS3Providers validates all providers and persists them: in the cluster's config, by the
// next config save (#2155), once they render with their keys encrypted; or, when the config
// does not keep them (s3ProvidersInConfig), encrypted in the per-cluster JSON file, written
// atomically via a temp-file + rename strategy. Pinned by a config file, nothing is written:
// the list in memory lasts until the restart, which reads the config file again. The private s3ProviderOnDisk struct is used to bypass S3Provider.MarshalJSON
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

	if cluster.S3ProvidersPinned() {
		return nil
	}
	if cluster.s3ProvidersInConfig() {
		// the render a config save does: a key that cannot be encrypted fails here, the caller
		// rolls back; the write itself is the next config save's
		if _, err := cluster.S3ProvidersToml("saved-" + cluster.Name); err != nil {
			return err
		}
		cluster.Save()
		return nil
	}

	toWrite := make([]s3ProviderOnDisk, len(snapshot))
	for i, p := range snapshot {
		enc := cluster.encryptS3Provider(p)
		toWrite[i] = toOnDisk(enc)
	}
	cluster.pruneS3Cipher(snapshot)

	data, err := json.MarshalIndent(toWrite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal S3 providers: %w", err)
	}
	path := cluster.s3ProvidersFilePath()
	parentDir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(parentDir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create S3 providers temp file in %s: %w", parentDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod S3 providers temp file %s: %w", tmpPath, err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write S3 providers temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync S3 providers temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close S3 providers temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename S3 providers file to %s: %w", path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod S3 providers file %s: %w", path, err)
	}
	return nil
}

// S3ProvidersToml renders the cluster's providers, keys encrypted, as the
// [[<section>.s3-provider]] blocks of its saved config (#2155). A key left in clear (no
// encryption key) is refused: the saved config goes to the config repository.
func (cluster *Cluster) S3ProvidersToml(section string) (string, error) {
	snapshot := cluster.GetS3ProvidersSnapshot()
	cluster.pruneS3Cipher(snapshot)
	for i := range snapshot {
		snapshot[i] = cluster.encryptS3Provider(snapshot[i])
		for _, k := range []string{snapshot[i].AccessKey, snapshot[i].SecretKey} {
			if k != "" && !strings.HasPrefix(k, "hash_") {
				return "", fmt.Errorf("S3 provider %q: its keys cannot be encrypted (no encryption key), not saved in the cluster config", snapshot[i].Name)
			}
		}
	}
	return config.S3ProvidersToml(section, snapshot)
}

// markS3ProvidersInConfig marks, once a config save holds the providers (#2155), that the
// config is their store: s3providers.json is left untouched (a release before #2155 still reads
// it) and never read again by this one (s3ProvidersInConfigMark).
func (cluster *Cluster) markS3ProvidersInConfig() {
	cluster.s3Providers.mu.Lock()
	fromFile := cluster.s3Providers.fromFile
	cluster.s3Providers.fromFile = false
	cluster.s3Providers.mu.Unlock()
	path := cluster.s3ProvidersFilePath()
	mark := filepath.Join(cluster.WorkingDir, s3ProvidersInConfigMark)
	if _, err := os.Stat(path); err != nil {
		return
	}
	if _, err := os.Stat(mark); err == nil {
		return
	}
	if err := os.WriteFile(mark, []byte("The S3 providers are in the cluster config ([[s3-provider]] blocks, #2155); s3providers.json is kept for older releases.\n"), 0600); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn,
			"S3 providers: cannot write %s: %s", mark, err)
		return
	}
	if fromFile {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"S3 providers moved to the cluster config (#2155): %s kept for older releases, no longer read", path)
	}
}

// ApplyS3ProviderSetting applies a peer's config event on the provider list (#2155): the
// saved config diffs it per entry, "s3-provider.<name>.<key>", one key per event. A key's
// ciphertext is decrypted, and kept so this instance saves the same bytes. A provider the
// events create or change is held in pending while its keys do not validate (a change
// between SSO and keys passes through an invalid step), and set once they do; unsetting its
// name removes it. A list pinned by a config file is not changed.
func (cluster *Cluster) ApplyS3ProviderSetting(name string, key string, value string, unset bool) error {
	if cluster.S3ProvidersPinned() {
		return fmt.Errorf("the S3 providers are set by a config file (s3-provider)")
	}
	return cluster.WithS3ProviderCRUDLock(func() error {
		var settings map[string]interface{}
		exists := false
		for _, p := range cluster.GetS3ProvidersSnapshot() {
			if p.Name == name {
				settings, exists = config.S3ProviderSettings(p), true
			}
		}
		cluster.s3Providers.mu.Lock()
		if pending, ok := cluster.s3Providers.pending[name]; ok {
			settings = pending
		}
		cluster.s3Providers.mu.Unlock()
		if unset && (key == "name" || settings == nil) {
			cluster.s3Providers.mu.Lock()
			delete(cluster.s3Providers.pending, name)
			cluster.s3Providers.mu.Unlock()
			if exists {
				if err := cluster.RemoveS3Provider(name); err != nil {
					return err
				}
				cluster.Save()
			}
			return nil
		}
		if settings == nil {
			settings = map[string]interface{}{"name": name}
		}
		switch {
		case unset:
			delete(settings, key)
		case key == "access-key" || key == "secret-key":
			settings[key] = cluster.decryptS3Key("s3provider-"+strings.ReplaceAll(key, "-", ""), value)
		default:
			settings[key] = value
		}
		settings["name"] = name
		p := config.S3ProviderFromSettings(settings)
		if err := p.Validate(); err != nil {
			cluster.s3Providers.mu.Lock()
			if cluster.s3Providers.pending == nil {
				cluster.s3Providers.pending = map[string]map[string]interface{}{}
			}
			cluster.s3Providers.pending[name] = settings
			cluster.s3Providers.mu.Unlock()
			return nil
		}
		cluster.s3Providers.mu.Lock()
		delete(cluster.s3Providers.pending, name)
		cluster.s3Providers.mu.Unlock()
		var err error
		if exists {
			err = cluster.UpdateS3Provider(p)
		} else {
			err = cluster.AddS3Provider(p)
		}
		if err == nil {
			cluster.Save()
			go cluster.RefreshBackupMount()
		}
		return err
	})
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
