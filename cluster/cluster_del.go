// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "strings"

func (cluster *Cluster) RemoveServerFromIndex(index int) {
	newServers := make([]*ServerMonitor, 0)
	newServers = append(newServers, cluster.Servers[:index]...)
	newServers = append(newServers, cluster.Servers[index+1:]...)
	cluster.Servers = newServers
}

func (cluster *Cluster) CancelRollingRestart() error {
	cluster.LogPrintf(LvlInfo, "API receive cancel rolling restart")
	for _, pr := range cluster.Proxies {
		pr.DelRestartCookie()
	}
	for _, db := range cluster.Servers {
		db.DelRestartCookie()
	}
	return nil
}

func (cluster *Cluster) CancelRollingReprov() error {
	cluster.LogPrintf(LvlInfo, "API receive cancel rolling re-provision")
	for _, pr := range cluster.Proxies {
		pr.DelReprovisionCookie()
	}
	for _, db := range cluster.Servers {
		db.DelReprovisionCookie()
	}
	return nil
}

func (cluster *Cluster) DropDBTag(dtag string) {

	cluster.LogPrintf(LvlInfo, "Dropping database tag %s ", dtag)
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
	cluster.SetClusterVariablesFromConfig()
	return changed
}

func (cluster *Cluster) DropProxyTag(dtag string) {

	cluster.Configurator.DropProxyTag(dtag)
	cluster.Conf.ProvProxTags = strings.Join(cluster.Configurator.GetProxyTags(), ",")
	cluster.SetClusterVariablesFromConfig()
	cluster.SetProxiesRestartCookie()
}
