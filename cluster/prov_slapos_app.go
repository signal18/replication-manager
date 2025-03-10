package cluster

import "github.com/signal18/replication-manager/cluster/app"

func (cluster *Cluster) SlapOSProvisionAppService(appi app.AppInterface) {

}

func (cluster *Cluster) SlapOSUnprovisionAppService(appi app.AppInterface) {

}

func (cluster *Cluster) SlapOSStartAppService(server app.AppInterface) error {
	server.SetWaitStartCookie()
	return nil
}

func (cluster *Cluster) SlapOSStopAppService(server app.AppInterface) error {
	server.SetWaitStopCookie()
	return nil
}
