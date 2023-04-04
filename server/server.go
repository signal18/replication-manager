// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log/syslog"
	"net"
	"os/signal"
	"runtime/pprof"
	"sort"
	"sync"

	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	log "github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"

	termbox "github.com/nsf/termbox-go"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/etc"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/regtest"
	"github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
)

var RepMan *ReplicationManager

// Global variables
type ReplicationManager struct {
	OpenSVC                                          opensvc.Collector           `json:"-"`
	Version                                          string                      `json:"version"`
	Fullversion                                      string                      `json:"fullVersion"`
	Os                                               string                      `json:"os"`
	Arch                                             string                      `json:"arch"`
	MemProfile                                       string                      `json:"memprofile"`
	Clusters                                         map[string]*cluster.Cluster `json:"-"`
	Agents                                           []opensvc.Host              `json:"agents"`
	UUID                                             string                      `json:"uuid"`
	Hostname                                         string                      `json:"hostname"`
	Status                                           string                      `json:"status"`
	SplitBrain                                       bool                        `json:"spitBrain"`
	ClusterList                                      []string                    `json:"clusters"`
	Tests                                            []string                    `json:"tests"`
	Conf                                             config.Config               `json:"config"`
	Logs                                             s18log.HttpLog              `json:"logs"`
	ServicePlans                                     []config.ServicePlan        `json:"servicePlans"`
	ServiceOrchestrators                             []config.ConfigVariableType `json:"serviceOrchestrators"`
	ServiceAcl                                       []config.Grant              `json:"serviceAcl"`
	ServiceRepos                                     []config.DockerRepo         `json:"serviceRepos"`
	ServiceTarballs                                  []config.Tarball            `json:"serviceTarballs"`
	ServiceFS                                        map[string]bool             `json:"serviceFS"`
	ServiceVM                                        map[string]bool             `json:"serviceVM"`
	ServiceDisk                                      map[string]string           `json:"serviceDisk"`
	ServicePool                                      map[string]bool             `json:"servicePool"`
	BackupLogicalList                                map[string]bool             `json:"backupLogicalList"`
	BackupPhysicalList                               map[string]bool             `json:"backupPhysicalList"`
	currentCluster                                   *cluster.Cluster            `json:"-"`
	tlog                                             s18log.TermLog
	termlength                                       int
	exitMsg                                          string
	exit                                             bool
	isStarted                                        bool
	Confs                                            map[string]config.Config
	ForcedConfs                                      map[string]config.Config
	grpcServer                                       *grpc.Server               `json:"-"`
	grpcWrapped                                      *grpcweb.WrappedGrpcServer `json:"-"`
	V3Up                                             chan bool                  `json:"-"`
	v3Config                                         Repmanv3Config             `json:"-"`
	repmanv3.UnimplementedClusterPublicServiceServer `json:"-"`
	repmanv3.UnimplementedClusterServiceServer       `json:"-"`
	sync.Mutex
}

const (
	ConstMonitorActif   string = "A"
	ConstMonitorStandby string = "S"
)

// Unused in server still used in client cmd line
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
	RejoinPseudoGTID    string   `json:"rejoinpseudogtid"`
	Test                string   `json:"test"`
	Heartbeat           string   `json:"heartbeat"`
	Status              string   `json:"runstatus"`
	IsActive            string   `json:"isactive"`
	ConfGroup           string   `json:"confgroup"`
	MonitoringTicker    string   `json:"monitoringticker"`
	FailResetTime       string   `json:"failresettime"`
	ToSessionEnd        string   `json:"tosessionend"`
	HttpAuth            string   `json:"httpauth"`
	HttpBootstrapButton string   `json:"httpbootstrapbutton"`
	GraphiteMetrics     string   `json:"graphitemetrics"`
	Clusters            []string `json:"clusters"`
	RegTests            []string `json:"regtests"`
	Topology            string   `json:"topology"`
	Version             string   `json:"version"`
	DBTags              []string `json:"databasetags"`
	ProxyTags           []string `json:"proxytags"`
	//	Scheduler           []cron.Entry `json:"scheduler"`
}

