package cluster

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
)

func connectProxysql(proxy *Proxy) (proxysql.ProxySQL, error) {
	psql := proxysql.ProxySQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
		WriterHG: fmt.Sprintf("%d", proxy.WriterHostgroup),
		ReaderHG: fmt.Sprintf("%d", proxy.ReaderHostgroup),
	}

	var err error
	err = psql.Connect()
	if err != nil {
		return psql, err
	}
	return psql, nil
}

func (cluster *Cluster) AddShardProxy(proxysql *Proxy, shardproxy *Proxy) {
	if cluster.Conf.ProxysqlOn == false {
		return
	}
	psql, err := connectProxysql(proxysql)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		return
	}
	defer psql.Connection.Close()
	psql.AddShardServer(misc.Unbracket(shardproxy.Host), shardproxy.Port)

}

func (cluster *Cluster) AddQueryRulesProxysql(proxy *Proxy, rules []proxysql.QueryRule) error {
	if cluster.Conf.ProxysqlOn == false {
		return errors.New("No proxysql enable in config")
	}
	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		return err
	}
	defer psql.Connection.Close()
	err = psql.AddQueryRules(rules)
	return err
}

func (cluster *Cluster) initProxysql(proxy *Proxy) {
	if !cluster.Conf.ProxysqlBootstrap || !cluster.Conf.ProxysqlOn {
		return
	}

	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		return
	}
	defer psql.Connection.Close()

	if cluster.Conf.ProxysqlBootstrapHG {
		psql.AddHostgroups(cluster.Name)
	}
	for _, s := range cluster.Servers {

		if s.State == stateUnconn || s.IsIgnored() {
			err = psql.AddOfflineServer(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.LogPrintf(LvlErr, "ProxySQL could not add server %s as offline (%s)", s.URL, err)
			}
		} else {
			//weight string, max_replication_lag string, max_connections string, compression string

			if s.State == stateMaster {
				err = psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL could not add writer %s (%s) ", s.URL, err)
				}
				if cluster.Conf.ProxysqlMasterIsReader {
					err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)))
					if err != nil {
						cluster.LogPrintf(LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
					}
				}
			} else {
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)))
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}
			}
			if cluster.Conf.LogLevel > 2 {
				cluster.LogPrintf(LvlWarn, "ProxySQL init backend  %s with state %s ", s.URL, s.State)
			}

		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
	if proxy.ClusterGroup.Conf.ProxysqlSaveToDisk {
		psql.SaveServersToDisk()
	}
}

func (cluster *Cluster) failoverProxysql(proxy *Proxy) {
	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		return
	}

	defer psql.Connection.Close()
	for _, s := range cluster.Servers {
		if s.State == stateUnconn || s.IsIgnored() {
			err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Failover ProxySQL could not set server %s offline (%s)", s.URL, err)
			} else {
				cluster.LogPrintf(LvlInfo, "Failover ProxySQL set server %s offline", s.URL)
			}
		}
		if s.IsMaster() && !s.IsRelay {
			err = psql.ReplaceWriter(misc.Unbracket(s.Host), s.Port, misc.Unbracket(cluster.oldMaster.Host), cluster.oldMaster.Port, cluster.Conf.ProxysqlMasterIsReader)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Failover ProxySQL could not set server %s Master (%s)", s.URL, err)
			} else {
				cluster.LogPrintf(LvlInfo, "Failover ProxySQL set server %s master", s.URL)
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failover ProxySQL could not load servers to runtime (%s)", err)
	}
	if proxy.ClusterGroup.Conf.ProxysqlSaveToDisk {
		err = psql.SaveServersToDisk()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Failover ProxySQL could not save servers to disk (%s)", err)
		}
	}

}

