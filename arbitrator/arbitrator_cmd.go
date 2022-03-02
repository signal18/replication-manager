//go:build arbitrator
// +build arbitrator

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package arbitrator

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "replication-manager",
	Short: "Replication Manager tool for MariaDB and MySQL",
	// Copyright 2017-2021 SIGNAL18 CLOUD SAS
	Long: `replication-manager allows users to monitor interactively MariaDB 10.x and MySQL GTID replication health
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
		fmt.Println("Replication Manager " + Version + " for MariaDB 10.x and MySQL 5.7 Series")
		fmt.Println("Full Version: ", FullVersion)
		fmt.Println("Build Time: ", Build)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	//initLogFlags(cmd)
	conf.GoArch = GoArch
	conf.GoOS = GoOS
	conf.Version = Version
	conf.FullVersion = FullVersion
	conf.MemProfile = memprofile
	conf.WithTarball = WithTarball
	rootCmd.PersistentFlags().StringVar(&conf.ConfigFile, "config", "", "Configuration file (default is config.toml)")
	rootCmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Configuration group (default is none)")
	rootCmd.Flags().StringVar(&conf.KeyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	rootCmd.PersistentFlags().BoolVar(&conf.Verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().StringVar(&memprofile, "memprofile", "/tmp/repmgr.mprof", "Write a memory profile to a file readable by pprof")

}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return nil
}

func initLogFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&conf.LogFile, "log-file", "", "Write output messages to log file")
	cmd.Flags().BoolVar(&conf.LogSyslog, "log-syslog", false, "Enable logging to syslog")
	cmd.Flags().IntVar(&conf.LogLevel, "log-level", 0, "Log verbosity level")
	cmd.Flags().IntVar(&conf.LogRotateMaxSize, "log-rotate-max-size", 5, "Log rotate max size")
	cmd.Flags().IntVar(&conf.LogRotateMaxBackup, "log-rotate-max-backup", 7, "Log rotate max backup")
	cmd.Flags().IntVar(&conf.LogRotateMaxAge, "log-rotate-max-age", 7, "Log rotate max age")

	viper.BindPFlags(cmd.Flags())

}
