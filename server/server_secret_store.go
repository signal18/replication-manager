//go:build !clients
// +build !clients

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	s18crypto "github.com/signal18/replication-manager/utils/crypto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	secretStorePruneKeepLast         int
	secretStorePruneDryRun           bool
	secretStoreCopyDryRun            bool
	secretStoreCopyOverwrite         bool
	secretStoreRestoreKeys           string
	secretStoreRestoreAll            bool
	secretStoreRestoreVer            string
	secretStoreRestoreAt             string
	secretStoreRestoreDecryptKeyPath string
	secretStoreRestoreDryRun         bool
	secretStoreApplyRuntime          bool
)

var secretHashTokenRegex = regexp.MustCompile(`hash_[^,:\s]+`)

func init() {
	ensureRepMan()

	secretStorePruneCmd.Flags().IntVar(&secretStorePruneKeepLast, "keep-last", 0, "Retain only the last N versions per secret key (required)")
	secretStorePruneCmd.Flags().BoolVar(&secretStorePruneDryRun, "dry-run", false, "Preview pruning without writing secret_store.json")
	secretStoreCopyCmd.Flags().BoolVar(&secretStoreCopyDryRun, "dry-run", false, "Preview copy without writing destination file")
	secretStoreCopyCmd.Flags().BoolVar(&secretStoreCopyOverwrite, "overwrite", false, "Overwrite destination when content differs")
	secretStoreRestoreCmd.Flags().StringVar(&secretStoreRestoreKeys, "secret-key", "", "Comma-separated secret keys to restore")
	secretStoreRestoreCmd.Flags().BoolVar(&secretStoreRestoreAll, "all-secrets", false, "Restore all secrets present in secret_store.json")
	secretStoreRestoreCmd.Flags().StringVar(&secretStoreRestoreVer, "secret-version", "", "Restore an exact secret version for all selected keys, or use 'latest'")
	secretStoreRestoreCmd.Flags().StringVar(&secretStoreRestoreAt, "at", "", "Restore latest version at or before RFC3339 timestamp for each selected key")
	secretStoreRestoreCmd.Flags().StringVar(&secretStoreRestoreDecryptKeyPath, "decrypt-key-path", "", "Legacy encryption key path used for decrypting stored values before re-encrypting with current cluster key")
	secretStoreRestoreCmd.Flags().BoolVar(&secretStoreRestoreDryRun, "dry-run", false, "Preview restore without writing configuration")
	secretStoreRestoreCmd.Flags().BoolVar(&secretStoreApplyRuntime, "apply-runtime", false, "Also apply restored secrets to in-process runtime cluster state")
	rootCmd.AddCommand(secretStorePruneCmd)
	rootCmd.AddCommand(secretStoreCopyCmd)
	rootCmd.AddCommand(secretStoreRestoreCmd)
}

var secretStorePruneCmd = &cobra.Command{
	Use:   "secret-store-prune",
	Short: "Manually prune cluster secret version history",
	Long:  "Prunes secret_store.json for a cluster by retaining only the last N versions per key.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfgGroup == "" {
			return fmt.Errorf("missing required --cluster value")
		}
		if secretStorePruneKeepLast < 1 {
			return fmt.Errorf("--keep-last must be >= 1")
		}

		RepMan.SetDefaultFlags(viper.GetViper())
		RepMan.InitConfig(conf, false)

		summary, err := pruneSecretStoreForCluster(RepMan, cfgGroup, secretStorePruneKeepLast, secretStorePruneDryRun)
		if err != nil {
			return err
		}

		fmt.Printf("Cluster: %s\n", cfgGroup)
		fmt.Printf("Store: %s\n", summary.StorePath)
		if !summary.Changed {
			fmt.Printf("No pruning needed\n")
			return nil
		}

		fmt.Printf("Pruned %d keys\n", summary.KeysPruned)
		fmt.Printf("Removed %d historical versions\n", summary.VersionsRemoved)
		if summary.DryRun {
			fmt.Printf("Dry run only; no changes written\n")
			return nil
		}

		fmt.Printf("Updated %s\n", summary.StorePath)
		return nil
	},
}

