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
)

func init() {
	if RepMan == nil {
		RepMan = new(ReplicationManager)
		RepMan.InitUser()
	}

	secretStorePruneCmd.Flags().IntVar(&secretStorePruneKeepLast, "keep-last", 0, "Retain only the last N versions per secret key (required)")
	secretStorePruneCmd.Flags().BoolVar(&secretStorePruneDryRun, "dry-run", false, "Preview pruning without writing secret_store.json")
	rootCmd.AddCommand(secretStorePruneCmd)
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
