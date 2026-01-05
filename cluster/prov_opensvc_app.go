// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

func (cluster *Cluster) OpenSVCUnprovisionAppService(app *App) {
	opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()
	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		err := opensvc.PurgeServiceV2(cluster.GetName(), app.GetServiceName(), app.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app service:  %s ", err)
			cluster.errorChan <- err
		}

		// Unprovision volumes
		for _, volumename := range app.GetVolumes(true) {
			err = opensvc.PurgeServiceV2(cluster.Name, cluster.Name+"/vol/"+volumename, app.GetAgent())
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app volume:  %s ", err)
				cluster.errorChan <- err
			}
		}
	} else {
		node, _ := cluster.OpenSVCFoundAppAgent(app)
		for _, svc := range node.Svc {
			if app.GetServiceName() == svc.Svc_name {
				idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't unprovision app %s, %s", app.GetId(), err)
				}
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) OpenSVCStopAppService(app *App, node string) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + app.GetName())
		if err != nil {
			return err
		}

		if node != "" {
			agents, err := cluster.FoundAllAppAgents(app)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not find app agents:  %s ", err)
				return err
			}
			for _, agent := range agents {
				if strings.ToUpper(node) == "ALL" || strings.EqualFold(node, agent.Node_name) {
					_, err := svc.StopService(agent.Node_id, service.Svc_id)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app in %s:  %s ", agent.Node_name, err)
						return err
					}
				}
			}
		} else {
			agent, err := cluster.OpenSVCFoundAppAgent(app)
			if err != nil {
				return err
			}
			svc.StopService(agent.Node_id, service.Svc_id)
		}
	} else {
		if strings.ToUpper(node) == "ALL" {
			err := svc.StopServiceV2(cluster.Name, app.GetServiceName(), "*")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
				return err
			}
		} else {
			agentName := app.GetAgent()
			if node != "" {
				agentName = node
			}
			err := svc.StopServiceV2(cluster.Name, app.GetServiceName(), agentName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
				return err
			}
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartAppService(app *App, node string) error {
	svc := cluster.OpenSVCConnect()
	agent := app.GetAgent()
	if strings.ToUpper(node) == "ALL" {
		nodes := strings.Split(app.GetAppConfig().ProvAppAgents, ",")
		for _, n := range nodes {
			err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), n)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start app:  %s ", err)
				return err
			}
		}
	} else {
		if node != "" {
			agent = node
		}
		err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start app:  %s ", err)
			return err
		}
		return nil
	}
	return nil
}

