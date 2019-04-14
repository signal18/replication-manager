// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OpenSVCProvisionProxies() error {

	for _, prx := range cluster.Proxies {
		cluster.OpenSVCProvisionProxyService(prx)
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionProxyService(prx *Proxy) error {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundProxyAgent(prx)
	if err != nil {
		return err
	}
	// Unprovision if already in OpenSVC

	var idsrv string
	mysrv, err := svc.GetServiceFromName(cluster.Name + "/" + prx.Name)
	if err == nil {
		idsrv = mysrv.Svc_id
		cluster.LogPrintf(LvlInfo, "Found existing service %s service %s", cluster.Name+"/"+prx.Name, idsrv)

	} else {
		idsrv, err = svc.CreateService(cluster.Name+"/"+prx.Name, "MariaDB")
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't create OpenSVC proxy service")
			return err
		}
	}
	cluster.LogPrintf(LvlInfo, "Attaching internal id  %s to opensvc service id %s", cluster.Name+"/"+prx.Name, idsrv)

	err = svc.DeteteServiceTags(idsrv)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't delete service tags")
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
	srvlist := make([]string, len(cluster.Servers))
	for i, s := range cluster.Servers {
		srvlist[i] = s.Host
	}

	if prx.Type == proxyMaxscale {
		if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
			res, err := cluster.GetMaxscaleTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(cluster.Name+"/"+prx.Name, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/"+prx.Name)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogPrintf(LvlErr, "Can't fetch task")
			}
		}
	}
	if prx.Type == proxySpider {
		if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
			srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, "mdbsproxy.cnf")
			err := srv.Refresh()
			if err == nil {
				cluster.LogPrintf(LvlWarn, "Can connect to requested signal18 sharding proxy")
				//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
				return nil
			}
			srv.ClusterGroup = cluster
			res, err := cluster.GetShardproxyTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(cluster.Name+"/"+prx.Name, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/"+prx.Name)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogPrintf(LvlErr, "Can't fetch task")
			}
		}
	}
	if prx.Type == proxyHaproxy {
		if strings.Contains(svc.ProvProxAgents, agent.Node_name) {
			res, err := cluster.GetHaproxyTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(cluster.Name+"/"+prx.Name, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/"+prx.Name)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogPrintf(LvlErr, "Can't fetch task")
			}
		}
	}
	if prx.Type == proxySphinx {
		if strings.Contains(cluster.Conf.ProvSphinxAgents, agent.Node_name) {
			res, err := cluster.GetSphinxTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(cluster.Name+"/"+prx.Name, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/"+prx.Name)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogPrintf(LvlErr, "Can't fetch task")
			}
		}
	}
	if prx.Type == proxySqlproxy {
		if strings.Contains(svc.ProvAgents, agent.Node_name) {
			res, err := cluster.GetProxysqlTemplate(svc, strings.Join(srvlist, ","), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(cluster.Name+"/"+prx.Name, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/"+prx.Name)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
			} else {
				cluster.LogPrintf(LvlErr, "Can't fetch task")
			}
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCUnprovisionProxyService(prx *Proxy) {
	opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()
	node, _ := cluster.FoundProxyAgent(prx)
	for _, svc := range node.Svc {
		if cluster.Name+"/"+prx.Name == svc.Svc_name {
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
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvProxAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.Proxies {
		if srv.Id == proxy.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in proxies agent list")
}

func (cluster *Cluster) OpenSVCStartService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	service, err := svc.GetServiceFromName(cluster.Name + "/" + server.Name)
	if err != nil {
		return err
	}
	agent, err := cluster.FoundDatabaseAgent(server)
	if err != nil {
		return err
	}
	svc.StartService(agent.Node_id, service.Svc_id)
	return nil
}

func (cluster *Cluster) GetProxiesEnv(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) string {
	i := 0
	ipPods := ""
	if !cluster.Conf.ProvNetCNI {
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Host + `
	`
	} else {
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

	if cluster.Conf.ExtProxyVIP != "" && cluster.Conf.ProvProxRouteAddr == "" {
		cluster.Conf.ProvProxRouteAddr, cluster.Conf.ProvProxRoutePort = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
	}

	conf := `
[env]
nodes = ` + agent.Node_name + `
size = ` + collector.ProvDisk + `
` + ipPods + `
mysql_root_password = ` + cluster.dbPass + `
mysql_root_user = ` + cluster.dbUser + `
network = ` + network + `
gateway =  ` + collector.ProvProxNetGateway + `
netmask =  ` + collector.ProvProxNetMask + `
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
mrm_cluster_name = ` + cluster.GetClusterName() + `
`
	return conf
}
