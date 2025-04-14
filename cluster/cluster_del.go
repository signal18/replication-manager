// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) RemoveServerFromIndex(index int) {
	newServers := make([]*ServerMonitor, 0)
	newServers = append(newServers, cluster.Servers[:index]...)
	newServers = append(newServers, cluster.Servers[index+1:]...)
	cluster.Servers = newServers
}

func (cluster *Cluster) RemoveServerMonitor(host string, port string) error {
	newServers := make([]*ServerMonitor, 0)
	newList := make([]string, 0)
	index := -1
	//Find the index
	for i, srv := range cluster.Servers {

		//Skip the server
		if srv.Host == host && srv.Port == port {
			index = i
			continue
		}

		newServers = append(newServers, srv)
		newList = append(newList, srv.HostCnf)
	}

	if index >= 0 {
		cluster.Conf.Hosts = strings.Join(newList, ",")
		cluster.StateMachine.SetFailoverState()
		cluster.Lock()
		cluster.Servers = newServers
		cluster.Unlock()
		cluster.StateMachine.RemoveFailoverState()
	} else {
		return errors.New(fmt.Sprintf("Host with address %s:%s not found in cluster!", host, port))
	}
	return nil
}

func (cluster *Cluster) CancelRollingRestart() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API receive cancel rolling restart")
	for _, pr := range cluster.Proxies {
		pr.DelRestartCookie()
	}
	for _, db := range cluster.Servers {
		db.DelRestartCookie()
	}
	return nil
}

func (cluster *Cluster) CancelRollingReprov() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API receive cancel rolling re-provision")
	for _, pr := range cluster.Proxies {
		pr.DelReprovisionCookie()
	}
	for _, db := range cluster.Servers {
		db.DelReprovisionCookie()
	}
	return nil
}

func (cluster *Cluster) DropDBTag(dtag string) {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Dropping database tag %s ", dtag)
	if cluster.Conf.ProvDBApplyDynamicConfig {
		for _, srv := range cluster.Servers {
			cmd := "mariadb_default"
			if !srv.IsMariaDB() {
				cmd = "mysql_default"
			}
			srv.GetDatabaseConfig()
			_, needrestart := srv.ExecScriptSQL(strings.Split(srv.GetDatabaseDynamicConfig(dtag, cmd), ";"))
			if needrestart {
				srv.SetRestartCookie()
			}
		}
	}
	changed := cluster.DropDBTagConfig(dtag)
	if changed && !cluster.Conf.ProvDBApplyDynamicConfig {
		cluster.SetDBRestartCookie()
	}

}

func (cluster *Cluster) DropDBTagConfig(dtag string) bool {
	changed := cluster.Configurator.DropDBTag(dtag)
	cluster.Conf.ProvTags = strings.Join(cluster.Configurator.GetDBTags(), ",")
	cluster.SetClusterCredentialsFromConfig()
	return changed
}

func (cluster *Cluster) DropProxyTag(dtag string) {

	cluster.Configurator.DropProxyTag(dtag)
	cluster.Conf.ProvProxTags = strings.Join(cluster.Configurator.GetProxyTags(), ",")
	cluster.SetClusterCredentialsFromConfig()
	cluster.SetProxiesRestartCookie()
}

func (cluster *Cluster) RemoveProxyMonitor(prx string, host string, port string) error {
	newProxies := make([]DatabaseProxy, 0)
	index := -1
	var prxhost string
	for i, pr := range cluster.Proxies {
		if pr.GetHost() == host && pr.GetPort() == port {
			index = i
			prxhost = pr.GetName() // use name since host might be altered by provNetCNI
			break                  // found the proxy
		}
	}
	if index >= 0 {
		cluster.StateMachine.SetFailoverState()
		cluster.Lock()
		if len(cluster.Proxies) > 1 {
			newProxies = append(cluster.Proxies[:index], cluster.Proxies[index+1:]...)
		}
		cluster.Proxies = newProxies

		switch prx {
		case config.ConstProxyHaproxy:
			cluster.Conf.HaproxyHosts = misc.RemoveFromList(cluster.Conf.HaproxyHosts, prxhost)
		case config.ConstProxyMaxscale:
			cluster.Conf.MxsHost = misc.RemoveFromList(cluster.Conf.MxsHost, prxhost)
		case config.ConstProxySqlproxy:
			cluster.Conf.ProxysqlHosts = misc.RemoveFromList(cluster.Conf.ProxysqlHosts, prxhost)
		case config.ConstProxySpider:
			cluster.Conf.MdbsProxyHosts = misc.RemoveFromList(cluster.Conf.MdbsProxyHosts, prxhost)
		}
		cluster.Unlock()
		cluster.StateMachine.RemoveFailoverState()
	} else {
		return errors.New(fmt.Sprintf("Proxy host with address %s:%s not found in cluster!", host, port))
	}

	return nil
}

func (cluster *Cluster) DelWaitDBACredCookie() {
	for _, srv := range cluster.Servers {
		srv.DelWaitDBACredCookie()
	}
}

func (cluster *Cluster) DelWaitSponsorCredCookie() {
	for _, srv := range cluster.Servers {
		srv.DelWaitSponsorCredCookie()
	}
}
