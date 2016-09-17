// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	loglevel int
  Version string
  Build   string
)

func init() {
	viper.SetConfigType("toml")
	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/replication-manager/")
	viper.AddConfigPath(".")
	viper.ReadInConfig()
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&user, "user", "", "User for MariaDB login, specified in the [user]:[password] format")
	rootCmd.PersistentFlags().StringVar(&hosts, "hosts", "", "List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	rootCmd.PersistentFlags().StringVar(&rpluser, "rpluser", "", "Replication user in the [user]:[password] format")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().IntVar(&loglevel, "log-level", 0, "Log verbosity level")
	viper.BindPFlags(rootCmd.PersistentFlags())
	user = viper.GetString("user")
	hosts = viper.GetString("hosts")
	rpluser = viper.GetString("rpluser")
	loglevel = viper.GetInt("log-level")
	if verbose == true && loglevel == 0 {
		loglevel = 1
	}
	if verbose == false && loglevel > 0 {
		verbose = true
	}
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
		cmd.Usage()
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the replication manager version number",
	Long:  `All software has versions. This is ours`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("MariaDB Replication Manager version", repmgrVersion)
		fmt.Println("Version: ", Version)
		fmt.Println("Build Time: ", Build)
	},
}
