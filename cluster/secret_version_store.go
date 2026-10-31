package cluster

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

const secretVersionStoreFilename = "secret_store.json"
const SecretVersionLatest = "latest"

// secretVersion is one version in the file: the version store's (#2227), with its writer.
type secretVersion = credstore.FileVersion

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

const secretHashPrefix = "hash_"

// MarkSecretVersionStoreDirty asks the next save to record the current secrets as versions
// and write the file: the save event, the file is written by the save callback only (T25).
func (cluster *Cluster) MarkSecretVersionStoreDirty() {
	if cluster == nil {
		return
	}

	cluster.secretVersionStoreMu.Lock()
	cluster.secretVersionStoreDirty = true
	cluster.secretVersionStoreMu.Unlock()
	cluster.Save()
}

// ReconcileSecretVersionStore is the save callback's part for the local versioned file
// (#2227 rule 2): when asked (MarkSecretVersionStoreDirty), the current value of each tracked
// secret becomes a version when it differs from the last one (a secret without a version gets
// version 1: the starting value); the versions the chain made at each change are already
// there, with their own time and writer. Then the file is written, atomically, from the
// version store. Encrypted values only (F9). Nothing when versioning is off.
func (cluster *Cluster) ReconcileSecretVersionStore() {
	if cluster == nil || cluster.Conf == nil {
		return
	}

	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}

	vs := cluster.versionStore()
	cluster.secretVersionStoreMu.Lock()
	dirty := cluster.secretVersionStoreDirty
	cluster.secretVersionStoreDirty = false
	cluster.secretVersionStoreMu.Unlock()
	if dirty {
		currentValues := cluster.getTrackedSecretStoreValues()
		for _, key := range sortedTrackedSecretKeys(currentValues) {
			current := currentValues[key]
			vs.Record(key, current.StoredValue, current.CompareValue)
		}
	}
	if err := cluster.writeSecretVersionFile(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to persist secret version store %s: %v", SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name), err)
	}
}

func (cluster *Cluster) getSecretVersionStoreAutoPruneSettings() (bool, int) {
	if cluster == nil || cluster.Conf == nil {
		return false, 0
	}

	if !cluster.Conf.MonitoringSecretVersioningAutoPrune {
		return false, 0
	}

	if cluster.Conf.MonitoringSecretVersioningKeepLast < 1 {
		return false, 0
	}

	return true, cluster.Conf.MonitoringSecretVersioningKeepLast
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
	if cluster.Conf == nil {
		return values
	}

	for key, secret := range cluster.Conf.Secrets.Snapshot() {
		if secret.Value == "" {
			continue
		}

		storedValue := cluster.GetEncryptedValueFromMemory(key)
		if storedValue == "" || !containsHashPrefixToken(storedValue) {
			storedValue = secret.Value
		}

		if storedValue == "" {
			continue
		}

		if !containsHashPrefixToken(storedValue) {
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

func containsHashPrefixToken(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}

	tokens := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ':', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	})

	for _, token := range tokens {
		if strings.HasPrefix(token, secretHashPrefix) {
			return true
		}
	}

	return false
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

func ResolveSecretVersionStoreEntries(path string, keys []string, versionSelector string, at *time.Time) ([]SecretVersionStoreRestoreEntry, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one key is required")
	}
	hasVersionSelector := strings.TrimSpace(versionSelector) != ""
	if hasVersionSelector && at != nil {
		return nil, fmt.Errorf("secret-version and at are mutually exclusive")
	}
	if !hasVersionSelector && at == nil {
		return nil, fmt.Errorf("either secret-version or at must be set")
	}

	requestedVersion := 0
	isLatest := false
	if hasVersionSelector {
		sel := strings.TrimSpace(versionSelector)
		if strings.EqualFold(sel, SecretVersionLatest) {
			isLatest = true
		} else {
			parsed, err := strconv.Atoi(sel)
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("invalid secret-version %q: expected positive integer or %q", versionSelector, SecretVersionLatest)
			}
			requestedVersion = parsed
		}
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

		if isLatest {
			entry := versions[len(versions)-1]
			resolved = append(resolved, SecretVersionStoreRestoreEntry{
				Key:       key,
				Version:   entry.Version,
				HashValue: entry.HashValue,
				RotatedAt: entry.RotatedAt,
			})
			continue
		}

		if requestedVersion > 0 {
			entry, err := resolveSecretStoreEntryByVersion(key, versions, requestedVersion)
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
		// the value a config file held when last seen stays, to tell an edit of the file
		// (#2227 rule 9)
		if seen := lastFromConfigFile(versions); seen >= 0 && seen < start {
			copied = append([]secretVersion{versions[seen]}, copied...)
			start--
		}
		pruned[key] = copied
		if start > 0 {
			keysPruned++
		}
		versionsRemoved += start
	}

	return pruned, keysPruned, versionsRemoved
}

// lastFromConfigFile is the index of the last version marked from_config_file, else -1.
func lastFromConfigFile(versions []secretVersion) int {
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].FromConfigFile {
			return i
		}
	}
	return -1
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
