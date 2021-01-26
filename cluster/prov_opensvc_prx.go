// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
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
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) OpenSVCStopProxyService(server *Proxy) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.FoundProxyAgent(server)
		if err != nil {
			return err
		}
		svc.StopService(agent.Node_id, service.Svc_id)
	} else {
		agent, err := cluster.GetProxyAgent(server)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop proxy:  %s ", err)
			return err
		}
		err = svc.StopServiceV2(cluster.Name, server.ServiceName, agent.HostName)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop proxy:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartProxyService(server *Proxy) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.FoundProxyAgent(server)
		if err != nil {
			return err
		}
		svc.StartService(agent.Node_id, service.Svc_id)
	} else {
		agent, err := cluster.GetProxyAgent(server)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop proxy:  %s ", err)
			return err
		}
		err = svc.StartServiceV2(cluster.Name, server.ServiceName, agent.HostName)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop proxy:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionProxyService(prx *Proxy) error {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundProxyAgent(prx)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	// Unprovision if already in OpenSVC
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		var idsrv string
		mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + prx.Name)
		if err == nil {
			idsrv = mysrv.Svc_id
			cluster.LogPrintf(LvlInfo, "Found existing service %s service %s", cluster.Name+"/"+prx.Name, idsrv)

		} else {
			idsrv, err = svc.CreateService(cluster.Name+"/svc/"+prx.Name, "MariaDB")
			if err != nil {
				cluster.LogPrintf(LvlErr, "Can't create OpenSVC proxy service")
				cluster.errorChan <- err
				return err
			}
		}
		cluster.LogPrintf(LvlInfo, "Attaching internal id  %s to opensvc service id %s", cluster.Name+"/"+prx.Name, idsrv)

		err = svc.DeteteServiceTags(idsrv)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't delete service tags")
			cluster.errorChan <- err
			return err
		}
		taglist := strings.Split(svc.ProvProxTags, ",")
		svctags, _ := svc.GetTags()
		for _, tag := range taglist {
			idtag, err := svc.GetTagIdFromTags(svctags, tag)
			if err != nil {
				idtag, _ = svc.CreateTag(tag)
			}
			svc.SetServiceTag(idtag, idsrv)
		}
	}
	cluster.OpenSVCCreateMaps()
	srvlist := make([]string, len(cluster.Servers))
	for i, s := range cluster.Servers {
		srvlist[i] = s.Host
	}
	if prx.Type == config.ConstProxyMaxscale {
		if !cluster.Conf.ProvOpensvcUseCollectorAPI {
			res, err := cluster.OpenSVCGetProxyTemplateV2(strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, agent.Node_name, res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
		} else {
			if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
				res, err := cluster.GetMaxscaleTemplate(svc, strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(cluster.Name+"/svc/"+prx.Name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+prx.Name)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}
	if prx.Type == config.ConstProxySpider {

		if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
			srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, true, cluster.GetDomain())
			err := srv.Refresh()
			if err == nil {
				cluster.LogPrintf(LvlWarn, "Can connect to requested signal18 sharding proxy")
				//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
				cluster.errorChan <- nil
				return nil
			}
			srv.ClusterGroup = cluster
			if !cluster.Conf.ProvOpensvcUseCollectorAPI {
				res, err := cluster.OpenSVCGetProxyTemplateV2(strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, agent.Node_name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
			} else {
				res, err := cluster.GetShardproxyTemplate(svc, strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(cluster.Name+"/svc/"+prx.Name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+prx.Name)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}
	if prx.Type == config.ConstProxyHaproxy {
		if !cluster.Conf.ProvOpensvcUseCollectorAPI {
			res, err := cluster.OpenSVCGetProxyTemplateV2(strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, agent.Node_name, res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
		} else {
			if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
				res, err := cluster.GetHaproxyTemplate(svc, strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(cluster.Name+"/svc/"+prx.Name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+prx.Name)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}
	if prx.Type == config.ConstProxySphinx {
		if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		} else {
			res, err := cluster.OpenSVCGetProxyTemplateV2(strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, agent.Node_name, res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
			if strings.Contains(cluster.Conf.ProvSphinxAgents, agent.Node_name) {
				res, err := cluster.GetSphinxTemplate(svc, strings.Join(srvlist, " "), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(cluster.Name+"/svc/"+prx.Name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+prx.Name)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}
	if prx.Type == config.ConstProxySqlproxy {
		if !cluster.Conf.ProvOpensvcUseCollectorAPI {
			res, err := cluster.OpenSVCGetProxyTemplateV2(strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			err = svc.CreateTemplateV2(cluster.Name, prx.ServiceName, agent.Node_name, res)
			if err != nil {
				cluster.errorChan <- err
				return err
			}
		} else {

			if strings.Contains(svc.ProvAgents, agent.Node_name) {
				res, err := cluster.GetProxysqlTemplate(svc, strings.Join(srvlist, ","), agent, prx)
				if err != nil {
					cluster.errorChan <- err
					return err
				}
				idtemplate, err := svc.CreateTemplate(cluster.Name+"/svc/"+prx.Name, res)
				if err != nil {
					cluster.errorChan <- err
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+prx.Name)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OpenSVCGetProxyTemplateV2(servers string, agent opensvc.Host, prx *Proxy) (string, error) {

	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = prx.OpenSVCGetProxyDefaultSection(agent.Node_name)
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	if cluster.Conf.ProvProxDiskType != "volume" {
		svcsection["disk#0000"] = cluster.OpenSVCGetDiskZpoolDockerPrivateSection()
		svcsection["disk#00"] = cluster.OpenSVCGetDiskLoopbackDockerPrivateSection()
		svcsection["disk#01"] = cluster.OpenSVCGetDiskLoopbackPodSection()
		svcsection["disk#0001"] = cluster.OpenSVCGetDiskLoopbackSnapshotPodSection()
		svcsection["fs#00"] = cluster.OpenSVCGetFSDockerPrivateSection()
		svcsection["fs#01"] = cluster.OpenSVCGetFSPodSection()
		//	svcsection["sync#01"] = server.OpenSVCGetZFSSnapshotSection()
		//	svcsection["task#02"] = server.OpenSVCGetTaskZFSSnapshotSection()

	} else {
		if cluster.Conf.ProvDockerDaemonPrivate {
			svcsection["volume#00"] = cluster.OpenSVCGetVolumeDockerSection()
		}
		svcsection["volume#01"] = cluster.OpenSVCGetProxyVolumeDataSection()
	}
	svcsection["container#0001"] = cluster.OpenSVCGetNamespaceContainerSection()
	svcsection["container#0002"] = cluster.OpenSVCGetInitContainerSection()
	switch prx.Type {
	case config.ConstProxySpider:
		svcsection["container#0003"] = cluster.OpenSVCGetShardproxyContainerSection(prx)
	case config.ConstProxySphinx:
		svcsection["container#0003"] = cluster.OpenSVCGetSphinxContainerSection(prx)
		svcsection["task#01"] = cluster.OpenSVCGetSphinxTaskSection(prx)
	case config.ConstProxyHaproxy:
		svcsection["container#0003"] = cluster.OpenSVCGetHaproxyContainerSection(prx)
	case config.ConstProxySqlproxy:
		svcsection["container#0003"] = cluster.OpenSVCGetProxysqlContainerSection(prx)
	case config.ConstProxyMaxscale:
		svcsection["container#0003"] = cluster.OpenSVCGetMaxscaleContainerSection(prx)
	default:
	}
	svcsection["env"] = cluster.OpenSVCGetProxyEnvSection(servers, agent, prx)

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return "", err
	}
	log.Println(svcsectionJson)
	return string(svcsectionJson), nil

}

func (cluster *Cluster) OpenSVCGetProxyVolumeDataSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}"
	svcvol["pool"] = cluster.Conf.ProvProxVolumeData
	svcvol["size"] = "{env.size}"
	return svcvol
}

func (cluster *Cluster) OpenSVCUnprovisionProxyService(prx *Proxy) {
	opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()
	node, _ := cluster.FoundProxyAgent(prx)
	for _, svc := range node.Svc {
		if cluster.Name+"/svc/"+prx.Name == svc.Svc_name {
			idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
			err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Can't unprovision proxy %s, %s", prx.Id, err)
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) FoundProxyAgent(proxy *Proxy) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvProxAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		return agent, errors.New("Indice not found in proxies agent list")
	}
	for i, srv := range cluster.Proxies {
		if srv.Id == proxy.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in proxies agent list")
}

func (cluster *Cluster) OpenSVCGetProxyEnvSection(servers string, agent opensvc.Host, prx *Proxy) map[string]string {

	ips := strings.Split(cluster.Conf.ProvProxGateway, ".")
	masks := strings.Split(cluster.Conf.ProvProxNetmask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")

	if cluster.Conf.ExtProxyVIP != "" && cluster.Conf.ProvProxRouteAddr == "" {
		cluster.Conf.ProvProxRouteAddr, cluster.Conf.ProvProxRoutePort = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
	}
	svcenv := make(map[string]string)
	svcenv["nodes"] = agent.Node_name
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["size"] = cluster.Conf.ProvProxDisk + "b"
	svcenv["ip_pod01"] = prx.Host
	svcenv["port_pod01"] = prx.Port
	svcenv["network"] = network
	svcenv["gateway"] = cluster.Conf.ProvProxGateway
	svcenv["netmask"] = cluster.Conf.ProvProxNetmask
	svcenv["sphinx_img"] = cluster.Conf.ProvSphinxImg
	svcenv["sphinx_mem"] = cluster.Conf.ProvSphinxMem
	svcenv["sphinx_max_children"] = cluster.Conf.ProvSphinxMaxChildren
	svcenv["haproxy_img"] = cluster.Conf.ProvProxHaproxyImg
	svcenv["proxysql_img"] = cluster.Conf.ProvProxProxysqlImg
	svcenv["maxscale_img"] = cluster.Conf.ProvProxMaxscaleImg
	svcenv["maxscale_maxinfo_port"] = strconv.Itoa(cluster.Conf.MxsMaxinfoPort)
	svcenv["vip_addr"] = cluster.Conf.ProvProxRouteAddr
	svcenv["vip_port"] = cluster.Conf.ProvProxRoutePort
	svcenv["vip_netmask"] = cluster.Conf.ProvProxRouteMask
	svcenv["port_rw"] = strconv.Itoa(prx.WritePort)
	svcenv["port_rw_split"] = strconv.Itoa(prx.ReadWritePort)
	svcenv["port_r_lb"] = strconv.Itoa(prx.ReadPort)
	svcenv["port_http"] = "80"
	svcenv["backend_ips"] = servers
	svcenv["port_binlog"] = strconv.Itoa(cluster.Conf.MxsBinlogPort)
	svcenv["port_telnet"] = prx.Port
	svcenv["port_admin"] = prx.Port
	svcenv["user_admin"] = prx.User
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	return svcenv
}

func (cluster *Cluster) GetProxiesEnv(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) string {
	i := 0
	ipPods := ""
	//if !cluster.Conf.ProvNetCNI {
	ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Host + `
	`
	portPods := `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Port + `
`
	/*} else {
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = 0.0.0.0`
	}
	ips := strings.Split(collector.ProvProxNetGateway, ".")
	masks := strings.Split(collector.ProvProxNetMask, ".")
	for i, mask := range masks {
			if mask == "0" {
				ips[i] = "0"
			}
		}
		network := strings.Join(ips, ".")
	*/
	if cluster.Conf.ExtProxyVIP != "" && cluster.Conf.ProvProxRouteAddr == "" {
		cluster.Conf.ProvProxRouteAddr, cluster.Conf.ProvProxRoutePort = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
	}

	conf := `
[env]
nodes = ` + agent.Node_name + `
size = ` + collector.ProvProxDisk + `
` + ipPods + `
` + portPods + `
sphinx_img = ` + cluster.Conf.ProvSphinxImg + `
sphinx_mem = ` + cluster.Conf.ProvSphinxMem + `
sphinx_max_children = ` + cluster.Conf.ProvSphinxMaxChildren + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerProxysqlImg + `
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
maxscale_maxinfo_port =` + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + `
vip_addr = ` + cluster.Conf.ProvProxRouteAddr + `
vip_port  = ` + cluster.Conf.ProvProxRoutePort + `
vip_netmask =  ` + cluster.Conf.ProvProxRouteMask + `
port_rw = ` + strconv.Itoa(prx.WritePort) + `
port_rw_split =  ` + strconv.Itoa(prx.ReadWritePort) + `
port_r_lb =  ` + strconv.Itoa(prx.ReadPort) + `
port_http = 80
base_dir = /srv/{namespace}-{svcname}
backend_ips = ` + servers + `
port_binlog = ` + strconv.Itoa(cluster.Conf.MxsBinlogPort) + `
port_telnet = ` + prx.Port + `
port_admin = ` + prx.Port + `
user_admin = ` + prx.User + `
password_admin = ` + prx.Pass + `
mrm_api_addr = ` + cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort + `
proxysql_read_on_master =  ` + prx.ProxySQLReadOnMaster() + `
`
	return conf
}

func (proxy *Proxy) GetPRXEnv() map[string]string {
	return map[string]string{
		//	"%%ENV:NODES_CPU_CORES%%":                  server.ClusterGroup.Conf.ProvCores,
		//	"%%ENV:SVC_CONF_ENV_MAX_CORES%%":           server.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":  string(proxy.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%": string(proxy.Id[2:10]),
		//		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%": server.ClusterGroup.dbPass,

		"%%ENV:SERVER_IP%%":                            "0.0.0.0",
		"%%ENV:SERVER_PORT%%":                          proxy.Port,
		"%%ENV:SVC_CONF_ENV_PROXYSQL_READ_ON_MASTER%%": proxy.ProxySQLReadOnMaster(),
	}

}

func (server *Proxy) OpenSVCGetProxyDefaultSection(agent string) map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = agent
	if server.ClusterGroup.Conf.ProvProxDiskPool == "zpool" && server.ClusterGroup.Conf.ProvProxAgentsFailover != "" {
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = agent
		svcdefault["rollback"] = "false"
	}
	svcdefault["app"] = server.ClusterGroup.Conf.ProvCodeApp
	if server.ClusterGroup.Conf.ProvProxType == "docker" {
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if server.ClusterGroup.Conf.ProvProxDiskType != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if server.ClusterGroup.Conf.ProvProxDiskPool == "zpool" {
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
