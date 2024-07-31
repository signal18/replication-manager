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
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) AddSeededServer(srv string) error {
	fmt.Printf("ADD SEEDED SERVER\n")

	if strings.Contains(cluster.Conf.Hosts, srv) {
		return errors.New("Server already exists")
	}

	hosts := strings.Split(cluster.Conf.Hosts, ",")

	//Remove empty slices
	n := 0
	for i := range hosts {
		if hosts[i] != "" {
			hosts[n] = hosts[i]
			n++
		}
	}
	hosts = hosts[:n]
	hosts = append(hosts, srv)

	newHosts := strings.Join(hosts, ",")

	cluster.StateMachine.SetFailoverState()
	cluster.SetDbServerHosts(newHosts)
	cluster.newServerList()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.TopologyDiscover(wg)
	wg.Wait()
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) AddDBTagConfig(tag string) {
	if !cluster.Configurator.HaveDBTag(tag) {
		cluster.Configurator.AddDBTag(tag)
		cluster.Conf.ProvTags = cluster.Configurator.GetConfigDBTags()
		cluster.SetClusterCredentialsFromConfig()
	}
}

func (cluster *Cluster) AddDBTag(tag string) {

	if !cluster.Configurator.HaveDBTag(tag) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Adding database tag %s ", tag)
		cluster.AddDBTagConfig(tag)
		if cluster.Conf.ProvDBApplyDynamicConfig {
			for _, srv := range cluster.Servers {
				cmd := "mariadb_command"
				if !srv.IsMariaDB() {
					cmd = "mysql_command"
				}
				srv.GetDatabaseConfig()
				_, needrestart := srv.ExecScriptSQL(strings.Split(srv.GetDatabaseDynamicConfig(tag, cmd), ";"))
				if needrestart {
					srv.SetRestartCookie()
				}
			}
		} else {
			cluster.SetDBRestartCookie()
		}
	}

}

func (cluster *Cluster) AddProxyTag(tag string) {
	cluster.Configurator.AddProxyTag(tag)
	cluster.Conf.ProvProxTags = cluster.Configurator.GetConfigProxyTags()
	cluster.SetClusterCredentialsFromConfig()
	cluster.SetProxiesRestartCookie()
}

func (cluster *Cluster) AddSeededProxy(prx string, srv string, port string, user string, password string) error {
	switch prx {
	case config.ConstProxyHaproxy:
		cluster.Conf.HaproxyOn = true
		if strings.Contains(cluster.Conf.HaproxyHosts, srv) {
			return errors.New("Proxy already exists")
		}
		if cluster.Conf.HaproxyHosts != "" {
			cluster.Conf.HaproxyHosts = cluster.Conf.HaproxyHosts + "," + srv
		} else {
			cluster.Conf.HaproxyHosts = srv
		}
	case config.ConstProxyMaxscale:
		cluster.Conf.MxsOn = true
		cluster.Conf.MxsPort = port
		if strings.Contains(cluster.Conf.MxsHost, srv) {
			return errors.New("Proxy already exists")
		}
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
		cluster.Conf.ProxysqlPort = port
		if strings.Contains(cluster.Conf.ProxysqlHosts, srv) {
			return errors.New("Proxy already exists")
		}
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
		if strings.Contains(cluster.Conf.MdbsProxyHosts, srv+":"+port) {
			return errors.New("Proxy already exists")
		}
		if user != "" || password != "" {
			cluster.Conf.MdbsProxyCredential = user + ":" + password
		}
		cluster.Conf.MdbsProxyOn = true
		if cluster.Conf.MdbsProxyHosts != "" {
			cluster.Conf.MdbsProxyHosts = cluster.Conf.MdbsProxyHosts + "," + srv + ":" + port
		} else {
			cluster.Conf.MdbsProxyHosts = srv + ":" + port
		}
	}
	cluster.SetClusterProxyCredentialsFromConfig()
	cluster.StateMachine.SetFailoverState()
	cluster.Lock()
	cluster.newProxyList()
	cluster.Unlock()
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) AddUser(user string) error {
	pass, _ := cluster.GeneratePassword()
	if _, ok := cluster.APIUsers[user]; ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "User %s already exist ", user)
	} else {
		if cluster.Conf.GetDecryptedValue("api-credentials-external") == "" {
			cluster.Conf.APIUsersExternal = user + ":" + pass

		} else {
			cluster.Conf.APIUsersExternal = cluster.Conf.GetDecryptedValue("api-credentials-external") + "," + user + ":" + pass
		}
		var new_secret config.Secret
		new_secret.Value = cluster.Conf.APIUsersExternal
		new_secret.OldValue = cluster.Conf.GetDecryptedValue("api-credentials-external")
		cluster.Conf.Secrets["api-credentials-external"] = new_secret
		cluster.LoadAPIUsers()
		cluster.Save()
	}

	return nil
}

func (cluster *Cluster) AddShardingHostGroup(proxy *MariadbShardProxy) error {
	if cluster.Conf.ClusterHead != "" {
		return nil
	}
	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*ProxySQLProxy); ok {
			cluster.AddShardProxy(pr, proxy)
		}
	}
	return nil
}

func (cluster *Cluster) AddShardingQueryRules(schema string, table string) error {
	if cluster.Conf.ClusterHead != "" {
		return nil
	}
	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*ProxySQLProxy); ok {
			var qr proxysql.QueryRule
			var qrs []proxysql.QueryRule
			qr.Id = misc.Hash("dml." + schema + "." + table)
			qr.Active = 1
			qr.Match_Pattern.String = "SELECT|DELETE|UPDATE|INSERT|REPLACE .* " + table + " .*"
			qr.Apply = 1
			qr.DestinationHostgroup.Int64 = 999
			qrs = append(qrs, qr)
			pr.AddQueryRulesProxysql(qrs)
		}
	}
	return nil
}
