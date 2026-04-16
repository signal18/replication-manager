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
	"slices"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

const (
	ErrAppConnectFailed    = "APPERR001"
	ErrAppUnexpectedStatus = "APPERR002"
	ErrAppTCPConnectFailed = "APPERR003"
	ErrAppUnsupportedProto = "APPERR004"
	appErrFailureThreshold = 3
)

// App defines a app
type App struct {
	Id            string `json:"id" groups:"apps"`
	Name          string `json:"name" groups:"apps"`
	Type          string `json:"type" groups:"apps"`
	Host          string `json:"host" groups:"apps"`
	HostIPV6      string `json:"hostIPV6"`
	Port          string `json:"port" groups:"apps"`
	User          string `json:"-"`
	Pass          string `json:"-"`
	Version       string `json:"version" groups:"apps"`
	Datadir       string `json:"datadir"`
	State         string `json:"state"`
	PrevState     string `json:"prevState"`
	SlapOSDatadir string `json:"slaposDatadir"`
	ServiceName   string `json:"serviceName"`
	Agent         string `json:"agent"`
	Weight        string `json:"weight"`
	FailCount     int    `json:"failCount"`
	// Route-scoped debounce counters are the single source of truth.
	AppErrConsecutiveMap map[string]int         `json:"-"`
	ErrState             map[string]state.State `json:"-"`
	ClusterGroup         *Cluster               `json:"-"`
	Process              *os.Process            `json:"process"`
	RouteStatus          []config.RouteStatus   `json:"routeStatus"`
	Variables            map[string]string      `json:"-"`
	AppConfig            *config.AppConfig      `json:"config" groups:"apps"`
	AppClusterSubstitute string                 `json:"appClusterSubstitute"`
	TemplateMD5Prov      string                 `json:"templateMD5Prov"`
	TemplateMD5          string                 `json:"templateMD5"`
	IsHashingTemplate    bool                   `json:"isHashingTemplate"`
	*sync.Mutex          `json:"-"`
}

type appList []*App

func (cluster *Cluster) newAppList() error {
	// Build into a temporary slice first, then swap atomically so that
	// concurrent readers never observe a partially-populated list.
	type pendingApp struct {
		app      *App
		hostport string
		s3Prov   bool
	}
	pending := make([]pendingApp, 0, len(cluster.Conf.Apps))
	news3providers := make([]string, 0)
	cluster.Conf.Cloud18ApplicationCreditsUsed = 0

	for k, appcnf := range cluster.Conf.Apps {
		if appcnf.AppHost == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Skipping app config at index %d: AppHost is empty (file may be incomplete)", k)
			continue
		}
		app := NewApp(k, cluster, appcnf.AppHost+":"+appcnf.AppPort)
		hostport := app.GetHost() + ":" + app.GetPort()
		pending = append(pending, pendingApp{app: app, hostport: hostport, s3Prov: appcnf.AppS3Provider})
	}

	// All apps are constructed — now build the final slice and swap in one step.
	newApps := make([]*App, 0, len(pending))
	for _, p := range pending {
		cluster.addAppToList(&newApps, p.app)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg,
			"New HA App created: %s %s (id=%s)", p.app.GetHost(), p.app.GetPort(), p.app.Id)
		if p.s3Prov {
			news3providers = append(news3providers, p.hostport)
			if !slices.Contains(cluster.AppS3Providers, p.hostport) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
					"Add app as S3 provider: %s", p.app.Name)
			}
		}
	}

	cluster.Apps = newApps
	cluster.AppS3Providers = news3providers

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Loaded %d apps", len(cluster.Apps))

	cluster.LoadAllAppTemplateMD5Provisioned()

	return nil
}

// addAppToList initialises an App and appends it to the supplied slice.
// It mirrors AddApp but writes to the provided slice instead of cluster.Apps,
// allowing newAppList to build the full list before the atomic swap.
func (c *Cluster) addAppToList(list *[]*App, app *App) {
	app.SetCluster(c)
	app.SetID()
	app.SetDataDir()
	app.SetServiceName(c.Name)
	app.SetDefaultRoute(c.Conf.Cloud18Domain, c.Conf.Cloud18SubDomain, c.Conf.Cloud18SubDomainZone, c.Name)
	c.LogModulePrintf(c.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"New application monitored %s: %s:%s", app.GetType(), app.GetHost(), app.GetPort())
	app.SetState(stateSuspect)
	*list = append(*list, app)
	if app.AppConfig.ProvAppCreditPlanned == 0 {
		app.AppConfig.ProvAppCreditPlanned = len(app.GetAppAgents())
	}
}