var secretStoreCopyCmd = &cobra.Command{
	Use:   "secret-store-copy",
	Short: "Copy secret store into monitoring confdir cluster.d",
	Long:  "Copies a cluster secret_store.json to {monitoring-confdir}/cluster.d/{cluster}_secret_store.json.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfgGroup == "" {
			return fmt.Errorf("missing required --cluster value")
		}

		RepMan.SetDefaultFlags(viper.GetViper())
		RepMan.InitConfig(conf, false)

		summary, err := copySecretStoreForCluster(RepMan, cfgGroup, secretStoreCopyDryRun, secretStoreCopyOverwrite)
		if err != nil {
			return err
		}

		fmt.Printf("Cluster: %s\n", cfgGroup)
		fmt.Printf("Source: %s\n", summary.SourcePath)
		fmt.Printf("Destination: %s\n", summary.DestinationPath)

		if summary.Skipped {
			switch summary.Reason {
			case "destination already up to date":
				fmt.Printf("Destination already up to date\n")
			case "dry run":
				fmt.Printf("Dry run only; copy would be performed\n")
			default:
				fmt.Printf("Skipped: %s\n", summary.Reason)
			}
			return nil
		}

		if summary.Copied {
			fmt.Printf("Copied secret store successfully\n")
		}
		return nil
	},
}

var secretStoreRestoreCmd = &cobra.Command{
	Use:   "secret-store-restore",
	Short: "Restore secret versions into cluster config",
	Long:  "Restores selected secret keys from secret_store.json into {monitoring-confdir}/cluster.d/{cluster}.toml.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfgGroup == "" {
			return fmt.Errorf("missing required --cluster value")
		}

		if strings.TrimSpace(secretStoreRestoreKeys) != "" && secretStoreRestoreAll {
			return fmt.Errorf("--secret-key and --all-secrets are mutually exclusive")
		}
		if strings.TrimSpace(secretStoreRestoreKeys) == "" && !secretStoreRestoreAll {
			return fmt.Errorf("either --secret-key or --all-secrets is required")
		}

		var keys []string
		var err error
		if strings.TrimSpace(secretStoreRestoreKeys) != "" {
			keys, err = parseRestoreKeys(secretStoreRestoreKeys)
			if err != nil {
				return err
			}
		}

		var at *time.Time
		if secretStoreRestoreAt != "" {
			parsed, err := time.Parse(time.RFC3339, secretStoreRestoreAt)
			if err != nil {
				return fmt.Errorf("invalid --at value, expected RFC3339 timestamp: %w", err)
			}
			at = &parsed
		}

		if strings.TrimSpace(secretStoreRestoreVer) != "" && at != nil {
			return fmt.Errorf("--secret-version and --at are mutually exclusive")
		}
		if strings.TrimSpace(secretStoreRestoreVer) == "" && at == nil {
			return fmt.Errorf("either --secret-version or --at is required")
		}

		RepMan.SetDefaultFlags(viper.GetViper())
		RepMan.InitConfig(conf, false)

		summary, err := restoreSecretStoreForCluster(RepMan, cfgGroup, keys, secretStoreRestoreAll, secretStoreRestoreVer, at, secretStoreRestoreDecryptKeyPath, secretStoreRestoreDryRun, secretStoreApplyRuntime)
		if err != nil {
			return err
		}

		fmt.Printf("Cluster: %s\n", cfgGroup)
		fmt.Printf("Target: %s\n", summary.ConfigPath)
		if summary.Mode == "version" {
			fmt.Printf("Mode: secret-version=%s\n", summary.RequestedVersion)
		} else {
			fmt.Printf("Mode: at=%s\n", summary.RequestedAt)
		}
		fmt.Printf("Selection: %s\n", summary.Selection)
		fmt.Printf("Keys:\n")
		for _, entry := range summary.Entries {
			fmt.Printf("- %s -> version %d (%s)\n", entry.Key, entry.Version, entry.RotatedAt)
		}

		if summary.DryRun {
			fmt.Printf("Dry run only; restore would update cluster config\n")
			return nil
		}

		fmt.Printf("Config restore completed\n")
		if summary.RuntimeApplied {
			fmt.Printf("Runtime apply completed\n")
		}
		return nil
	},
}

