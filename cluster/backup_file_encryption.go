package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func (cluster *Cluster) resolveMysqldumpBackupEncryptionMaterial() (string, string, error) {
	if cluster == nil || cluster.Conf == nil {
		return "", "", fmt.Errorf("cluster configuration is not available for backup encryption")
	}

	sponsorCredentials := cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials")
	apiCredentials := cluster.Conf.GetDecryptedValue("api-credentials")

	secret, source, err := backupmgr.ResolveBackupEncryptionKeyMaterial(sponsorCredentials, apiCredentials)
	if err != nil {
		return "", "", err
	}

	storePath := SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name)
	storeKey := source
	if source == backupmgr.BackupEncryptionSecretSourceAdmin {
		storeKey = "api-credentials"
	}

	entries, err := ResolveSecretVersionStoreEntries(storePath, []string{storeKey}, SecretVersionLatest, nil)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve backup encryption key version from secret store %s: %w", storePath, err)
	}
	if len(entries) == 0 {
		return "", "", fmt.Errorf("cannot resolve backup encryption key version from secret store %s: no entries returned", storePath)
	}

	keyRef, err := backupmgr.FormatBackupSecretKeyReference(source, entries[0].Version)
	if err != nil {
		return "", "", err
	}

	return secret, keyRef, nil
}

func (server *ServerMonitor) finalizeMysqldumpSingleFileEncryption(path string) error {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.Conf == nil {
		return nil
	}
	if !cluster.Conf.BackupEncryption {
		return nil
	}

	secret, keyRef, err := cluster.resolveMysqldumpBackupEncryptionMaterial()
	if err != nil {
		return err
	}

	iv, err := backupmgr.EncryptBackupFileAES256CBC(path, secret)
	if err != nil {
		return err
	}

	mac, err := backupmgr.ComputeBackupFileHMACSHA256(path, secret)
	if err != nil {
		return err
	}

	server.backupMetaMutex.Lock()
	if server.LastBackupMeta.Logical != nil {
		server.LastBackupMeta.Logical.Encrypted = true
		server.LastBackupMeta.Logical.EncryptionAlgo = backupmgr.BackupEncryptionAlgorithm
		server.LastBackupMeta.Logical.EncryptionIV = strings.TrimSpace(iv)
		server.LastBackupMeta.Logical.EncryptionMAC = strings.TrimSpace(mac)
		server.LastBackupMeta.Logical.EncryptionKey = keyRef
		server.LastBackupMeta.Logical.EncryptionKeyCluster = cluster.Name
	}
	server.backupMetaMutex.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Encrypted mysqldump backup artifact %s with %s", path, backupmgr.BackupEncryptionAlgorithm)

	return nil
}

// finalizePhysicalSingleFileEncryption encrypts a physical backup artifact
// (xtrabackup/mariabackup) in-place when backup-encryption is enabled.
// It populates encryption metadata on server.LastBackupMeta.Physical.
func (server *ServerMonitor) finalizePhysicalSingleFileEncryption(path string) error {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.Conf == nil {
		return nil
	}
	if !cluster.Conf.BackupEncryption {
		return nil
	}

	secret, keyRef, err := cluster.resolveMysqldumpBackupEncryptionMaterial()
	if err != nil {
		return err
	}

	iv, err := backupmgr.EncryptBackupFileAES256CBC(path, secret)
	if err != nil {
		return err
	}

	mac, err := backupmgr.ComputeBackupFileHMACSHA256(path, secret)
	if err != nil {
		return err
	}

	server.backupMetaMutex.Lock()
	if server.LastBackupMeta.Physical != nil {
		server.LastBackupMeta.Physical.Encrypted = true
		server.LastBackupMeta.Physical.EncryptionAlgo = backupmgr.BackupEncryptionAlgorithm
		server.LastBackupMeta.Physical.EncryptionIV = strings.TrimSpace(iv)
		server.LastBackupMeta.Physical.EncryptionMAC = strings.TrimSpace(mac)
		server.LastBackupMeta.Physical.EncryptionKey = keyRef
		server.LastBackupMeta.Physical.EncryptionKeyCluster = cluster.Name
	}
	server.backupMetaMutex.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Encrypted physical backup artifact %s with %s", path, backupmgr.BackupEncryptionAlgorithm)

	return nil
}
