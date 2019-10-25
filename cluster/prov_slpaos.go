package cluster

func (cluster *Cluster) SlapOSProvisionCluster() error {

	err := cluster.SlapOSProvisionOneSrvPerDB()
	err = cluster.SlapOSProvisionProxies()
	return err
}

func (cluster *Cluster) SlapOSProvisionOneSrvPerDB() error {

	for _, s := range cluster.Servers {

		go cluster.SlapOSProvisionDatabaseService(s)

	}
	for _, s := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+s.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+s.Name)
			}
		}
	}

	return nil
}

func (cluster *Cluster) SlapOSConnectAPI() error {

	return nil
}

func (cluster *Cluster) SlapOSGetNodes() ([]Agent, error) {

	return nil, nil
}

func (cluster *Cluster) SlapOSUnprovision() {

	for _, db := range cluster.Servers {
		go cluster.SlapOSUnprovisionDatabaseService(db)

	}
	for _, db := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning error %s on  %s", err, db.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for database %s", db.Name)
			}
		}
	}

	for _, prx := range cluster.Proxies {
		go cluster.SlapOSUnprovisionProxyService(prx)
	}
	for _, prx := range cluster.Proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning proxy error %s on  %s", err, prx.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for proxy %s", prx.Name)
			}
		}
	}

}
