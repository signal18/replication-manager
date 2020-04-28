package cluster

func (cluster *Cluster) SlapOSProvisionProxyService(prx *Proxy) {

}

func (cluster *Cluster) SlapOSUnprovisionProxyService(prx *Proxy) {

}

func (cluster *Cluster) SlapOSStartProxyService(server *Proxy) error {
	server.SetWaitStartCookie()
	return nil
}

func (cluster *Cluster) SlapOSStopProxyService(server *Proxy) error {
	server.SetWaitStopCookie()
	return nil
}