// A Heartbeat returns a quick overview of the cluster status
//
// swagger:response heartbeat
type HeartbeatResponse struct {
	// Heartbeat message
	// in: body
	Body Heartbeat
}

type Heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
	Status  string `json:"status"`
	Hosts   int    `json:"hosts"`
	Failed  int    `json:"failed"`
}

var confs = make(map[string]config.Config)
var cfgGroup string
var cfgGroupIndex int

// DicoverClusters from viper merged config send a sperated list of clusters
func (repman *ReplicationManager) DiscoverClusters(FirstRead *viper.Viper) string {
	m := FirstRead.AllKeys()

	var clusterDiscovery = map[string]string{}
	var discoveries []string
	for _, k := range m {

		if strings.Contains(k, ".") {
			mycluster := strings.Split(k, ".")[0]
			//	log.Infof("Evaluate key %s %s", mycluster, k)
			if strings.ToLower(mycluster) != "default" {
				if strings.HasPrefix(mycluster, "saved-") {
					mycluster = strings.TrimPrefix(mycluster, "saved-")
				}
				_, ok := clusterDiscovery[mycluster]
				if !ok {
					clusterDiscovery[mycluster] = mycluster
					discoveries = append(discoveries, mycluster)
					log.Infof("Cluster discover from config: %s", strings.Split(k, ".")[0])
				}
			}

		}
	}
	return strings.Join(discoveries, ",")

}

func (repman *ReplicationManager) OverwriteParameterFlags(destViper *viper.Viper) {
	m := viper.AllSettings()
	//m := viper.AllSettings()
	for k, v := range m {
		if destViper.Get(k) != viper.Get(k) {
			fmt.Printf("%s:%v\n", k, v)
		}

	}

}

func (repman *ReplicationManager) initEmbed() error {
	//test si y'a  un repertoire ./.replication-manager sinon on le créer
	//test si y'a  un repertoire ./.replication-manager/config.toml sinon on le créer depuis embed
	//test y'a  un repertoire ./.replication-manager/data sinon on le créer
	//test y'a  un repertoire ./.replication-manager/share sinon on le créer
	if _, err := os.Stat("./.replication-manager"); os.IsNotExist(err) {
		os.MkdirAll("./.replication-manager", os.ModePerm)
		os.MkdirAll("./.replication-manager/data", os.ModePerm)
		os.MkdirAll("./.replication-manager/share", os.ModePerm)
	}

	if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {

		file, err := etc.EmbededDbModuleFS.ReadFile("local/embed/config.toml")
		if err != nil {
			log.Errorf("failed opening file because: %s", err.Error())
			return err
		}
		err = ioutil.WriteFile("./.replication-manager/config.toml", file, 0644) //remplacer nil par l'obj créer pour config.toml dans etc/local/embed
		if err != nil {
			log.Errorf("failed write file because: %s", err.Error())
			return err
		}
		if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {
			log.Errorf("failed create ./.replication-manager/config.toml file because: %s", err.Error())
			return err
		}
	}

	return nil
}

