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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/liip/sheriff/v2"
	"github.com/tidwall/sjson"

	//jsoniter "github.com/json-iterator/go"
	"github.com/signal18/replication-manager/config"
)

// appSubstitutionView mirrors the groups:"apps" subset of App
// (cluster/app.go). It exists so GetAppsSubstitutionJSon never hands sheriff
// a live *App: sheriff's reflection boxes each struct element via
// reflect.Value.Interface() to recurse into it, which bulk-copies the whole
// struct's memory *before* its own group-tag filtering runs -- so handing it
// a live App races against any concurrent app-refresh worker mutating that
// same app, regardless of whether the racing field is itself group-tagged.
// buildAppSubstitutionView instead produces an independent copy up front.
type appSubstitutionView struct {
	Id        string            `json:"id" groups:"apps"`
	Name      string            `json:"name" groups:"apps"`
	Type      string            `json:"type" groups:"apps"`
	Host      string            `json:"host" groups:"apps"`
	Port      string            `json:"port" groups:"apps"`
	Version   string            `json:"version" groups:"apps"`
	AppConfig *config.AppConfig `json:"config" groups:"apps"`
}

// cloneAppConfigForSubstitution returns an independent copy of cnf, safe to
// hand to sheriff even while cnf (or the App owning it) is concurrently
// mutated elsewhere. config.AppConfig has no embedded lock, so a shallow
// value copy is safe; config.Deployment does embed a sync.RWMutex
// (config/deployment.go), so it's rebuilt fresh rather than copied by
// value, with Routes/PrimaryRoute deep-cloned via config.Route.Clone() (the
// only Deployment fields the refresh loop -- CheckPrimaryRoute -- mutates)
// and Storages/Paths/Variables passed through by reference (only touched by
// config/API flows, not the refresh loop).
//
// Callers owning an *App that shares this AppConfig (via app.AppConfig)
// must call this under app.Lock(), since CheckPrimaryRoute mutates
// Routes/PrimaryRoute under that same lock. cluster.Conf.Apps entries with
// no matching App (e.g. skipped by newAppList for an invalid host) are
// never touched by the refresh loop, so cloning them lock-free is safe.
func cloneAppConfigForSubstitution(cnf *config.AppConfig) *config.AppConfig {
	if cnf == nil {
		return nil
	}
	clone := *cnf
	if d := cnf.Deployment; d != nil {
		var routes config.Routes
		if d.Routes != nil {
			routes = make(config.Routes, len(d.Routes))
			for i, r := range d.Routes {
				routes[i] = r.Clone()
			}
		}
		clone.Deployment = &config.Deployment{
			PrimaryRoute: d.PrimaryRoute.Clone(),
			Routes:       routes,
			Storages:     d.Storages,
			Paths:        d.Paths,
			Variables:    d.Variables,
		}
	}
	return &clone
}

// buildAppSubstitutionView builds an independent, race-free copy of the
// groups:"apps" subset of app's fields, safe to hand to sheriff even while
// other app-refresh workers concurrently mutate this same app.
//
// Id/Name/Type/Host/Port/Version are set once at construction/config-load
// and never mutated by the refresh loop, so they're read lock-free here --
// the same trust already given to GetHost()/GetPort(), called lock-free
// elsewhere in this exact refresh path. AppConfig is cloned under
// app.Lock() (see cloneAppConfigForSubstitution).
func (app *App) buildAppSubstitutionView() *appSubstitutionView {
	view := &appSubstitutionView{
		Id:      app.Id,
		Name:    app.Name,
		Type:    app.Type,
		Host:    app.Host,
		Port:    app.Port,
		Version: app.Version,
	}
	if app.AppConfig == nil {
		return view
	}
	app.Lock()
	view.AppConfig = cloneAppConfigForSubstitution(app.AppConfig)
	app.Unlock()
	return view
}

