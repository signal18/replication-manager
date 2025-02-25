// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/pflag"
)

type AppNginx struct {
	App
}

func NewAppNginx(placement int, cluster *Cluster, App1Host string) *AppNginx {
	app := new(AppNginx)

	app.Type = config.ConstAppNginx
	app.Host, app.Port = misc.SplitHostPort(App1Host)

	if app.Name == "" {
		app.Name = app.Host
	}
	// Source name will equal to cluster name

	return app
}

func (server *AppNginx) OpenSVCGetAppDiskPool() string {
	return server.ClusterGroup.Conf.ProvAppNginxDiskPool
}

func (server *AppNginx) OpenSVCGetAppDiskType() string {
	return server.ClusterGroup.Conf.ProvAppNginxDiskType
}

func (server *AppNginx) OpenSVCGetAppAgents() string {
	return server.ClusterGroup.Conf.ProvAppNginxAgents
}

func (server *AppNginx) OpenSVCGetAppAgentsFailover() string {
	return server.ClusterGroup.Conf.ProvAppNginxAgentsFailover
}

func (server *AppNginx) OpenSVCGetAppGateway() string {
	return server.ClusterGroup.Conf.ProvAppNginxGateway
}

func (server *AppNginx) OpenSVCGetAppNetMask() string {
	switch server.Type {
	case config.ConstAppNginx:
		return server.ClusterGroup.Conf.ProvAppNginxNetmask
	default:
		return server.ClusterGroup.Conf.ProvAppNetmask
	}
}

func (server *AppNginx) OpenSVCGetAppRouteAddr() string {
	switch server.Type {
	case config.ConstAppNginx:
		return server.ClusterGroup.Conf.ProvAppNginxRouteAddr
	default:
		return server.ClusterGroup.Conf.ProvAppRouteAddr
	}
}

func (server *AppNginx) OpenSVCGetAppDefaultSection() map[string]string {
	cluster := server.ClusterGroup
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = server.Agent
	if server.OpenSVCGetAppDiskPool() == "zpool" && server.OpenSVCGetAppAgentsFailover() != "" {
		svcdefault["nodes"] = server.Agent + "," + server.OpenSVCGetAppAgentsFailover()
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = server.Agent
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = cluster.Conf.ProvCodeApp
	if cluster.Conf.ProvAppNginxType == "docker" {
		if cluster.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if server.OpenSVCGetAppDiskType() != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if server.OpenSVCGetAppDiskPool() == "zpool" {
				svcdefault["docker_daemon_args"] = " --storage-driver=zfs"
			} else {
				svcdefault["docker_daemon_args"] = " --storage-driver=overlay"
			}
		} else {
			svcdefault["docker_daemon_private"] = "false"
		}

	}
	return svcdefault
}

func (app *AppNginx) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.StringVar(&conf.ProvAppNginxAgents, "prov-app-nginx-agents", "", "Comma seperated list of agents for micro services provisionning")
	flags.StringVar(&conf.ProvAppNginxDiskSize, "prov-app-nginx-disk-size", "20", "Disk in g for micro service VM")
	flags.StringVar(&conf.ProvAppNginxCpuCores, "prov-app-nginx-cpu-cores", "1", "Number of cpu cores for the micro service VM")
	flags.StringVar(&conf.ProvAppNginxMemory, "prov-app-nginx-memory", "256", "Memory in M for micro service VM")
	flags.StringVar(&conf.ProvAppNginxVolumeData, "prov-app-nginx-volume-data", "default", "Volume name of the data files")
	flags.StringVar(&conf.ProvAppNginxDockerRunArgs, "prov-app-nginx-docker-run-args", "--ulimit nofile=262144:262144 --sysctl net.ipv4.tcp_tw_reuse=1 --sysctl net.core.somaxconn=1024  --sysctl net.ipv4.tcp_fin_timeout=10", "Additional docker run arguments for app")
	flags.StringVar(&conf.ProvAppNginxDockerImg, "prov-app-nginx-docker-img", "signal18/php8:latest", "Docker image for app")
	flags.StringVar(&conf.AppNginxHosts, "app-nginx-hosts", "app1:80,app2:80", "")
	flags.StringVar(&conf.AppNginxRunCommand, "app-nginx-run-command", "", "Container run command")
	flags.StringVar(&conf.AppNginxConfigGitCloneUrl, "app-nginx-config-git-clone-url", "", "Git clone url to fetch config")
	flags.StringVar(&conf.AppNginxConfigGitUser, "app-nginx-config-git-user", "", "Git user to to fetch config")
	flags.StringVar(&conf.AppNginxConfigGitPassword, "app-nginx-config-git-password", "", "Git password to to fetch config")
	flags.StringVar(&conf.AppNginxConfigGitBranch, "app-nginx-config-git-branch", "master", "Git branch to to fetch config")
	flags.StringVar(&conf.AppNginxConfigSecretVariables, "app-nginx-config-secret-variables", "", "List of key:value,key:value")
	flags.StringVar(&conf.AppNginxConfigEnvVariables, "app-nginx-config-env-variables", "", "List of key:value,key:value")
	flags.StringVar(&conf.AppNginxConfigVolumes, "app-nginx-config-volumes", "", "List of key:value,key:value")
	flags.StringVar(&conf.AppNginxDataGitCloneUrl, "app-nginx-data-git-clone-url", "", "Git clone url to fetch config")
	flags.StringVar(&conf.AppNginxDataGitUser, "app-nginx-data-git-user", "", "Git user to to fetch data")
	flags.StringVar(&conf.AppNginxDataGitPassword, "app-nginx-data-git-password", "", "Git password to to fetch data")
	flags.StringVar(&conf.AppNginxDataGitBranch, "app-nginx-data-git-branch", "master", "Git branch to to fetch data")
	flags.StringVar(&conf.AppNginxDataVolumes, "app-nginx-data-volumes", "", "List of key:value,key:value")
	flags.StringVar(&conf.AppNginxLogVolumes, "app-nginx-log-volumes", "", "List of key:value,key:value")
}

func (app *AppNginx) Refresh() error {
	return nil
}
