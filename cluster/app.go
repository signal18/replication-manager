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
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

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
	ErrAppGatewayConflict  = "APPERR005"
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
	// WarnCount counts consecutive Refresh() cycles reporting stateAppWarning,
	// saturating at appErrorDebounceThreshold(cluster.Conf) -- see the
	// stateAppWarning case in Refresh(). It is reset on any non-warning
	// observation (AppRunning, Failed, maintenance) so interrupted warning
	// streaks cannot accumulate across an unrelated state. Without it, a
	// single transient check failure flips State (and fires the ALERT log)
	// immediately. json:"-" is deliberate: AppAPIView does not expose it and
	// no UI currently reads it -- add it explicitly there (plus GUI/docs
	// updates) if operators should see the pending warning count.
	WarnCount int `json:"-"`
	// Per-app refresh freshness (cluster-level AppRefreshLast* on Cluster
	// only shows batch-wide timing, not which app is actually slow). Set
	// via SetRefreshInProgress/SetRefreshResult under app.Mutex -- read
	// them the same way (App.Lock()/Unlock(), or GetAppAPIView) rather than
	// directly: these are read from a different goroutine than the one
	// that writes them (maybeRefreshAppsAsync's worker vs. any status/API
	// reader).
	LastRefreshStart      time.Time `json:"lastRefreshStart"`
	LastRefreshEnd        time.Time `json:"lastRefreshEnd"`
	LastRefreshDurationMs int64     `json:"lastRefreshDurationMs"`
	LastRefreshError      string    `json:"lastRefreshError"`
	RefreshInProgress     bool      `json:"refreshInProgress"`
	// Route-scoped debounce counters are the single source of truth.
	AppErrConsecutiveMap map[string]int         `json:"-"`
	ErrState             map[string]state.State `json:"-"`
	ClusterGroup         *Cluster               `json:"-"`
	Process              *os.Process            `json:"process" swaggerignore:"true"`
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
	// Serialize whole rebuilds: NewApp()/addAppToList() mutate shared
	// *config.AppConfig pointers in place, so two rebuilds racing corrupts them.
	cluster.appListRebuildMu.Lock()
	defer cluster.appListRebuildMu.Unlock()

	// Snapshot Conf.Apps under cluster.Lock() — other mutators write it under the same lock.
	cluster.Lock()
	confApps := make([]*config.AppConfig, len(cluster.Conf.Apps))
	copy(confApps, cluster.Conf.Apps)
	cluster.Unlock()

	// Build into a temporary slice first, then swap atomically so that
	// concurrent readers never observe a partially-populated list.
	type pendingApp struct {
		app      *App
		hostport string
		s3Prov   bool
	}
	pending := make([]pendingApp, 0, len(confApps))
	news3providers := make([]string, 0)

	for k, appcnf := range confApps {
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
		if err := cluster.addAppToList(&newApps, p.app); err != nil {
			return fmt.Errorf("app list rebuild failed on %s: %w", p.app.Name, err)
		}
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

	cluster.Lock()
	cluster.Apps = newApps
	cluster.AppS3Providers = news3providers
	cluster.bumpAppListVersion()
	cluster.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Loaded %d apps", len(cluster.Apps))

	cluster.LoadAllAppTemplateMD5Provisioned()

	// Backfill ProvAppCreditUsed for apps that are provisioned but whose saved
	// value is zero — this covers apps created before credit persistence was added.
	// We only save when the value actually changes so restarts after backfill are
	// no-ops.
	for _, app := range cluster.Apps {
		if app.HasProvisionCookie() && app.AppConfig.ProvAppCreditUsed == 0 && app.AppConfig.ProvAppCreditPlanned > 0 {
			app.AppConfig.ProvAppCreditUsed = app.AppConfig.ProvAppCreditPlanned
			if _, err := cluster.SaveApp(app, ""); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
					"Failed to persist backfilled credit usage for %s: %s", app.Name, err)
			}
		}
	}

	cluster.recomputeAppCredits()

	return nil
}

// initializeAppForRegistration performs the shared app bootstrap sequence used by
// both addAppToList and AddApp.
func (c *Cluster) initializeAppForRegistration(app *App) error {
	app.SetCluster(c)
	app.SetID()
	app.SetDataDir()
	app.SetServiceName(c.Name)
	if err := app.SetDefaultRoute(c.Conf.Cloud18Domain, c.Conf.Cloud18SubDomain, c.Conf.Cloud18SubDomainZone, c.Name); err != nil {
		return fmt.Errorf("app %s: default route generation failed: %w", app.Name, err)
	}
	c.LogModulePrintf(c.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"New application monitored %s: %s:%s", app.GetType(), app.GetHost(), app.GetPort())
	app.SetState(stateSuspect)
	if app.AppConfig.ProvAppCreditPlanned == 0 {
		app.AppConfig.ProvAppCreditPlanned = len(app.GetAppAgents())
	}
	return nil
}

