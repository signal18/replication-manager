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
	"strconv"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/spf13/pflag"
)

// App defines a app
type App struct {
	DatabaseApp
	BackendsWrite   []Backend         `json:"backendsWrite"`
	BackendsRead    []Backend         `json:"backendsRead"`
	Id              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Host            string            `json:"host"`
	HostIPV6        string            `json:"hostIPV6"`
	Port            string            `json:"port"`
	User            string            `json:"-"`
	Pass            string            `json:"-"`
	Version         string            `json:"version"`
	Datadir         string            `json:"datadir"`
	State           string            `json:"state"`
	PrevState       string            `json:"prevState"`
	SlapOSDatadir   string            `json:"slaposDatadir"`
	ServiceName     string            `json:"serviceName"`
	Agent           string            `json:"agent"`
	Weight          string            `json:"weight"`
	WritePort       int               `json:"writePort"`
	ReadPort        int               `json:"readPort"`
	ReadWritePort   int               `json:"readWritePort"`
	ReaderHostgroup int               `json:"readerHostGroup"`
	WriterHostgroup int               `json:"writerHostGroup"`
	FailCount       int               `json:"failCount"`
	ShardApp        *ServerMonitor    `json:"shardApp"`
	ClusterGroup    *Cluster          `json:"-"`
	Process         *os.Process       `json:"process"`
	Variables       map[string]string `json:"-"`
	Lock            sync.Mutex        `json:"-"`
	TunnelPort      int               `json:"tunnelPort"`
	TunnelWritePort int               `json:"tunnelWritePort"`
	Tunnel          bool              `json:"tunnel"`
	IsStaging       bool              `json:"isStaging"`
}

type DatabaseApp interface {
	SetCluster(c *Cluster)
	AddFlags(flags *pflag.FlagSet, conf *config.Config)
	Init()
	Refresh() error
	Failover()
	SetMaintenance(server *ServerMonitor)
	BackendsStateChange()
	GetType() string
	CertificatesReload() error
	IsRunning() bool
	IsIgnored() bool
	SetCredential(credential string)

	GetFailCount() int
	SetFailCount(c int)
	DelLock()
	SetLock()
	GetAgent() string
	GetName() string
	GetHost() string
	GetPort() string
	GetURL() string
	GetWritePort() int
	GetReadWritePort() int
	GetReadPort() int
	GetId() string
	GetState() string
	SetState(v string)
	GetUser() string
	GetPass() string
	GetServiceName() string
	GetOrchestrator() string

	GetPrevState() string

	SetPrevState(state string)
	GetCluster() *Cluster
	GetClusterConnection() (*sqlx.DB, error)

	SetMaintenanceApp(server *ServerMonitor)

	IsFilterInTags(filter string) bool
	IsDown() bool
	GetAppConfig() config.AppConfig
	GetJanitorWeight() string
	// GetInitContainer(collector opensvc.Collector) string
	GetBindAddress() string
	GetBindAddressExtraIPV6() string
	GetUseSSL() string
	GetUseCompression() string
	GetDatadir() string
	GetConfigDatadir() string
	GetConfigConfigdir() string
	GetEnv() map[string]string
	GetSshEnv() string
	GetConfigAppModule(variable string) string
	SendStats() error

	OpenSVCGetAppDefaultSection() map[string]string

	SetSuspect()

	SetID()
	SetDataDir()
	SetServiceName(namespace string)
	SetStaging(staging bool)
	IsInStaging() bool

	SetProvisionCookie() error
	SetUnprovisionCookie() error
	SetReprovCookie() error
	SetRestartCookie() error
	SetWaitStartCookie() error
	SetWaitStopCookie() error
	SetConfigCookie() error
	SetConfigRefreshCookie() error
	SetNoConfigFetchCookie() error

	HasProvisionCookie() bool
	HasUnprovisionCookie() bool
	HasReprovCookie() bool
	HasRestartCookie() bool
	HasWaitStartCookie() bool
	HasWaitStopCookie() bool
	HasConfigCookie() bool
	HasConfigRefreshCookie() bool
	HasNoConfigFetchCookie() bool
	HasDNS() bool

	DelProvisionCookie() error
	DelUnprovisionCookie() error
	DelReprovisionCookie() error
	DelRestartCookie() error
	DelWaitStartCookie() error
	DelWaitStopCookie() error
	DelConfigCookie() error
	DelConfigRefreshCookie() error
	DelNoConfigFetchCookie() error

	RotateAppPasswords(password string)
}

