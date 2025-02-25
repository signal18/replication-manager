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
package cluster

import (
	"os"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/pflag"
)

type appList []AppInterface

type App struct {
	AppInterface
	Id                    string            `json:"id"`
	Name                  string            `json:"name"`
	Type                  string            `json:"type"`
	Host                  string            `json:"host"`
	HostIPV6              string            `json:"hostIPV6"`
	Port                  string            `json:"port"`
	ClusterGroup          *Cluster          `json:"-"`
	Datadir               string            `json:"datadir"`
	State                 string            `json:"state"`
	PrevState             string            `json:"prevState"`
	FailCount             int               `json:"failCount"`
	SlapOSDatadir         string            `json:"slaposDatadir"`
	Process               *os.Process       `json:"process"`
	ConfigGitCloneUrl     string            `json:"configGitCloneUrl"`
	ConfigGitUser         string            `json:"configGitUser"`
	ConfigGitPassword     string            `json:"configGitPassword"`
	ConfigGitBranch       string            `json:"configGitBranch"`
	ConfigSecretVariables map[string]string `json:"-"`
	ConfigEnvVariables    map[string]string `json:"-"`
	ConfigVolumeMount     map[string]string `json:"configVolumeMount"`
	DataGitCloneUrl       string            `json:"dataGitCloneUrl"`
	DataGitUser           string            `json:"dataGitUser"`
	DataGitPassword       string            `json:"-"`
	DataGitBranch         string            `json:"dataGitBranch"`
	DataVolumeMount       map[string]string `json:"dataVolumeMount"`
	LogVolumeMount        map[string]string `json:"logVolumeMount"`
	ServiceName           string            `json:"serviceName"`
	Agent                 string            `json:"agent"`
	Weight                string            `json:"weight"`
	Lock                  sync.Mutex
}

type AppInterface interface {
	SetCluster(c *Cluster)
	AddFlags(flags *pflag.FlagSet, conf *config.Config)
	Init()
	Refresh() error
	Failover()
	SetMaintenance(server *ServerMonitor)
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
	GetCluster() *Cluster
	IsDown() bool
	GetDatadir() string
	GetConfigDatadir() string
	GetConfigConfigdir() string
	GetEnv() map[string]string
	GetSshEnv() string
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
	OpenSVCSetRouteAddr(addr string)
	OpenSVCSetRoutePort(port string)
	GetVIP() string
	SetSuspect()
	SetID()
	SetDataDir()
	SetServiceName(namespace string)
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
