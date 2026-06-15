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
	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) GetAppsSubstitutionJSon(app *App) (string, error) {

	o := &sheriff.Options{Groups: []string{"apps"}}
	data, err := sheriff.Marshal(o, cluster)
	if err != nil {
		return "", err
	}
	result, err2 := json.Marshal(data)
	if err2 != nil {
		return string(result), err2
	}

	// Add app specific data
	child, err3 := sheriff.Marshal(o, app)
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

func (app *App) GetInitContainer(collector opensvc.Collector) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#0002]
detach = false
type = docker
image = busybox
netns = container#01
start_timeout = 30s
rm = true
volume_mounts = /etc/localtime:/etc/localtime:ro {env.base_dir}/pod01:/data
command = sh -c 'wget -qO- http://{env.mrm_api_addr}/api/clusters/{env.mrm_cluster_name}/servers/{env.ip_pod01}/{env.port_pod01}/config|tar xzvf - -C /data'
optional=true

 `
	}
	return vm
}

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
		if vol := appcnf.Deployment.GetVolumeByPool(pool); vol != nil && vol.Name != "" {
			return vol.Name
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
