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
	AddFlags(flags *pflag.FlagSet, conf *config.Config)
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
	OpenSVCGetAppDockerImg() string
	OpenSVCGetAppDockerRunArgs() string
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
	GetConf() config.Config
	GetAPIUserByUsername(username string) (clusterauth.APIUser, bool)
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