func pruneSecretStoreForCluster(repman *ReplicationManager, clusterName string, keepLast int, dryRun bool) (cluster.SecretVersionStorePruneSummary, error) {
	if repman == nil {
		return cluster.SecretVersionStorePruneSummary{}, fmt.Errorf("replication manager is not initialized")
	}
	if clusterName == "" {
		return cluster.SecretVersionStorePruneSummary{}, fmt.Errorf("cluster name is required")
	}

	clusterConf, ok := repman.Confs[clusterName]
	if !ok {
		return cluster.SecretVersionStorePruneSummary{}, fmt.Errorf("cluster %s not found in configuration", clusterName)
	}

	storePath := cluster.SecretVersionStorePath(clusterConf.WorkingDir, clusterName)
	return cluster.PruneSecretVersionStoreFile(storePath, keepLast, dryRun)
}

func copySecretStoreForCluster(repman *ReplicationManager, clusterName string, dryRun bool, overwrite bool) (cluster.SecretVersionStoreCopySummary, error) {
	if repman == nil {
		return cluster.SecretVersionStoreCopySummary{}, fmt.Errorf("replication manager is not initialized")
	}
	if clusterName == "" {
		return cluster.SecretVersionStoreCopySummary{}, fmt.Errorf("cluster name is required")
	}

	clusterConf, ok := repman.Confs[clusterName]
	if !ok {
		return cluster.SecretVersionStoreCopySummary{}, fmt.Errorf("cluster %s not found in configuration", clusterName)
	}

	confDir := clusterConf.ConfDir
	if confDir == "" && repman.Conf != nil {
		confDir = repman.Conf.ConfDir
	}
	if confDir == "" {
		return cluster.SecretVersionStoreCopySummary{}, fmt.Errorf("monitoring confdir is not configured")
	}

	srcPath := cluster.SecretVersionStorePath(clusterConf.WorkingDir, clusterName)
	dstPath := cluster.SecretVersionStoreExportPath(confDir, clusterName)
	return cluster.CopySecretVersionStoreFile(srcPath, dstPath, dryRun, overwrite)
}

type secretStoreRestoreSummary struct {
	ConfigPath       string
	Selection        string
	Mode             string
	RequestedVersion string
	RequestedAt      string
	Entries          []cluster.SecretVersionStoreRestoreEntry
	DryRun           bool
	RuntimeApplied   bool
}