func (cluster *Cluster) OpenSVCRestartAppService(app *App, node string, rid string) error {
	svc := cluster.OpenSVCConnect()
	agent := app.GetAgent()

	if strings.ToUpper(node) == "ALL" || node == "*" {
		err := svc.RestartServiceV2(cluster.Name, app.GetServiceName(), "*", rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart app:  %s ", err)
			return err
		}
	} else {
		if node != "" {
			agent = node
		}
		err := svc.RestartServiceV2(cluster.Name, app.GetServiceName(), agent, rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart app:  %s ", err)
			return err
		}
		return nil
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Provisioning app %s on OpenSVC", app.GetId())
	agent, err := cluster.OpenSVCFoundAppAgent(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not find app agent:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Found app agent %s. Creating maps", agent.Node_name)

	err = cluster.OpenSVCCreateAppVariableMaps(agent.Node_name, app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create maps:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Getting app template %s for OpenSVC", app.GetId())

	res, err := cluster.OpenSVCGetAppTemplateV2(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not get app template:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Creating app template %s on OpenSVC", app.GetId())

	templatefile := filepath.Join(app.Datadir, "opensvc_template.json")
	_ = os.WriteFile(templatefile, res, 0644)

	app.TemplateMD5Prov = misc.GetMD5HashFromBytes(res)
	app.TemplateMD5 = app.TemplateMD5Prov

	err = svc.CreateTemplateV2(cluster.Name, app.ServiceName, app.Agent, res)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create app template:  %s ", err)
		cluster.errorChan <- err
		return err
	}
	err = cluster.OpenSVCProvisionRoute(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create app route:  %s ", err)
		cluster.errorChan <- err
		return err
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateV2(app *App) ([]byte, error) {
	// Check if app image not empty
	if app.AppConfig.ProvAppDockerImg == "" {
		return []byte(""), errors.New("App image is not defined in app config")
	}

	deployment := app.AppConfig.Deployment

	containernum := 1
	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = cluster.OpenSVCGetAppDefaultSection(app)
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	svcsection = cluster.OpenSVCGetAppVolumeSections(svcsection, app)
	svcsection[fmt.Sprintf("container#%02d", containernum)] = cluster.OpenSVCGetNamespaceContainerSection()
	var err error
	for _, gc := range deployment.Storages.GitClones {
		if gc.Volume == nil {
			// If the volume is not set, try to resolve it
			if vol, err := deployment.GetVolumeByName(gc.VolumeName); err == nil {
				gc.Volume = vol
			} else {
				return []byte(""), fmt.Errorf("Git clone volume %s not found in deployment", gc.VolumeName)
			}
		}
		containernum++
		sectionName := fmt.Sprintf("container#%02dinit%s", containernum, gc.Name)
		svcsection[sectionName] = cluster.OpenSVCGetAppGitInitContainerSection(app, gc)
	}

	for _, s3m := range deployment.Storages.S3Mounts {
		if s3m.Volume == nil {
			// If the volume is not set, try to resolve it
			if vol, err := deployment.GetVolumeByName(s3m.VolumeName); err == nil {
				s3m.Volume = vol
			} else {
				return []byte(""), fmt.Errorf("S3 mount volume %s not found in deployment", s3m.VolumeName)
			}
		}

		// Only allow internal S3 mounts for now
		s3_app, appidx := cluster.GetAppByURL(s3m.Endpoint)
		if s3_app == nil || appidx < 0 {
			return []byte(""), fmt.Errorf("S3 mount app %s not found in cluster %s", s3m.Endpoint, cluster.Name)
		}

		containernum++
		sectionName := fmt.Sprintf("container#%02dinit%s", containernum, s3m.Name)
		svcsection[sectionName] = cluster.OpenSVCGetAppS3MountContainerSection(app, s3m)
	}
	svcsection["container#app"] = cluster.OpenSVCGetAppContainerSection(app)
	svcsection["env"] = cluster.OpenSVCGetAppEnvSection(app)

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return []byte(""), err
	}

	return svcsectionJson, nil

}

func (cluster *Cluster) OpenSVCGetAppVolumeSections(basemap map[string]map[string]string, app *App) map[string]map[string]string {

	appcnf := app.AppConfig
	if appcnf == nil {
		return basemap
	}

	deployment := appcnf.Deployment
	if len(deployment.Storages.Volumes) == 0 {
		return basemap
	}

	deployment.Paths.Sort()
	volumemap := deployment.Storages.Volumes.GroupByPool()
	pathmap := deployment.Paths.GetVolumeDirs()
	pools := cluster.Conf.GetAppVolumePools("")

	seq := 1
	for pool, volumes := range volumemap {
		svcvol := make(map[string]string)
		svcvol["name"] = app.GetAppVolumeName(pool, false)
		svcvol["pool"] = pool
		svcvol["size"] = "{env.size}"

		if poolConfig, ok := pools[pool]; ok {
			if poolConfig.Type == "shared" {
				svcvol["shared"] = "true"
			}
		}

		// Use set to avoid duplicate directories
		directorySet := make(map[string]struct{})

		for volName, baseDir := range volumes {
			if baseDir != "" {
				directorySet[baseDir] = struct{}{}
			}
			if mappedPaths, ok := pathmap[volName]; ok {
				for _, path := range mappedPaths {
					// if path is not end with "/", get parent directory
					if !strings.HasSuffix(path, "/") {
						path = filepath.Dir(path) + "/"
					}
					directorySet[filepath.Join(path)] = struct{}{}
				}
			}
		}

		// Join unique directory list
		dirs := make([]string, 0, len(directorySet))
		for dir := range directorySet {
			dirs = append(dirs, dir)
		}

		sort.Strings(dirs) // Optional: consistent order
		svcvol["directories"] = strings.Join(dirs, " ")
		basemap["volume#"+strconv.Itoa(seq)] = svcvol
		seq++
	}

	return basemap
}

func (cluster *Cluster) OpenSVCFoundAppAgent(app *App) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	svc.ProvAppAgents = cluster.GetAppAgents(app.AppConfig)

	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvAppAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		return agent, errors.New("Indice not found in apps agent list")
	}
	for i, srv := range cluster.Apps {
		if srv.GetId() == app.GetId() {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in apps agent list")
}

func (cluster *Cluster) FoundAllAppAgents(app *App) ([]opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	svc.ProvAppAgents = cluster.GetAppAgents(app.AppConfig)

	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	var clusteragents []opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvAppAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		return nil, errors.New("Indice not found in apps agent list")
	}

	return clusteragents, nil
}

func (cluster *Cluster) OpenSVCGetAppEnvSection(app *App) map[string]string {
	domain := cluster.GetDomain()

	appcnf := app.AppConfig
	svcenv := make(map[string]string)

	// Cluster values section
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	// App section
	// FQDN: Fully Qualified Domain Name
	fqdn := appcnf.AppHost
	if !strings.Contains(appcnf.AppHost, domain) {
		fqdn = fqdn + "." + domain
	}

	svcenv["nodes"] = strings.ReplaceAll(cluster.GetAppAgents(appcnf), ",", " ")
	svcenv["size"] = cluster.GetAppDisk(appcnf) + "g"
	svcenv["app_img"] = appcnf.ProvAppDockerImg
	svcenv["app_host"] = appcnf.AppHost
	svcenv["app_port"] = appcnf.AppPort
	svcenv["fqdn"] = fqdn

	svcenv["ip_pod01"] = app.GetHost()
	svcenv["port_pod01"] = app.GetPort()
	svcenv["port_telnet"] = app.GetPort()
	svcenv["port_admin"] = app.GetPort()
	svcenv["port_http"] = "80"
	svcenv["user_admin"] = app.User

	return svcenv
}

func (cluster *Cluster) OpenSVCGetAppDefaultSection(app *App) map[string]string {
	appcnf := app.AppConfig
	svcdefault := make(map[string]string)

	svcdefault["nodes"] = strings.ReplaceAll(cluster.GetAppAgents(appcnf), ",", " ")
	nodes := strings.Split(svcdefault["nodes"], " ")

	if appcnf.ProvAppHATopology == "flex" {
		svcdefault["topology"] = "flex"
		svcdefault["flex_primary"] = nodes[0]
		svcdefault["flex_target"] = strconv.Itoa(len(nodes))
	} else {
		svcdefault["orchestrate"] = "ha"
		svcdefault["rollback"] = "false"
	}

	svcdefault["app"] = cluster.Conf.ProvCodeApp
	return svcdefault
}

func (cluster *Cluster) OpenSVCGetAppContainerSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["hostname"] = "{fqdn}"
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "{env.app_img}"
		svccontainer["type"] = cluster.Conf.ProvType
		if app.AppConfig.ProvAppHATopology == "failover" {
			svccontainer["shared"] = "true"
		}

		if app.AppConfig.ProvAppDockerCmd != "" {
			svccontainer["run_command"], _ = app.ClusterGroup.ParseAppTemplate(app.AppConfig.ProvAppDockerCmd, app.AppClusterSubstitute)
		}

		if cluster.Conf.ProvDBDockerRunArgsLimit {
			svccontainer["run_args"] = svccontainer["run_args"] + " --memory=" + cluster.GetAppMemory(app.AppConfig) + "m --memory-swap=" + cluster.GetAppMemory(app.AppConfig) + "m --cpus=" + cluster.GetAppCores(app.AppConfig) + ".0"
		}

		svccontainer["volume_mounts"] = cluster.GetOpenSVCDeploymentPathMapping(app)
		svccontainer["configs_environment"] = app.GetOpenSVCDeploymentAppEnv("env")
		svccontainer["secrets_environment"] = app.GetOpenSVCDeploymentAppEnv("secret")
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppGitInitDefaultSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["detach"] = "false"
		svccontainer["type"] = "docker"
		svccontainer["image"] = "alpine/git"
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["start_timeout"] = "300s"
		svccontainer["optional"] = "true"
		svccontainer["entrypoint"] = "/bin/sh"
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppGitInitContainerSection(app *App, gc *config.GitClone) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer = cluster.OpenSVCGetAppGitInitDefaultSection(app)
		svccontainer["volume_mounts"] = fmt.Sprintf("/etc/localtime:/etc/localtime:ro %s:/bootstrap", app.GetAppVolumeName(gc.GetSourcePoolName(), false))
		svccontainer["secrets_environment"] = gc.GetVariableKeys(app.Name, "secret")
		svccontainer["configs_environment"] = gc.GetVariableKeys(app.Name, "env")
		dirname := filepath.Join("/bootstrap", gc.GetSourcePath())

		prefix := gc.GetVariablePrefix()
		branchKey := "$" + prefix + config.GitVarSuffixBranch
		gituser := prefix + config.GitVarSuffixUser
		gitpass := prefix + config.GitVarSuffixPass
		gitURL := prefix + config.GitVarSuffixRepo

		urlString := "$" + gitURL
		if cluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass) != "" {
			if strings.Contains(gc.GitRepo, "github.com") {
				urlString = fmt.Sprintf("$%s@$%s", gitpass, gitURL)
			} else {
				urlString = fmt.Sprintf("$%s:$%s@$%s", gituser, gitpass, gitURL)
			}
		}

		svccontainer["command"] = "-c 'rm -rf " + dirname + ";mkdir " + dirname + ";git clone -b " + branchKey + " https://" + urlString + " " + dirname + "'"
	}

	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppS3MountContainerSection(app *App, s3m *config.S3Mount) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["type"] = "docker"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "signal18/nfsmixr:latest"
		svccontainer["netns"] = "container#01"
		svccontainer["privileged"] = "true"
		svccontainer["secrets_environment"] = s3m.GetVariableKeys(app.Name, "secret")
		svccontainer["configs_environment"] = s3m.GetVariableKeys(app.Name, "env")
		diskname := app.GetAppVolumeName(s3m.GetSourcePoolName(), false)
		svccontainer["volume_mounts"] = fmt.Sprintf("%s:/mnt:rw,rshared", filepath.Join(diskname, s3m.VolumeDir))
		svccontainer["run_command"] = "-o allow_other -o nonempty --use-content-type --uid 33 --gid 33 -f"
		svccontainer["blocking_post_provision"] = "sleep 3"
		svccontainer["blocking_post_start"] = "sleep 3"
	}
	return svccontainer
}

