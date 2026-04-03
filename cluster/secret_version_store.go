package cluster

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

const secretVersionStoreFilename = "secret_store.json"

type secretVersion struct {
	Version   int    `json:"version"`
	HashValue string `json:"hash_value"`
	RotatedAt string `json:"rotated_at"`
}

type secretVersionStore map[string][]secretVersion

type trackedSecretValue struct {
	StoredValue  string
	CompareValue string
}

type SecretVersionStorePruneSummary struct {
	StorePath       string
	KeysTotal       int
	KeysPruned      int
	VersionsRemoved int
	Changed         bool
	DryRun          bool
}

type SecretVersionStoreCopySummary struct {
	SourcePath      string
	DestinationPath string
	SourceHash      string
	DestinationHash string
	Copied          bool
	Skipped         bool
	DryRun          bool
	Reason          string
}

type SecretVersionStoreRestoreEntry struct {
	Key       string
	Version   int
	HashValue string
	RotatedAt string
}

// ReconcileSecretVersionStore keeps the per-cluster secret_store.json in sync
// with currently tracked secrets for this cluster.
//
// Behavior:
//   - returns immediately when feature is disabled
//   - bootstraps missing keys as version 1
//   - appends a new version only when semantic (decrypted) value changed
//   - stores encrypted/hash values only
//   - writes atomically to avoid partial store files
func (cluster *Cluster) ReconcileSecretVersionStore() {
	if cluster == nil || cluster.Conf == nil {
		return
	}

	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}

	storePath := filepath.Join(cluster.WorkingDir, secretVersionStoreFilename)
	store, err := loadSecretVersionStore(storePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Secret versioning disabled for this tick: cannot read store %s: %v", storePath, err)
		return
	}

	currentValues := cluster.getTrackedSecretStoreValues()
	now := time.Now().UTC().Format(time.RFC3339)
	hasChanges := false

	for _, key := range sortedTrackedSecretKeys(currentValues) {
		current := currentValues[key]
		versions := store[key]

		if len(versions) == 0 {
			store[key] = append(versions, secretVersion{Version: 1, HashValue: current.StoredValue, RotatedAt: now})
			hasChanges = true
			continue
		}

		latest := versions[len(versions)-1]
		latestCompareValue := cluster.getStoredSecretCompareValue(key, latest.HashValue)
		if latestCompareValue == current.CompareValue {
			continue
		}

		store[key] = append(versions, secretVersion{Version: latest.Version + 1, HashValue: current.StoredValue, RotatedAt: now})
		hasChanges = true
	}

	if !hasChanges {
		return
	}

	if err := writeSecretVersionStoreAtomic(storePath, store); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to persist secret version store %s: %v", storePath, err)
	}
}

// TrackedSecretCompareSnapshot returns the current tracked secret semantic
// values (decrypted/plaintext compare form) keyed by secret name.
//
// This is intended for cheap change detection in call paths (for example API
// setting updates) that need to know if a tracked secret changed and should
// trigger immediate reconciliation.
func (cluster *Cluster) TrackedSecretCompareSnapshot() map[string]string {
	snapshot := make(map[string]string)
	for key, value := range cluster.getTrackedSecretStoreValuesWithWarnings(false) {
		snapshot[key] = value.CompareValue
	}
	return snapshot
}

// getTrackedSecretStoreValues builds the per-key current snapshot used by
// reconciliation:
//   - StoredValue: encrypted/hash form to persist into secret_store.json
//   - CompareValue: plaintext semantic value used only for change detection
func (cluster *Cluster) getTrackedSecretStoreValues() map[string]trackedSecretValue {
	return cluster.getTrackedSecretStoreValuesWithWarnings(true)
}

