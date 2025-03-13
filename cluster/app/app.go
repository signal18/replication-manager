// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package app

import (
	"hash"
	"hash/crc64"
	"net/http"
	"os"
	"sync"

	clusterauth "github.com/signal18/replication-manager/cluster/auth"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/spf13/pflag"
	"k8s.io/client-go/kubernetes"
)

var AppConfig config.AppConfig

type AppList []*App

type App struct {
	Id                    string                             `json:"id"`
	Name                  string                             `json:"name"`
	Domain                string                             `json:"domain"`
	Type                  string                             `json:"type"`
	Host                  string                             `json:"host"`
	HostCnf               string                             `json:"-"`
	HostIPV6              string                             `json:"hostIPV6"`
	Port                  string                             `json:"port"`
	AppConfig             config.AppConfig                   `json:"config"`
	DeployConfigMap       map[string]config.AppSectionConfig `json:"deployConfigs"`
	Cluster               ClusterInterface                   `json:"clustername"`
	User                  string                             `json:"user"`
	Pass                  string                             `json:"-"`
	Configurator          *configurator.Configurator         `json:"-"`
	Datadir               string                             `json:"datadir"`
	State                 string                             `json:"state"`
	PrevState             string                             `json:"prevState"`
	FailCount             int                                `json:"failCount"`
	SlapOSDatadir         string                             `json:"slaposDatadir"`
	Process               *os.Process                        `json:"process"`
	IsCompute             bool                               `json:"isCompute"`
	ConfigGitCloneUrl     string                             `json:"configGitCloneUrl"`
	ConfigGitUser         string                             `json:"configGitUser"`
	ConfigGitPassword     string                             `json:"configGitPassword"`
	ConfigGitBranch       string                             `json:"configGitBranch"`
	ConfigSecretVariables map[string]string                  `json:"-"`
	ConfigEnvVariables    map[string]string                  `json:"-"`
	ConfigVolumeMount     map[string]string                  `json:"configVolumeMount"`
	DataGitCloneUrl       string                             `json:"dataGitCloneUrl"`
	DataGitUser           string                             `json:"dataGitUser"`
	DataGitPassword       string                             `json:"-"`
	DataGitBranch         string                             `json:"dataGitBranch"`
	DataVolumeMount       map[string]string                  `json:"dataVolumeMount"`
	LogVolumeMount        map[string]string                  `json:"logVolumeMount"`
	ServiceName           string                             `json:"serviceName"`
	Agent                 string                             `json:"agent"`
	Weight                string                             `json:"weight"`
	Lock                  sync.Mutex
	Logger                *config.LogrusWrapper `json:"-"`
	RepMgrVersion         string                `json:"-"`
	Version               *version.Version      `json:"-"`
}

type ClusterInterface interface {
	GetName() string
	GetCrcTable() *crc64.Table
	GetConf() *config.Config
	GetDbPass() string
	GetDbUser() string
	GetConfigurator() configurator.Configurator
	GetAPIUserByUsername(username string) (clusterauth.APIUser, bool)
	GetChecksumConfig(key string) (hash.Hash, bool)
	SetChecksumConfig(key string, value hash.Hash)
	SetIsNeedGitPush(value bool)
	IsInFailover() bool
	OnPremiseGetSSHKey() string
	LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int
	K8SConnectAPI() (*kubernetes.Clientset, error)
}

const (
	stateFailed       string = "Failed"
	stateMaster       string = "Master"
	stateSlave        string = "Slave"
	stateSlaveErr     string = "SlaveErr"
	stateSlaveLate    string = "SlaveLate"
	stateMaintenance  string = "Maintenance"
	stateUnconn       string = "StandAlone"
	stateErrorAuth    string = "ErrorAuth"
	stateSuspect      string = "Suspect"
	stateShard        string = "Shard"
	stateProv         string = "Provision"
	stateMasterAlone  string = "MasterAlone"
	stateRelay        string = "Relay"
	stateRelayErr     string = "RelayErr"
	stateRelayLate    string = "RelayLate"
	stateWsrep        string = "Wsrep"
	stateWsrepDonor   string = "WsrepDonor"
	stateWsrepLate    string = "WsrepUnsync"
	stateProxyRunning string = "ProxyRunning"
	stateProxyDesync  string = "ProxyDesync"
)

