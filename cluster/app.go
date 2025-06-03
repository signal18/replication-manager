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
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/pflag"
)

// App defines a app
type App struct {
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
	FailCount       int               `json:"failCount"`
	ShardApp        *ServerMonitor    `json:"shardApp"`
	ClusterGroup    *Cluster          `json:"-"`
	Process         *os.Process       `json:"process"`
	Variables       map[string]string `json:"-"`
	Lock            sync.Mutex        `json:"-"`
	AppConfig       *config.AppConfig `json:"config"`
	TunnelPort      int               `json:"tunnelPort"`
	TunnelWritePort int               `json:"tunnelWritePort"`
	Tunnel          bool              `json:"tunnel"`
	IsStaging       bool              `json:"isStaging"`
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

func (cluster *Cluster) SendAppStats(app *App) error {
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
	app := new(App)
	app.Name, app.Port = misc.SplitHostPort(appHost)
	app.Host = app.Name
	appCnf := cluster.GetAppConfig(app.Name, app.Port)
	app.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSAppPartitions, conf.AppHostsIPV6)
	app.AppConfig = appCnf
	if conf.ProvNetCNI {
		app.Host = app.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}

	return app
}

func (app *App) AddFlags(flags *pflag.FlagSet, conf *config.AppConfig) {
	flags.StringVar(&conf.AppHost, "app-host", "admin", "App Host")
	flags.StringVar(&conf.AppPort, "app-port", "80", "App Port")
}

func (app *App) Init() {

}

func (app *App) Refresh() error {

	return nil
}

func (app *App) BackendsStateChange() {
	app.Refresh()
}

func (app *App) CertificatesReload() error {
	return nil
}
