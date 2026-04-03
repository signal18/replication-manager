//go:build !clients
// +build !clients

package server

import (
	"fmt"

	"github.com/signal18/replication-manager/cluster"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	secretStorePruneKeepLast int
	secretStorePruneDryRun   bool
	secretStoreCopyDryRun    bool
	secretStoreCopyOverwrite bool
)

func init() {
	if RepMan == nil {
		RepMan = new(ReplicationManager)
		RepMan.InitUser()
	}

	secretStorePruneCmd.Flags().IntVar(&secretStorePruneKeepLast, "keep-last", 0, "Retain only the last N versions per secret key (required)")
	secretStorePruneCmd.Flags().BoolVar(&secretStorePruneDryRun, "dry-run", false, "Preview pruning without writing secret_store.json")
	secretStoreCopyCmd.Flags().BoolVar(&secretStoreCopyDryRun, "dry-run", false, "Preview copy without writing destination file")
	secretStoreCopyCmd.Flags().BoolVar(&secretStoreCopyOverwrite, "overwrite", false, "Overwrite destination when content differs")
	rootCmd.AddCommand(secretStorePruneCmd)
	rootCmd.AddCommand(secretStoreCopyCmd)
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