func (cluster *Cluster) getTrackedSecretStoreValuesWithWarnings(logWarnings bool) map[string]trackedSecretValue {
	values := make(map[string]trackedSecretValue)
	if cluster == nil || cluster.Conf == nil {
		return values
	}

	for key, secret := range cluster.Conf.Secrets {
		if secret.Value == "" {
			continue
		}

		storedValue := cluster.GetEncryptedValueFromMemory(key)
		if storedValue == "" || !strings.Contains(storedValue, "hash_") {
			storedValue = secret.Value
		}

		if storedValue == "" {
			continue
		}

		if !strings.Contains(storedValue, "hash_") {
			if !logWarnings {
				continue
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping secret versioning for key %s: value is not encrypted hash format", key)
			continue
		}

		values[key] = trackedSecretValue{
			StoredValue:  storedValue,
			CompareValue: cluster.Conf.GetDecryptedValue(key),
		}
	}

	return values
}

// getStoredSecretCompareValue converts a stored encrypted/hash payload into the
// plaintext semantic value used for comparison only.
func (cluster *Cluster) getStoredSecretCompareValue(key string, value string) string {
	if value == "" || cluster == nil || cluster.Conf == nil {
		return value
	}

	return cluster.Conf.DecryptSecretValue(key, value)
}

// loadSecretVersionStore reads secret_store.json.
// Missing/empty files return an initialized empty store.
func loadSecretVersionStore(path string) (secretVersionStore, error) {
	store := make(secretVersionStore)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}

	if len(data) == 0 {
		return store, nil
	}

	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}

	if store == nil {
		store = make(secretVersionStore)
	}

	return store, nil
}

// writeSecretVersionStoreAtomic writes the store through a temp file + rename
// sequence so readers never observe a partially written JSON file.
func writeSecretVersionStoreAtomic(path string, store secretVersionStore) error {
	if store == nil {
		store = make(secretVersionStore)
	}

	payload, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return writeFileAtomic(path, payload, "secret_store-*.tmp")
}

func sortedTrackedSecretKeys(values map[string]trackedSecretValue) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func SecretVersionStorePath(workingDir string, clusterName string) string {
	return filepath.Join(workingDir, clusterName, secretVersionStoreFilename)
}

func SecretVersionStoreExportPath(confDir string, clusterName string) string {
	return filepath.Join(confDir, "cluster.d", clusterName+"_secret_store.json")
}

func CopySecretVersionStoreFile(srcPath string, dstPath string, dryRun bool, overwrite bool) (SecretVersionStoreCopySummary, error) {
	summary := SecretVersionStoreCopySummary{
		SourcePath:      srcPath,
		DestinationPath: dstPath,
		DryRun:          dryRun,
	}

	if _, err := loadSecretVersionStore(srcPath); err != nil {
		if os.IsNotExist(err) {
			return summary, fmt.Errorf("secret version store not found: %s", srcPath)
		}
		return summary, err
	}

	srcPayload, err := os.ReadFile(srcPath)
	if err != nil {
		return summary, err
	}
	summary.SourceHash = hashBytes(srcPayload)

	dstPayload, err := os.ReadFile(dstPath)
	if err == nil {
		summary.DestinationHash = hashBytes(dstPayload)
		if summary.SourceHash == summary.DestinationHash {
			summary.Skipped = true
			summary.Reason = "destination already up to date"
			return summary, nil
		}
		if !overwrite && !dryRun {
			return summary, fmt.Errorf("destination exists and differs; use --overwrite to replace it")
		}
	} else if !os.IsNotExist(err) {
		return summary, err
	}

	if dryRun {
		summary.Skipped = true
		summary.Reason = "dry run"
		return summary, nil
	}

	if err := writeFileAtomic(dstPath, srcPayload, "secret_store_export-*.tmp"); err != nil {
		return summary, err
	}

	summary.Copied = true
	if summary.Reason == "" {
		summary.Reason = "copied"
	}
	return summary, nil
}

