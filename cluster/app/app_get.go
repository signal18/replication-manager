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
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

func (app *App) GetSectionConfig(section string) (config.AppSectionConfig, bool) {
	appsection, ok := app.DeployConfigMap[section]
	return appsection, ok
}

func (app *App) GetAppDockerImg(section string) string {
	appsection, ok := app.GetSectionConfig(section)
	if !ok {
		return ""
	}
	return appsection.DockerImg
}

func (app *App) GetAppDockerRunArgs(section string) string {
	appsection, ok := app.GetSectionConfig(section)
	if !ok {
		return ""
	}
	return appsection.DockerRunArgs
}

func (app *App) GetAppDockerDiskArgs(section string) string {
	appsection, ok := app.GetSectionConfig(section)
	if !ok {
		return ""
	}
	return appsection.DockerDiskArgs
}

func (app *App) GetAppDockerVolumeArgs(section string) string {
	appsection, ok := app.GetSectionConfig(section)
	if !ok {
		return ""
	}
	return appsection.DockerVolumeArgs
}

func (app *App) GetJanitorWeight() string {
	return app.Weight
}

func (app *App) GetAppDiskPool() string {
	return app.AppConfig.ProvAppDiskPool
}

func (app *App) GetAppDiskType() string {
	return app.AppConfig.ProvAppDiskType
}

func (app *App) GetAppAgents() string {
	return app.AppConfig.ProvAppAgents
}

func (app *App) GetAppAgentsFailover() string {
	return app.AppConfig.ProvAppAgentsFailover
}

func (app *App) GetAppGateway() string {
	return app.AppConfig.ProvAppGateway
}

func (app *App) GetAppNetMask() string {
	return app.AppConfig.ProvAppNetmask
}

func (app *App) GetAppRouteAddr() string {
	return app.AppConfig.ProvAppRouteAddr
}

func (app *App) GetAppRoutePort() string {
	return app.AppConfig.ProvAppRoutePort
}

func (app *App) GetAppRouteMask() string {
	return app.AppConfig.ProvAppRouteMask
}

func (app *App) OpenSVCSetRouteAddr(addr string) {
	app.AppConfig.ProvAppRouteAddr = addr
}

func (app *App) OpenSVCSetRoutePort(port string) {
	app.AppConfig.ProvAppRoutePort = port
}

func (app *App) GetAppDiskSize() string {
	return app.AppConfig.ProvAppDiskSize
}

func (app *App) GetAppServiceType() string {
	return app.AppConfig.ProvAppType
}

func (app *App) GetAppCpuCores() string {
	return app.AppConfig.ProvAppCpuCores
}

func (app *App) GetAppMemory() string {
	return app.AppConfig.ProvAppMemory
}

func (app *App) GetAppVolumeData() string {
	return app.AppConfig.ProvAppVolumeData
}

func (app *App) GetAppConfig() string {
	return ""
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

func (app *App) GetDatadir() string {
	return app.Datadir
}

func (app *App) GetName() string {
	return app.Name
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

func (p *App) GetFailCount() int {
	return p.FailCount
}

func (p *App) GetPrevState() string {
	return p.PrevState
}

func (p *App) GetOrchestrator() string {
	return p.Cluster.GetConf().ProvOrchestrator
}

func (p *App) GetServiceName() string {
	return p.Cluster.GetName() + "/svc/" + p.GetName()
}

func (p *App) GetURL() string {
	return p.GetHost() + ":" + p.GetPort()
}

func (app *App) GetSshEnv() string {
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
	if user, ok := app.Cluster.GetAPIUserByUsername(adminuser); ok {
		adminpassword = user.Password
	}
	return "export REPLICATION_MANAGER_URL=\"https://" + app.Cluster.GetConf().MonitorAddress + ":" + app.Cluster.GetConf().APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + app.GetHost() + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + app.GetPort() + "\";export REPLICATION_MANAGER_HOST_TYPE=\"" + app.Type + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + app.Cluster.GetName() + "\"\n"
}

func (app *App) GetEnv() map[string]string {
	return app.GetBaseEnv()
}

func (app *App) GetBaseEnv() map[string]string {
	return map[string]string{}
}

func (app *App) GetUser() string {
	return app.User
}

func (app *App) GetPass() string {
	return app.Pass
}
