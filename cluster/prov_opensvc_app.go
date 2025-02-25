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

func (cluster *Cluster) OpenSVCGetAppicationContainerSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if app.ClusterGroup.Conf.ProvProxType == "docker" || app.ClusterGroup.Conf.ProvProxType == "podman" || app.ClusterGroup.Conf.ProvProxType == "oci" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["image"] = "{env.app_img}"
		svccontainer["rm"] = "true"
		svccontainer["type"] = app.ClusterGroup.Conf.ProvType
		svccontainer["run_args"] = app.ClusterGroup.Conf.ProvAppDockerRunArgs
		svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}/data:/var/lib/" + app.Name + ":rw {name}/etc/" + app.Name + ":/etc/" + app.Name + ":rw"
		svccontainer["environment"] = ""
	}

	return svccontainer
}

func (cluster *Cluster) GetAppTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, app *App) (string, error) {

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
	conf = conf + app.GetInitContainer(collector)
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerAppTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	conf = conf + cluster.GetAppEnv(collector, servers, agent, app)
	log.Println(conf)
	return conf, nil
}
