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
	"slices"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) OpenSVCUnprovisionAppService(prx app.AppInterface) {
	opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()
	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		err := opensvc.PurgeServiceV2(cluster.GetName(), prx.GetServiceName(), prx.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app service:  %s ", err)
			cluster.errorChan <- err
		}
		err = opensvc.PurgeServiceV2(cluster.Name, cluster.Name+"/vol/"+prx.GetName(), prx.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app volume:  %s ", err)
			cluster.errorChan <- err
		}
	} else {
		node, _ := cluster.FoundAppAgent(prx)
		for _, svc := range node.Svc {
			if prx.GetServiceName() == svc.Svc_name {
				idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't unprovision app %s, %s", prx.GetId(), err)
				}
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) OpenSVCStopAppService(server app.AppInterface) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.GetName())
		if err != nil {
			return err
		}
		agent, err := cluster.FoundAppAgent(server)
		if err != nil {
			return err
		}
		svc.StopService(agent.Node_id, service.Svc_id)
	} else {
		err := svc.StopServiceV2(cluster.Name, server.GetServiceName(), server.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartAppService(server app.AppInterface) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.GetName())
		if err != nil {
			return err
		}
		agent, err := cluster.FoundAppAgent(server)
		if err != nil {
			return err
		}
		svc.StartService(agent.Node_id, service.Svc_id)
	} else {
		err := svc.StartServiceV2(cluster.Name, server.GetServiceName(), server.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionAppService(pri app.AppInterface) error {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundAppAgent(pri)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	// Unprovision if already in OpenSVC
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		var idsrv string
		mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + pri.GetName())
		if err == nil {
			idsrv = mysrv.Svc_id
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Found existing service %s service %s", cluster.Name+"/"+pri.GetName(), idsrv)

		} else {
			idsrv, err = svc.CreateService(cluster.Name+"/svc/"+pri.GetName(), "MariaDB")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC app service")
				cluster.errorChan <- err
				return err
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Attaching internal id  %s to opensvc service id %s", cluster.Name+"/"+pri.GetName(), idsrv)

		err = svc.DeteteServiceTags(idsrv)
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
	if prx, ok := pri.(*app.App); ok {
		if !cluster.Conf.ProvOpensvcUseCollectorAPI {
			res, err := cluster.OpenSVCGetAppTemplateV2(strings.Join(srvlist, " "), prx)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, prx.Agent, res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
		} else {
			if strings.Contains(svc.ProvAppAgents, agent.Node_name) {
				res, err := cluster.GetNginxTemplate(svc, strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(prx.GetServiceName(), res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, prx.GetServiceName())
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't fetch task")
				}
			}
		}
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateV2(servers string, pri app.AppInterface) (string, error) {
	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = pri.OpenSVCGetAppDefaultSection()
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	if pri.OpenSVCGetAppDiskType() != "volume" {
		svcsection["disk#0000"] = cluster.OpenSVCGetDiskZpoolDockerPrivateSection()
		svcsection["disk#00"] = cluster.OpenSVCGetDiskLoopbackDockerPrivateSection()
		svcsection["disk#01"] = cluster.OpenSVCGetDiskLoopbackPodSection()
		svcsection["disk#0001"] = cluster.OpenSVCGetDiskLoopbackSnapshotPodSection()
		svcsection["fs#00"] = cluster.OpenSVCGetFSDockerPrivateSection()
		svcsection["fs#01"] = cluster.OpenSVCGetFSPodSection()
	} else {
		if cluster.Conf.ProvDockerDaemonPrivate {
			svcsection["volume#00"] = cluster.OpenSVCGetVolumeDockerSection()
		}
		svcsection["volume#01"] = cluster.OpenSVCGetAppVolumeDataSection(pri)
	}

	svcsection["container#01"] = cluster.OpenSVCGetNamespaceContainerSection()
	svcsection["container#02"] = cluster.OpenSVCGetInitContainerSection(pri.GetPort())

	if app, ok := pri.(*app.App); ok {
		svcsection["container# app"] = cluster.OpenSVCGetAppContainerSection(app)
	}

	svcsection["env"] = cluster.OpenSVCGetAppEnvSection(servers, pri)

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return "", err
	}
	log.Println(svcsectionJson)
	return string(svcsectionJson), nil

}

func (cluster *Cluster) OpenSVCGetAppDefaultSection(appi app.AppInterface) map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = appi.GetAgent()
	if appi.OpenSVCGetAppDiskPool() == "zpool" && appi.OpenSVCGetAppAgentsFailover() != "" {
		svcdefault["nodes"] = appi.GetAgent() + "," + appi.OpenSVCGetAppAgentsFailover()
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = appi.GetAgent()
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = cluster.Conf.ProvCodeApp
	if appi.OpenSVCGetAppServiceType() == "docker" {
		if cluster.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if appi.OpenSVCGetAppDiskType() != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if appi.OpenSVCGetAppDiskPool() == "zpool" {
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

func (cluster *Cluster) OpenSVCGetAppVolumeDataSection(appi app.AppInterface) map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}"
	svcvol["size"] = "{env.size}"
	svcvol["pool"] = appi.OpenSVCGetAppDiskPool()

	return svcvol
}

func (cluster *Cluster) FoundAppAgent(app app.AppInterface) (opensvc.Host, error) {
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

func (cluster *Cluster) OpenSVCGetAppEnvSection(proxies string, app app.AppInterface) map[string]string {
	ips := strings.Split(app.OpenSVCGetAppGateway(), ".")
	masks := strings.Split(app.OpenSVCGetAppNetMask(), ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")

	if app.GetVIP() != "" && app.OpenSVCGetRouteAddr() == "" {
		routeaddr, routeport := misc.SplitHostPort(app.GetVIP())
		app.OpenSVCSetRouteAddr(routeaddr)
		app.OpenSVCSetRoutePort(routeport)
	}
	svcenv := make(map[string]string)
	svcenv["nodes"] = app.GetAgent()
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["size"] = app.OpenSVCGetAppDiskSize() + "g"
	svcenv["ip_pod01"] = app.GetHost()
	svcenv["port_pod01"] = app.GetPort()
	svcenv["network"] = network
	svcenv["gateway"] = app.OpenSVCGetAppGateway()
	svcenv["netmask"] = app.OpenSVCGetAppNetMask()
	svcenv["vip_addr"] = app.OpenSVCGetRouteAddr()
	svcenv["vip_port"] = app.OpenSVCGetRoutePort()
	svcenv["vip_netmask"] = app.OpenSVCGetRouteMask()
	svcenv["port_http"] = "80"
	svcenv["proxy_ips"] = proxies
	svcenv["port_telnet"] = app.GetPort()
	svcenv["port_admin"] = app.GetPort()
	svcenv["user_admin"] = app.GetUser()
	svcenv["password_admin"] = app.GetPass()
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	return svcenv
}

func (cluster *Cluster) GetAppsEnv(collector opensvc.Collector, servers string, agent opensvc.Host, app app.AppInterface) string {
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
	if app.GetVIP() != "" && app.OpenSVCGetRouteAddr() == "" {
		routeaddr, routeport := misc.SplitHostPort(app.GetVIP())
		app.OpenSVCSetRouteAddr(routeaddr)
		app.OpenSVCSetRoutePort(routeport)
	}

	conf := `
[env]
nodes = ` + agent.Node_name + `
size = ` + collector.ProvAppDisk + `
` + ipPods + `
` + portPods + `
sphinx_img = ` + cluster.Conf.ProvSphinxImg + `
vip_addr = ` + app.OpenSVCGetRouteAddr() + `
vip_port  = ` + app.OpenSVCGetRoutePort() + `
vip_netmask =  ` + app.OpenSVCGetRouteMask() + `
port_http = 80
base_dir = /srv/{namespace}-{svcname}
backend_ips = ` + servers + `
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

func (cluster *Cluster) OpenSVCGetAppContainerSection(server app.AppInterface) map[string]string {
	svccontainer := make(map[string]string)
	if slices.Contains([]string{"docker", "podman", "oci"}, server.OpenSVCGetAppServiceType()) {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.nginx_img}"
		svccontainer["rm"] = "true"
		svccontainer["type"] = server.OpenSVCGetAppServiceType()
		if server.OpenSVCGetAppDiskType() != "volume" {
			svccontainer["run_args"] = `-v {env.base_dir}/pod01/init/checkslave:/usr/bin/checkslave:rw -v {env.base_dir}/pod01/init/checkmaster:/usr/bin/checkmaster:rw -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/etc/nginx:/usr/local/etc/nginx:rw ` + server.OpenSVCGetAppDockerRunArgs()
		} else {
			//	svccontainer["post_provision"] = "chown -R 99:99 {env.base_dir}/data"
			svccontainer["run_args"] = "--sysctl net.ipv4.ip_unprivileged_port_start=0 " + server.OpenSVCGetAppDockerRunArgs()
			svccontainer["volume_mounts"] = `{name}/init/checkslave:/usr/bin/checkslave:rw {name}/init/checkmaster:/usr/bin/checkmaster:rw /etc/localtime:/etc/localtime:ro {name}/etc/nginx:/usr/local/etc/nginx:rw`
		}
	}

	return svccontainer
}

func (cluster *Cluster) GetNginxTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, appi app.AppInterface) (string, error) {

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
	conf = conf + appi.GetInitContainer(collector)
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerNginxTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + cluster.GetAppsEnv(collector, servers, agent, appi)
	log.Println(conf)
	return conf, nil
}

func (cluster *Cluster) GetPodDockerNginxTemplate(collector opensvc.Collector, pod string) string {
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
run_image = {env.nginx_img}
netns = container#00` + pod + `
rm = true
run_args = -v {env.base_dir}/pod` + pod + `/init/checkslave:/usr/bin/checkslave:rw
		-v {env.base_dir}/pod` + pod + `/init/checkmaster:/usr/bin/checkmaster:rw
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/etc:/usr/local/etc/nginx:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) OpenSVCProvisionReloadNginxConf(Conf string) string {
	svc := cluster.OpenSVCConnect()
	svc.SetRulesetVariableValue("mariadb.svc.mrm.app.cnf.nginx", "app_cnf_nginx", Conf)
	return ""
}
