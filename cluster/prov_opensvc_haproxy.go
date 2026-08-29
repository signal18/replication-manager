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

func (cluster *Cluster) OpenSVCGetHaproxyContainerSection(server *HaproxyProxy) map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvProxType == "docker" || server.ClusterGroup.Conf.ProvProxType == "podman" || server.ClusterGroup.Conf.ProvProxType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.haproxy_img}"
		svccontainer["rm"] = "true"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		if server.ClusterGroup.Conf.ProvProxDiskType != "volume" {
			svccontainer["run_args"] = `-v {env.base_dir}/pod01/init/checkslave:/usr/bin/checkslave:rw -v {env.base_dir}/pod01/init/checkmaster:/usr/bin/checkmaster:rw -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/etc/haproxy:/usr/local/etc/haproxy:rw ` + server.ClusterGroup.Conf.ProvProxDockerRunArgs
		} else {
			//	svccontainer["post_provision"] = "chown -R 99:99 {env.base_dir}/data"
			svccontainer["run_args"] = "--sysctl net.ipv4.ip_unprivileged_port_start=0 " + server.ClusterGroup.Conf.ProvProxDockerRunArgs
			svccontainer["volume_mounts"] = `{name}/init/checkslave:/usr/bin/checkslave:rw {name}/init/checkmaster:/usr/bin/checkmaster:rw /etc/localtime:/etc/localtime:ro {name}/etc/haproxy:/usr/local/etc/haproxy:rw`
		}

		// The image's default entrypoint/CMD always launches haproxy.cfg
		// (proxy_cnf_haproxy_runtime_api, no external-check), regardless of
		// haproxy-mode. standby needs haproxy_check.cfg instead -- it's the
		// one with checkmaster/checkslave wired in via "option
		// external-check" -- and "-db" since haproxy_check.cfg's "daemon"
		// directive would otherwise background the process and let PID 1
		// exit immediately (same reasoning as k8sProxyDeployment's standby
		// branch, cluster/prov_k8s_prx.go). Mirrors container#02's
		// entrypoint/command split above.
		if server.ClusterGroup.Conf.HaproxyMode == "standby" {
			svccontainer["entrypoint"] = "/bin/sh"
			svccontainer["command"] = `-c "exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg"`
		}
	}

	return svccontainer
}

func (cluster *Cluster) GetHaproxyTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *HaproxyProxy) (string, error) {

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

	//conf = conf + `post_provision = {svcmgr} -s {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.proxy
	//`
	conf = conf + prx.GetInitContainer(collector)
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerHaproxyTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + cluster.GetProxiesEnv(collector, servers, agent, prx)
	log.Println(conf)
	return conf, nil
}

func (cluster *Cluster) GetPodDockerHaproxyTemplate(collector opensvc.Collector, pod string) string {
	var vm string
	if collector.ProvProxMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = ghcr.io/opensvc/pause
rm = true

[container#20` + pod + `]
tags = pod` + pod + `
type = docker
run_image = {env.haproxy_img}
netns = container#00` + pod + `
rm = true
run_args = -v {env.base_dir}/pod` + pod + `/init/checkslave:/usr/bin/checkslave:rw
		-v {env.base_dir}/pod` + pod + `/init/checkmaster:/usr/bin/checkmaster:rw
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/etc:/usr/local/etc/haproxy:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) OpenSVCProvisionReloadHaproxyConf(Conf string) string {
	svc := cluster.OpenSVCConnect()
	svc.SetRulesetVariableValue("mariadb.svc.mrm.proxy.cnf.haproxy", "proxy_cnf_haproxy", Conf)
	return ""
}
