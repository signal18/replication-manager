// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "github.com/signal18/replication-manager/config"

func (cluster *Cluster) LocalhostProvisionProxyService(pri DatabaseProxy) error {
	pri.GetProxyConfig()

	if prx, ok := pri.(*MariadbShardProxy); ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Bootstrap MariaDB Sharding Cluster")
		srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.GetPort(), prx.User, prx.Pass, true, "")
		err := srv.Refresh()
		if err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Can connect to requested signal18 sharding proxy")
			//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
			cluster.errorChan <- err
			return nil
		}
		srv.ClusterGroup = cluster
		err = cluster.LocalhostProvisionDatabaseService(srv)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Bootstrap MariaDB Sharding Cluster Failed")
			cluster.errorChan <- err
			return err
		}
		srv.Close()
		cluster.ShardProxyBootstrap(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		err := cluster.LocalhostProvisionProxySQLService(prx)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Bootstrap Proxysql Failed")
			cluster.errorChan <- err
			return err
		}
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		err := cluster.LocalhostProvisionHaProxyService(prx)
		cluster.errorChan <- err
		return err
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionProxyService(pri DatabaseProxy) error {
	if prx, ok := pri.(*MariadbShardProxy); ok {
		cluster.LocalhostUnprovisionDatabaseService(prx.ShardProxy)
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.LocalhostUnprovisionHaProxyService(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.LocalhostUnprovisionProxySQLService(prx)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStartProxyService(pri DatabaseProxy) error {
	if prx, ok := pri.(*MariadbShardProxy); ok {
		prx.ShardProxy.Shutdown()
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.LocalhostStartHaProxyService(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.LocalhostStartProxySQLService(prx)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopProxyService(pri DatabaseProxy) error {
	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.LocalhostStopHaProxyService(prx)
	}
	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.LocalhostStopProxySQLService(prx)
	}

	return nil
}