// addAppToList initialises an App and appends it to the supplied slice.
// It mirrors AddApp but writes to the provided slice instead of cluster.Apps,
// allowing newAppList to build the full list before the atomic swap.
func (c *Cluster) addAppToList(list *[]*App, app *App) error {
	if err := c.initializeAppForRegistration(app); err != nil {
		return err
	}
	*list = append(*list, app)
	return nil
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

// IncWarnCount increments WarnCount and returns the new value, saturating it
// at threshold (threshold <= 0 disables saturation). It is locked (app.Lock())
// so the read-modify-write is atomic with respect to a concurrent Refresh()
// on the same App -- unlike GetWarnCount()+1 followed by a separate
// SetWarnCount() call, which would race.
func (app *App) IncWarnCount(threshold int) int {
	app.Lock()
	defer app.Unlock()

	app.WarnCount++
	if threshold > 0 && app.WarnCount > threshold {
		app.WarnCount = threshold
	}
	return app.WarnCount
}

// appErrorDebounceThreshold resolves the effective consecutive-observation
// count required before a debounced app check commits: the per-route APPERR
// debounce in GetMonitoringStatus (app_chk.go) and the aggregate
// AppRunning->AppWarning debounce in Refresh() both call this so a change to
// the legacy default (appErrFailureThreshold) or to the config fallback rule
// only needs to happen in one place.
func appErrorDebounceThreshold(conf *config.Config) int {
	if conf.AppErrorDebounceThreshold > 0 {
		return conf.AppErrorDebounceThreshold
	}
	return appErrFailureThreshold
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

	start := time.Now()
	app.SetRefreshInProgress(true)
	var refreshErr error
	defer func() {
		app.SetRefreshResult(start, time.Now(), refreshErr)
		app.SetRefreshInProgress(false)
	}()

	app.CheckPrimaryRoute()
	appState := app.GetMonitoringStatus()
	sub, err := cluster.GetAppsSubstitutionJSon(app)
	if err == nil {
		app.AppClusterSubstitute = sub
	}
	refreshErr = err

	switch appState {
	case stateMaintenance:
		app.SetState(stateMaintenance)
		// A warning streak interrupted by maintenance is not consecutive:
		// require a fresh run of warning observations once maintenance ends.
		app.SetWarnCount(0)
	case stateAppRunning:
		app.SetState(stateAppRunning)
		app.SetFailCount(0)
		app.SetWarnCount(0)
	case stateFailed:
		if app.GetFailCount() >= cluster.Conf.MaxFail {
			app.SetState(stateFailed)
		} else {
			app.SetState(stateSuspect)
			app.SetFailCount(app.GetFailCount() + 1)
		}
		// Same reasoning as stateMaintenance: a warning streak interrupted by
		// a Failed observation is not consecutive.
		app.SetWarnCount(0)
	case stateAppWarning:
		// Debounce like stateFailed above: a single transient check failure
		// should not flip State (and fire the ALERT log) on its own. Reuses
		// the same AppErrorDebounceThreshold knob as the per-route APPERR
		// debounce in GetMonitoringStatus (app_chk.go) via
		// appErrorDebounceThreshold, so both debounces move together.
		//
		// IncWarnCount both increments and saturates atomically under a
		// single app.Lock() -- GetWarnCount()+SetWarnCount() as two separate
		// locked calls would race against a concurrent Refresh() on the same
		// App (BackendsStateChange() calls Refresh() directly and is not
		// covered by the async single-flight guarantee in
		// maybeRefreshAppsAsync).
		warnThreshold := appErrorDebounceThreshold(cluster.Conf)
		warnCount := app.IncWarnCount(warnThreshold)
		if warnCount >= warnThreshold {
			app.SetState(stateAppWarning)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
				"Debounced app %s state change to %s (warn count %d/%d)",
				app.Name, stateAppWarning, warnCount, warnThreshold)
		}
	default:
		app.SetWarnCount(0)
	}

	// CommitStateTransition atomically compares State against PrevState and
	// advances PrevState under a single app.Lock(), so the ALERT/ALERTOK
	// decision below sees a consistent (old, new) pair even if another
	// Refresh() call is racing on this App. It only reports changed=true on
	// the cycle that actually commits a new State -- e.g. a still-debouncing
	// AppWarning cycle above never calls SetState, so State==PrevState and no
	// alert fires below the threshold.
	if oldState, newState, changed := app.CommitStateTransition(); changed {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "app %s state changed from %s to %s", app.Name, oldState, newState)
		if lvl := appTransitionAlertLevel(newState); lvl != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, lvl, "app %s state changed from %s to %s", app.Name, oldState, newState)
		}
	}
	return nil
}

// appTransitionAlertLevel returns the LogModulePrintf level for a committed
// App state transition landing on newState, or "" to suppress the alert
// entirely. stateSuspect is the transient state stateFailed's own
// FailCount/MaxFail debounce commits below its threshold -- it is not yet a
// confirmed failure, so it must not alert.
//
// Every other landing state -- stateAppRunning (recovery) included -- uses
// ALERT, matching cluster/srv.go's database state-change logging: the server
// monitor also always logs ALERT (see srv.go's "Server %s state changed from
// %s to %s" call) and lets the oldState/newState values themselves
// distinguish a recovery (e.g. "Failed to Slave") from a new problem, rather
// than switching level. This keeps the two state-machine logging paths
// consistent instead of introducing an app-only ALERTOK convention.
func appTransitionAlertLevel(newState string) string {
	if newState == stateSuspect {
		return ""
	}
	return "ALERT"
}

func (app *App) BackendsStateChange() {
	app.Refresh()
}

func (app *App) CertificatesReload() error {
	return nil
}