func (repman *ReplicationManager) InitConfig(conf config.Config) {
	repman.ForcedConfs = make(map[string]config.Config)
	// call after init if configuration file is provide
	if conf.WithEmbed == "ON" {
		repman.initEmbed()
	}
	fistRead := viper.GetViper()
	fistRead.SetConfigType("toml")
	if conf.ConfigFile != "" {
		if _, err := os.Stat(conf.ConfigFile); os.IsNotExist(err) {
			//	log.Fatal("No config file " + conf.ConfigFile)
			log.Error("No config file " + conf.ConfigFile)
		}
		fistRead.SetConfigFile(conf.ConfigFile)

	} else {
		fistRead.SetConfigName("config")
		fistRead.AddConfigPath("/etc/replication-manager/")
		fistRead.AddConfigPath(".")
		fistRead.AddConfigPath("./.replication-manager")
		if conf.WithTarball == "ON" {
			fistRead.AddConfigPath("/usr/local/replication-manager/etc")
			if _, err := os.Stat("/usr/local/replication-manager/etc/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file /usr/local/replication-manager/etc/config.toml")
			}
		}
		if conf.WithEmbed == "ON" {
			if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file ./.replication-manager/config.toml ")
			}
		} else {
			if _, err := os.Stat("/etc/replication-manager/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file /etc/replication-manager/config.toml ")
			}
		}
	}
	conf.ClusterConfigPath = conf.WorkingDir + "/cluster.d"

	fistRead.SetEnvPrefix("DEFAULT")
	err := fistRead.ReadInConfig()
	if err == nil {
		log.WithFields(log.Fields{
			"file": fistRead.ConfigFileUsed(),
		}).Debug("Using config file")
	} else {

		//	if _, ok := err.(fistRead.ConfigParseError); ok {
		//log.WithError(err).Fatal("Could not parse config file")
		log.Errorf("Could not parse config file: %s", err)
	}

	// Proceed include files

	if fistRead.GetString("default.include") != "" {
		log.Info("Reading default section include directory: " + fistRead.GetString("default.include"))

		if _, err := os.Stat(fistRead.GetString("default.include")); os.IsNotExist(err) {
			log.Warning("Include config directory does not exist " + conf.Include)
		} else {
			conf.ClusterConfigPath = fistRead.GetString("default.include")
		}

		files, err := ioutil.ReadDir(conf.ClusterConfigPath)
		if err != nil {
			log.Infof("No config include directory %s ", conf.ClusterConfigPath)
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".toml") {
				fistRead.SetConfigName(f.Name())
				fistRead.SetConfigFile(conf.ClusterConfigPath + "/" + f.Name())
				//	viper.Debug()
				fistRead.AutomaticEnv()
				fistRead.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))

				err := fistRead.MergeInConfig()
				if err != nil {
					log.Fatal("Config error in " + conf.ClusterConfigPath + "/" + f.Name() + ":" + err.Error())
				}
			}
		}
	} else {
		log.Warning("No include directory in default section")
	}

	// Proceed dynamic config

	if fistRead.GetBool("default.monitoring-save-config") {
		if fistRead.GetString("default.monitoring-datadir") != "" {
			conf.WorkingDir = fistRead.GetString("default.monitoring-datadir")
		}
		files, err := ioutil.ReadDir(conf.WorkingDir)
		if err != nil {
			log.Infof("No working directory %s ", conf.WorkingDir)
		}
		for _, f := range files {
			if f.IsDir() && f.Name() != "graphite" {
				fistRead.SetConfigName(f.Name())
				if _, err := os.Stat(conf.WorkingDir + "/" + f.Name() + "/config.toml"); os.IsNotExist(err) {
					log.Warning("No monitoring saved config found " + conf.WorkingDir + "/" + f.Name() + "/config.toml")
				} else {
					log.Infof("Parsing saved config from working directory %s ", conf.WorkingDir+"/"+f.Name()+"/config.toml")
					fistRead.SetConfigFile(conf.WorkingDir + "/" + f.Name() + "/config.toml")
					err := fistRead.MergeInConfig()
					if err != nil {
						log.Fatal("Config error in " + conf.WorkingDir + "/" + f.Name() + "/config.toml" + ":" + err.Error())
					}
				}
			}
		}

	} else {
		log.Warning("No monitoring-save-config variable in default section config change lost on restart")
	}

	var strClusters string
	strClusters = cfgGroup

	if strClusters == "" {
		// Discovering the clusters from all merged conf files build clusterDiscovery map
		strClusters = repman.DiscoverClusters(fistRead)
		log.WithField("clusters", strClusters).Debug("New clusters discovered")
	}

	cfgGroupIndex = 0
	cf1 := fistRead.Sub("Default")
	vipersave := viper.GetViper()
	//cf1.Debug()
	if cf1 == nil {
		log.Warning("config.toml has no [Default] configuration group and config group has not been specified")
	} else {
		cf1.AutomaticEnv()
		cf1.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
		cf1.SetEnvPrefix("DEFAULT")

		vipersave.MergeConfigMap(cf1.AllSettings())
		//fmt.Printf("%+v\n", vipersave.AllSettings())
		vipersave.Unmarshal(&conf)
		//	fmt.Printf("%+v\n", conf)
		//os.Exit(3)
		repman.Conf = conf
	}
	//	backupvipersave := viper.GetViper()
	if strClusters != "" {
		repman.ClusterList = strings.Split(strClusters, ",")
		for _, cluster := range repman.ClusterList {
			//vipersave := backupvipersave

			confs[cluster] = repman.GetClusterConfig(fistRead, cluster, conf)
			cfgGroupIndex++

		}

		cfgGroupIndex--
		log.WithField("cluster", repman.ClusterList[cfgGroupIndex]).Debug("Default Cluster set")

	} else {
		repman.ClusterList = append(repman.ClusterList, "Default")
		log.WithField("cluster", repman.ClusterList[cfgGroupIndex]).Debug("Default Cluster set")

		confs["Default"] = conf

	}
	repman.Confs = confs
	//repman.Conf = conf
}

