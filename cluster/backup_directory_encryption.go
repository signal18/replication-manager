package cluster

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// finalizeDirectoryEncryption encrypts all regular files inside outputDir in-place,
// computes HMAC-SHA256 for each file, writes the encryption manifest alongside the
// directory, and updates server.LastBackupMeta.Logical to reflect the encrypted state.
//
// It is called after a directory-based backup tool (mydumper, dumpling) has
// completed writing files to outputDir and before backup metadata is persisted.
func (server *ServerMonitor) finalizeDirectoryEncryption(outputDir string, toolName string) error {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.Conf == nil {
		return fmt.Errorf("cluster configuration is not available for directory backup encryption")
	}

	// Reuse the same key material resolution as single-file encryption.
	secret, keyRef, err := cluster.resolveMysqldumpBackupEncryptionMaterial()
	if err != nil {
		return err
	}

	cleanDir := strings.TrimRight(outputDir, "/\\")

	entries, err := encryptDirectoryFiles(cleanDir, secret)
	if err != nil {
		return err
	}

	manifest := &backupmgr.BackupEncryptionManifest{
		KeyRef:     keyRef,
		KeyCluster: cluster.Name,
		Entries:    entries,
	}
	if err := backupmgr.WriteBackupEncryptionManifest(cleanDir, manifest); err != nil {
		return err
	}

	server.backupMetaMutex.Lock()
	if server.LastBackupMeta.Logical != nil {
		server.LastBackupMeta.Logical.Encrypted = true
		server.LastBackupMeta.Logical.EncryptionAlgo = backupmgr.BackupEncryptionAlgorithm
		server.LastBackupMeta.Logical.EncryptionKey = keyRef
		server.LastBackupMeta.Logical.EncryptionKeyCluster = cluster.Name
		// IV and MAC are per-file; stored in the manifest, not in the single-file metadata fields.
	}
	server.backupMetaMutex.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Encrypted %d %s backup artifact(s) in %s with %s; manifest written",
		len(entries), toolName, cleanDir, backupmgr.BackupEncryptionAlgorithm)

	return nil
}

// encryptDirectoryFiles walks dir, encrypts each regular file in-place, and returns
// the manifest entries (path relative to dir, IV token, MAC).
func encryptDirectoryFiles(dir string, secret string) ([]backupmgr.BackupEncryptionManifestEntry, error) {
	var entries []backupmgr.BackupEncryptionManifestEntry

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("cannot compute relative path for %s: %w", path, err)
		}

		ivToken, err := backupmgr.EncryptBackupFileAES256CBC(path, secret)
		if err != nil {
			return fmt.Errorf("encryption failed for %s: %w", relPath, err)
		}

		mac, err := backupmgr.ComputeBackupFileHMACSHA256(path, secret)
		if err != nil {
			return fmt.Errorf("HMAC computation failed for %s: %w", relPath, err)
		}

		entries = append(entries, backupmgr.BackupEncryptionManifestEntry{
			Path: relPath,
			IV:   ivToken,
			MAC:  mac,
		})

		return nil
	})

	return entries, err
}

// runEncryptedDirectoryRestorePipeline reads the encryption manifest for outputDir,
// resolves the key, validates all entries (existence + HMAC), and decrypts each file
// in-place. Restore fails closed if any entry is missing or tampered.
//
// If no manifest file is found, the function returns nil (non-encrypted backup, pass-through).
func (cluster *Cluster) runEncryptedDirectoryRestorePipeline(outputDir string) error {
	if cluster == nil || cluster.Conf == nil {
		return fmt.Errorf("cluster configuration is not available for encrypted directory restore")
	}

	cleanDir := strings.TrimRight(outputDir, "/\\")

	manifest, err := backupmgr.ReadBackupEncryptionManifest(cleanDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no manifest → not encrypted, pass through
		}
		return fmt.Errorf("encrypted directory restore: failed to read manifest for %s: %w", cleanDir, err)
	}

	secret, err := cluster.resolveEncryptedRestoreSecretFromReference(manifest.KeyRef, manifest.KeyCluster)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted directory restore key resolution failed for %s: %v", cleanDir, err)
		return fmt.Errorf("encrypted directory restore key resolution failed: %w", err)
	}

	if err := backupmgr.ValidateBackupEncryptionManifestEntries(manifest, cleanDir, secret); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted directory restore manifest validation failed for %s: %v", cleanDir, err)
		return fmt.Errorf("encrypted directory restore manifest validation failed (fail-closed): %w", err)
	}

	for i, entry := range manifest.Entries {
		artifactPath := filepath.Join(cleanDir, entry.Path)
		if err := backupmgr.DecryptBackupFileAES256CBC(artifactPath, secret, entry.IV); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"Encrypted directory restore decryption failed for entry[%d] %q: %v", i, entry.Path, err)
			return fmt.Errorf("encrypted directory restore decryption failed for %q: %w", entry.Path, err)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Decrypted %d artifact(s) in %s using %s", len(manifest.Entries), cleanDir, backupmgr.BackupEncryptionAlgorithm)

	return nil
}