func (cluster *Cluster) GetOpenSVCDeploymentPathMapping(app *App) string {
	var results []string
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return ""
	}

	deployment := appcnf.Deployment
	deployment.SortPaths()
	if len(deployment.Paths) == 0 {
		return ""
	}

	for _, path := range deployment.Paths {
		vol, err := deployment.GetVolumeByName(path.VolumeName)
		if err != nil {
			continue
		}

		diskname := app.GetAppVolumeName(vol.PoolName, false)
		results = append(results, path.GetDockerMapping(diskname))
	}

	return strings.Join(results, " ")
}

func (cluster *Cluster) OpenSVCCreateAppVariableMaps(agent string, app *App) error {
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		return errors.New("No support of Maps in Collector API")
	}

	svc := cluster.OpenSVCConnect()
	err := svc.CreateSecretV2(cluster.Name, app.Name, agent)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create secret: %s ", err)
	}

	err = svc.CreateConfigV2(cluster.Name, app.Name, agent)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create config: %s ", err)
	}

	for _, v := range app.AppConfig.Deployment.Variables {
		if v.Type == "secret" {
			err = svc.CreateSecretKeyValueV2(cluster.Name, app.Name, v.Name, cluster.Conf.GetDecryptedPassword(v.Name, v.Value))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", v.Name, err)
			}

			for _, cd := range v.Conditional {
				cdname := fmt.Sprintf("%s@%s", v.Name, cd.Agent)
				err = svc.CreateSecretKeyValueV2(cluster.Name, app.Name, cdname, cluster.Conf.GetDecryptedPassword(cdname, cd.Value))
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add conditional key to secret: %s %s ", cdname, err)
				}
			}
		} else {
			err = svc.CreateConfigKeyValueV2(cluster.Name, app.Name, v.Name, v.Value)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", v.Name, err)
			}

			for _, cd := range v.Conditional {
				cdname := fmt.Sprintf("%s@%s", v.Name, cd.Agent)
				err = svc.CreateConfigKeyValueV2(cluster.Name, app.Name, cdname, cd.Value)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add conditional key to config: %s %s ", cdname, err)
				}
			}
		}
	}

	for _, gc := range app.AppConfig.Deployment.Storages.GitClones {
		prefix := gc.GetVariablePrefix()
		envs := gc.GetEnvVariables()
		for k, val := range envs {
			vName := prefix + k

			// Create the git clone config key
			if k == config.GitVarSuffixRepo {
				// If the git repo is a URL, we need to remove the protocol part
				if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
					val = strings.TrimPrefix(val, "http://")
					val = strings.TrimPrefix(val, "https://")
				}
			}
			if k == config.GitVarSuffixBranch {
				// If the git branch is not set, we use the default branch
				if val == "" {
					val = "master"
				}
			}

			err = svc.CreateConfigKeyValueV2(cluster.Name, app.Name, vName, val)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", vName, err)
			}
		}

		// Create the git clone secret key
		err = svc.CreateSecretKeyValueV2(cluster.Name, app.Name, prefix+config.GitVarSuffixPass, cluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", prefix+config.GitVarSuffixPass, err)
		}
	}

	// Create the s3 mount config keys
	for _, s3m := range app.AppConfig.Deployment.Storages.S3Mounts {
		if s3m.Node == nil {
			node, _ := cluster.GetAppByURL(s3m.Endpoint)
			if node == nil {
				return fmt.Errorf("S3 mount node %s not found in cluster %s", s3m.Endpoint, cluster.Name)
			}
			s3m.Node = node
		}
		prefix := s3m.GetVariablePrefix()
		envs := s3m.GetEnvVariables()
		for k, val := range envs {
			vName := prefix + k
			if k == config.S3VarSuffixEndpoint {
				val = "http://" + s3m.Node.GetS3Endpoint()
			} else if k == config.S3VarSuffixMountDir {
				// If the mount directory is not set, we use the default mount directory
				if val == "" {
					val = "/mnt"
				}
			} else if k == config.S3VarSuffixRegion {
				// If the region is not set, we use the default region
				if val == "" {
					val = "fr-east-1"
				}
			} else if k == config.S3VarSuffixBucket {
				// If the bucket is not set, we use the app name as the default bucket
				if val == "" {
					val = app.Name
				}
			}

			err = svc.CreateConfigKeyValueV2(cluster.Name, app.Name, vName, val)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", vName, err)
			}
		}

		// Create the s3 mount secret key
		err = svc.CreateSecretKeyValueV2(cluster.Name, app.Name, prefix+config.S3VarSuffixSecretKey, cluster.Conf.GetDecryptedPassword(s3m.Name, s3m.SecretKey))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", prefix+config.S3VarSuffixSecretKey, err)
		}
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionRoute(app *App) error {
	svc := cluster.OpenSVCConnect()

	cloud18GatewayServiceConfig := strings.Split(cluster.Conf.Cloud18GatewayService, "/")

	for _, route := range app.AppConfig.Deployment.Routes {

		// Add the DNS input if not exist
		result, err := net.LookupCNAME(route.CName)

		if err != nil {
			slice := strings.Split(route.CName, ".")
			if len(slice) > 2 {
				if slice[len(slice)-2] != "cloud18" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "DNS %s does not resolv to gateway %s and is not under our control please fixed it with CNAME before provison ", route.CName, cluster.Conf.Cloud18GatewayDomainName)
					return nil
				}
				slice = slice[:len(slice)-1]
				slice = slice[:len(slice)-1]
				cname := strings.Join(slice, ".")
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Adding CNAME %s to gateway %s", cname, cluster.Conf.Cloud18GatewayDomainName)

				cluster.BashScriptProvDNS(cname)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Bad DNS entry %s to gateway %s", route.CName, cluster.Conf.Cloud18GatewayDomainName)
			}
		} else {
			if strings.ToLower(strings.TrimRight(result, ".")) != strings.ToLower(strings.TrimRight(cluster.Conf.Cloud18GatewayDomainName, ".")) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Skipping add CNAME %s pointing to % different location from the gateway %s", route.CName, result, cluster.Conf.Cloud18GatewayDomainName)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Skipping add CNAME %s already resolving to gateway %s", route.CName, result, cluster.Conf.Cloud18GatewayDomainName)
			}
		}

		backend := app.Name + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster + "_" + route.Port

		haproxyfragment := `
frontend https
    use_backend ` + backend + ` if { hdr(host) -i ` + route.CName + ` }

backend ` + backend + `
    cookie SERVER insert indirect nocache dynamic
    balance roundrobin
    dynamic-cookie-key mysecretphrase
    server-template srv 2 ` + app.Name + `.` + cluster.Name + `.svc.` + cluster.Conf.ProvOrchestratorCluster + `:` + route.Port + ` resolvers cluster check init-addr none
`
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Creating app route fragment %s %s %s %s", cloud18GatewayServiceConfig[0], cloud18GatewayServiceConfig[2], "haproxy.cfg.d/"+backend, haproxyfragment)

		err = svc.CreateConfigKeyValueV2(cloud18GatewayServiceConfig[0], cloud18GatewayServiceConfig[2], "haproxy.cfg.d/"+backend, haproxyfragment)
		if err != nil {
			return err
		}
	}
	// merge the config fragents

	nodes, err := svc.GetServiceNodeFromState(cluster.Conf.Cloud18GatewayService)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not found node of gateway service: %s", err)
		return err
	}

	var errtask ErrSlice
	for _, node := range nodes {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Creating app route %s on OpenSVC service host %s", app.GetId(), node)
		err = svc.RunTaskV2(cluster.Name, cluster.Conf.Cloud18GatewayService, node, "task#mergecfg", "")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not aggregate fragments of gateway service: %s", err)
			errtask = append(errtask, err)
		}
	}

	if len(errtask) > 0 {
		return ErrSlice(errtask)
	}

	return nil
}

type ErrSlice []error

func (e ErrSlice) Error() string {
	var buf bytes.Buffer
	for i, err := range e {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(err.Error())
	}
	return buf.String()
}