func restoreSecretStoreForCluster(repman *ReplicationManager, clusterName string, keys []string, allSecrets bool, versionSelector string, at *time.Time, legacyKeyPath string, dryRun bool, applyRuntime bool) (secretStoreRestoreSummary, error) {
	if repman == nil {
		return secretStoreRestoreSummary{}, fmt.Errorf("replication manager is not initialized")
	}
	if clusterName == "" {
		return secretStoreRestoreSummary{}, fmt.Errorf("cluster name is required")
	}

	clusterConf, ok := repman.Confs[clusterName]
	if !ok {
		return secretStoreRestoreSummary{}, fmt.Errorf("cluster %s not found in configuration", clusterName)
	}

	storePath := cluster.SecretVersionStorePath(clusterConf.WorkingDir, clusterName)
	selection := "explicit keys"
	if allSecrets {
		storeKeys, err := cluster.ListSecretVersionStoreKeys(storePath)
		if err != nil {
			return secretStoreRestoreSummary{}, err
		}
		if len(storeKeys) == 0 {
			return secretStoreRestoreSummary{}, fmt.Errorf("no secrets found in secret store for cluster %s", clusterName)
		}
		keys = storeKeys
		selection = "all secrets from store"
	}
	if len(keys) == 0 {
		return secretStoreRestoreSummary{}, fmt.Errorf("at least one key is required")
	}

	if applyRuntime {
		if err := validateSupportedRuntimeApplyKeys(keys); err != nil {
			return secretStoreRestoreSummary{}, err
		}
	}

	entries, err := cluster.ResolveSecretVersionStoreEntries(storePath, keys, versionSelector, at)
	if err != nil {
		return secretStoreRestoreSummary{}, err
	}

	if strings.TrimSpace(legacyKeyPath) != "" {
		entries, err = reencryptRestoreEntriesWithLegacyKey(entries, legacyKeyPath, clusterConf, repman.Conf)
		if err != nil {
			return secretStoreRestoreSummary{}, err
		}
	}

	confDir := clusterConf.ConfDir
	if confDir == "" && repman.Conf != nil {
		confDir = repman.Conf.ConfDir
	}
	if confDir == "" {
		return secretStoreRestoreSummary{}, fmt.Errorf("monitoring confdir is not configured")
	}

	configPath := filepath.Join(confDir, "cluster.d", clusterName+".toml")

	summary := secretStoreRestoreSummary{
		ConfigPath:       configPath,
		Selection:        selection,
		RequestedVersion: versionSelector,
		Entries:          entries,
		DryRun:           dryRun,
	}
	if strings.TrimSpace(versionSelector) != "" {
		summary.Mode = "version"
	}
	if at != nil {
		summary.Mode = "at"
		summary.RequestedAt = at.Format(time.RFC3339)
	}

	if dryRun {
		return summary, nil
	}

	if err := applyRestoreEntriesToClusterConfigFile(configPath, clusterName, entries); err != nil {
		return summary, err
	}

	if applyRuntime {
		if err := applyRestoreEntriesToRuntime(repman, clusterName, entries); err != nil {
			return summary, err
		}
		summary.RuntimeApplied = true
	}

	return summary, nil
}

func reencryptRestoreEntriesWithLegacyKey(entries []cluster.SecretVersionStoreRestoreEntry, legacyKeyPath string, clusterConf config.Config, globalConf *config.Config) ([]cluster.SecretVersionStoreRestoreEntry, error) {
	legacyKey, err := s18crypto.ReadKey(legacyKeyPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read legacy key from %s: %w", legacyKeyPath, err)
	}

	currentConf := clusterConf
	if currentConf.MonitoringKeyPath == "" && globalConf != nil {
		currentConf.MonitoringKeyPath = globalConf.MonitoringKeyPath
	}
	if currentConf.MonitoringKeyPath == "" {
		return nil, fmt.Errorf("current cluster monitoring key path is not configured")
	}
	if _, err := currentConf.LoadEncrytionKey(); err != nil {
		return nil, fmt.Errorf("cannot load current cluster key from %s: %w", currentConf.MonitoringKeyPath, err)
	}

	transformed := make([]cluster.SecretVersionStoreRestoreEntry, len(entries))
	copy(transformed, entries)

	for i := range transformed {
		newValue, err := reencryptStoredHashTokens(transformed[i].HashValue, legacyKey, &currentConf)
		if err != nil {
			return nil, fmt.Errorf("failed to transform key %s: %w", transformed[i].Key, err)
		}
		transformed[i].HashValue = newValue
	}

	return transformed, nil
}