func (repman *ReplicationManager) GetClusterConfig(fistRead *viper.Viper, cluster string, conf config.Config) config.Config {

	clusterconf := conf
	//vipersave := viper.GetViper()
	if cluster != "" {
		log.WithField("group", cluster).Debug("Reading configuration group")
		def := fistRead.Sub("Default")
		//	def.Debug()
		def.AutomaticEnv()
		def.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
		def.SetEnvPrefix("DEFAULT")
		if def != nil {
			repman.initAlias(def)
			def.Unmarshal(&clusterconf)

		}
		//fmt.Printf("default for cluster %s %+v\n", cluster, clusterconf)

		cf2 := fistRead.Sub(cluster)

		//def.SetEnvPrefix(strings.ToUpper(cluster))
		//

		if cf2 == nil {
			log.WithField("group", cluster).Infof("Could not parse configuration group")
		} else {
			cf2.AutomaticEnv()
			cf2.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
			repman.initAlias(cf2)
			//	cf2.Unmarshal(&def)
			cf2.Unmarshal(&clusterconf)
			//			fmt.Printf("include config cf2 for cluster %s %+v\n", cluster, clusterconf)
			//		vipersave.MergeConfigMap(cf2.AllSettings())
			//	vipersave.Unmarshal(&clusterconf)
			//		fmt.Printf("include config for cluster %s %+v\n", cluster, clusterconf)

		}

		repman.ForcedConfs[cluster] = clusterconf
		if clusterconf.ConfRewrite {
			cf3 := fistRead.Sub("saved-" + cluster)
			if cf3 == nil {
				log.WithField("group", cluster).Info("Could not parse saved configuration group")
			} else {
				repman.initAlias(cf3)
				cf3.Unmarshal(&def)
				cf3.Unmarshal(&clusterconf)
				//	vipersave.MergeConfigMap(cf3.AllSettings())
				//	vipersave.Unmarshal(&clusterconf)
			}
		}
	}
	return clusterconf
}

