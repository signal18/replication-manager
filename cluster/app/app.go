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
	"hash/crc64"
	"os"
	"sync"

	clusterauth "github.com/signal18/replication-manager/cluster/auth"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/spf13/pflag"
)

var AppConfig config.AppConfig

type AppList []AppInterface

type App struct {
	AppInterface
	Id                    string                     `json:"id"`
	Name                  string                     `json:"name"`
	Domain                string                     `json:"domain"`
	Type                  string                     `json:"type"`
	Host                  string                     `json:"host"`
	HostCnf               string                     `json:"-"`
	HostIPV6              string                     `json:"hostIPV6"`
	Port                  string                     `json:"port"`
	AppConfig             config.AppConfig           `json:"config"`
	Cluster               ClusterInterface           `json:"clustername"`
	User                  string                     `json:"user"`
	Pass                  string                     `json:"-"`
	Configurator          *configurator.Configurator `json:"-"`
	Datadir               string                     `json:"datadir"`
	State                 string                     `json:"state"`
	PrevState             string                     `json:"prevState"`
	FailCount             int                        `json:"failCount"`
	SlapOSDatadir         string                     `json:"slaposDatadir"`
	Process               *os.Process                `json:"process"`
	IsCompute             bool                       `json:"isCompute"`
	ConfigGitCloneUrl     string                     `json:"configGitCloneUrl"`
	ConfigGitUser         string                     `json:"configGitUser"`
	ConfigGitPassword     string                     `json:"configGitPassword"`
	ConfigGitBranch       string                     `json:"configGitBranch"`
	ConfigSecretVariables map[string]string          `json:"-"`
	ConfigEnvVariables    map[string]string          `json:"-"`
	ConfigVolumeMount     map[string]string          `json:"configVolumeMount"`
	DataGitCloneUrl       string                     `json:"dataGitCloneUrl"`
	DataGitUser           string                     `json:"dataGitUser"`
	DataGitPassword       string                     `json:"-"`
	DataGitBranch         string                     `json:"dataGitBranch"`
	DataVolumeMount       map[string]string          `json:"dataVolumeMount"`
	LogVolumeMount        map[string]string          `json:"logVolumeMount"`
	ServiceName           string                     `json:"serviceName"`
	Agent                 string                     `json:"agent"`
	Weight                string                     `json:"weight"`
	Lock                  sync.Mutex
	Logger                *config.LogrusWrapper `json:"-"`
	RepMgrVersion         string                `json:"-"`
	Version               *version.Version      `json:"-"`
}

type AppInterface interface {
	AddFlags(flags *pflag.FlagSet, conf *config.AppConfig)
	Init()
	Refresh() error
	Failover()
	GetType() string
	IsRunning() bool
	IsIgnored() bool
	GetFailCount() int
	SetFailCount(c int)
	DelLock()
	SetLock()
	GetAgent() string
	GetName() string
	GetHost() string
	GetPort() string
	GetUser() string
	GetPass() string
	GetURL() string
	GetId() string
	GetState() string
	GetServiceName() string
	GetOrchestrator() string
	GetPrevState() string
	SetPrevState(state string)
	GetClustername() string
	IsDown() bool
	GetAppConfig() string
	GetDatadir() string
	GetConfigDatadir() string
	GetConfigConfigdir() string
	GetEnv() map[string]string
	GetSshEnv() string
	GetInitContainer(collector opensvc.Collector) string
	OpenSVCGetAppDefaultSection() map[string]string
	OpenSVCGetAppDiskPool() string
	OpenSVCGetAppDiskType() string
	OpenSVCGetAppAgentsFailover() string
	OpenSVCGetAppDiskSize() string
	OpenSVCGetAppServiceType() string
	OpenSVCGetAppGateway() string
	OpenSVCGetAppNetMask() string
	OpenSVCGetRouteAddr() string
	OpenSVCGetRoutePort() string
	OpenSVCGetRouteMask() string
	OpenSVCGetAppCpuCores() string
	OpenSVCGetAppMemory() string
	OpenSVCGetAppVolumeData() string
	GetAppDockerImg() string
	GetAppDockerRunArgs() string
	OpenSVCSetRouteAddr(addr string)
	OpenSVCSetRoutePort(port string)
	GetVIP() string
	SetSuspect()
	SetID()
	SetDataDir()
	SetServiceName(namespace string)
	SetProvAppImage(value string) error
	SetProvAppAgents(value string) error
	SetProvAppDiskSize(value string) error
	SetProvAppServiceType(value string) error
	SetProvAppCpuCores(value string) error
	SetProvAppMemory(value string) error
	SetProvAppVolumeData(value string) error
	SetProvAppDockerImg(value string) error
	SetProvAppDockerRunArgs(value string) error
	SetProvAppGateway(value string) error
	SetProvAppNetMask(value string) error
	SetProvAppRouteAddr(value string) error
	SetProvAppRoutePort(value string) error
	SetProvAppRouteMask(value string) error
	SetProvAppType(value string) error
	SetProvAppDiskPool(value string) error
	SetProvAppDiskType(value string) error
	SetProvAppAgentsFailover(value string) error
	SetProvisionCookie() error
	SetUnprovisionCookie() error
	SetReprovCookie() error
	SetRestartCookie() error
	SetWaitStartCookie() error
	SetWaitStopCookie() error
	HasProvisionCookie() bool
	HasUnprovisionCookie() bool
	HasReprovCookie() bool
	HasRestartCookie() bool
	HasWaitStartCookie() bool
	HasWaitStopCookie() bool
	HasConfigCookie() bool
	DelProvisionCookie() error
	DelUnprovisionCookie() error
	DelReprovisionCookie() error
	DelRestartCookie() error
	DelWaitStartCookie() error
	DelWaitStopCookie() error
}

