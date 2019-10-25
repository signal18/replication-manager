package cluster

func (cluster *Cluster) SlapOSProvisionProxies() error {

	for _, prx := range cluster.Proxies {
		cluster.SlapOSProvisionProxyService(prx)
	}

	return nil
}

func (cluster *Cluster) SlapOSProvisionProxyService(prx *Proxy) {

}

func (cluster *Cluster) SlapOSUnprovisionProxyService(prx *Proxy) {

}