func (repman *ReplicationManager) initAlias(v *viper.Viper) {
	v.RegisterAlias("monitoring-config-rewrite", "monitoring-save-config")
	v.RegisterAlias("api-user", "api-credentials")
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

func (repman *ReplicationManager) InitRestic() error {
	os.Setenv("AWS_ACCESS_KEY_ID", repman.Conf.BackupResticAwsAccessKeyId)
	os.Setenv("AWS_SECRET_ACCESS_KEY", repman.Conf.BackupResticAwsAccessSecret)
	os.Setenv("RESTIC_REPOSITORY", repman.Conf.BackupResticRepository)
	os.Setenv("RESTIC_PASSWORD", repman.Conf.BackupResticPassword)
	//os.Setenv("RESTIC_FORGET_ARGS", repman.Conf.BackupResticStoragePolicy)
	return nil
}

func (repman *ReplicationManager) Run() error {
	var err error
	repman.Version = Version
	repman.Fullversion = FullVersion
	repman.Arch = GoArch
	repman.Os = GoOS
	//repman.MemProfile = repman.Conf.MemProfile

	repman.Clusters = make(map[string]*cluster.Cluster)
	repman.UUID = misc.GetUUID()
	if repman.Conf.Arbitration {
		repman.Status = ConstMonitorStandby
	} else {
		repman.Status = ConstMonitorActif
	}
	repman.SplitBrain = false
	repman.Hostname, err = os.Hostname()
	regtest := new(regtest.RegTest)
	repman.Tests = regtest.GetTests()

	if err != nil {
		log.Fatalln("ERROR: replication-manager could not get hostname from system")
	}

	if repman.Conf.LogSyslog {
		hook, err := lSyslog.NewSyslogHook("udp", "localhost:514", syslog.LOG_INFO, "")
		if err == nil {
			log.AddHook(hook)
		}
	}

	if repman.Conf.LogLevel > 1 {
		log.SetLevel(log.DebugLevel)
	}

	if repman.Conf.LogFile != "" {
		log.WithField("version", repman.Version).Info("Log to file: " + repman.Conf.LogFile)
		hook, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
			Filename:   repman.Conf.LogFile,
			MaxSize:    repman.Conf.LogRotateMaxSize,
			MaxBackups: repman.Conf.LogRotateMaxBackup,
			MaxAge:     repman.Conf.LogRotateMaxAge,
			Level:      log.GetLevel(),
			Formatter: &log.TextFormatter{
				DisableColors:   true,
				TimestampFormat: "2006-01-02 15:04:05",
				FullTimestamp:   true,
			},
		})
		if err != nil {
			log.WithError(err).Error("Can't init log file")
		}
		log.AddHook(hook)
	}

	if !repman.Conf.Daemon {
		err := termbox.Init()
		if err != nil {
			log.WithError(err).Fatal("Termbox initialization error")
		}
	}
	repman.termlength = 40
	log.WithField("version", repman.Version).Info("Replication-Manager started in daemon mode")
	loglen := repman.termlength - 9 - (len(strings.Split(repman.Conf.Hosts, ",")) * 3)
	repman.tlog = s18log.NewTermLog(loglen)
	repman.Logs = s18log.NewHttpLog(80)
	repman.InitServicePlans()
	repman.ServiceOrchestrators = repman.Conf.GetOrchestratorsProv()
	repman.InitGrants()
	repman.ServiceRepos, err = repman.Conf.GetDockerRepos(repman.Conf.ShareDir+"/repo/repos.json", repman.Conf.Test)
	if err != nil {
		log.WithError(err).Errorf("Initialization docker repo failed: %s %s", repman.Conf.ShareDir+"/repo/repos.json", err)
	}
	repman.ServiceTarballs, err = repman.Conf.GetTarballs(repman.Conf.Test)
	if err != nil {
		log.WithError(err).Errorf("Initialization tarballs repo failed: %s %s", repman.Conf.ShareDir+"/repo/tarballs.json", err)
	}

	repman.ServiceVM = repman.Conf.GetVMType()
	repman.ServiceFS = repman.Conf.GetFSType()
	repman.ServiceDisk = repman.Conf.GetDiskType()
	repman.ServicePool = repman.Conf.GetPoolType()
	repman.BackupLogicalList = repman.Conf.GetBackupLogicalType()
	repman.BackupPhysicalList = repman.Conf.GetBackupPhysicalType()

	go repman.apiserver()

	if repman.Conf.ProvOrchestrator == "opensvc" {
		repman.Agents = []opensvc.Host{}
		repman.OpenSVC.Host, repman.OpenSVC.Port = misc.SplitHostPort(repman.Conf.ProvHost)
		repman.OpenSVC.User, repman.OpenSVC.Pass = misc.SplitPair(repman.Conf.ProvAdminUser)
		repman.OpenSVC.RplMgrUser, repman.OpenSVC.RplMgrPassword = misc.SplitPair(repman.Conf.ProvUser) //yaml licence
		repman.OpenSVC.RplMgrCodeApp = repman.Conf.ProvCodeApp
		if !repman.Conf.ProvOpensvcUseCollectorAPI {
			repman.OpenSVC.UseAPI = repman.Conf.ProvOpensvcUseCollectorAPI
			repman.OpenSVC.CertsDERSecret = repman.Conf.ProvOpensvcP12Secret
			err := repman.OpenSVC.LoadCert(repman.Conf.ProvOpensvcP12Certificate)
			if err != nil {
				log.Fatalf("Cannot load OpenSVC cluster certificate %s ", err)
			}
		}
		//don't Bootstrap opensvc to speedup test
		if repman.Conf.ProvRegister {
			err := repman.OpenSVC.Bootstrap(repman.Conf.ShareDir + "/opensvc/")
			if err != nil {
				log.Fatalf("%s", err)
			}
			log.Fatalf("Registration to %s collector done", repman.Conf.ProvHost)
		} else {
			repman.OpenSVC.User, repman.OpenSVC.Pass = misc.SplitPair(repman.Conf.ProvUser)
		}

	}

	// Initialize go-carbon
	if repman.Conf.GraphiteEmbedded {
		go graphite.RunCarbon(repman.Conf.ShareDir, repman.Conf.WorkingDir, repman.Conf.GraphiteCarbonPort, repman.Conf.GraphiteCarbonLinkPort, repman.Conf.GraphiteCarbonPicklePort, repman.Conf.GraphiteCarbonPprofPort, repman.Conf.GraphiteCarbonServerPort)
		log.WithFields(log.Fields{
			"metricport": repman.Conf.GraphiteCarbonPort,
			"httpport":   repman.Conf.GraphiteCarbonServerPort,
		}).Info("Carbon server started")
		time.Sleep(2 * time.Second)
		go graphite.RunCarbonApi("http://0.0.0.0:"+strconv.Itoa(repman.Conf.GraphiteCarbonServerPort), repman.Conf.GraphiteCarbonApiPort, 20, "mem", "", 200, 0, "", repman.Conf.WorkingDir)
		log.WithField("apiport", repman.Conf.GraphiteCarbonApiPort).Info("Carbon server API started")
	}

	go repman.MountS3()

	//repman.InitRestic()
	log.Infof("repman.Conf.WorkingDir : %s", repman.Conf.WorkingDir)
	log.Infof("repman.Conf.ShareDir : %s", repman.Conf.ShareDir)

	// If there's an existing encryption key, decrypt the passwords

	for _, gl := range repman.ClusterList {
		repman.StartCluster(gl)
	}
	for _, cluster := range repman.Clusters {
		cluster.SetClusterList(repman.Clusters)
	}
	//	repman.currentCluster.SetCfgGroupDisplay(strClusters)

	// HTTP server should start after Cluster Init or may lead to various nil pointer if clients still requesting
	if repman.Conf.HttpServ {
		go repman.httpserver()
	}

	//	ticker := time.NewTicker(interval * time.Duration(repman.Conf.MonitoringTicker))
	repman.isStarted = true
	sigs := make(chan os.Signal, 1)
	// catch all signals since not explicitly listing
	//	signal.Notify(sigs)
	signal.Notify(sigs, os.Interrupt)
	// method invoked upon seeing signal
	go func() {
		s := <-sigs
		log.Printf("RECEIVED SIGNAL: %s", s)
		repman.UnMountS3()
		for _, cl := range repman.Clusters {
			cl.Stop()
		}

		repman.exit = true

	}()

	for repman.exit == false {
		if repman.Conf.Arbitration {
			repman.Heartbeat()
		}
		if repman.Conf.Enterprise {
			//			agents = svc.GetNodes()
		}
		time.Sleep(time.Second * time.Duration(repman.Conf.MonitoringTicker))
	}
	if repman.exitMsg != "" {
		log.Println(repman.exitMsg)
	}
	fmt.Println("Cleanup before leaving")
	repman.Stop()
	os.Exit(1)
	return nil

}

