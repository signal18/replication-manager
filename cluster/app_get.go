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
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) GetAppFromName(name string) AppInterface {
	for _, app := range cluster.Apps {
		if app.GetId() == name {
			return app
		}
	}
	return nil
}

func (app *App) GetJanitorWeight() string {
	return app.Weight
}

func (app *App) GetAppConfig() string {
	cluster := app.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Aoo Config generation "+app.Datadir+"/config.tar.gz")
	err := cluster.Configurator.GenerateAppConfig(app.Datadir, cluster.Conf.WorkingDir+"/"+cluster.Name, app.GetEnv(), cluster.RepMgrVersion)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, " "+app.Datadir+"/config.tar.gz error: %s", err)
	}
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
	if user, ok := app.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	return "export REPLICATION_MANAGER_URL=\"https://" + app.ClusterGroup.Conf.MonitorAddress + ":" + app.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + app.GetHost() + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + app.GetPort() + "\";export REPLICATION_MANAGER_HOST_TYPE=\"" + app.Type + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + app.ClusterGroup.Name + "\"\n"
}
