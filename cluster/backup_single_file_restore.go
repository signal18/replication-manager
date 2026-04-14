package cluster

import (
	"fmt"
	"path/filepath"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// runEncryptedSingleFileRestorePipeline executes the shared decrypt/verify pipeline
// for single-file encrypted backup artifacts.
//
// Pipeline order: metadata preflight → key resolution → HMAC verification → AES-256-CBC decryption.
// The file at path is decrypted in-place on success; restore aborts before touching backup data on any failure.
func (cluster *Cluster) runEncryptedSingleFileRestorePipeline(path string, meta *backupmgr.BackupMetadata) error {
	if err := backupmgr.ValidateEncryptedRestorePreflight(meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted restore preflight failed for %s: %v", path, err)
		return fmt.Errorf("encrypted restore preflight failed: %w", err)
	}

	secret, err := cluster.resolveEncryptedRestoreSecretFromReference(meta.EncryptionKey, meta.EncryptionKeyCluster)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted restore key resolution failed for %s: %v", path, err)
		return fmt.Errorf("encrypted restore key resolution failed: %w", err)
	}

	if err := backupmgr.VerifyBackupFileHMACSHA256(path, secret, meta.EncryptionMAC); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted restore HMAC verification failed for %s: %v", path, err)
		return fmt.Errorf("encrypted restore HMAC verification failed: %w", err)
	}

	if err := backupmgr.DecryptBackupFileAES256CBC(path, secret, meta.EncryptionIV); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Encrypted restore decryption failed for %s: %v", path, err)
		return fmt.Errorf("encrypted restore decryption failed: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Decrypted single-file backup artifact %s using %s", path, backupmgr.BackupEncryptionAlgorithm)

	return nil
}

// resolvePhysicalBackupMetaFromPath resolves the sidecar metadata file for a physical backup
// by convention: {dir}/{backupType}.meta.json lives alongside the backup artifact.
// Returns nil when metadata cannot be loaded (missing file, parse error, etc.).
func resolvePhysicalBackupMetaFromPath(backupfile, backupType string) *backupmgr.BackupMetadata {
	metaPath := filepath.Join(filepath.Dir(backupfile), backupType+".meta.json")
	meta, err := readBackupMetadataFile(metaPath)
	if err != nil {
		return nil
	}
	return meta
}
