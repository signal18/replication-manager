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
	"strings"

	log "github.com/Sirupsen/logrus"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tanji/replication-manager/config"
)

var (
	cfgFile       string
	cfgGroup      string
	cfgGroupList  []string
	cfgGroupIndex int
	conf          config.Config
	memprofile    string
)
var confs = make(map[string]config.Config)

var (
	// Version is the semantic version number, e.g. 1.0.1
	Version string
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build string
)

func init() {

	log.SetFormatter(&log.TextFormatter{})

	cobra.OnInitialize(initConfig)

	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Configuration file (default is config.toml)")
	rootCmd.PersistentFlags().StringVar(&cfgGroup, "config-group", "", "Configuration group (default is none)")
	rootCmd.PersistentFlags().StringVar(&conf.User, "user", "", "User for MariaDB login, specified in the [user]:[password] format")
	rootCmd.PersistentFlags().StringVar(&conf.Hosts, "hosts", "", "List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	rootCmd.PersistentFlags().StringVar(&conf.RplUser, "rpluser", "", "Replication user in the [user]:[password] format")
	rootCmd.Flags().StringVar(&conf.KeyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	rootCmd.PersistentFlags().BoolVar(&conf.Verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().IntVar(&conf.LogLevel, "log-level", 0, "Log verbosity level")
	rootCmd.PersistentFlags().StringVar(&memprofile, "memprofile", "/tmp/repmgr.mprof", "Write a memory profile to a file readable by pprof")

	viper.BindPFlags(rootCmd.PersistentFlags())
	if conf.Verbose == true && conf.LogLevel == 0 {
		conf.LogLevel = 1
	}
	if conf.Verbose == false && conf.LogLevel > 0 {
		conf.Verbose = true
	}

}

func initConfig() {
	// call after init if configuration file is provide
	viper.SetConfigType("toml")
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath("/etc/replication-manager/")
		viper.AddConfigPath(".")
	}
	viper.SetEnvPrefix("MRM")
	err := viper.ReadInConfig()
	if err == nil {
		log.WithFields(log.Fields{
			"file": viper.ConfigFileUsed(),
		}).Debug("Using config file")
	}
	if _, ok := err.(viper.ConfigParseError); ok {
		log.WithError(err).Fatal("Could not parse config file")
	}
	m := viper.AllKeys()
	if cfgGroup == "" {
		var clusterDicovery = map[string]string{}
		var discoveries []string
		for _, k := range m {

			if strings.Contains(k, ".") {
				mycluster := strings.Split(k, ".")[0]
				if mycluster != "default" {
					_, ok := clusterDicovery[mycluster]
					if !ok {
						clusterDicovery[mycluster] = mycluster
						discoveries = append(discoveries, mycluster)
						//	log.Println(strings.Split(k, ".")[0])
					}
				}

			}
		}
		cfgGroup = strings.Join(discoveries, ",")
		log.WithField("clusters", cfgGroup).Debug("New clusters discovered")

	}
	cfgGroupIndex = 0

	cf1 := viper.Sub("Default")
	cf1.Unmarshal(&conf)

	if cfgGroup != "" {
		cfgGroupList = strings.Split(cfgGroup, ",")

		for _, gl := range cfgGroupList {

			if gl != "" {
				cfgGroup = gl
				log.WithField("group", gl).Debug("Reading configuration group")
				cf2 := viper.Sub(gl)
				if cf2 == nil {
					log.WithField("group", gl).Fatal("Could not parse configuration group")
				}
				cf2.Unmarshal(&conf)
				confs[cfgGroup] = conf
				cfgGroupIndex++
			}
		}

		cfgGroupIndex--
		log.WithField("cluster", cfgGroupList[cfgGroupIndex]).Debug("Default Cluster set")
		cfgGroup = cfgGroupList[cfgGroupIndex]

	} else {
		cfgGroupList = append(cfgGroupList, "Default")
		log.WithField("cluster", cfgGroupList[cfgGroupIndex]).Debug("Default Cluster set")

		confs["Default"] = conf
		cfgGroup = "Default"
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
		fmt.Println("Replication Manager " + Version + " for MariaDB 10.x Series")
		fmt.Println("Full Version: ", FullVersion)
		fmt.Println("Build Time: ", Build)
	},
}
