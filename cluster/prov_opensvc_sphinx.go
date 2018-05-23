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
	conf = conf + `post_provision = {svcmgr} -s {svcname} push status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.proxy
`
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerSphinxTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + `[task0]
schedule = ` + cluster.Conf.ProvSphinxCron + `
command = {env.base_dir}/{svcname}/pod01/init/reindex.sh
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
run_image = busybox:latest
run_args =  --net=none  -i -t
-v /etc/localtime:/etc/localtime:ro
run_command = /bin/sh

[container#20` + pod + `]
tags = pod` + pod + `
type = docker
run_image = {env.sphinx_img}
run_args = --ulimit nofile=262144:262144 --net=container:{svcname}.container.00` + pod + `
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
