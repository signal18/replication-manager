package cluster

import (
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/misc"
)

const secretVersionStoreFilename = "secret_store.json"
const SecretVersionLatest = "latest"

// secretVersion is one version in the file: the version store's (#2227), with its writer.
type secretVersion = credstore.FileVersion

type secretVersionStore = map[string][]secretVersion

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

// recordDirtySecretVersions is the memory part of the reconcile (#2227 rule 2): when asked
// (MarkSecretVersionStoreDirty), the current value of each tracked secret becomes a version
// when it differs from the last one (a secret without a version gets version 1: the starting
// value); the versions the chain made at each change are already there, with their own time
// and writer. Memory only, no file: the file is written by the save callback (T25), so a
// reader of the history (a backup recording the password version it used) calls this and
// never ReconcileSecretVersionStore. Versioning on only.
func (cluster *Cluster) recordDirtySecretVersions() {
	if cluster == nil || cluster.Conf == nil || !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := cluster.versionStore()
	cluster.secretVersionStoreMu.Lock()
	dirty := cluster.secretVersionStoreDirty
	cluster.secretVersionStoreDirty = false
	cluster.secretVersionStoreMu.Unlock()
	if !dirty {
		return
	}
	currentValues := cluster.getTrackedSecretStoreValues()
	for _, key := range sortedTrackedSecretKeys(currentValues) {
		current := currentValues[key]
		vs.Record(key, current.StoredValue, current.CompareValue)
	}
	// asked to save: a file removed since is written again, even with no new version
	if _, err := os.Stat(cluster.secretKv.file.Path()); os.IsNotExist(err) && !vs.Empty() {
		vs.MarkDirty()
	}
}

// ReconcileSecretVersionStore is the save callback's part for the local versioned file
// (#2227 rule 2): the tracked secrets recorded as versions (recordDirtySecretVersions), then
// the file written, atomically, from the version store. It is called by the save callback
// only (T25): every other path marks the store dirty and saves. Encrypted values only (F9).
// Nothing when versioning is off. The error is the file's write: the save is then not
// complete (ConfigSavedAsOf).
func (cluster *Cluster) ReconcileSecretVersionStore() error {
	if cluster == nil || cluster.Conf == nil {
		return nil
	}

	if !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return nil
	}

	cluster.recordDirtySecretVersions()
	if err := cluster.writeSecretVersionFile(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to persist secret version store %s: %v", SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name), err)
		return err
	}
	return nil
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
		// a server secret's versions are the server's only (CREDENTIAL_STORES.md "Two levels")
		if secret.Value == "" || cluster.isServerSecret(key) {
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

// containsHashPrefixToken: the value holds a repman-encrypted token (credstore.LooksEncrypted).
func containsHashPrefixToken(value string) bool {
	return credstore.LooksEncrypted(value)
}

// getStoredSecretCompareValue converts a stored encrypted/hash payload into the
// plaintext semantic value used for comparison only.
func (cluster *Cluster) getStoredSecretCompareValue(key string, value string) string {
	if value == "" || cluster == nil || cluster.Conf == nil {
		return value
	}

	return cluster.Conf.DecryptSecretValue(key, value)
}

// loadSecretVersionStore reads a versioned file: the secret_kv-config plugin's reader.
func loadSecretVersionStore(path string) (secretVersionStore, error) {
	return credstore.LoadVersionFile(path)
}

// writeSecretVersionStoreAtomic writes a versioned file: the secret_kv-config plugin's writer.
func writeSecretVersionStoreAtomic(path string, store secretVersionStore) error {
	return credstore.WriteVersionFile(path, store)
}

func sortedTrackedSecretKeys(values map[string]trackedSecretValue) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// SecretVersionStorePath is a cluster's versioned local file, as the secret_kv-config plugin
// places it (credstore.LocalFile).
func SecretVersionStorePath(workingDir string, clusterName string) string {
	return credstore.LocalFile{DataDir: workingDir}.Path(credstore.ClusterScope(clusterName))
}

// SecretVersionStoreExportPath is a cluster's exported copy, as the plugin places it.
func SecretVersionStoreExportPath(confDir string, clusterName string) string {
	return credstore.LocalFile{DataDir: confDir}.ExportPath(confDir, credstore.ClusterScope(clusterName))
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

// pruneSecretVersionStore keeps the last keepLast versions of each key (credstore.PruneVersions).
func pruneSecretVersionStore(store secretVersionStore, keepLast int) (secretVersionStore, int, int) {
	return credstore.PruneVersions(store, keepLast)
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

// writeFileAtomic writes a secret file durably, owner-only (misc.WriteFileDurable).
func writeFileAtomic(path string, payload []byte, tempPattern string) error {
	return misc.WriteFileDurable(path, payload, 0o600, tempPattern)
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum)
}
