package app

func (app *App) OpenSVCGetAppDiskPool() string {
	return app.AppConfig.ProvAppDiskPool
}

func (app *App) OpenSVCGetAppDiskType() string {
	return app.AppConfig.ProvAppDiskType
}

func (app *App) OpenSVCGetAppAgents() string {
	return app.AppConfig.ProvAppAgents
}

func (app *App) OpenSVCGetAppAgentsFailover() string {
	return app.AppConfig.ProvAppAgentsFailover
}

func (app *App) OpenSVCGetAppGateway() string {
	return app.AppConfig.ProvAppGateway
}

func (app *App) OpenSVCGetAppNetMask() string {
	return app.AppConfig.ProvAppNetmask
}

func (app *App) OpenSVCGetAppRouteAddr() string {
	return app.AppConfig.ProvAppRouteAddr
}

func (app *App) OpenSVCGetAppRoutePort() string {
	return app.AppConfig.ProvAppRoutePort
}

func (app *App) OpenSVCGetAppRouteMask() string {
	return app.AppConfig.ProvAppRouteMask
}

func (app *App) OpenSVCSetRouteAddr(addr string) {
	app.AppConfig.ProvAppRouteAddr = addr
}

func (app *App) OpenSVCSetRoutePort(port string) {
	app.AppConfig.ProvAppRoutePort = port
}

func (app *App) OpenSVCGetAppDiskSize() string {
	return app.AppConfig.ProvAppDiskSize
}

func (app *App) OpenSVCGetAppServiceType() string {
	return app.AppConfig.ProvAppType
}

func (app *App) OpenSVCGetAppCpuCores() string {
	return app.AppConfig.ProvAppCpuCores
}

func (app *App) OpenSVCGetAppMemory() string {
	return app.AppConfig.ProvAppMemory
}

func (app *App) OpenSVCGetAppVolumeData() string {
	return app.AppConfig.ProvAppVolumeData
}

func (app *App) OpenSVCGetAppDockerImg() string {
	return app.AppConfig.ProvAppDockerImg
}

func (app *App) OpenSVCGetAppDockerRunArgs() string {
	return app.AppConfig.ProvAppDockerRunArgs
}

func (app *App) OpenSVCGetAppDefaultSection() map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = app.Agent
	if app.OpenSVCGetAppDiskPool() == "zpool" && app.OpenSVCGetAppAgentsFailover() != "" {
		svcdefault["nodes"] = app.Agent + "," + app.OpenSVCGetAppAgentsFailover()
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = app.Agent
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = app.Cluster.GetConf().ProvCodeApp
	if app.OpenSVCGetAppServiceType() == "docker" {
		if app.Cluster.GetConf().ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if app.OpenSVCGetAppDiskType() != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if app.OpenSVCGetAppDiskPool() == "zpool" {
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
