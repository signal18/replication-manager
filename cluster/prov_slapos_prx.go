package cluster

func (cluster *Cluster) SlapOSProvisionProxyService(prx DatabaseProxy) {

}

func (cluster *Cluster) SlapOSUnprovisionProxyService(prx DatabaseProxy) {

}

func (cluster *Cluster) SlapOSStartProxyService(server DatabaseProxy) error {
	server.SetWaitStartCookie()
	return nil
}

func (cluster *Cluster) SlapOSStopProxyService(server DatabaseProxy) error {
	server.SetWaitStopCookie()
	return nil
}