// proxySubstitutionView mirrors the groups:"apps" subset of Proxy
// (cluster/prx.go). Every concrete proxy type (HaproxyProxy, ProxySQLProxy,
// MaxscaleProxy, etc.) embeds Proxy by value with no fields of its own, so
// this one view type covers all of them. Built for the same reason as
// appSubstitutionView: sheriff's reflect.Value.Interface() bulk-copies the
// whole struct to box it for recursion, before its own group-tag filtering
// runs, so handing it a live DatabaseProxy races against refreshProxies
// (cluster/prx.go) regardless of which field is racing -- refreshProxies
// runs as its own goroutine, concurrently with app-refresh workers building
// this same substitution JSON, since app refresh was decoupled from the
// tick's waited phase. Proven under -race, not assumed.
type proxySubstitutionView struct {
	Id            string `json:"id" groups:"apps"`
	Name          string `json:"name" groups:"apps"`
	Type          string `json:"type" groups:"apps"`
	Host          string `json:"host" groups:"apps"`
	Port          string `json:"port" groups:"apps"`
	WritePort     int    `json:"writePort" groups:"apps"`
	ReadPort      int    `json:"readPort" groups:"apps"`
	ReadWritePort int    `json:"readWritePort" groups:"apps"`
	Version       string `json:"version" groups:"apps"`
}

// buildProxySubstitutionView builds an independent, race-free copy of the
// groups:"apps" subset of pr's fields, safe to hand to sheriff even while
// refreshProxies concurrently mutates this same proxy.
//
// Id/Name/Type/Host/Port/WritePort/ReadPort/ReadWritePort are set once at
// proxy construction/config-load and never mutated by the refresh loop, so
// they're read lock-free here. Version IS reassigned on every Refresh() by
// several proxy types (ExternalProxy, MariadbShardProxy, ProxySQLProxy,
// HaproxyProxy, ProxyJanitor, SphinxProxy); GetVersion() takes p.Lock
// internally (see prx_get.go), matching SetVersion() on the write side, so
// no manual lock pairing is needed (or exposed) here.
func buildProxySubstitutionView(pr DatabaseProxy) *proxySubstitutionView {
	if pr == nil {
		return nil
	}
	return &proxySubstitutionView{
		Id:            pr.GetId(),
		Name:          pr.GetName(),
		Type:          pr.GetType(),
		Host:          pr.GetHost(),
		Port:          pr.GetPort(),
		WritePort:     pr.GetWritePort(),
		ReadPort:      pr.GetReadPort(),
		ReadWritePort: pr.GetReadWritePort(),
		Version:       pr.GetVersion(),
	}
}

// serverSubstitutionView mirrors the groups:"apps" subset of ServerMonitor
// (cluster/srv.go). ServerMonitor embeds several sync.Mutex/sync.RWMutex
// fields by value (freezeMu, workingAgentMu, jobMutex, etc.), so it can
// never be copied by value -- this view exists precisely to avoid that,
// same as appSubstitutionView/proxySubstitutionView. Proven under -race:
// ServerMonitor.SetState (cluster/srv_set.go) is unlocked and runs
// continuously as part of normal DB topology monitoring, a goroutine
// independent of app-refresh workers; sheriff's whole-struct bulk copy
// races against it exactly like App/Proxy, regardless of State itself not
// being groups:"apps"-tagged.
type serverSubstitutionView struct {
	Id                string `json:"id" groups:"apps"`
	Name              string `json:"name" groups:"apps"`
	Domain            string `json:"domain" groups:"apps"`
	SourceClusterName string `json:"sourceClusterName" groups:"apps"`
	URL               string `json:"url" groups:"apps"`
	Host              string `json:"host" groups:"apps"`
	Port              string `json:"port" groups:"apps"`
}

// buildServerSubstitutionView builds an independent, race-free copy of the
// groups:"apps" subset of srv's fields, safe to hand to sheriff even while
// the DB monitoring loop concurrently mutates this same server.
//
// All 7 fields are set once at server construction/config-load and never
// reassigned by ServerMonitor.Refresh() (grepped; no hits) -- same
// write-once trust already given to App/Proxy identity fields -- so they're
// read lock-free here. No lock is needed because, unlike Deployment.Routes
// or Proxy.Version, nothing in the refresh loop mutates these specific
// fields; the race is purely sheriff's whole-struct bulk copy racing
// against *other* ServerMonitor fields, which this view sidesteps by never
// handing sheriff the live struct at all.
func buildServerSubstitutionView(srv *ServerMonitor) *serverSubstitutionView {
	if srv == nil {
		return nil
	}
	return &serverSubstitutionView{
		Id:                srv.Id,
		Name:              srv.Name,
		Domain:            srv.Domain,
		SourceClusterName: srv.SourceClusterName,
		URL:               srv.URL,
		Host:              srv.Host,
		Port:              srv.Port,
	}
}

