// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) OpenSVCGetShardproxyContainerSection(server *Proxy) map[string]string {

	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvProxType == "docker" || server.ClusterGroup.Conf.ProvProxType == "podman" || server.ClusterGroup.Conf.ProvProxType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#0001"
		svccontainer["image"] = " {env.shardproxy_img}"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		if server.ClusterGroup.Conf.ProvProxDiskType != "volume" {
			svccontainer["run_args"] = `-e MYSQL_ROOT_PASSWORD={env.mysql_root_password} -e MYSQL_INITDB_SKIP_TZINFO=yes -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/data:/var/lib/mysql:rw -v {env.base_dir}/pod01/etc/mysql:/etc/mysql:rw -v {env.base_dir}/pod01/init:/docker-entrypoint-initdb.d:rw
`
		} else {
			svccontainer["volume_mounts"] = `/etc/localtime:/etc/localtime:ro {name}-data/data:/var/lib/mysql:rw {name}-system/data/.system:/var/lib/mysql/.system:rw {name}-temp/data/.system/tmp:/var/lib/mysql/.system/tmp:rw {name}-data/etc/mysql:/etc/mysql:rw {name}-data/init:/docker-entrypoint-initdb.d:rw`
			svccontainer["environment"] = `MYSQL_ROOT_PASSWORD={env.mysql_root_password} MYSQL_INITDB_SKIP_TZINFO=yes`
		}

	}
	return svccontainer
}

func (cluster *Cluster) GetShardproxyTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

	ipPods := ""

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
	conf = conf + `# post_provision = {svcmgr} -s  {svcpath} push status;{svcmgr} -s  {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.db
`
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + prx.GetInitContainer(collector)
	conf = conf + cluster.GetPodDockerShardproxyTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Host + `
`
	ips := strings.Split(collector.ProvProxNetGateway, ".")
	masks := strings.Split(collector.ProvProxNetMask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")
	conf = conf + `
[env]
nodes = ` + agent.Node_name + `
size = ` + collector.ProvProxDisk + `g
` + ipPods + `
port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + strconv.Itoa(prx.WritePort) + `
mysql_root_password = ` + prx.Pass + `
mysql_root_user = ` + prx.User + `
network = ` + network + `
gateway =  ` + collector.ProvProxNetGateway + `
netmask =  ` + collector.ProvProxNetMask + `
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerProxysqlImg + `
shardproxy_img = ` + collector.ProvProxDockerShardproxyImg + `
base_dir = /srv/{namespace}-{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
max_cores = ` + collector.ProvCores + `
micro_srv = ` + collector.ProvMicroSrv + `
gcomm	 = ` + cluster.GetGComm() + `
mrm_api_addr = ` + cluster.Conf.BindAddr + ":" + cluster.Conf.HttpPort + `
mrm_cluster_name = ` + cluster.GetClusterName() + `
server_id = ` + string(prx.Id[2:10]) + `
innodb_buffer_pool_size = ` + cluster.GetConfigInnoDBBPSize() + `
innodb_log_file_size = ` + cluster.GetConfigInnoDBLogFileSize() + `
innodb_buffer_pool_instances = ` + cluster.GetConfigInnoDBBPInstances() + `
innodb_log_buffer_size = 8
`
	log.Println(conf)
	return conf, nil
}

func (cluster *Cluster) GetPodDockerShardproxyTemplate(collector opensvc.Collector, pod string) string {
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
run_image = {env.shardproxy_img}
rm =true
netns = container#00` + pod + `
run_args = -e SHARDPROXY_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/mysql:rw
 -v {env.base_dir}/pod` + pod + `/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/pod` + pod + `/init:/docker-entrypoint-initdb.d:rw
`

		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}