func (repman *ReplicationManager) StartCluster(clusterName string) (*cluster.Cluster, error) {

	k, err := crypto.ReadKey(repman.Conf.MonitoringKeyPath)
	if err != nil {
		log.WithError(err).Info("No existing password encryption scheme")
		k = nil
	}
	/*	apiUser, apiPass = misc.SplitPair(repman.Conf.APIUser)
		if k != nil {
			p := crypto.Password{Key: k}
			p.CipherText = apiPass
			p.Decrypt()
			apiPass = p.PlainText
		}*/
	repman.currentCluster = new(cluster.Cluster)
	myClusterConf := repman.Confs[clusterName]
	if myClusterConf.MonitorAddress == "localhost" {
		myClusterConf.MonitorAddress = repman.resolveHostIp()
	}
	if myClusterConf.FailMode == "manual" {
		myClusterConf.Interactive = true
	} else {
		myClusterConf.Interactive = false
	}
	if myClusterConf.BaseDir != "system" {
		myClusterConf.ShareDir = myClusterConf.BaseDir + "/share"
		myClusterConf.WorkingDir = myClusterConf.BaseDir + "/data"
	}
	repman.currentCluster.Init(myClusterConf, clusterName, &repman.tlog, &repman.Logs, repman.termlength, repman.UUID, repman.Version, repman.Hostname, k)
	repman.Clusters[clusterName] = repman.currentCluster
	repman.currentCluster.SetCertificate(repman.OpenSVC)
	go repman.currentCluster.Run()
	return repman.currentCluster, nil
}

