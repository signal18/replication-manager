// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) GetDatabaseServiceConfig(s *ServerMonitor) string {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundDatabaseAgent(s)
	if err != nil {
		cluster.errorChan <- err
		return ""
	}
	res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, s.Id, agent.Node_name)
	if err != nil {
		return ""
	}
	return res
}

func (cluster *Cluster) OpenSVCProvisionDatabaseService(s *ServerMonitor) {

	svc := cluster.OpenSVCConnect()
	var taglist []string

	agent, err := cluster.FoundDatabaseAgent(s)
	if err != nil {
		cluster.errorChan <- err
		return
	}

	// Unprovision if already in OpenSVC
	var idsrv string
	mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + s.Name)
	if err == nil {
		cluster.LogPrintf(LvlInfo, "Found opensvc database service %s service %s", cluster.Name+"/svc/"+s.Name, mysrv.Svc_id)
		idsrv = mysrv.Svc_id
	} else {
		idsrv, err = svc.CreateService(cluster.Name+"/svc/"+s.Name, "MariaDB")
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't create OpenSVC service %s", err)
			cluster.errorChan <- err
			return
		}
	}

	err = svc.DeteteServiceTags(idsrv)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't delete service tags")
		cluster.errorChan <- err
		return
	}
	taglist = strings.Split(svc.ProvTags, ",")
	svctags, _ := svc.GetTags()
	for _, tag := range taglist {
		idtag, err := svc.GetTagIdFromTags(svctags, tag)
		if err != nil {
			idtag, _ = svc.CreateTag(tag)
		}
		svc.SetServiceTag(idtag, idsrv)
	}

	// create template && bootstrap
	res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, cluster.Name+"/svc/"+s.Name, agent.Node_name)
	if err != nil {
		cluster.errorChan <- err
		return
	}
	idtemplate, _ := svc.CreateTemplate(cluster.Name+"/svc/"+s.Name, res)
	idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+s.Name)
	err = cluster.OpenSVCWaitDequeue(svc, idaction)
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		cluster.errorChan <- err
		return
	}
	task := svc.GetAction(strconv.Itoa(idaction))
	if task != nil {
		cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
	} else {
		cluster.LogPrintf(LvlErr, "Can't fetch task")
	}
	cluster.WaitDatabaseStart(s)

	cluster.errorChan <- nil
	return
}

func (cluster *Cluster) OpenSVCProvisionOneSrvPerDB() error {

	for _, s := range cluster.Servers {

		go cluster.OpenSVCProvisionDatabaseService(s)

	}
	for _, s := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+s.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+s.Name)
			}
		}
	}

	return nil
}

