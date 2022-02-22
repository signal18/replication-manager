// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/server"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	memprofile string
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
	WithMySQLRouter       string
	WithSphinx            string = "ON"
	WithBackup            string = "ON"
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build    string
	GoOS     string = "linux"
	GoArch   string = "amd64"
	conf     config.Config
	cfgGroup string
)

var RepMan *server.ReplicationManager

func init() {

	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&conf.ConfigFile, "config", "", "Configuration file (default is config.toml)")
	rootCmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Configuration group (default is none)")
	rootCmd.Flags().StringVar(&conf.KeyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	rootCmd.PersistentFlags().BoolVar(&conf.Verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().StringVar(&memprofile, "memprofile", "/tmp/repmgr.mprof", "Write a memory profile to a file readable by pprof")

	viper.BindPFlags(rootCmd.PersistentFlags())
	if conf.Verbose == true && conf.LogLevel == 0 {
		conf.LogLevel = 1
	}
	if conf.Verbose == false && conf.LogLevel > 0 {
		conf.Verbose = true
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
	conf.MemProfile = memprofile
	conf.WithTarball = WithTarball

}

// initRepmgrFlags function is used to initialize flags that are common to several subcommands
// e.g. monitor, failover, switchover.
// If you add a subcommand that shares flags with other subcommand scenarios please call this function.
// If you add flags that impact all the possible scenarios please do it here.
func initRepmgrFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&conf.LogFile, "log-file", "", "Write output messages to log file")
	cmd.Flags().BoolVar(&conf.LogSyslog, "log-syslog", false, "Enable logging to syslog")
	cmd.Flags().IntVar(&conf.LogLevel, "log-level", 0, "Log verbosity level")
	cmd.Flags().IntVar(&conf.LogRotateMaxSize, "log-rotate-max-size", 5, "Log rotate max size")
	cmd.Flags().IntVar(&conf.LogRotateMaxBackup, "log-rotate-max-backup", 7, "Log rotate max backup")
	cmd.Flags().IntVar(&conf.LogRotateMaxAge, "log-rotate-max-age", 7, "Log rotate max age")

	viper.BindPFlags(cmd.Flags())

}
