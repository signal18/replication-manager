// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
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
		err = opensvc.PurgeServiceV2(cluster.Name, cluster.Name+"/vol/"+app.GetName(), app.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app volume:  %s ", err)
			cluster.errorChan <- err
		}
	} else {
		node, _ := cluster.FoundAppAgent(app)
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

func (cluster *Cluster) OpenSVCStopAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + app.GetName())
		if err != nil {
			return err
		}
		agent, err := cluster.FoundAppAgent(app)
		if err != nil {
			return err
		}
		svc.StopService(agent.Node_id, service.Svc_id)
	} else {
		err := svc.StopServiceV2(cluster.Name, app.GetServiceName(), app.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + app.GetName())
		if err != nil {
			return err
		}
		agent, err := cluster.FoundAppAgent(app)
		if err != nil {
			return err
		}
		svc.StartService(agent.Node_id, service.Svc_id)
	} else {
		err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), app.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundAppAgent(app)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	// Unprovision if already in OpenSVC
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		var idsrv string
		mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + app.GetName())
		if err == nil {
			idsrv = mysrv.Svc_id
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Found existing service %s service %s", cluster.Name+"/"+app.GetName(), idsrv)

		} else {
			idsrv, err = svc.CreateService(cluster.Name+"/svc/"+app.GetName(), "MariaDB")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC app service")
				cluster.errorChan <- err
				return err
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Attaching internal id  %s to opensvc service id %s", cluster.Name+"/"+app.GetName(), idsrv)

		err = svc.DeleteServiceTags(idsrv)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't delete service tags")
			cluster.errorChan <- err
			return err
		}
		taglist := strings.Split(svc.ProvAppTags, ",")
		svctags, _ := svc.GetTags()
		for _, tag := range taglist {
			idtag, err := svc.GetTagIdFromTags(svctags, tag)
			if err != nil {
				idtag, _ = svc.CreateTag(tag)
			}
			svc.SetServiceTag(idtag, idsrv)
		}
	}
	cluster.OpenSVCCreateMaps(agent.Node_name)
	srvlist := make([]string, len(cluster.Servers))
	for i, s := range cluster.Servers {
		srvlist[i] = s.Host
	}

	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		res, err := cluster.OpenSVCGetAppTemplateV2(strings.Join(srvlist, " "), app)
		if err != nil {
			cluster.errorChan <- err
			return err
		}
		err = svc.CreateTemplateV2(cluster.Name, app.ServiceName, app.Agent, res)
		if err != nil {
			cluster.errorChan <- err
			return err
		}
	} else {
		if strings.Contains(svc.ProvAppAgents, agent.Node_name) {
			res, err := cluster.GetAppTemplate(svc, strings.Join(srvlist, " "), agent, app)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			idtemplate, err := svc.CreateTemplate(app.GetServiceName(), res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, app.GetServiceName())
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't fetch task")
			}
		}
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateV2(backend string, app *App) (string, error) {
	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = app.OpenSVCGetAppDefaultSection()
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	svcsection["volume#01"] = cluster.OpenSVCGetAppVolumeDataSection(app.Name)
	svcsection["container#01"] = cluster.OpenSVCGetNamespaceContainerSection()
	svcsection["container#02"] = cluster.OpenSVCGetInitContainerSection(app.GetPort())
	svcsection["container#app"] = cluster.OpenSVCGetAppContainerSection(app)
	svcsection["env"] = cluster.OpenSVCGetAppEnvSection(app)

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return "", err
	}
	log.Println(svcsectionJson)
	return string(svcsectionJson), nil

}

func (cluster *Cluster) OpenSVCGetAppVolumeDataSection(appname string) map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}"
	svcvol["pool"] = cluster.GetAppConfig(appname).ProvAppVolumeData
	svcvol["size"] = "{env.size}"
	return svcvol
}

func (cluster *Cluster) FoundAppAgent(app *App) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
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

func (cluster *Cluster) OpenSVCGetAppEnvSection(app *App) map[string]string {
	appcnf := cluster.GetAppConfig(app.GetName())

	svcenv := make(map[string]string)
	svcenv["nodes"] = app.GetAgent()
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["size"] = appcnf.ProvAppDisk + "g"
	svcenv["ip_pod01"] = app.GetHost()
	svcenv["port_pod01"] = app.GetPort()
	svcenv["app_img"] = appcnf.ProvAppDockerImg
	svcenv["port_http"] = "80"
	svcenv["port_telnet"] = app.GetPort()
	svcenv["port_admin"] = app.GetPort()
	svcenv["user_admin"] = app.GetUser()
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	return svcenv
}

