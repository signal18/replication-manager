package cluster

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
)

// resolveEncryptedRestoreSecretFromReference resolves the exact historical secret
// value referenced by encrypted backup metadata.
//
// It requires the original cluster secret_store.json history and fails closed
// when the versioned key reference cannot be resolved.
func (cluster *Cluster) resolveEncryptedRestoreSecretFromReference(keyRef string, keyCluster string) (string, error) {
	if cluster == nil || cluster.Conf == nil {
		return "", fmt.Errorf("cluster configuration is not available for encrypted restore")
	}

	source, version, err := backupmgr.ParseBackupSecretKeyReference(keyRef)
	if err != nil {
		return "", err
	}

	resolvedCluster := strings.TrimSpace(keyCluster)
	if resolvedCluster == "" {
		return "", fmt.Errorf("encrypted restore metadata missing encryption key cluster")
	}

	storePath := SecretVersionStorePath(cluster.Conf.WorkingDir, resolvedCluster)
	if _, err := os.Stat(storePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("encrypted restore requires original secret_store history, but %s is missing", storePath)
		}
		return "", fmt.Errorf("cannot access secret_store history %s: %w", storePath, err)
	}

	storeKey := source
	if source == backupmgr.BackupEncryptionSecretSourceAdmin {
		storeKey = "api-credentials"
	}

	entries, err := ResolveSecretVersionStoreEntries(storePath, []string{storeKey}, strconv.Itoa(version), nil)
	if err != nil {
		return "", fmt.Errorf("cannot resolve encrypted restore key reference %s: %w", keyRef, err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("cannot resolve encrypted restore key reference %s: no entries returned", keyRef)
	}

	decryptedValue := cluster.Conf.DecryptSecretValue(storeKey, entries[0].HashValue)

	switch source {
	case backupmgr.BackupEncryptionSecretSourceSponsor:
		_, pass := misc.SplitPair(strings.TrimSpace(decryptedValue))
		if strings.TrimSpace(pass) == "" {
			return "", fmt.Errorf("sponsor secret version %d is present but password is empty", version)
		}
		return pass, nil
	case backupmgr.BackupEncryptionSecretSourceAdmin:
		for _, credential := range strings.Split(decryptedValue, ",") {
			user, pass := misc.SplitPair(strings.TrimSpace(credential))
			if user == "admin" && strings.TrimSpace(pass) != "" {
				return pass, nil
			}
		}
		return "", fmt.Errorf("api-credentials version %d does not contain admin password", version)
	default:
		return "", fmt.Errorf("unsupported encrypted restore key source: %s", source)
	}
}