func (repman *ReplicationManager) HeartbeatPeerSplitBrain(peer string, bcksplitbrain bool) bool {
	timeout := time.Duration(time.Duration(repman.Conf.MonitoringTicker) * time.Second * 4)
	/*	Host, _ := misc.SplitHostPort(peer)
		ha, err := net.LookupHost(Host)
		if err != nil {
			log.Errorf("Heartbeat: Resolv %s DNS err: %s", Host, err)
		} else {
			log.Errorf("Heartbeat: Resolv %s DNS say: %s", Host, ha[0])
		}
	*/

	url := "http://" + peer + "/api/heartbeat"
	client := &http.Client{
		Timeout: timeout,
	}
	if repman.Conf.LogHeartbeat {
		log.Debugf("Heartbeat: Sending peer request to node %s", peer)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Error building HTTP request: %s", err)
		}
		return true
	}
	resp, err := client.Do(req)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Could not reach peer node, might be down or incorrect address")
		}
		return true
	}
	defer resp.Body.Close()
	monjson, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Could not read body from peer response")
		}
		return true
	}
	if repman.Conf.LogHeartbeat {
		log.Debugf("splitbrain http call result: %s ", monjson)
	}
	// Use json.Decode for reading streams of JSON data
	var h Heartbeat
	if err := json.Unmarshal(monjson, &h); err != nil {
		if repman.Conf.LogHeartbeat {
			log.Debugf("Could not unmarshal JSON from peer response %s", err)
		}
		return true
	} else {

		if repman.Conf.LogHeartbeat {
			log.Debugf("RETURN: %v", h)
		}

		if repman.Conf.LogHeartbeat {
			log.Infof("No peer split brain setting status to %s", repman.Status)
		}

	}

	return false
}

func (repman *ReplicationManager) Heartbeat() {
	if cfgGroup == "arbitrator" {
		log.Debugf("Arbitrator cannot send heartbeat to itself. Exiting")
		return
	}

	var peerList []string
	// try to found an active peer replication-manager
	if repman.Conf.ArbitrationPeerHosts != "" {
		peerList = strings.Split(repman.Conf.ArbitrationPeerHosts, ",")
	} else {
		log.Debugf("Arbitration peer not specified. Disabling arbitration")
		repman.Conf.Arbitration = false
		return
	}

	bcksplitbrain := repman.SplitBrain

	for _, peer := range peerList {
		repman.Lock()
		repman.SplitBrain = repman.HeartbeatPeerSplitBrain(peer, bcksplitbrain)
		repman.Unlock()
		if repman.Conf.LogHeartbeat {
			log.Infof("SplitBrain set to %t on peer %s", repman.SplitBrain, peer)
		}
	} //end check all peers

	// propagate SplitBrain state to clusters after peer negotiation
	for _, cl := range repman.Clusters {
		cl.IsSplitBrain = repman.SplitBrain

		if repman.Conf.LogHeartbeat {
			log.Infof("SplitBrain set to %t on cluster %s", repman.SplitBrain, cl.Name)
		}
	}
}

