// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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

	switch prx.Type {
	case config.ConstProxySpider:
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

	case config.ConstProxySqlproxy:
		err := cluster.LocalhostProvisionProxySQLService(prx)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Bootstrap Proxysql Failed")
			cluster.errorChan <- err
			return err
		}
	case config.ConstProxyHaproxy:
		err := cluster.LocalhostProvisionHaProxyService(prx)
		cluster.errorChan <- err
		return err
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionProxyService(prx *Proxy) error {
	switch prx.Type {
	case config.ConstProxySpider:
		cluster.LocalhostUnprovisionDatabaseService(prx.ShardProxy)
	case config.ConstProxySphinx:

	case config.ConstProxyHaproxy:
		cluster.LocalhostUnprovisionHaProxyService(prx)
	case config.ConstProxySqlproxy:
		cluster.LocalhostUnprovisionProxySQLService(prx)
	case config.ConstProxyMaxscale:

	default:
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStartProxyService(prx *Proxy) error {
	switch prx.Type {
	case config.ConstProxySpider:
		prx.ShardProxy.Shutdown()
	case config.ConstProxySphinx:

	case config.ConstProxyHaproxy:
		cluster.LocalhostStartHaProxyService(prx)
	case config.ConstProxySqlproxy:
		cluster.LocalhostStartProxySQLService(prx)
	case config.ConstProxyMaxscale:

	default:
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopProxyService(prx *Proxy) error {
	switch prx.Type {
	case config.ConstProxySpider:

	case config.ConstProxySphinx:

	case config.ConstProxyHaproxy:
		cluster.LocalhostStartHaProxyService(prx)
	case config.ConstProxySqlproxy:
		cluster.LocalhostStartProxySQLService(prx)
	case config.ConstProxyMaxscale:

	default:
		return errors.New("Can't stop proxy")
	}
	return nil
}
