// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "strings"

func (cluster *Cluster) AddSeededServer(srv string) error {
	if cluster.Conf.Hosts != "" {
		cluster.Conf.Hosts = cluster.Conf.Hosts + "," + srv
	} else {
		cluster.Conf.Hosts = srv
	}
	cluster.sme.SetFailoverState()
	cluster.newServerList()
	cluster.TopologyDiscover()
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) AddDBTag(tag string) {
	cluster.DBTags = append(cluster.DBTags, tag)
	cluster.Conf.ProvTags = strings.Join(cluster.DBTags, ",")
	cluster.SetClusterVariablesFromConfig()
}

func (cluster *Cluster) AddProxyTag(tag string) {
	cluster.ProxyTags = append(cluster.ProxyTags, tag)
	cluster.Conf.ProvProxTags = strings.Join(cluster.ProxyTags, ",")
	cluster.SetClusterVariablesFromConfig()
}

func (cluster *Cluster) AddSeededProxy(prx string, srv string, port string) error {
	switch prx {
	case proxyHaproxy:
		cluster.Conf.HaproxyOn = true
		if cluster.Conf.HaproxyHosts != "" {
			cluster.Conf.HaproxyHosts = cluster.Conf.HaproxyHosts + "," + srv
		} else {
			cluster.Conf.HaproxyHosts = srv
		}
	case proxyMaxscale:
		cluster.Conf.MxsOn = true
		if cluster.Conf.MxsHost != "" {
			cluster.Conf.MxsHost = cluster.Conf.MxsHost + "," + srv
		} else {
			cluster.Conf.MxsHost = srv
		}
	case proxySqlproxy:
		cluster.Conf.ProxysqlOn = true
		if cluster.Conf.ProxysqlHosts != "" {
			cluster.Conf.ProxysqlHosts = cluster.Conf.ProxysqlHosts + "," + srv
		} else {
			cluster.Conf.ProxysqlHosts = srv
		}
	case proxySpider:
		cluster.Conf.MdbsProxyOn = true
		if cluster.Conf.ProxysqlHosts != "" {
			cluster.Conf.MdbsProxyHosts = cluster.Conf.MdbsProxyHosts + "," + srv
		} else {
			cluster.Conf.MdbsProxyHosts = srv
		}
	}
	cluster.sme.SetFailoverState()
	cluster.Lock()
	cluster.newProxyList()
	cluster.Unlock()
	cluster.sme.RemoveFailoverState()
	return nil
}
