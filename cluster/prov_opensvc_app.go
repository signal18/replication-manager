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
	"log"
	"net"
	"path/filepath"
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
	err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), app.GetAgent())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
		return err
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Provisioning app %s on OpenSVC", app.GetId())
	agent, err := cluster.FoundAppAgent(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not find app agent:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Found app agent %s. Creating maps", agent.Node_name)

	err = cluster.OpenSVCCreateAppPathMaps(agent.Node_name, app)
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

func (cluster *Cluster) OpenSVCGetAppTemplateV2(app *App) (string, error) {
	// Check if app image not empty
	if app.AppConfig.ProvAppDockerImg == "" {
		return "", errors.New("App image is not defined in app config")
	}

	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = cluster.OpenSVCGetAppDefaultSection(app)
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	svcsection["volume#01"] = cluster.OpenSVCGetAppVolumeDataSection(app)
	svcsection["container#01"] = cluster.OpenSVCGetNamespaceContainerSection()
	for i, gc := range app.AppConfig.Deployment.GitClones {
		sectionName := fmt.Sprintf("container#%02dinit%s", i+2, gc.Dest)
		svcsection[sectionName] = cluster.OpenSVCGetAppGitInitContainerSection(app, gc)
	}
	svcsection["container#app"] = cluster.OpenSVCGetAppContainerSection(app)
	svcsection["env"] = cluster.OpenSVCGetAppEnvSection(app)

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return "", err
	}
	log.Println(svcsectionJson)
	return string(svcsectionJson), nil

}

func (cluster *Cluster) OpenSVCGetAppVolumeDataSection(app *App) map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}"
	svcvol["pool"] = cluster.GetAppVolumeData(app)
	svcvol["size"] = "{env.size}"
	svcvol["directories"] = "var etc log"
	return svcvol
}

func (cluster *Cluster) FoundAppAgent(app *App) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	svc.ProvAppAgents = cluster.GetAppAgents(app)

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
	appcnf := app.AppConfig

	svcenv := make(map[string]string)
	svcenv["nodes"] = cluster.GetAppAgents(app)
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["size"] = cluster.GetAppDisk(app) + "g"
	svcenv["ip_pod01"] = app.GetHost()
	svcenv["port_pod01"] = app.GetPort()
	svcenv["app_img"] = appcnf.ProvAppDockerImg
	svcenv["port_http"] = "80"
	svcenv["port_telnet"] = app.GetPort()
	svcenv["port_admin"] = app.GetPort()
	svcenv["user_admin"] = app.User
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	return svcenv
}

func (cluster *Cluster) OpenSVCGetAppDefaultSection(app *App) map[string]string {
	appcnf := app.AppConfig
	svcdefault := make(map[string]string)

	svcdefault["nodes"] = cluster.GetAppAgents(app)
	nodes := strings.Split(svcdefault["nodes"], ",")

	if appcnf.ProvAppAgentsFailover != "" {
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["orchestrate"] = "ha"
		svcdefault["flex_primary"] = nodes[0]
		svcdefault["topology"] = "flex"
		svcdefault["rollback"] = "false"
		svcdefault["flex_target"] = strconv.Itoa(len(nodes))
	}

	svcdefault["app"] = "app"
	return svcdefault
}

func (cluster *Cluster) OpenSVCGetAppContainerSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "{env.app_img}"
		svccontainer["type"] = cluster.Conf.ProvType

		if cluster.Conf.ProvDBDockerRunArgsLimit {
			svccontainer["run_args"] = svccontainer["run_args"] + " --memory=" + cluster.GetAppMemory(app) + "m --memory-swap=" + cluster.GetAppMemory(app) + "m --cpus=" + cluster.GetAppCores(app) + ".0"
		}

		svccontainer["volume_mounts"] = cluster.GetOpenSVCDeploymentPathMapping(app)
		svccontainer["environment"] = cluster.GetOpenSVCDeploymentConfigEnv(app)
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
		if cluster.Conf.ProvDiskType != "volume" {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {env.base_dir}:/bootstrap"
		} else {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}:/bootstrap"
		}
		svccontainer["entrypoint"] = "/bin/sh"
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppGitInitContainerSection(app *App, gc config.GitClone) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer = cluster.OpenSVCGetAppGitInitDefaultSection(app)
		svccontainer["secrets_environment"] = app.GetOpenSVCDeploymentGitEnv(gc, "secret")
		svccontainer["configs_environment"] = app.GetOpenSVCDeploymentGitEnv(gc, "env")
		dirname := filepath.Join("/bootstrap", gc.VolumeDir, gc.Dest)
		branch := app.GetOpenSVCDeplopymentGitPrefix(gc, "BRANCH")
		gituser := app.GetOpenSVCDeplopymentGitPrefix(gc, "USER")
		gitpass := app.GetOpenSVCDeplopymentGitPrefix(gc, "PASSWORD")
		gitURL := app.GetOpenSVCDeplopymentGitPrefix(gc, "URL")
		if strings.HasPrefix(gitURL, "github.com") {
			svccontainer["command"] = "-c 'rm -rf " + dirname + ";mkdir " + dirname + ";git clone -b $" + branch + " https://$" + gitpass + "@$" + gitURL + " " + dirname + "'"
		} else {
			svccontainer["command"] = "-c 'rm -rf " + dirname + ";mkdir " + dirname + ";git clone -b $" + branch + " https://$" + gituser + ":$" + gitpass + "@$" + gitURL + " " + dirname + "'"
		}
	}

	return svccontainer
}

func (cluster *Cluster) GetOpenSVCDeploymentPathMapping(app *App) string {
	var results []string
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return ""
	}

	for _, p := range appcnf.Deployment.Paths {
		from := filepath.Join("{name}", p.VolumeDir, p.From)
		results = append(results, from+":"+p.To)
	}

	return strings.Join(results, " ")
}
func (cluster *Cluster) GetOpenSVCDeploymentConfigEnv(app *App) string {
	var result string

	return result
}

func (cluster *Cluster) OpenSVCCreateAppPathMaps(agent string, app *App) error {
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
		value := v.Value
		if len(v.Conditional) > 0 {
			for _, av := range v.Conditional {
				if av.Agent == agent {
					value = av.Value
					break
				}
			}
		}

		if v.Type == "secret" {
			err = svc.CreateSecretKeyValueV2(cluster.Name, app.Name, v.Name, value)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", v.Name, err)
			}
		} else {
			err = svc.CreateConfigKeyValueV2(cluster.Name, app.Name, v.Name, value)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", v.Name, err)
			}
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
    server-template srv 2 ` + app.Name + `.` + cluster.Name + `.svc.` + cluster.Conf.ProvOrchestratorCluster + `:` + app.Port + ` resolvers cluster check init-addr none
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

	return errtask
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
