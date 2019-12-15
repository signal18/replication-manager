package cluster

import "errors"

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
