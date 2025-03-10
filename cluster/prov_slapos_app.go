package cluster

import "github.com/signal18/replication-manager/cluster/app"

func (cluster *Cluster) SlapOSProvisionAppService(apl *app.App) {

}

func (cluster *Cluster) SlapOSUnprovisionAppService(apl *app.App) {

}

func (cluster *Cluster) SlapOSStartAppService(apl *app.App) error {
	apl.SetWaitStartCookie()
	return nil
}

func (cluster *Cluster) SlapOSStopAppService(apl *app.App) error {
	apl.SetWaitStopCookie()
	return nil
}
