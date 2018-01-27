// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	//toml "github.com/pelletier/go-toml"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	WithArbitrationClient string
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
	WithTarball           string
	WithMySQLRouter       string
	WithSphinx            string
	WithBackup            string
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
	Enterprise          string              `json:"enterprise"`
	Interactive         string              `json:"interactive"`
	FailoverCtr         string              `json:"failoverctr"`
	MaxDelay            string              `json:"maxdelay"`
	Faillimit           string              `json:"faillimit"`
	LastFailover        string              `json:"lastfailover"`
	MonHearbeats        string              `json:"monheartbeats"`
	Uptime              string              `json:"uptime"`
	UptimeFailable      string              `json:"uptimefailable"`
	UptimeSemiSync      string              `json:"uptimesemisync"`
	RplChecks           string              `json:"rplchecks"`
	FailSync            string              `json:"failsync"`
	SwitchSync          string              `json:"switchsync"`
	Verbose             string              `json:"verbose"`
	Rejoin              string              `json:"rejoin"`
	RejoinBackupBinlog  string              `json:"rejoinbackupbinlog"`
	RejoinSemiSync      string              `json:"rejoinsemisync"`
	RejoinFlashback     string              `json:"rejoinflashback"`
	RejoinUnsafe        string              `json:"rejoinunsafe"`
	RejoinDump          string              `json:"rejoindump"`
	RejoinPseudoGTID    string              `json:"rejoinpseudogtid"`
	Test                string              `json:"test"`
	Heartbeat           string              `json:"heartbeat"`
	Status              string              `json:"runstatus"`
	IsActive            string              `json:"isactive"`
	ConfGroup           string              `json:"confgroup"`
	MonitoringTicker    string              `json:"monitoringticker"`
	FailResetTime       string              `json:"failresettime"`
	ToSessionEnd        string              `json:"tosessionend"`
	HttpAuth            string              `json:"httpauth"`
	HttpBootstrapButton string              `json:"httpbootstrapbutton"`
	GraphiteMetrics     string              `json:"graphitemetrics"`
	Clusters            []string            `json:"clusters"`
	RegTests            []string            `json:"regtests"`
	Topology            string              `json:"topology"`
	Version             string              `json:"version"`
	DBTags              []string            `json:"databasetags"`
	ProxyTags           []string            `json:"proxytags"`
	Scheduler           []cluster.CronEntry `json:"scheduler"`
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
			//	log.Fatal("No config file " + conf.ConfigFile)
			log.Error("No config file " + conf.ConfigFile)

		}
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath("/etc/replication-manager/")
		viper.AddConfigPath(".")
		if WithTarball == "ON" {
			viper.AddConfigPath("/usr/local/replication-manager/etc")
			conf.ClusterConfigPath = "/usr/local/replication-manager/data/cluster.d"

			if _, err := os.Stat("/usr/local/replication-manager/etc/config.toml"); os.IsNotExist(err) {
				//log.Fatal("No config file /usr/local/replication-manager/etc/config.toml")
				log.Warning("No config file /usr/local/replication-manager/etc/config.toml")
			}
		} else {
			conf.ClusterConfigPath = "/var/lib/replication-manager/cluster.d"
			if _, err := os.Stat("/etc/replication-manager/config.toml"); os.IsNotExist(err) {
				//log.Fatal("No config file /etc/replication-manager/config.toml")
				log.Warning("No config file /etc/replication-manager/config.toml ")
			}
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
		//log.WithError(err).Fatal("Could not parse config file")
		log.Warningf("Could not parse config file: %s", err)
	}

	// Procedd include files
	if viper.GetString("default.include") != "" {
		if _, err := os.Stat(viper.GetString("default.include")); os.IsNotExist(err) {
			//	log.Fatal("No include config directory " + conf.Include)
			log.Warning("No include config directory " + conf.Include)
		} else {
			conf.ClusterConfigPath = viper.GetString("default.include")
		}
	}
	files, err := ioutil.ReadDir(conf.ClusterConfigPath)
	if err != nil {
		log.Warningf("Can't found include path %s %s", conf.ClusterConfigPath, err)
	}
	for _, f := range files {
		if !f.IsDir() {
			viper.SetConfigName(f.Name())
			viper.SetConfigFile(conf.ClusterConfigPath + "/" + f.Name())
			viper.MergeInConfig()
			//fmt.Println(f.Name())
		}
	}

	// Procedd include files

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
		//log.Fatal("config.toml has no [Default] configuration group and config group has not been specified")
		log.Warning("config.toml has no [Default] configuration group and config group has not been specified")
	} else {

		cf1.Unmarshal(&conf)
	}
	if currentClusterName != "" {
		cfgGroupList = strings.Split(currentClusterName, ",")

		for _, gl := range cfgGroupList {

			if gl != "" {
				clusterconf := conf
				cf2 := viper.Sub("Default")
				if cf2 != nil {
					initAlias(cf2)
					cf2.Unmarshal(&clusterconf)
				}
				currentClusterName = gl
				log.WithField("group", gl).Debug("Reading configuration group")
				cf2 = viper.Sub(gl)
				initAlias(cf2)
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
	// Copyright 2017 Signal 18 SARL
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

func initAlias(v *viper.Viper) {
	v.RegisterAlias("replication-master-connection", "replication-source-name")
	v.RegisterAlias("logfile", "log-file")
	v.RegisterAlias("wait-kill", "switchover-wait-kill")
	v.RegisterAlias("user", "db-servers-credential")
	v.RegisterAlias("hosts", "db-servers-hosts")
	v.RegisterAlias("hosts-tls-ca-cert", "db-servers-tls-ca-cert")
	v.RegisterAlias("hosts-tls-client-key", "db-servers-tls-client-key")
	v.RegisterAlias("hosts-tls-client-cert", "db-servers-tls-client-cert")
	v.RegisterAlias("connect-timeout", "db-servers-connect-timeout")
	v.RegisterAlias("rpluser", "replication-credential")
	v.RegisterAlias("prefmaster", "db-servers-prefered-master")
	v.RegisterAlias("ignore-servers", "db-servers-ignored-hosts")
	v.RegisterAlias("master-connection", "replication-master-connection")
	v.RegisterAlias("master-connect-retry", "replication-master-connection-retry")
	v.RegisterAlias("api-user", "api-credential")
	v.RegisterAlias("readonly", "failover-readonly-state")
	v.RegisterAlias("maxscale-host", "maxscale-servers")
	v.RegisterAlias("mdbshardproxy-hosts", "mdbshardproxy-servers")
	v.RegisterAlias("multimaster", "replication-multi-master")
	v.RegisterAlias("multi-tier-slave", "replication-multi-tier-slave")
	v.RegisterAlias("pre-failover-script", "failover-pre-script")
	v.RegisterAlias("post-failover-script", "failover-post-script")
	v.RegisterAlias("rejoin-script", "autorejoin-script")
	v.RegisterAlias("share-directory", "monitoring-sharedir")
	v.RegisterAlias("working-directory", "monitoring-datadir")
	v.RegisterAlias("interactive", "failover-mode")
	v.RegisterAlias("failcount", "failover-falsepositive-ping-counter")
	v.RegisterAlias("wait-write-query", "switchover-wait-write-query")
	v.RegisterAlias("wait-trx", "switchover-wait-trx")
	v.RegisterAlias("gtidcheck", "switchover-at-equal-gtid")
	v.RegisterAlias("maxdelay", "failover-max-slave-delay")
	v.RegisterAlias("maxscale-host", "maxscale-servers")
	v.RegisterAlias("maxscale-pass", "maxscale-password")
}