type appList []*App

func (cluster *Cluster) newAppList() error {
	cluster.Apps = make([]*App, 0)
	for k, apphost := range strings.Split(cluster.Conf.AppHosts, ",") {
		app := NewApp(k, cluster, apphost)
		cluster.AddApp(app)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg, "New HA App created: %s %s", app.GetHost(), app.GetPort())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Loaded %d apps", len(cluster.Apps))

	return nil
}

func (cluster *Cluster) initApps() {
	for _, pr := range cluster.Apps {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "New app monitored: %s %s:%s", pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.Init()
	}
}

func (cluster *Cluster) SendAppStats(app DatabaseApp) error {
	return app.SendStats()
}

func (app *App) SendStats() error {
	cluster := app.ClusterGroup
	graph, err := graphite.NewGraphite(cluster.Conf.GraphiteCarbonHost, cluster.Conf.GraphiteCarbonPort)
	if err != nil {
		return err
	}

	graph.Disconnect()

	return nil
}

func NewApp(placement int, cluster *Cluster, appHost string) *App {
	conf := cluster.Conf
	appCnf := cluster.GetAppConfig(appHost)
	app := new(App)
	app.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSAppPartitions, conf.AppHostsIPV6)
	app.Port = strconv.Itoa(appCnf.AppAPIPort)
	app.ReadPort = appCnf.AppReadPort
	app.WritePort = appCnf.AppWritePort
	app.ReadWritePort = appCnf.AppWritePort
	app.Name = appHost
	app.Host = appHost
	if conf.ProvNetCNI {
		app.Host = app.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}
	app.User = appCnf.AppUser
	app.Pass = cluster.Conf.GetDecryptedValue("app-password")

	return app
}

func (app *App) AddFlags(flags *pflag.FlagSet, conf *config.AppConfig) {
	flags.StringVar(&conf.AppMode, "app-mode", "runtimeapi", "App mode [standby|runtimeapi|dataplaneapi]")
	flags.BoolVar(&conf.AppDebug, "app-debug", true, "Extra info on monitoring backend")
	flags.StringVar(&conf.AppUser, "app-user", "admin", "App API user")
	flags.StringVar(&conf.AppPassword, "app-password", "admin", "App API password")
	flags.IntVar(&conf.AppAPIPort, "app-api-port", 1999, "App runtime api port")
	flags.IntVar(&conf.AppWritePort, "app-write-port", 3306, "App read-write port to leader")
	flags.IntVar(&conf.AppReadPort, "app-read-port", 3307, "App load balancer read port to all nodes")
	flags.IntVar(&conf.AppStatPort, "app-stat-port", 1988, "App statistics port")
	flags.StringVar(&conf.AppBinaryPath, "app-binary-path", "/usr/sbin/app", "App binary location")
	flags.StringVar(&conf.AppReadBindIp, "app-ip-read-bind", "0.0.0.0", "App input bind address for read")
	flags.StringVar(&conf.AppWriteBindIp, "app-ip-write-bind", "0.0.0.0", "App input bind address for write")
	flags.StringVar(&conf.AppAPIReadBackend, "app-api-read-backend", "service_read", "App API backend name used for read")
	flags.StringVar(&conf.AppAPIWriteBackend, "app-api-write-backend", "service_write", "App API backend name used for write")
}

func (app *App) Init() {

}

func (app *App) Refresh() error {

	return nil
}

func (cluster *Cluster) setMaintenanceApp(pr *App, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

func (app *App) Failover() {
	appcnf := app.ClusterGroup.GetAppConfig(app.Name)
	if appcnf.AppMode == "runtimeapi" {
		app.Refresh()
	}
	if appcnf.AppMode == "standby" {
		app.Init()
	}
}

func (app *App) BackendsStateChange() {
	app.Refresh()
}

func (app *App) CertificatesReload() error {
	return nil
}