func NewAppInstance(cluster ClusterInterface, placement int, host string) *App {
	conf := cluster.GetConf()
	app := new(App)
	app.SetPlacement(placement, conf.ProvAppAgents, conf.SlapOSAppPartitions, conf.AppHostsIPV6)
	app.AppConfig = conf.ToAppConfig() // store app config from cluster config
	app.DeployConfigMap = make(map[string]config.AppSectionConfig)
	app.HostCnf = host // store host from config file
	app.Cluster = cluster
	app.FailCount = 0
	app.Name = host
	app.Host = host

	if cluster.GetConf().ProvNetCNI {
		app.Host = app.Host + "." + cluster.GetName() + ".svc." + cluster.GetConf().ProvOrchestratorCluster
	}

	// Source name will equal to cluster name
	app.ServiceName = cluster.GetName() + "/svc/" + app.Name

	//will be overide in Refresh with show variables server_id, used for provisionning configurator for server_id
	app.SetID()

	// NOTE: does this make sense to set the state to the same?
	app.SetPrevState(stateSuspect)
	app.SetState(stateSuspect)

	app.Datadir = cluster.GetConf().WorkingDir + "/" + cluster.GetName() + "/" + app.Host + "_" + app.Port
	if _, err := os.Stat(app.Datadir); os.IsNotExist(err) {
		os.MkdirAll(app.Datadir, os.ModePerm)
		os.MkdirAll(app.Datadir+"/log", os.ModePerm)
		os.MkdirAll(app.Datadir+"/var", os.ModePerm)
		os.MkdirAll(app.Datadir+"/init", os.ModePerm)
	}

	app.LoadConfig() // Update app config from file
	app.LoadDeploymentsConfig()

	return app
}

func (app *App) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.StringVar(&conf.AppHosts, "app-servers", "127.0.0.1", "App hosts")
	flags.StringVar(&conf.AppHostsIPV6, "app-servers-ipv6", "", "App IPv6 bind address ")
	flags.StringVar(&conf.ProvAppAgents, "prov-app-agents", "", "List of application agents")
	flags.StringVar(&conf.ProvAppCpuCores, "prov-app-cpu-cores", "1", "Cpu cores ")
	flags.StringVar(&conf.ProvAppMemory, "prov-app-memory", "1", "Memory usage in giga bytes")
	flags.StringVar(&conf.ProvAppDiskType, "prov-app-disk-type", "volume", "Disk type: [loopback|physical|pool|directory|volume]")
	flags.StringVar(&conf.ProvAppDiskSize, "prov-app-disk-size", "1", "Disk in g for micro service VM")
	flags.StringVar(&conf.ProvAppVolumeData, "prov-app-volume-data", "tank", "Volume name for data")
}

func NewAppConfig() *config.AppConfig {
	return &config.AppConfig{}
}

func NewAppSectionConfig() *config.AppSectionConfig {
	return &config.AppSectionConfig{}
}

func (app *App) Init() {
	webappdir := app.Datadir + "/var"

	if _, err := os.Stat(webappdir); os.IsNotExist(err) {
		app.GetAppConfig()
		os.Symlink(app.Datadir+"/init/data", webappdir)
	}
}

func (app *App) Refresh() error {
	resp, err := http.Get(app.GetURL())
	status := StateWebDown
	if err == nil {
		status = StateWebRunning
		resp.Body.Close()
	}

	app.State = status
	return nil
}

func (app *App) Failover() {
	app.BackendsStateChange()
}

func (app *App) BackendsStateChange() {
	app.Refresh()
}

func (app *App) CertificatesReload() error {
	return nil
}
