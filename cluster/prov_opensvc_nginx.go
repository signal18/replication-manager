// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"log"

	"github.com/signal18/replication-manager/opensvc"
)

func (cluster *Cluster) OpenSVCGetNginxContainerSection(server *AppNginx) map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvAppNginxType == "docker" || server.ClusterGroup.Conf.ProvAppNginxType == "podman" || server.ClusterGroup.Conf.ProvAppNginxType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.nginx_img}"
		svccontainer["rm"] = "true"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		if server.ClusterGroup.Conf.ProvAppNginxDiskType != "volume" {
			svccontainer["run_args"] = `-v {env.base_dir}/pod01/init/checkslave:/usr/bin/checkslave:rw -v {env.base_dir}/pod01/init/checkmaster:/usr/bin/checkmaster:rw -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/etc/nginx:/usr/local/etc/nginx:rw ` + server.ClusterGroup.Conf.ProvAppNginxDockerRunArgs
		} else {
			//	svccontainer["post_provision"] = "chown -R 99:99 {env.base_dir}/data"
			svccontainer["run_args"] = "--sysctl net.ipv4.ip_unprivileged_port_start=0 " + server.ClusterGroup.Conf.ProvAppNginxDockerRunArgs
			svccontainer["volume_mounts"] = `{name}/init/checkslave:/usr/bin/checkslave:rw {name}/init/checkmaster:/usr/bin/checkmaster:rw /etc/localtime:/etc/localtime:ro {name}/etc/nginx:/usr/local/etc/nginx:rw`
		}
	}

	return svccontainer
}

func (cluster *Cluster) GetNginxTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, app *AppNginx) (string, error) {

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
	conf = conf + cluster.GetPodDockerNginxTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + cluster.GetAppsEnv(collector, servers, agent, app)
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
