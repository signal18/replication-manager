// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"log"

	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) OpenSVCGetSphinxContainerSection(server *Proxy) map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvProxType == "docker" || server.ClusterGroup.Conf.ProvProxType == "podman" || server.ClusterGroup.Conf.ProvProxType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.sphinx_img}"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		if server.ClusterGroup.Conf.ProvProxDiskType != "volume" {
			svccontainer["run_args"] = `--ulimit nofile=262144:262144 -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/conf:/usr/local/etc:rw	-v {env.base_dir}/pod01/data:/var/lib/sphinx:rw -v {env.base_dir}/pod01/data:/var/idx/sphinx:rw	-v {env.base_dir}/pod01/log:/var/log/sphinx:rw`
		} else {
			svccontainer["run_args"] = "--ulimit nofile=262144:262144"
			svccontainer["volume_mounts"] = `/etc/localtime:/etc/localtime:ro {env.base_dir}/pod01/conf:/usr/local/etc:rw	{env.base_dir}/pod01/data:/var/lib/sphinx:rw {env.base_dir}/pod01/data:/var/idx/sphinx:rw	{env.base_dir}/pod01/log:/var/log/sphinx:rw`
		}
		svccontainer["run_command"] = "indexall.sh"
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetSphinxTaskSection(server *Proxy) map[string]string {
	svccontainer := make(map[string]string)
	svccontainer["schedule"] = cluster.Conf.ProvSphinxCron
	svccontainer["command"] = "{env.base_dir}/{namespace}-{svcname}/pod01/init/reindex.sh"
	svccontainer["user"] = "root"
	svccontainer["run_requires"] = "fs#01(up,stdby up)"
	return svccontainer
}

func (cluster *Cluster) GetSphinxTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

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
	conf = conf + prx.GetInitContainer(collector)
	//	conf = conf + `post_provision = {svcmgr} -s {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.proxy
	//`
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerSphinxTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + `[task0]
schedule = ` + cluster.Conf.ProvSphinxCron + `
command = {env.base_dir}/{namespace}-{svcname}/pod01/init/reindex.sh
user = root
run_requires = fs#01(up,stdby up)

`
	conf = conf + cluster.GetProxiesEnv(collector, servers, agent, prx)

	log.Println(conf)
	return conf, nil
}

func (cluster *Cluster) GetPodDockerSphinxTemplate(collector opensvc.Collector, pod string) string {
	var vm string
	if collector.ProvProxMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = google/pause
rm = true


[container#20` + pod + `]
tags = pod` + pod + `
type = docker
run_image = {env.sphinx_img}
netns = container#00` + pod + `
rm = true
run_args = --ulimit nofile=262144:262144
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/conf:/usr/local/etc:rw
		-v {env.base_dir}/pod` + pod + `/data:/var/lib/sphinx:rw
		-v {env.base_dir}/pod` + pod + `/data:/var/idx/sphinx:rw
		-v {env.base_dir}/pod` + pod + `/log:/var/log/sphinx:rw
run_command = indexall.sh
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}