func (cluster *Cluster) OpenSVCUnprovisionDatabaseService(db *ServerMonitor) {
	opensvc := cluster.OpenSVCConnect()
	node, _ := cluster.FoundDatabaseAgent(db)
	for _, svc := range node.Svc {
		if cluster.Name+"/svc/"+db.Name == svc.Svc_name {
			idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
			err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", cluster.Name+"/svc/"+db.Name, err)
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) OpenSVCStopDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
	if err != nil {
		return err
	}
	agent, err := cluster.FoundDatabaseAgent(server)
	if err != nil {
		return err
	}
	svc.StopService(agent.Node_id, service.Svc_id)
	return nil
}

func (cluster *Cluster) FoundDatabaseAgent(server *ServerMonitor) (opensvc.Host, error) {
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()

	if agents == nil {
		return agent, errors.New("Error getting agent list")
	}
	for _, node := range agents {
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			if len(clusteragents) == 0 {
				return agent, errors.New("Indice not found in database agent list")
			}
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in database agent list")
}

func (server *ServerMonitor) GenerateDBTemplate(collector opensvc.Collector, servers []string, ports []string, agents []opensvc.Host, name string, agent string) (string, error) {

	ipPods := ""
	portPods := ""
	conf := ""
	//if zfs snap
	if collector.ProvFSPool == "zpool" && server.ClusterGroup.GetConf().AutorejoinZFSFlashback && server.IsPrefered() {
		conf = `
[DEFAULT]
nodes = {env.nodes}
cluster_type = failover
rollback = true
orchestrate = start
`
	} else {
		conf = `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
topology = flex
rollback = false
`
	}
	conf += "app = " + server.ClusterGroup.Conf.ProvCodeApp

	conf = conf + server.ClusterGroup.GetDockerDiskTemplate(collector)
	//main loop over db instances
	for i, host := range servers {
		pod := fmt.Sprintf("%02d", i+1)
		conf = conf + server.ClusterGroup.GetPodDiskTemplate(collector, pod, agent)
		conf = conf + `post_provision =  {svcmgr} -s  {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.db;
	`
		conf = conf + server.GetSnapshot(collector)
		conf = conf + server.ClusterGroup.GetPodNetTemplate(collector, pod, i)
		conf = conf + server.GetPodDockerDBTemplate(collector, pod, i)
		conf = conf + server.ClusterGroup.GetPodPackageTemplate(collector, pod)
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + host + `
	`
		portPods = portPods + `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + ports[i] + `
	`
	}
	ips := strings.Split(collector.ProvNetGateway, ".")
	masks := strings.Split(collector.ProvNetMask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")

	conf = conf + `[task#01]
schedule = @1
command = {env.base_dir}/pod01/init/trigger-dbjobs
user = root
run_requires = fs#01(up,stdby up) container#0001(up,stdby up)

`

	conf = conf + `
[env]
nodes = ` + agent + `
size = ` + collector.ProvDisk + `g
db_img = ` + collector.ProvDockerImg + `
` + ipPods + `
` + portPods + `
mysql_root_password = ` + server.ClusterGroup.dbPass + `
mysql_root_user = ` + server.ClusterGroup.dbUser + `
network = ` + network + `
gateway =  ` + collector.ProvNetGateway + `
netmask =  ` + collector.ProvNetMask + `
base_dir = /srv/{namespace}-{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
max_cores = ` + collector.ProvCores + `
micro_srv = ` + collector.ProvMicroSrv + `
gcomm	 = ` + server.ClusterGroup.GetGComm() + `
mrm_api_addr = ` + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort + `
mrm_cluster_name = ` + server.ClusterGroup.GetClusterName() + `
safe_ssl_ca_uuid = ` + server.ClusterGroup.Conf.ProvSSLCaUUID + `
safe_ssl_cert_uuid = ` + server.ClusterGroup.Conf.ProvSSLCertUUID + `
safe_ssl_key_uuid = ` + server.ClusterGroup.Conf.ProvSSLKeyUUID + `
server_id = ` + string(server.Id[2:10]) + `
innodb_buffer_pool_size = ` + server.ClusterGroup.GetConfigInnoDBBPSize() + `
innodb_log_file_size = ` + server.ClusterGroup.GetConfigInnoDBLogFileSize() + `
innodb_buffer_pool_instances = ` + server.ClusterGroup.GetConfigInnoDBBPInstances() + `
innodb_log_buffer_size = 8
`
	log.Println(conf)

	return conf, nil
}

func (server *ServerMonitor) GetPodDockerDBTemplate(collector opensvc.Collector, pod string, i int) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = google/pause
rm = true


[container#20` + pod + `]
tags = pod` + pod + `
type = docker
rm = true
netns = container#00` + pod + `
run_image = {env.db_img}
run_args = -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/mysql:rw
 -v {env.base_dir}/pod` + pod + `/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/pod` + pod + `/init:/docker-entrypoint-initdb.d:rw

`

		if server.ClusterGroup.GetTopology() == topoMultiMasterWsrep && server.ClusterGroup.TopologyClusterDown() && server.ClusterGroup.GetMaster().Id == server.Id {
			//Proceed with galera specific
			if server.ClusterGroup.GetMaster() == nil {
				server.ClusterGroup.vmaster = server
			}
			//s.Conn.Exec("set global wsrep_provider_option='pc.bootstrap=1'")
			//if err != nil {
			//	return err
			//}
			//			vm = vm + `run_command = galera_new_cluster
			//`
			vm = vm + `run_command = mysqld --wsrep_new_cluster
`
		}

		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (server *ServerMonitor) GetDBEnv() map[string]string {
	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                           server.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":                    server.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":                     string(server.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":                    string(server.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":          server.ClusterGroup.dbPass,
		"%%ENV:SVC_CONF_ENV_MAX_MEM%%":                      server.ClusterGroup.GetConfigInnoDBBPSize(),
		"%%ENV:IBPINSTANCES%%":                              server.ClusterGroup.GetConfigInnoDBBPInstances(),
		"%%ENV:SVC_CONF_ENV_GCOMM%%":                        server.ClusterGroup.GetGComm(),
		"%%ENV:SERVER_IP%%":                                 "0.0.0.0",
		"%%ENV:SERVER_PORT%%":                               server.Port,
		"%%ENV:CHECKPOINTIOPS%%":                            server.ClusterGroup.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_MAX_IOPS%%":                     server.ClusterGroup.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY%%":           server.ClusterGroup.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY_MAX%%":       server.ClusterGroup.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_INSTANCES%%": server.ClusterGroup.GetConfigInnoDBBPInstances(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%":      server.ClusterGroup.GetConfigInnoDBBPSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_LOG_BUFFER_SIZE%%":       server.ClusterGroup.GetConfigInnoDBLogFileSize(),
	}

	//	size = ` + collector.ProvDisk + `
}