func (cluster *Cluster) GetAppsSubstitutionJSon(app *App) (string, error) {
	o := &sheriff.Options{Groups: []string{"apps"}}

	// Snapshot cluster.Apps, cluster.Proxies, cluster.Servers, cluster.Conf
	// and cluster.Conf.Apps under cluster.Lock(): all are reassigned
	// wholesale under this lock elsewhere (see newAppList, cluster_del.go,
	// and GetAppConfig's own "Conf.Apps is mutated under cluster.Lock()
	// elsewhere" note) -- reading any of them without it races on the
	// slice/pointer header itself, separately from the per-object races
	// fixed below. confCopy is taken here (not later) so the whole-Config
	// value copy is covered by the same lock as Conf.Apps. nil is preserved
	// (not made into an empty-but-non-nil slice) so a cluster with nothing
	// in a given collection still marshals e.g. "apps": null, matching
	// sheriff's original behavior for a nil slice -- templates rely on that
	// to leave {{apps...}}/{{proxies...}} placeholders unresolved rather
	// than "".
	cluster.Lock()
	var apps []*App
	if cluster.Apps != nil {
		apps = make([]*App, len(cluster.Apps))
		copy(apps, cluster.Apps)
	}
	var proxies []DatabaseProxy
	if cluster.Proxies != nil {
		proxies = make([]DatabaseProxy, len(cluster.Proxies))
		copy(proxies, cluster.Proxies)
	}
	var servers []*ServerMonitor
	if cluster.Servers != nil {
		servers = make([]*ServerMonitor, len(cluster.Servers))
		copy(servers, cluster.Servers)
	}
	var liveConfApps []*config.AppConfig
	var confCopy config.Config
	haveConf := cluster.Conf != nil
	if haveConf {
		confCopy = *cluster.Conf
		if cluster.Conf.Apps != nil {
			liveConfApps = make([]*config.AppConfig, len(cluster.Conf.Apps))
			copy(liveConfApps, cluster.Conf.Apps)
		}
	}
	cluster.Unlock()

	var proxyViews []*proxySubstitutionView
	if proxies != nil {
		proxyViews = make([]*proxySubstitutionView, 0, len(proxies))
		for _, pr := range proxies {
			if pr != nil {
				proxyViews = append(proxyViews, buildProxySubstitutionView(pr))
			}
		}
	}

	var serverViews []*serverSubstitutionView
	if servers != nil {
		serverViews = make([]*serverSubstitutionView, 0, len(servers))
		for _, srv := range servers {
			if srv != nil {
				serverViews = append(serverViews, buildServerSubstitutionView(srv))
			}
		}
	}

	// Build each app's substitution view (clones AppConfig/Deployment under
	// that app's own lock) and remember which cloned AppConfig corresponds
	// to which live one, so cluster.Conf.Apps below can reuse it instead of
	// reflecting the exact same live object a second, unsynchronized way:
	// cluster.Conf.Apps[i] and app.AppConfig are literally the same
	// *config.AppConfig pointer (see GetAppConfig), so leaving Conf's copy
	// live would still race against CheckPrimaryRoute even after fixing the
	// App-list path above.
	var appViews []*appSubstitutionView
	cloneByLiveConfig := make(map[*config.AppConfig]*config.AppConfig, len(apps))
	if apps != nil {
		appViews = make([]*appSubstitutionView, 0, len(apps))
		for _, a := range apps {
			if a == nil {
				continue
			}
			view := a.buildAppSubstitutionView()
			appViews = append(appViews, view)
			if a.AppConfig != nil {
				cloneByLiveConfig[a.AppConfig] = view.AppConfig
			}
		}
	}

	var confAppClones []*config.AppConfig
	if liveConfApps != nil {
		confAppClones = make([]*config.AppConfig, 0, len(liveConfApps))
		for _, cnf := range liveConfApps {
			if cnf == nil {
				continue
			}
			if clone, ok := cloneByLiveConfig[cnf]; ok {
				confAppClones = append(confAppClones, clone)
				continue
			}
			confAppClones = append(confAppClones, cloneAppConfigForSubstitution(cnf))
		}
	}

	// config.Config itself has no embedded lock, so the value copy taken
	// under cluster.Lock() above is safe; it also means the rest of
	// Config's ~500 other fields are read from a single point-in-time
	// snapshot rather than reflected live for the whole marshal walk. Only
	// Apps needed replacing here.
	var confView *config.Config
	if haveConf {
		confCopy.Apps = confAppClones
		confView = &confCopy
	}

	clusterView := struct {
		Name    string                    `json:"name" groups:"apps,web"`
		Servers []*serverSubstitutionView `json:"servers" groups:"apps"`
		Apps    []*appSubstitutionView    `json:"apps" groups:"apps"`
		Proxies []*proxySubstitutionView  `json:"proxies" groups:"apps"`
		Conf    *config.Config            `json:"config" groups:"apps"`
	}{cluster.Name, serverViews, appViews, proxyViews, confView}

	data, err := sheriff.Marshal(o, &clusterView)
	if err != nil {
		return "", err
	}
	result, err2 := json.Marshal(data)
	if err2 != nil {
		return string(result), err2
	}

	// Add app specific data
	child, err3 := sheriff.Marshal(o, app.buildAppSubstitutionView())
	if err3 != nil {
		return string(result), err3
	}

	result, err5 := sjson.SetBytes(result, "app", child)
	return string(result), err5

}

