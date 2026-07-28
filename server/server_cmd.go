// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	memprofile string
	cpuprofile string
	// Version is the semantic version number, e.g. 1.0.1
	Version string
	// Provisoning to add flags for compile
	WithProvisioning      string = "ON"
	WithArbitration       string = "OFF"
	WithArbitrationClient string = "ON"
	WithProxysql          string = "ON"
	WithHaproxy           string = "ON"
	WithMaxscale          string = "ON"
	WithMariadbshardproxy string = "ON"
	WithMonitoring        string = "ON"
	WithMail              string = "ON"
	WithHttp              string = "ON"
	WithSpider            string
	WithEnforce           string = "ON"
	WithDeprecate         string = "ON"
	WithOpenSVC           string = "OFF"
	WithTarball           string
	WithEmbed             string = "OFF"
	WithMySQLRouter       string
	WithSphinx            string = "ON"
	WithBackup            string = "ON"
	WithReact             string = "ON"
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build         string
	GoOS          string = "linux"
	GoArch        string = "amd64"
	conf          config.Config
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

	conf.GoArch = GoArch
	conf.GoOS = GoOS
	conf.Version = Version
	conf.FullVersion = FullVersion
	conf.WithTarball = WithTarball
	conf.WithEmbed = WithEmbed
	rootCmd.PersistentFlags().StringVar(&conf.ConfigFile, "config", "", "Configuration file (default is config.toml)")
	rootCmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Configuration group (default is none)")
	rootCmd.Flags().StringVar(&conf.KeyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	rootCmd.PersistentFlags().BoolVar(&conf.Verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().StringVar(&memprofile, "memprofile", "", "Write a memory profile to this file readable by pprof")
	rootCmd.PersistentFlags().StringVar(&cpuprofile, "cpuprofile", "", "Write a cpu profile to this file readable by pprof")

	//configMergeCmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Cluster name (default is none)")
	//configMergeCmd.PersistentFlags().StringVar(&conf.ConfigFile, "config", "", "Configuration file (default is config.toml)")
	//rootCmd.PersistentFlags().StringVar(&conf.WorkingDir, "monitoring-datadir", "", "Configuration file (default is config.toml)")

	rootCmd.AddCommand(versionCmd)
	//rootCmd.AddCommand(configMergeCmd)

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
	cmd.Flags().IntVar(&conf.LogRotateMaxSize, "log-rotate-max-size", 5, "Log rotate max size")
	cmd.Flags().IntVar(&conf.LogRotateMaxBackup, "log-rotate-max-backup", 7, "Log rotate max backup")
	cmd.Flags().IntVar(&conf.LogRotateMaxAge, "log-rotate-max-age", 7, "Log rotate max age")
	cmd.Flags().BoolVar(&conf.DBLogRotate, "db-log-rotate", false, "Rotate fetched DB logs (log_error.log, log_slow_query.log, log_sql_error.log, log_audit.log). Disabled by default for compatibility with external logrotate")
	cmd.Flags().BoolVar(&conf.DBLogOnBackupStorage, "db-log-on-backup-storage", false, "Store fetched DB logs under backup-backed storage instead of the legacy per-server cluster working dir. Disabled by default for compatibility")
	cmd.Flags().IntVar(&conf.DBLogRotateMaxSize, "db-log-rotate-max-size", 100, "Fetched DB log rotate max size in MB")
	cmd.Flags().IntVar(&conf.DBLogRotateMaxBackup, "db-log-rotate-max-backup", 10, "Fetched DB log rotate max backup count")
	cmd.Flags().IntVar(&conf.DBLogRotateMaxAge, "db-log-rotate-max-age", 7, "Fetched DB log rotate max age in days")
	cmd.Flags().IntVar(&conf.LogLevel, "log-level", 3, "Log verbosity level. Default 3 (INFO)")
	cmd.Flags().IntVar(&conf.LogFileLevel, "log-level-file", 3, "Log verbosity level. Default 3 (INFO)")
	cmd.Flags().BoolVar(&conf.LogConfigLoad, "log-config-load", true, "Log config decryption")
	cmd.Flags().IntVar(&conf.LogConfigLoadLevel, "log-level-config-load", 2, "Log Config Load Level. Default 2 (WARNING)")
	cmd.Flags().BoolVar(&conf.LogSecrets, "log-secrets", false, "Decrypt values of secrets and log them")

	viper.BindPFlags(cmd.Flags())
	// if conf.Verbose == true && conf.LogLevel == 0 {
	// 	conf.LogLevel = 1
	// }
	// if conf.Verbose == false && conf.LogLevel > 0 {
	// 	conf.Verbose = true
	// }
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

}