func (cluster *Cluster) refreshProxysql(proxy *Proxy) error {
	if cluster.Conf.ProxysqlOn == false {
		return nil
	}

	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		cluster.sme.CopyOldStateFromUnknowServer(proxy.Name)
		return err
	}
	defer psql.Connection.Close()
	proxy.Version = psql.GetVersion()

	var updated bool
	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	for _, s := range cluster.Servers {
		proxysqlHostgroup, proxysqlServerStatus, proxysqlServerConnections, proxysqlByteOut, proxysqlByteIn, proxysqlLatency, err := psql.GetStatsForHostWrite(misc.Unbracket(s.Host), s.Port)
		var bke = Backend{
			Host:           s.Host,
			Port:           s.Port,
			Status:         s.State,
			PrxName:        s.URL,
			PrxStatus:      proxysqlServerStatus,
			PrxConnections: strconv.Itoa(proxysqlServerConnections),
			PrxByteIn:      strconv.Itoa(proxysqlByteOut),
			PrxByteOut:     strconv.Itoa(proxysqlByteIn),
			PrxLatency:     strconv.Itoa(proxysqlLatency),
			PrxHostgroup:   proxysqlHostgroup,
		}

		s.MxsServerName = s.URL
		s.ProxysqlHostgroup = proxysqlHostgroup
		s.MxsServerStatus = proxysqlServerStatus

		if err != nil {

			s.MxsServerStatus = "REMOVED"
			bke.PrxStatus = "REMOVED"
		} else {
			proxy.BackendsWrite = append(proxy.BackendsWrite, bke)
		}
		rproxysqlHostgroup, rproxysqlServerStatus, rproxysqlServerConnections, rproxysqlByteOut, rproxysqlByteIn, rproxysqlLatency, err := psql.GetStatsForHostRead(misc.Unbracket(s.Host), s.Port)
		var bkeread = Backend{
			Host:           s.Host,
			Port:           s.Port,
			Status:         s.State,
			PrxName:        s.URL,
			PrxStatus:      rproxysqlServerStatus,
			PrxConnections: strconv.Itoa(rproxysqlServerConnections),
			PrxByteIn:      strconv.Itoa(rproxysqlByteOut),
			PrxByteOut:     strconv.Itoa(rproxysqlByteIn),
			PrxLatency:     strconv.Itoa(rproxysqlLatency),
			PrxHostgroup:   rproxysqlHostgroup,
		}
		if err == nil {
			proxy.BackendsRead = append(proxy.BackendsRead, bkeread)
		}
		// if ProxySQL and replication-manager states differ, resolve the conflict
		if bke.PrxStatus == "OFFLINE_HARD" && s.State == stateSlave && !s.IsIgnored() {
			cluster.LogPrintf(LvlDbg, "Monitor ProxySQL setting online rejoining server %s", s.URL)
			err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.SetSugarState("ERR00069", "PRX", proxy.Name, s.URL, err)
			}
			updated = true
		}

		// if server is Standalone, set offline in ProxySQL
		if s.State == stateUnconn && bke.PrxStatus == "ONLINE" {
			cluster.LogPrintf(LvlDbg, "Monitor ProxySQL setting offline standalone server %s", s.URL)
			err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.SetSugarState("ERR00070", "PRX", proxy.Name, s.URL, err)

			}
			updated = true

			// if the server comes back from a previously failed or standalone state, reintroduce it in
			// the appropriate HostGroup
		} else if s.PrevState == stateUnconn || s.PrevState == stateFailed {
			if s.State == stateMaster {
				cluster.LogPrintf(LvlDbg, "Monitor ProxySQL setting writer standalone server %s", s.URL)
				err = psql.SetWriter(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					// NOTE: this one had AddState ERR00071 but threw the error from ERR00070, which is what I wanted to prevent
					// with the SetSugarState
					cluster.SetSugarState("ERR00071", "PRX", proxy.Name, s.URL, err)
				}
				updated = true
			} else if s.IsSlave && !s.IsIgnored() {
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				cluster.LogPrintf(LvlDbg, "Monitor ProxySQL setting reader standalone server %s", s.URL)
				if err != nil {
					cluster.SetSugarState("ERR00072", "PRX", proxy.Name, s.URL, err)
				}
				updated = true
			}
		}
		// load the grants
		if s.IsMaster() && cluster.Conf.ProxysqlCopyGrants {
			myprxusermap, _, err := dbhelper.GetProxySQLUsers(psql.Connection)
			if err != nil {
				cluster.SetSugarState("ERR00053", "PRX", proxy.Name, err)
			}
			uniUsers := make(map[string]dbhelper.Grant)
			dupUsers := make(map[string]string)

			for _, u := range s.Users {
				user, ok := uniUsers[u.User+":"+u.Password]
				if ok {
					dupUsers[user.User] = user.User
					cluster.SetSugarState("ERR00057", "MON", proxy.Name, user.User)
				} else {
					if u.Password != "" && u.Password != "invalid" {
						if u.User != cluster.dbUser {
							uniUsers[u.User+":"+u.Password] = u
						} else if cluster.Conf.MonitorWriteHeartbeatCredential == "" {
							//  load the repman DB user in proxy beacause we don't have an extra user to query master
							uniUsers[u.User+":"+u.Password] = u
						}
					}
				}
			}

			for _, user := range uniUsers {
				if _, ok := myprxusermap[user.User+":"+user.Password]; !ok {
					cluster.LogPrintf(LvlInfo, "Add ProxySQL user %s ", user.User)

					err := psql.AddUser(user.User, user.Password)
					if err != nil {
						cluster.SetSugarState("ERR00054", "MON", proxy.Name, err)

					}
				}
			}
		}
	}
	if updated {
		err = psql.LoadServersToRuntime()
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
		}
	}
	proxy.QueryRules, err = psql.GetQueryRulesRuntime()
	if err != nil {
		cluster.SetSugarState("WARN0092", "MON", proxy.Name, err)
	}
	proxy.Variables, err = psql.GetVariables()
	if err != nil {
		cluster.SetSugarState("WARN0098", "MON", proxy.Name, err)
	}
	if proxy.ClusterGroup.Conf.ProxysqlBootstrapVariables {
		if proxy.Variables["MYSQL-MULTIPLEXING"] == "TRUE" && !proxy.ClusterGroup.Conf.ProxysqlMultiplexing {
			psql.SetMySQLVariable("MYSQL-MULTIPLEXING", "FALSE")
			psql.LoadMySQLVariablesToRuntime()
			if proxy.ClusterGroup.Conf.ProxysqlSaveToDisk {
				psql.SaveMySQLVariablesToDisk()
			}
		}
		if proxy.Variables["MYSQL-MULTIPLEXING"] == "FALSE" && proxy.ClusterGroup.Conf.ProxysqlMultiplexing {

			psql.SetMySQLVariable("MYSQL-MULTIPLEXING", "TRUE")
			psql.LoadMySQLVariablesToRuntime()
			if proxy.ClusterGroup.Conf.ProxysqlSaveToDisk {
				psql.SaveMySQLVariablesToDisk()
			}
		}
	}
	return nil
}

func (cluster *Cluster) setMaintenanceProxysql(proxy *Proxy, s *ServerMonitor) {
	if cluster.Conf.ProxysqlOn == false {
		return
	}

	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.SetSugarState("ERR00051", "MON", "", err)
		return
	}
	defer psql.Connection.Close()

	if s.IsMaintenance {
		err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not set %s:%s as offline_soft (%s)", s.Host, s.Port, err)
		}
	} else {
		err = psql.SetOnline(misc.Unbracket(s.Host), s.Port)
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not set %s:%s as online (%s)", s.Host, s.Port, err)
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
}
