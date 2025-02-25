package app

func (server *App) OpenSVCGetAppDiskPool() string {
	return server.AppConfig.ProvAppDiskPool
}

func (server *App) OpenSVCGetAppDiskType() string {
	return server.AppConfig.ProvAppDiskType
}

func (server *App) OpenSVCGetAppAgents() string {
	return server.AppConfig.ProvAppAgents
}

func (server *App) OpenSVCGetAppAgentsFailover() string {
	return server.AppConfig.ProvAppAgentsFailover
}

func (server *App) OpenSVCGetAppGateway() string {
	return server.AppConfig.ProvAppGateway
}

func (server *App) OpenSVCGetAppNetMask() string {
	return server.AppConfig.ProvAppNetmask
}

func (server *App) OpenSVCGetAppRouteAddr() string {
	return server.AppConfig.ProvAppRouteAddr
}

func (server *App) OpenSVCGetAppRoutePort() string {
	return server.AppConfig.ProvAppRoutePort
}

func (server *App) OpenSVCGetAppRouteMask() string {
	return server.AppConfig.ProvAppRouteMask
}

func (server *App) OpenSVCSetRouteAddr(addr string) {
	server.AppConfig.ProvAppRouteAddr = addr
}

func (server *App) OpenSVCSetRoutePort(port string) {
	server.AppConfig.ProvAppRoutePort = port
}

func (server *App) OpenSVCGetAppDiskSize() string {
	return server.AppConfig.ProvAppDiskSize
}

func (server *App) OpenSVCGetAppServiceType() string {
	return server.AppConfig.ProvAppType
}

func (server *App) OpenSVCGetAppCpuCores() string {
	return server.AppConfig.ProvAppCpuCores
}

func (server *App) OpenSVCGetAppMemory() string {
	return server.AppConfig.ProvAppMemory
}

func (server *App) OpenSVCGetAppVolumeData() string {
	return server.AppConfig.ProvAppVolumeData
}

func (server *App) OpenSVCGetAppDockerImg() string {
	return server.AppConfig.ProvAppDockerImg
}

func (server *App) OpenSVCGetAppDockerRunArgs() string {
	return server.AppConfig.ProvAppDockerRunArgs
}

func (server *App) OpenSVCGetAppDefaultSection() map[string]string {
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
	svcdefault["app"] = server.ClusterConfig.ProvCodeApp
	if server.OpenSVCGetAppServiceType() == "docker" {
		if server.ClusterConfig.ProvDockerDaemonPrivate {
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
