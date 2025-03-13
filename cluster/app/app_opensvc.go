package app

import "slices"

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

func (app *App) OpenSVCGetAppVolumeDataSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}"
	svcvol["size"] = "{env.size}"
	svcvol["pool"] = app.GetAppVolumeData()
	svcvol["shared"] = "true"
	svcvol["directories"] = "/data /etc /log"

	return svcvol
}

func (app *App) OpenSVCGetInitContainerSection() map[string]string {
	svccontainer := make(map[string]string)
	if app.Cluster.GetConf().ProvType == "docker" || app.Cluster.GetConf().ProvType == "podman" {
		svccontainer["detach"] = "false"
		svccontainer["type"] = "docker"
		svccontainer["image"] = "alpine"
		svccontainer["netns"] = "container#00"
		svccontainer["rm"] = "true"
		svccontainer["start_timeout"] = "30s"
		svccontainer["optional"] = "true"
		if app.Cluster.GetConf().ProvDiskType != "volume" {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {env.base_dir}:/bootstrap"
		} else {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}:/bootstrap"
		}
		svccontainer["command"] = "-c 'wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/opensvc/bootstrap | sh'"
	}
	svccontainer["entrypoint"] = "/bin/sh"
	svccontainer["secrets_environment"] = "env/REPLICATION_MANAGER_PASSWORD"
	svccontainer["configs_environment"] = "env/REPLICATION_MANAGER_USER env/REPLICATION_MANAGER_URL"
	svccontainer["environment"] = "REPLICATION_MANAGER_CLUSTER_NAME={namespace} REPLICATION_MANAGER_HOST_NAME={fqdn} REPLICATION_MANAGER_HOST_PORT=" + app.GetPort()
	//	svccontainer["# Debug"] = ""
	//	svccontainer["# interactive"] = "true"
	//	svccontainer["# tty"] = "true"
	return svccontainer
}

func (app *App) OpenSVCGetAppContainerSection(section string) map[string]string {
	svccontainer := make(map[string]string)
	if slices.Contains([]string{"docker", "podman", "oci"}, app.GetAppServiceType()) {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#00"
		svccontainer["image"] = app.GetAppDockerImg(section)
		svccontainer["rm"] = "true"
		svccontainer["type"] = app.GetAppServiceType()
		svccontainer["run_args"] = "--sysctl net.ipv4.ip_unprivileged_port_start=0 "
		if app.GetAppDiskType() != "volume" {
			svccontainer["run_args"] = svccontainer["run_args"] + app.GetAppDockerDiskMapping(section) + ` ` + app.GetAppDockerRunArgs(section)
		} else {
			svccontainer["run_args"] = svccontainer["run_args"] + app.GetAppDockerRunArgs(section)
			svccontainer["volume_mounts"] = app.GetAppDockerDiskMapping(section)
		}
	}

	return svccontainer
}

func (app *App) OpenSVCGetAllContainerSections(oldmap map[string]map[string]string) map[string]map[string]string {
	for _, dep := range app.Deployments {
		oldmap[dep.Name] = app.OpenSVCGetAppContainerSection(dep.Name)
	}

	return oldmap
}