func ResolveSecretVersionStoreEntries(path string, keys []string, secretVersion int, at *time.Time) ([]SecretVersionStoreRestoreEntry, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one key is required")
	}
	if secretVersion > 0 && at != nil {
		return nil, fmt.Errorf("secret-version and at are mutually exclusive")
	}
	if secretVersion <= 0 && at == nil {
		return nil, fmt.Errorf("either secret-version or at must be set")
	}

	store, err := loadSecretVersionStore(path)
	if err != nil {
		return nil, err
	}

	resolved := make([]SecretVersionStoreRestoreEntry, 0, len(keys))
	for _, key := range keys {
		versions, ok := store[key]
		if !ok || len(versions) == 0 {
			return nil, fmt.Errorf("key %s not found in secret store", key)
		}

		if secretVersion > 0 {
			entry, err := resolveSecretStoreEntryByVersion(key, versions, secretVersion)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, entry)
			continue
		}

		entry, err := resolveSecretStoreEntryByTime(key, versions, *at)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, entry)
	}

	return resolved, nil
}

func ListSecretVersionStoreKeys(path string) ([]string, error) {
	store, err := loadSecretVersionStore(path)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(store))
	for key := range store {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}

func PruneSecretVersionStoreFile(path string, keepLast int, dryRun bool) (SecretVersionStorePruneSummary, error) {
	summary := SecretVersionStorePruneSummary{StorePath: path, DryRun: dryRun}

	if keepLast < 1 {
		return summary, fmt.Errorf("keep-last must be >= 1")
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return summary, fmt.Errorf("secret version store not found: %s", path)
		}
		return summary, err
	}

	store, err := loadSecretVersionStore(path)
	if err != nil {
		return summary, err
	}

	prunedStore, keysPruned, versionsRemoved := pruneSecretVersionStore(store, keepLast)
	summary.KeysTotal = len(store)
	summary.KeysPruned = keysPruned
	summary.VersionsRemoved = versionsRemoved
	summary.Changed = versionsRemoved > 0

	if !summary.Changed || dryRun {
		return summary, nil
	}

	if err := writeSecretVersionStoreAtomic(path, prunedStore); err != nil {
		return summary, err
	}

	return summary, nil
}

func pruneSecretVersionStore(store secretVersionStore, keepLast int) (secretVersionStore, int, int) {
	if store == nil {
		store = make(secretVersionStore)
	}

	pruned := make(secretVersionStore, len(store))
	keysPruned := 0
	versionsRemoved := 0

	for key, versions := range store {
		if len(versions) <= keepLast {
			copied := append([]secretVersion(nil), versions...)
			pruned[key] = copied
			continue
		}

		start := len(versions) - keepLast
		copied := append([]secretVersion(nil), versions[start:]...)
		pruned[key] = copied
		keysPruned++
		versionsRemoved += start
	}

	return pruned, keysPruned, versionsRemoved
}

func resolveSecretStoreEntryByVersion(key string, versions []secretVersion, requestedVersion int) (SecretVersionStoreRestoreEntry, error) {
	for _, version := range versions {
		if version.Version == requestedVersion {
			return SecretVersionStoreRestoreEntry{
				Key:       key,
				Version:   version.Version,
				HashValue: version.HashValue,
				RotatedAt: version.RotatedAt,
			}, nil
		}
	}

	return SecretVersionStoreRestoreEntry{}, fmt.Errorf("key %s does not contain version %d", key, requestedVersion)
}

func resolveSecretStoreEntryByTime(key string, versions []secretVersion, at time.Time) (SecretVersionStoreRestoreEntry, error) {
	var selected *secretVersion
	var selectedTs time.Time

	for i := range versions {
		ts, err := time.Parse(time.RFC3339, versions[i].RotatedAt)
		if err != nil {
			return SecretVersionStoreRestoreEntry{}, fmt.Errorf("invalid rotated_at for key %s version %d: %v", key, versions[i].Version, err)
		}
		if ts.After(at) {
			continue
		}
		if selected == nil || ts.After(selectedTs) {
			selected = &versions[i]
			selectedTs = ts
		}
	}

	if selected == nil {
		return SecretVersionStoreRestoreEntry{}, fmt.Errorf("key %s has no version at or before %s", key, at.Format(time.RFC3339))
	}

	return SecretVersionStoreRestoreEntry{
		Key:       key,
		Version:   selected.Version,
		HashValue: selected.HashValue,
		RotatedAt: selected.RotatedAt,
	}, nil
}

func writeFileAtomic(path string, payload []byte, tempPattern string) error {
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}

	return nil
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum)
}
