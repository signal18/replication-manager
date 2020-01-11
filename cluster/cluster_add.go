// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/misc"
)

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

func (cluster *Cluster) AddSeededProxy(prx string, srv string, port string, user string, password string) error {
	switch prx {
	case config.ConstProxyHaproxy:
		cluster.Conf.HaproxyOn = true

		if cluster.Conf.HaproxyHosts != "" {
			cluster.Conf.HaproxyHosts = cluster.Conf.HaproxyHosts + "," + srv
		} else {
			cluster.Conf.HaproxyHosts = srv
		}
	case config.ConstProxyMaxscale:
		cluster.Conf.MxsOn = true
		cluster.Conf.MxsPort = port
		if user != "" || password != "" {
			cluster.Conf.MxsUser = user
			cluster.Conf.MxsPass = password
		}
		if cluster.Conf.MxsHost != "" {
			cluster.Conf.MxsHost = cluster.Conf.MxsHost + "," + srv
		} else {
			cluster.Conf.MxsHost = srv
		}
	case config.ConstProxySqlproxy:
		cluster.Conf.ProxysqlOn = true
		cluster.Conf.ProxysqlAdminPort = port
		if user != "" || password != "" {
			cluster.Conf.ProxysqlUser = user
			cluster.Conf.ProxysqlPassword = password
		}

		if cluster.Conf.ProxysqlHosts != "" {
			cluster.Conf.ProxysqlHosts = cluster.Conf.ProxysqlHosts + "," + srv
		} else {
			cluster.Conf.ProxysqlHosts = srv
		}
	case config.ConstProxySpider:
		if user != "" || password != "" {
			cluster.Conf.MdbsProxyUser = user + ":" + password
		}
		cluster.Conf.MdbsProxyOn = true
		if cluster.Conf.MdbsProxyHosts != "" {
			cluster.Conf.MdbsProxyHosts = cluster.Conf.MdbsProxyHosts + "," + srv + ":" + port
		} else {
			cluster.Conf.MdbsProxyHosts = srv + ":" + port
		}
	}
	cluster.sme.SetFailoverState()
	cluster.Lock()
	cluster.newProxyList()
	cluster.Unlock()
	cluster.sme.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) AddUser(user string) error {
	pass, _ := cluster.GeneratePassword()
	if _, ok := cluster.APIUsers[user]; ok {
		cluster.LogPrintf(LvlErr, "User %s already exist ", user)
	} else {
		if cluster.Conf.APIUsersExternal == "" {
			cluster.Conf.APIUsersExternal = user + ":" + pass
		} else {
			cluster.Conf.APIUsersExternal = cluster.Conf.APIUsersExternal + "," + user + ":" + pass
		}
		cluster.LoadAPIUsers()
		cluster.Save()
	}

	return nil
}

func (cluster *Cluster) AddShardingHostGroup(proxy *Proxy) error {
	for _, pr := range cluster.Proxies {
		if pr.Type == config.ConstProxySqlproxy {
			cluster.AddShardProxy(pr, proxy)
		}
	}
	return nil
}

func (cluster *Cluster) AddShardingQueryRules(schema string, table string) error {
	for _, pr := range cluster.Proxies {
		if pr.Type == config.ConstProxySqlproxy {
			var qr proxysql.QueryRule
			var qrs []proxysql.QueryRule
			qr.Id = misc.Hash(schema + "." + table)
			qr.Active = 1
			qr.Match_Pattern.String = "SELECT|DELETE|UPDATE|INSERT|REPLACE .*" + table + ".*"
			qr.Apply = 1
			qr.DestinationHostgroup.Int64 = 999
			qrs = append(qrs, qr)

			cluster.AddQueryRulesProxysql(pr, qrs)
		}
	}
	return nil
}