func (cluster *Cluster) GetAppsEnv(collector opensvc.Collector, backend string, agent opensvc.Host, app *App) string {
	i := 0
	ipPods := ""
	//if !cluster.Conf.ProvNetCNI {
	ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + app.GetHost() + `
	`
	portPods := `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + app.GetPort() + `
`
	/*} else {
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = 0.0.0.0`
	}
	ips := strings.Split(collector.ProvAppNetGateway, ".")
	masks := strings.Split(collector.ProvAppNetMask, ".")
	for i, mask := range masks {
			if mask == "0" {
				ips[i] = "0"
			}
		}
		network := strings.Join(ips, ".")
	*/

	conf := `
[env]
nodes = ` + agent.Node_name + `
size = ` + collector.ProvAppDisk + `
` + ipPods + `
` + portPods + `
app_img = ` + collector.ProvAppDockerImg + `
port_rw = ` + strconv.Itoa(app.GetWritePort()) + `
port_rw_split =  ` + strconv.Itoa(app.GetReadWritePort()) + `
port_r_lb =  ` + strconv.Itoa(app.GetReadPort()) + `
port_http = 80
base_dir = /srv/{namespace}-{svcname}
port_binlog = ` + strconv.Itoa(cluster.Conf.MxsBinlogPort) + `
port_telnet = ` + app.GetPort() + `
port_admin = ` + app.GetPort() + `
user_admin = ` + app.GetUser() + `
password_admin = ` + app.GetPass() + `
mrm_api_addr = ` + cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort + `
mrm_cluster_name = ` + cluster.GetClusterName() + `
`

	return conf
}

func (app *App) OpenSVCGetAppDefaultSection() map[string]string {
	cluster := app.ClusterGroup
	appcnf := cluster.GetAppConfig(app.GetName())
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = app.Agent
	if appcnf.ProvAppDiskPool == "zpool" && appcnf.ProvAppAgentsFailover != "" {
		svcdefault["nodes"] = app.Agent + "," + appcnf.ProvAppAgentsFailover
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = app.Agent
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = cluster.Conf.ProvCodeApp
	if appcnf.ProvAppType == "docker" {
		if cluster.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if appcnf.ProvAppDiskType != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if appcnf.ProvAppDiskPool == "zpool" {
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

func (cluster *Cluster) OpenSVCGetAppContainerSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.GetAppConfig(app.GetName()).ProvAppType == "docker" || cluster.GetAppConfig(app.GetName()).ProvAppType == "podman" || cluster.GetAppConfig(app.GetName()).ProvAppType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.app_img}"
		svccontainer["rm"] = "true"
		svccontainer["type"] = cluster.Conf.ProvType
		if cluster.GetAppConfig(app.GetName()).ProvAppDiskType != "volume" {
			svccontainer["run_args"] = `-v {env.base_dir}/pod01/init/checkslave:/usr/bin/checkslave:rw -v {env.base_dir}/pod01/init/checkmaster:/usr/bin/checkmaster:rw -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/etc/app:/usr/local/etc/app:rw `
		} else {
			//	svccontainer["post_provision"] = "chown -R 99:99 {env.base_dir}/data"
			svccontainer["run_args"] = "--sysctl net.ipv4.ip_unprivileged_port_start=0 "
			svccontainer["volume_mounts"] = `{name}/init/checkslave:/usr/bin/checkslave:rw {name}/init/checkmaster:/usr/bin/checkmaster:rw /etc/localtime:/etc/localtime:ro {name}/etc/app:/usr/local/etc/app:rw`
		}
	}

	return svccontainer
}

func (cluster *Cluster) GetAppTemplate(collector opensvc.Collector, backend string, agent opensvc.Host, app *App) (string, error) {

	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
topology = flex
rollback = false
orchestrate = start
`
	conf += "app = " + cluster.Conf.ProvCodeApp
	conf = conf + cluster.GetDockerDiskTemplate(collector)
	i := 0
	pod := fmt.Sprintf("%02d", i+1)
	conf = conf + cluster.GetPodDiskTemplate(collector, pod, agent.Node_name)

	//conf = conf + `post_provision = {svcmgr} -s {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.app
	//`
	conf = conf + app.GetInitContainer(collector)
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerAppTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + cluster.GetAppsEnv(collector, backend, agent, app)
	log.Println(conf)
	return conf, nil
}

func (cluster *Cluster) GetPodDockerAppTemplate(collector opensvc.Collector, pod string) string {
	var vm string
	if collector.ProvAppMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = ghcr.io/opensvc/pause
rm = true

[container#20` + pod + `]
tags = pod` + pod + `
type = docker
run_image = {env.app_img}
netns = container#00` + pod + `
rm = true
run_args = -v {env.base_dir}/pod` + pod + `/init/checkslave:/usr/bin/checkslave:rw
		-v {env.base_dir}/pod` + pod + `/init/checkmaster:/usr/bin/checkmaster:rw
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/etc:/usr/local/etc/app:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) OpenSVCProvisionReloadAppConf(Conf string) string {
	svc := cluster.OpenSVCConnect()
	svc.SetRulesetVariableValue("mariadb.svc.mrm.app.cnf.app", "app_cnf_app", Conf)
	return ""
}