func (repman *ReplicationManager) resolveHostIp() string {
	netInterfaceAddresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, netInterfaceAddress := range netInterfaceAddresses {
		networkIp, ok := netInterfaceAddress.(*net.IPNet)
		if ok && !networkIp.IP.IsLoopback() && networkIp.IP.To4() != nil {
			ip := networkIp.IP.String()
			return ip
		}
	}
	return ""
}

func (repman *ReplicationManager) Stop() {

	//termbox.Close()
	fmt.Println("Prof profile into file: " + memprofile)
	if memprofile != "" {
		f, err := os.Create(memprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}
}

func (repman *ReplicationManager) DownloadFile(url string, file string) error {
	client := http.Client{
		Timeout: 3 * time.Second,
	}
	response, err := client.Get(url)
	if err != nil {
		log.Errorf("Get File %s to %s : %s", url, file, err)
		return err
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Errorf("Read File %s to %s : %s", url, file, err)
		return err
	}

	err = ioutil.WriteFile(file, contents, 0644)
	if err != nil {
		log.Errorf("Write File %s to %s : %s", url, file, err)
		return err
	}
	return nil
}

func (repman *ReplicationManager) InitServicePlans() error {
	var err error
	if !repman.Conf.Test {

		if _, err := os.Stat(repman.Conf.WorkingDir + "/serviceplan.csv"); os.IsNotExist(err) {
			misc.CopyFile(repman.Conf.ShareDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.csv")
		}
		err = misc.ConvertCSVtoJSON(repman.Conf.WorkingDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.json", ",", repman.Conf.Test)
	} else {
		err = repman.DownloadFile(repman.Conf.ProvServicePlanRegistry, repman.Conf.WorkingDir+"/serviceplan.csv")
		if err != nil {
			log.Errorf("GetServicePlans download csv  %s", err)
			// copy from share if not downloadable
			if _, err := os.Stat(repman.Conf.WorkingDir + "/serviceplan.csv"); os.IsNotExist(err) {
				misc.CopyFile(repman.Conf.ShareDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.csv")
			}
		}
		err = misc.ConvertCSVtoJSON(repman.Conf.WorkingDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.json", ",", true)
		// copy from share if not downloadable

	}
	if err != nil {
		log.Errorf("GetServicePlans ConvertCSVtoJSON %s", err)
		return err
	}

	file, err := ioutil.ReadFile(repman.Conf.WorkingDir + "/serviceplan.json")
	if err != nil {
		log.Errorf("failed opening file because: %s", err.Error())
		return err
	}

	type Message struct {
		Rows []config.ServicePlan `json:"rows"`
	}
	var m Message
	err = json.Unmarshal([]byte(file), &m.Rows)
	if err != nil {
		log.Errorf("GetServicePlans  %s", err)
		return err
	}
	repman.ServicePlans = m.Rows

	return nil
}

type GrantSorter []config.Grant

func (a GrantSorter) Len() int           { return len(a) }
func (a GrantSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a GrantSorter) Less(i, j int) bool { return a[i].Grant < a[j].Grant }

func (repman *ReplicationManager) InitGrants() error {

	acls := []config.Grant{}

	for _, value := range repman.Conf.GetGrantType() {
		var acl config.Grant
		acl.Grant = value
		acls = append(acls, acl)
	}
	repman.ServiceAcl = acls
	sort.Sort(GrantSorter(repman.ServiceAcl))
	return nil
}
