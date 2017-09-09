// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
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

	log "github.com/sirupsen/logrus"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/signal18/replication-manager/config"
)

var (
	cfgGroup      string
	cfgGroupList  []string
	cfgGroupIndex int
	conf          config.Config
	memprofile    string
	// Version is the semantic version number, e.g. 1.0.1
	Version string
	// Provisoning to add flags for compile
	WithProvisioning      string
	WithArbitration       string
	WithProxysql          string
	WithHaproxy           string
	WithMaxscale          string
	WithMariadbshardproxy string
	WithMonitoring        string
	WithMail              string
	WithHttp              string
	WithSpider            string
	WithEnforce           string
	WithDeprecate         string
	WithOpenSVC           string
	WithMultiTiers        string

	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build  string
	GoOS   string
	GoArch string
)
var confs = make(map[string]config.Config)
var currentClusterName string

type Settings struct {
	Enterprise          string   `json:"enterprise"`
	Interactive         string   `json:"interactive"`
	FailoverCtr         string   `json:"failoverctr"`
	MaxDelay            string   `json:"maxdelay"`
	Faillimit           string   `json:"faillimit"`
	LastFailover        string   `json:"lastfailover"`
	MonHearbeats        string   `json:"monheartbeats"`
	Uptime              string   `json:"uptime"`
	UptimeFailable      string   `json:"uptimefailable"`
	UptimeSemiSync      string   `json:"uptimesemisync"`
	RplChecks           string   `json:"rplchecks"`
	FailSync            string   `json:"failsync"`
	SwitchSync          string   `json:"switchsync"`
	Verbose             string   `json:"verbose"`
	Rejoin              string   `json:"rejoin"`
	RejoinBackupBinlog  string   `json:"rejoinbackupbinlog"`
	RejoinSemiSync      string   `json:"rejoinsemisync"`
	RejoinFlashback     string   `json:"rejoinflashback"`
	RejoinUnsafe        string   `json:"rejoinunsafe"`
	RejoinDump          string   `json:"rejoindump"`
	Test                string   `json:"test"`
	Heartbeat           string   `json:"heartbeat"`
	Status              string   `json:"runstatus"`
	ConfGroup           string   `json:"confgroup"`
	MonitoringTicker    string   `json:"monitoringticker"`
	FailResetTime       string   `json:"failresettime"`
	ToSessionEnd        string   `json:"tosessionend"`
	HttpAuth            string   `json:"httpauth"`
	HttpBootstrapButton string   `json:"httpbootstrapbutton"`
	Clusters            []string `json:"clusters"`
	RegTests            []string `json:"regtests"`
	Topology            string   `json:"topology"`
}
type heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
	Status  string `json:"status"`
	Hosts   int    `json:"hosts"`
	Failed  int    `json:"failed"`
}

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

func initConfig() {
	// call after init if configuration file is provide
	viper.SetConfigType("toml")
	if conf.ConfigFile != "" {
		viper.SetConfigFile(conf.ConfigFile)
		if _, err := os.Stat(conf.ConfigFile); os.IsNotExist(err) {
			log.Fatal("No config file " + conf.ConfigFile)
		}
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath("/etc/replication-manager/")
		viper.AddConfigPath(".")
		if _, err := os.Stat("/etc/replication-manager/config.toml"); os.IsNotExist(err) {
			log.Fatal("No config file etc/replication-manager/config.toml ")
		}
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
	currentClusterName = cfgGroup
	if currentClusterName == "" {
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
						//						log.Println(strings.Split(k, ".")[0])
					}
				}

			}
		}
		currentClusterName = strings.Join(discoveries, ",")
		log.WithField("clusters", currentClusterName).Debug("New clusters discovered")

	}
	cfgGroupIndex = 0

	cf1 := viper.Sub("Default")
	if cf1 == nil {
		log.Fatal("config.toml has no [Default] configuration group and config group has not been specified")
	}

	cf1.Unmarshal(&conf)

	if currentClusterName != "" {
		cfgGroupList = strings.Split(currentClusterName, ",")

		for _, gl := range cfgGroupList {

			if gl != "" {
				clusterconf := conf
				cf2 := viper.Sub("Default")
				cf2.Unmarshal(&clusterconf)
				currentClusterName = gl
				log.WithField("group", gl).Debug("Reading configuration group")
				cf2 = viper.Sub(gl)
				if cf2 == nil {
					log.WithField("group", gl).Fatal("Could not parse configuration group")
				}
				cf2.Unmarshal(&clusterconf)
				confs[gl] = clusterconf
				cfgGroupIndex++
			}
		}

		cfgGroupIndex--
		log.WithField("cluster", cfgGroupList[cfgGroupIndex]).Debug("Default Cluster set")
		currentClusterName = cfgGroupList[cfgGroupIndex]

	} else {
		cfgGroupList = append(cfgGroupList, "Default")
		log.WithField("cluster", cfgGroupList[cfgGroupIndex]).Debug("Default Cluster set")

		confs["Default"] = conf
		currentClusterName = "Default"
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
