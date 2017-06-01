package cluster

import (
	"strings"

	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/opensvc"
)

func (cluster *Cluster) OpenSVCProvision() error {

	var svc opensvc.Collector
	svc.Host, svc.Port = misc.SplitHostPort(cluster.conf.ProvHost)
	svc.User, svc.Pass = misc.SplitPair(cluster.conf.ProvAdminUser)
	svc.RplMgrUser, svc.RplMgrPassword = misc.SplitPair(cluster.conf.ProvUser)
	servers := cluster.GetServers()
	var iplist []string
	for _, s := range servers {
		iplist = append(iplist, s.Host)
	}
	svc.ProvAgents = cluster.conf.ProvAgents
	svc.ProvTemplate = cluster.conf.ProvTemplate
	svc.ProvMem = cluster.conf.ProvMem
	svc.ProvPwd = cluster.GetDbPass()
	svc.ProvIops = cluster.conf.ProvIops
	svc.ProvDisk = cluster.conf.ProvDisk
	svc.ProvNetMask = cluster.conf.ProvNetmask
	svc.ProvNetGateway = cluster.conf.ProvGateway
	if svc.IsServiceBootstrap(cluster.GetName()) == false {
		// create template && bootstrap
		agents := svc.GetNodes()
		var clusteragents []opensvc.Host

		for _, node := range agents {
			if strings.Contains(svc.ProvAgents, node.Node_name) {
				clusteragents = append(clusteragents, node)
			}
		}
		res, err := svc.GenerateTemplate(iplist, clusteragents)
		if err != nil {
			return err
		}

		idtemplate, _ := svc.CreateTemplate(cluster.GetName(), res)

		for _, node := range agents {
			if strings.Contains(svc.ProvAgents, node.Node_name) {
				svc.ProvisionTemplate(idtemplate, node.Node_id, cluster.GetName())
			}
		}
	}
	return nil
}
