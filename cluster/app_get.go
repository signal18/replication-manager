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
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

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

func (p *App) GetGitCloneFromVolumeDir(volumeDir string) *config.GitClone {
	appcnf := p.GetAppConfig()
	if appcnf == nil {
		return nil
	}

	if appcnf.Deployment.GitClones == nil {
		return nil
	}

	for _, gc := range appcnf.Deployment.GitClones {
		if gc.VolumeDir == volumeDir {
			return &gc
		}
	}

	return nil
}

func (app *App) GetOpenSVCDeploymentGitEnv(gc config.GitClone, vartype string) string {
	prefix := "GIT_CODE"
	if gc.VolumeDir == "etc" {
		return "GIT_CONFIG"
	}

	result := make([]string, 0)

	replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_")
	prefix = prefix + "_" + strings.ToUpper(replacer.Replace(gc.Dest))

	for _, s := range app.GetAppConfig().Deployment.Variables {
		if s.Type == vartype && strings.HasPrefix(s.Name, prefix) {
			result = append(result, app.Name+"/"+s.Name)
		}
	}

	return strings.Join(result, " ")
}

func (app *App) GetOpenSVCDeplopymentGitPrefix(gc config.GitClone, envname string) string {
	prefix := "GIT_CODE"
	if gc.VolumeDir == "etc" {
		return "GIT_CONFIG"
	}

	replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_")
	return prefix + "_" + strings.ToUpper(replacer.Replace(gc.Dest)) + "_" + envname
}