func reencryptStoredHashTokens(value string, legacyKey []byte, currentConf *config.Config) (string, error) {
	if value == "" {
		return value, nil
	}

	matches := secretHashTokenRegex.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}

	var out strings.Builder
	last := 0
	for _, m := range matches {
		out.WriteString(value[last:m[0]])
		token := value[m[0]:m[1]]

		plain, err := decryptHashTokenWithKey(token, legacyKey)
		if err != nil {
			return "", err
		}

		encrypted := currentConf.GetEncryptedString(plain)
		if !strings.HasPrefix(encrypted, "hash_") {
			return "", fmt.Errorf("failed to encrypt token with current cluster key")
		}
		out.WriteString(encrypted)
		last = m[1]
	}
	out.WriteString(value[last:])

	return out.String(), nil
}

func decryptHashTokenWithKey(token string, key []byte) (string, error) {
	if !strings.HasPrefix(token, "hash_") {
		return token, nil
	}

	p := s18crypto.Password{Key: key, CipherText: strings.TrimPrefix(token, "hash_")}
	if err := p.Decrypt(); err != nil {
		return "", err
	}
	return p.PlainText, nil
}

func applyRestoreEntriesToClusterConfigFile(configPath string, clusterName string, entries []cluster.SecretVersionStoreRestoreEntry) error {
	tree, _ := toml.TreeFromMap(map[string]interface{}{})
	if data, err := os.ReadFile(configPath); err == nil {
		loaded, err := toml.LoadBytes(data)
		if err != nil {
			return err
		}
		tree = loaded
	} else if !os.IsNotExist(err) {
		return err
	}

	for _, entry := range entries {
		tree.Set(clusterName+"."+entry.Key, entry.HashValue)
	}

	payload := []byte(tree.String())
	return writeConfigFileAtomic(configPath, payload)
}

func writeConfigFileAtomic(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "cluster_restore-*.tmp")
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
	return os.Rename(tmpPath, path)
}

func validateSupportedRuntimeApplyKeys(keys []string) error {
	for _, key := range keys {
		if _, ok := supportedRuntimeApplySecretKeys[key]; !ok {
			return fmt.Errorf("unsupported --apply-runtime key: %s", key)
		}
	}
	return nil
}

var supportedRuntimeApplySecretKeys = map[string]bool{
	"db-servers-credential":            true,
	"replication-credential":           true,
	"cloud18-dba-user-credentials":     true,
	"cloud18-sponsor-user-credentials": true,
	"backup-restic-password":           true,
}

func parseRestoreKeys(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("missing required --secret-key value")
	}

	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			return nil, fmt.Errorf("invalid empty key in --secret-key list")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	return keys, nil
}

func applyRestoreEntriesToRuntime(repman *ReplicationManager, clusterName string, entries []cluster.SecretVersionStoreRestoreEntry) error {
	cl, ok := repman.Clusters[clusterName]
	if !ok {
		return fmt.Errorf("cluster %s is not loaded in runtime", clusterName)
	}

	for _, entry := range entries {
		switch entry.Key {
		case "db-servers-credential":
			var secret config.Secret
			secret.Value = entry.HashValue
			secret.OldValue = cl.Conf.GetDecryptedValue(entry.Key)
			cl.Conf.Secrets[entry.Key] = secret
			cl.SetClusterMonitorCredentialsFromConfig()
		case "replication-credential":
			cl.SetReplicationCredential(entry.HashValue)
		case "cloud18-dba-user-credentials":
			if err := cl.SetDatabaseCredentials("dba", entry.HashValue); err != nil {
				return err
			}
		case "cloud18-sponsor-user-credentials":
			if err := cl.SetDatabaseCredentials("sponsor", entry.HashValue); err != nil {
				return err
			}
		case "backup-restic-password":
			cl.SetResticPassword(entry.HashValue)
		default:
			return fmt.Errorf("unsupported runtime restore key: %s", entry.Key)
		}
	}

	cl.MarkSecretVersionStoreDirty()
	cl.ReconcileSecretVersionStore()
	return nil
}