func (cluster *Cluster) GetAppFromName(name string) *App {
	for _, pr := range cluster.Apps {
		if pr.GetId() == name {
			return pr
		}
	}
	return nil
}

func (app *App) GetAppConfig() *config.AppConfig {
	return app.AppConfig
}

func (app *App) GetJanitorWeight() string {
	return app.Weight
}

// Remove unused method

func (app *App) GetBindAddress() string {
	if app.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return app.Host
	}
	return "0.0.0.0"
}
func (app *App) GetBindAddressExtraIPV6() string {
	if app.HostIPV6 != "" {
		return app.HostIPV6 + ";"
	}
	return ""
}

func (app *App) GetConfigDatadir() string {
	if app.GetOrchestrator() == config.ConstOrchestratorSlapOS {
		return app.SlapOSDatadir
	}
	if app.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		return "/var/lib/" + app.Type
	}

	return "/var/lib/" + app.Type
}

func (app *App) GetConfigConfigdir() string {
	if app.GetOrchestrator() == config.ConstOrchestratorSlapOS {
		return app.SlapOSDatadir + "/etc/" + app.GetType()
	}
	return "/etc"
}

func (app *App) GetDatadir() string {
	return app.Datadir
}

func (app *App) GetName() string {
	return app.Name
}

func (app *App) GetEnv() map[string]string {
	return app.GetBaseEnv()
}

