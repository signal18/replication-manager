// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"

	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) LocalhostProvisionProxyService(prx *Proxy) error {
	prx.GetProxyConfig()
	if prx.Type == config.ConstProxySpider {
		cluster.LogPrintf(LvlInfo, "Bootstrap MariaDB Sharding Cluster")
		srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, true, "")
		err := srv.Refresh()
		if err == nil {
			cluster.LogPrintf(LvlWarn, "Can connect to requested signal18 sharding proxy")
			//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
			cluster.errorChan <- err
			return nil
		}
		srv.ClusterGroup = cluster
		err = cluster.LocalhostProvisionDatabaseService(srv)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Bootstrap MariaDB Sharding Cluster Failed")
			cluster.errorChan <- err
			return err
		}
		srv.Close()
		cluster.ShardProxyBootstrap(prx)
	}
	if prx.Type == config.ConstProxySqlproxy {
		err := cluster.LocalhostProvisionProxySQLService(prx)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Bootstrap Proxysql Failed")
			cluster.errorChan <- err
			return err
		}
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionProxyService(prx *Proxy) error {

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStartProxyService(server *Proxy) error {
	return errors.New("Can't start proxy")
}
func (cluster *Cluster) LocalhostStopProxyService(server *Proxy) error {
	return errors.New("Can't stop proxy")
}
