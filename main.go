package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&conf, "conf", "config.toml", "Path of the configuration file")
	rootCmd.PersistentFlags().StringVar(&user, "user", "", "User for MariaDB login, specified in the [user]:[password] format")
	rootCmd.PersistentFlags().StringVar(&hosts, "hosts", "", "List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	rootCmd.PersistentFlags().StringVar(&rpluser, "rpluser", "", "Replication user in the [user]:[password] format")
	rootCmd.PersistentFlags().BoolVar(&interactive, "interactive", true, "Ask for user interaction when failures are detected")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Print detailed execution info")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "replication-manager",
	Short: "MariaDB Replication Manager Utility",
	Long: `replication-manager allows users to monitor interactively MariaDB 10.x GTID replication health
and trigger slave to master promotion (aka switchover), or elect a new master in case of failure (aka failover).`,
	Run: func(cmd *cobra.Command, args []string) {

	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the replication manager version number",
	Long:  `All software has versions. This is ours`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("MariaDB Replication Manager version", repmgrVersion)
	},
}