func (app *App) FetchStats() {
	// TO DO: implement app specific stats fetching
}

func NewApp(placement int, cluster *Cluster, appHost string) *App {
	conf := cluster.Conf
	app := new(App)
	app.Mutex = &sync.Mutex{}
	app.Name, app.Port = misc.SplitHostPortApp(appHost)
	app.Host = app.Name
	app.State = stateSuspect
	appCnf := cluster.GetAppConfig(app.Name, app.Port)
	app.SetPlacement(placement, appCnf.ProvAppAgents, conf.SlapOSAppPartitions)
	app.AppConfig = appCnf
	if conf.ProvNetCNI {
		app.Host = app.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}

	app.RouteStatus = make([]config.RouteStatus, 0)
	app.ErrState = make(map[string]state.State)
	app.AppErrConsecutiveMap = make(map[string]int)
	app.CheckPrimaryRoute()
	return app
}

func (app *App) RecordAppError(key string, st state.State) {
	app.Lock()
	defer app.Unlock()
	if app.ErrState == nil {
		app.ErrState = make(map[string]state.State)
	}

	app.ErrState[key] = st
}

func (app *App) ResetAppError(keys ...string) {
	app.Lock()
	defer app.Unlock()

	if app.ErrState != nil {
		for _, key := range keys {
			delete(app.ErrState, key)
		}
	}
}

func (app *App) ClearAppError() {
	app.Lock()
	defer app.Unlock()

	app.ErrState = make(map[string]state.State)
}

func (app *App) IncAppErrConsecutiveCnt(routeKey string) int {
	app.Lock()
	defer app.Unlock()

	if app.AppErrConsecutiveMap == nil {
		app.AppErrConsecutiveMap = make(map[string]int)
	}
	app.AppErrConsecutiveMap[routeKey]++
	return app.AppErrConsecutiveMap[routeKey]
}

func (app *App) ResetAppErrConsecutiveCnt(routeKey string) {
	app.Lock()
	defer app.Unlock()

	if app.AppErrConsecutiveMap != nil {
		delete(app.AppErrConsecutiveMap, routeKey)
	}
}

func (app *App) ResetAllAppErrConsecutiveCnt() {
	app.Lock()
	defer app.Unlock()

	app.AppErrConsecutiveMap = make(map[string]int)
}

func (app *App) AddFlags(flags *pflag.FlagSet, conf *config.AppConfig) {
	flags.StringVar(&conf.AppHost, "app-host", "app1", "App Host")
	flags.StringVar(&conf.AppPort, "app-port", "80", "App Port")
	flags.StringVar(&conf.AppDbUser, "app-db-user", "", "App Database User")
	flags.StringVar(&conf.AppDbPass, "app-db-pass", "", "App Database Password")
	flags.StringVar(&conf.AppDbSchema, "app-db-schema", "", "App Database Schema")
	flags.BoolVar(&conf.AppS3Provider, "app-s3-provider", false, "Whether the app is an S3 provider, default is false.")
	flags.IntVar(&conf.ProvAppCreditPlanned, "prov-app-credit-planned", 0, "Planned App Credit for the application, default is 0.")
	flags.IntVar(&conf.ProvAppCreditUsed, "prov-app-credit-used", 0, "Used App Credit for the application, default is 0.")
}

func (app *App) Refresh() error {
	cluster := app.ClusterGroup
	app.CheckPrimaryRoute()
	appState := app.GetMonitoringStatus()
	sub, err := cluster.GetAppsSubstitutionJSon(app)
	if err == nil {
		app.AppClusterSubstitute = sub
	}

	switch appState {
	case stateMaintenance:
		app.SetState(stateMaintenance)
	case stateAppRunning:
		app.SetState(stateAppRunning)
		app.FailCount = 0
	case stateFailed:
		if app.FailCount >= cluster.Conf.MaxFail {
			app.SetState(stateFailed)
		} else {
			app.SetState(stateSuspect)
			app.FailCount++
		}
	case stateAppWarning:
		app.SetState(stateAppWarning)
	}

	// Send alert if state has changed
	if app.PrevState != app.State {
		//if cluster.Conf.Verbose {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "app %s state changed from %s to %s", app.Name, app.PrevState, app.State)
		if app.State != stateSuspect {
			lvl := "ALERT"
			if app.State == stateAppRunning {
				lvl = "ALERTOK"
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, lvl, "app %s state changed from %s to %s", app.Name, app.PrevState, app.State)
		}
	}

	if app.PrevState != app.State {
		app.SetPrevState(app.State)
	}
	return nil
}

func (app *App) BackendsStateChange() {
	app.Refresh()
}

func (app *App) CertificatesReload() error {
	return nil
}