type ClusterInterface interface {
	GetName() string
	GetHost() string
	GetPort() string
	GetCrcTable() *crc64.Table
	GetConf() *config.Config
	GetConfigurator() configurator.Configurator
	GetAPIUserByUsername(username string) (clusterauth.APIUser, bool)
	IsInFailover() bool
	OnPremiseGetSSHKey() string
	LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{})
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

func NewAppInstance(cluster ClusterInterface, placement int, apptype, url, domain string, name string, compute bool) *App {
	app := new(App)
	app.Type = apptype
	app.HostCnf = url // store host from config file
	app.Cluster = cluster
	app.Version, _ = version.NewVersionFromString(apptype, "")
	app.FailCount = 0

	app.Host, app.Port = misc.SplitHostPort(url)
	app.Name = name
	if app.Name == "" {
		app.Name = app.Host
	}

	// Source name will equal to cluster name
	app.ServiceName = cluster.GetName() + "/svc/" + app.Name

	app.Domain = domain

	//will be overide in Refresh with show variables server_id, used for provisionning configurator for server_id
	app.SetID()
	// NOTE: does this make sense to set the state to the same?
	app.SetPrevState(stateSuspect)
	app.SetState(stateSuspect)

	app.Datadir = cluster.GetConf().WorkingDir + "/" + cluster.GetName() + "/apps/" + app.Host + "_" + app.Port
	if _, err := os.Stat(app.Datadir); os.IsNotExist(err) {
		os.MkdirAll(app.Datadir, os.ModePerm)
		os.MkdirAll(app.Datadir+"/log", os.ModePerm)
		os.MkdirAll(app.Datadir+"/var", os.ModePerm)
		os.MkdirAll(app.Datadir+"/init", os.ModePerm)
	}

	return app
}

func (app *App) AddDefaultFlags(flags *pflag.FlagSet, conf *config.AppConfig) {
	flags.StringVar(&conf.ProvAppType, "prov-app-type", "", "Application type")
	flags.StringVar(&conf.ProvAppDiskPool, "prov-app-disk-pool", "", "Application disk pool")
	flags.StringVar(&conf.ProvAppDiskType, "prov-app-disk-type", "", "Application disk type")
	flags.StringVar(&conf.ProvAppDockerImg, "prov-app-docker-img", "", "Application docker image")
	flags.StringVar(&conf.ProvAppAgents, "prov-app-agents", "", "Application agents")
	flags.StringVar(&conf.ProvAppDiskSize, "prov-app-disk-size", "", "Application disk size")
	flags.StringVar(&conf.ProvAppCpuCores, "prov-app-cpu-cores", "", "Application CPU cores")
	flags.StringVar(&conf.ProvAppMemory, "prov-app-memory", "", "Application memory")
	flags.StringVar(&conf.ProvAppVolumeData, "prov-app-volume-data", "", "Application volume data")
	flags.StringVar(&conf.ProvAppDockerRunArgs, "prov-app-docker-run-args", "", "Application docker run args")
	flags.StringVar(&conf.ProvAppAgentsFailover, "prov-app-agents-failover", "", "Application agents failover")
	flags.StringVar(&conf.ProvAppNetIface, "prov-app-net-iface", "", "Application net iface")
	flags.StringVar(&conf.ProvAppNetmask, "prov-app-net-mask", "", "Application net mask")
	flags.StringVar(&conf.ProvAppGateway, "prov-app-net-gateway", "", "Application net gateway")
	flags.StringVar(&conf.ProvAppRouteAddr, "prov-app-route-addr", "", "Application route addr")
	flags.StringVar(&conf.ProvAppRoutePort, "prov-app-route-port", "", "Application route port")
	flags.StringVar(&conf.ProvAppRouteMask, "prov-app-route-mask", "", "Application route mask")
	flags.StringVar(&conf.ProvAppRoutePolicy, "prov-app-route-policy", "", "Application route policy")
	flags.StringVar(&conf.AppHosts, "app-hosts", "", "Application hosts")
	flags.StringVar(&conf.AppRunCommand, "app-run-command", "", "Application run command")
	flags.StringVar(&conf.AppConfigGitCloneUrl, "app-config-git-clone-url", "", "Application config git clone url")
	flags.StringVar(&conf.AppConfigGitUser, "app-config-git-user", "", "Application config git user")
	flags.StringVar(&conf.AppConfigGitPassword, "app-config-git-password", "", "Application config git password")
	flags.StringVar(&conf.AppConfigGitBranch, "app-config-git-branch", "", "Application config git branch")
	flags.StringVar(&conf.AppConfigSecretVariables, "app-config-secret-variables", "", "Application config secret variables")
	flags.StringVar(&conf.AppConfigEnvVariables, "app-config-env-variables", "", "Application config env variables")
	flags.StringVar(&conf.AppConfigVolumes, "app-config-volumes", "", "Application config volumes")
	flags.StringVar(&conf.AppDataGitCloneUrl, "app-data-git-clone-url", "", "Application data git clone url")
	flags.StringVar(&conf.AppDataGitUser, "app-data-git-user", "", "Application data git user")
	flags.StringVar(&conf.AppDataGitPassword, "app-data-git-password", "", "Application data git password")
	flags.StringVar(&conf.AppDataGitBranch, "app-data-git-branch", "", "Application data git branch")
	flags.StringVar(&conf.AppDataVolumes, "app-data-volumes", "", "Application data volumes")
	flags.StringVar(&conf.AppLogVolumes, "app-log-volumes", "", "Application log volumes")
}
