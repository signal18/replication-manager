// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"log"
	"strings"

	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) GetShardproxyTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

	ipPods := ""

	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
cluster_type = flex
rollback = false
show_disabled = false
`
	conf = conf + cluster.GetDockerDiskTemplate(collector)
	i := 0
	pod := fmt.Sprintf("%02d", i+1)
	conf = conf + cluster.GetPodDiskTemplate(collector, pod, agent.Node_name)
	conf = conf + `post_provision = {svcmgr} -s {svcname} push service status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.db
`
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
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
size = ` + collector.ProvDisk + `
` + ipPods + `
mysql_root_password = ` + cluster.dbPass + `
mysql_root_user = ` + cluster.dbUser + `
network = ` + network + `
gateway =  ` + collector.ProvProxNetGateway + `
netmask =  ` + collector.ProvProxNetMask + `
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerProxysqlImg + `
shardproxy_img = ` + collector.ProvProxDockerShardproxyImg + `
base_dir = /srv/{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
max_cores = ` + collector.ProvCores + `
micro_srv = ` + collector.ProvMicroSrv + `
gcomm	 = ` + cluster.GetGComm() + `
mrm_api_addr = ` + cluster.Conf.BindAddr + ":" + cluster.Conf.HttpPort + `
mrm_cluster_name = ` + cluster.GetClusterName() + `
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
run_image = busybox:latest
run_args =  --net=none  -i -t
	-v /etc/localtime:/etc/localtime:ro
run_command = /bin/sh

[container#20` + pod + `]
tags = pod` + pod + `
type = docker
run_image = {env.shardproxy_img}
run_args =  --net=container:{svcname}.container.00` + pod + `
 -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/shardproxy:rw
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
