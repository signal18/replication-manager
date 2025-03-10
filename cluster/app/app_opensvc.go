package app

func (app *App) OpenSVCGetAppDefaultSection() map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = app.Agent
	if app.GetAppDiskPool() == "zpool" && app.GetAppAgentsFailover() != "" {
		svcdefault["nodes"] = app.Agent + "," + app.GetAppAgentsFailover()
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = app.Agent
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = app.Cluster.GetConf().ProvCodeApp
	if app.GetAppServiceType() == "docker" {
		if app.Cluster.GetConf().ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if app.GetAppDiskType() != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if app.GetAppDiskPool() == "zpool" {
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