func (app *App) GetBaseEnv() map[string]string {
	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                    app.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":             app.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":              string(app.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":             string(app.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":   app.ClusterGroup.GetDbPass(),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%":       app.ClusterGroup.GetDbUser(),
		"%%ENV:SERVER_IP%%":                          app.GetBindAddress(),
		"%%ENV:EXTRA_BIND_SERVER_IPV6%%":             app.GetBindAddressExtraIPV6(),
		"%%ENV:SERVER_PORT%%":                        app.Port,
		"%%ENV:SVC_NAMESPACE%%":                      app.ClusterGroup.Name,
		"%%ENV:SVC_NAME%%":                           app.Name,
		"%%ENV:SVC_CONF_APP_DNS%%":                   app.GetConfigAppDNS(),
		"%%ENV:SVC_CONF_ENV_PORT_HTTP%%":             "80",
		"%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%": strconv.Itoa(app.ClusterGroup.Conf.MxsMaxinfoPort),
		"%%ENV:SVC_CONF_ENV_PORT_BINLOG%%":           strconv.Itoa(app.ClusterGroup.Conf.MxsBinlogPort),
		"%%ENV:SVC_CONF_ENV_PORT_TELNET%%":           app.Port,
		"%%ENV:SVC_CONF_ENV_PORT_ADMIN%%":            app.Port,
		"%%ENV:SVC_CONF_ENV_USER_ADMIN%%":            app.User,
		"%%ENV:SVC_CONF_ENV_PASSWORD_ADMIN%%":        app.Pass,
		"%%ENV:SVC_CONF_ENV_SPHINX_MEM%%":            app.ClusterGroup.Conf.ProvSphinxMem,
		"%%ENV:SVC_CONF_ENV_SPHINX_MAX_CHILDREN%%":   app.ClusterGroup.Conf.ProvSphinxMaxChildren,
		"%%ENV:SVC_CONF_ENV_VIP_ADDR%%":              app.ClusterGroup.Conf.ProvProxRouteAddr,
		"%%ENV:SVC_CONF_ENV_VIP_NETMASK%%":           app.ClusterGroup.Conf.ProvProxRouteMask,
		"%%ENV:SVC_CONF_ENV_VIP_PORT%%":              app.ClusterGroup.Conf.ProvProxRoutePort,
		"%%ENV:SVC_CONF_ENV_MRM_API_ADDR%%":          app.ClusterGroup.Conf.MonitorAddress + ":" + app.ClusterGroup.Conf.HttpPort,
		"%%ENV:SVC_CONF_ENV_MRM_CLUSTER_NAME%%":      app.ClusterGroup.GetClusterName(),
		"%%ENV:SVC_CONF_ENV_DATADIR%%":               app.GetConfigDatadir(),
		"%%ENV:SVC_CONF_ENV_CONFDIR%%":               app.GetConfigConfigdir(),
	}
}

func (app *App) GetConfigAppDNS() string {
	if app.HasDNS() {
		return `
resolvers dns
 parse-resolv-conf
 resolve_retries       3
 timeout resolve       1s
 timeout retry         1s
 hold other           30s
 hold refused         30s
 hold nx              30s
 hold timeout         30s
 hold valid           10s
 hold obsolete        30s
`
	}

	return ""
}

func (p *App) GetAgent() string {
	return p.Agent
}

func (p *App) GetType() string {
	return p.Type
}

func (p *App) GetHost() string {
	return p.Host
}

func (p *App) GetPort() string {
	return p.Port
}

func (p *App) GetId() string {
	return p.Id
}

func (p *App) GetState() string {
	return p.State
}

func (p *App) GetUser() string {
	return p.User
}

func (p *App) GetPass() string {
	return p.Pass
}

func (p *App) GetFailCount() int {
	return p.FailCount
}

func (p *App) GetPrevState() string {
	return p.PrevState
}

func (p *App) GetOrchestrator() string {
	return p.GetCluster().Conf.ProvOrchestrator
}

func (p *App) GetServiceName() string {
	return p.GetCluster().GetName() + "/svc/" + p.GetName()
}

func (p *App) GetCluster() *Cluster {
	return p.ClusterGroup
}

func (p *App) GetURL() string {
	return p.GetHost() + ":" + p.GetPort()
}

func (p *App) GetSshEnv() string {
	/*
		REPLICATION_MANAGER_USER
		REPLICATION_MANAGER_PASSWORD
		REPLICATION_MANAGER_URL
		REPLICATION_MANAGER_CLUSTER_NAME
		REPLICATION_MANAGER_HOST_NAME
		REPLICATION_MANAGER_HOST_USER
		REPLICATION_MANAGER_HOST_PASSWORD
		REPLICATION_MANAGER_HOST_PORT
		REPLICATION_MANAGER_HOST_TYPE
	*/
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := p.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	return "export REPLICATION_MANAGER_HOST_USER=\"" + p.GetUser() + "\";export REPLICATION_MANAGER_HOST_PASSWORD=\"" + p.GetPass() + "\";export REPLICATION_MANAGER_URL=\"https://" + p.ClusterGroup.Conf.MonitorAddress + ":" + p.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + p.GetHost() + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + p.GetPort() + "\";export REPLICATION_MANAGER_HOST_TYPE=\"" + p.Type + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + p.ClusterGroup.Name + "\"\n"
}

