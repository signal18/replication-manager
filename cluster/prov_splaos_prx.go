package cluster

import "errors"

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

func (cluster *Cluster) SlapOSStartProxyService(server *Proxy) error {
	return errors.New("Can't start proxy")
}
func (cluster *Cluster) SlapOSStopProxyService(server *Proxy) error {
	return errors.New("Can't stop proxy")
}