func (app *App) GetOpenSVCDeploymentAppEnv(vartype string) string {
	if app == nil {
		return ""
	}

	// Secrets/configs are provisioned in OpenSVCCreateAppVariableMaps.
	// In service templates, reference the app map as a whole so newly
	// registered keys are available without enumerating each key here.
	switch vartype {
	case config.VariableTypeSecret, config.VariableTypeEnv:
		if app.Name == "" {
			return ""
		}
		return app.Name + "/*"
	default:
		return ""
	}
}

func (app *App) GetExternalFQDN() string {
	for _, route := range app.AppConfig.Deployment.Routes {
		if route.Primary {
			if route.CName != "" {
				return route.CName
			}
			// Port route without cname: return a *:sourcePort display string.
			if route.Mode == "port" && route.SourcePort != "" {
				name := route.Name
				if name == "" {
					name = "port-route"
				}
				return name + " (*:" + route.SourcePort + ")"
			}
			return ""
		}
	}
	return ""
}

func (app *App) GetAppAgents() []string {
	agents := make([]string, 0)
	if app.AppConfig == nil {
		return agents
	}

	stragents := strings.Split(app.AppConfig.ProvAppAgents, ",")
	for _, agent := range stragents {
		agent = strings.TrimSpace(agent)
		if agent != "" {
			agents = append(agents, agent)
		}
	}

	return agents
}

func (app *App) GetGitClone(name string) (*config.GitClone, int) {
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return nil, -1
	}

	if appcnf.Deployment.Storages.GitClones == nil {
		return nil, -1
	}

	for i, gc := range appcnf.Deployment.Storages.GitClones {
		if gc.Name == name {
			return gc, i
		}
	}

	return nil, -1
}

func (app *App) GetAppVolume(name string) (*config.Volume, int) {
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return nil, -1
	}

	if appcnf.Deployment.Storages.Volumes == nil {
		return nil, -1
	}

	for i, ld := range appcnf.Deployment.Storages.Volumes {
		if ld.Name == name {
			return ld, i
		}
	}

	return nil, -1
}

// GetAppVolumeName returns the runtime and provisioned volume name for pool.
// For V1/unflagged content, each OpenSVC pool has exactly one saved
// deployment.storages.volumes row (see config.CanonicalizeAppVolumesTOML),
// and that row's Name is the actual volume identity; pool is only used to
// look it up, not to derive it. V2 content may have multiple intentional
// rows for the same pool (see Deployment.GetVolumeByPool); this lookup
// returns only the first one, so callers that need every row for a pool
// (e.g. GetVolumes) must iterate Storages.Volumes directly. If no saved row
// exists yet (content that hasn't been through canonicalization), fall back
// to the historical {name}-<pool> / <app>-<pool> convention.
func (app *App) GetAppVolumeName(pool string, resolved bool) string {
	if appcnf := app.GetAppConfig(); appcnf != nil {
		if appcnf.Deployment != nil {
			if vol := appcnf.Deployment.GetVolumeByPool(pool); vol != nil && vol.Name != "" {
				return vol.Name
			}
		}
	}
	if resolved {
		return fmt.Sprintf("%s-%s", app.Name, pool)
	}
	return fmt.Sprintf("{name}-%s", pool)
}

func (app *App) GetS3Mount(name string) (*config.S3Mount, int) {
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return nil, -1
	}

	if appcnf.Deployment.Storages.S3Mounts == nil {
		return nil, -1
	}

	for i, s3m := range appcnf.Deployment.Storages.S3Mounts {
		if s3m.Name == name {
			return s3m, i
		}
	}

	return nil, -1
}

func (app *App) GetVolumes(resolved bool) []string {
	volumes := make([]string, 0)
	distinctVolumes := make(map[string]bool)

	if app.AppConfig == nil {
		return volumes
	}

	// Each saved row's Name is its own identity (enforced unique by
	// InsertVolume), so use it directly rather than re-deriving it via
	// GetAppVolumeName, which is keyed by pool and would collapse multiple
	// V2 rows sharing a pool down to the first row's name.
	for _, v := range app.AppConfig.Deployment.Storages.Volumes {
		if v.Name == "" {
			continue
		}
		if _, exists := distinctVolumes[v.Name]; !exists {
			volumes = append(volumes, v.Name)
			distinctVolumes[v.Name] = true
		}
	}

	return volumes
}

func (app *App) GetS3Endpoint() string {
	return app.GetHost() + ":" + app.GetPort()
}
