package cluster

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
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
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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

			err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)))
			if err != nil {
				cluster.LogPrintf(LvlWarn, "ProxySQL could not add server %s (%s)", s.URL, err)
			}
			if s.State == stateMaster {
				if cluster.Conf.ProxysqlMasterIsReader {
					psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port)
				} else {
					psql.SetWriter(misc.Unbracket(s.Host), s.Port)
				}
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
	psql.SaveServersToDisk()

}

func (cluster *Cluster) failoverProxysql(proxy *Proxy) {
	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}

	defer psql.Connection.Close()
	for _, s := range cluster.Servers {
		if s.State == stateUnconn || s.IsIgnored() {
			err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.LogPrintf(LvlErr, "ProxySQL could not set server %s offline (%s)", s.URL, err)
			}
		}
		if s.IsMaster() {
			err = psql.ReplaceWriter(misc.Unbracket(s.Host), s.Port, misc.Unbracket(cluster.oldMaster.Host), cluster.oldMaster.Port, cluster.Conf.ProxysqlMasterIsReader)
			if err != nil {
				cluster.LogPrintf(LvlErr, "ProxySQL could not set server %s Master (%s)", s.URL, err)
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
	err = psql.SaveServersToDisk()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not save servers to disk (%s)", err)
	}
}

func (cluster *Cluster) refreshProxysql(proxy *Proxy) error {
	if cluster.Conf.ProxysqlOn == false {
		return nil
	}

	psql, err := connectProxysql(proxy)
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
			cluster.LogPrintf(LvlDbg, "ProxySQL setting online rejoining server %s", s.URL)
			err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.sme.AddState("ERR00069", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00069"], s.URL, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
			}
			updated = true
		}

		// if server is Standalone, set offline in ProxySQL
		if s.State == stateUnconn && bke.PrxStatus == "ONLINE" {
			cluster.LogPrintf(LvlDbg, "ProxySQL setting offline standalone server %s", s.URL)
			err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.sme.AddState("ERR00070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})

			}
			updated = true

			// if the server comes back from a previously failed or standalone state, reintroduce it in
			// the appropriate HostGroup
		} else if s.PrevState == stateUnconn || s.PrevState == stateFailed {
			if s.State == stateMaster {
				err = psql.SetWriter(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.sme.AddState("ERR00071", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			} else if s.IsSlave && !s.IsIgnored() {
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.sme.AddState("ERR00072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00072"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			}
		}
		// load the grants
		if s.IsMaster() && cluster.Conf.ProxysqlCopyGrants {
			myprxusermap, _, err := dbhelper.GetProxySQLUsers(psql.Connection)
			if err != nil {
				cluster.sme.AddState("ERR00053", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00053"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
			}
			uniUsers := make(map[string]dbhelper.Grant)
			dupUsers := make(map[string]string)

			for _, u := range s.Users {
				user, ok := uniUsers[u.User+":"+u.Password]
				if ok {
					dupUsers[user.User] = user.User
					cluster.sme.AddState("ERR00057", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00057"], user.User), ErrFrom: "MON", ServerUrl: proxy.Name})
				} else {
					if u.Password != "" {
						uniUsers[u.User+":"+u.Password] = u
					}
				}
			}

			for _, user := range uniUsers {
				if _, ok := myprxusermap[user.User+":"+user.Password]; !ok {
					cluster.LogPrintf(LvlInfo, "Add ProxySQL user %s ", user.User)
					err := psql.AddUser(user.User, user.Password)
					if err != nil {
						cluster.sme.AddState("ERR00054", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00054"], err), ErrFrom: "MON", ServerUrl: proxy.Name})

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
		cluster.sme.AddState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
	}
	proxy.Variables, err = psql.GetVariables()
	if err != nil {
		cluster.sme.AddState("WARN0098", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0098"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
	}
	if proxy.ClusterGroup.Conf.ProxysqlBootstrapVariables {
		if proxy.Variables["MYSQL-MULTIPLEXING"] == "TRUE" && !proxy.ClusterGroup.Conf.ProxysqlMultiplexing {
			psql.SetMySQLVariable("MYSQL-MULTIPLEXING", "FALSE")
			psql.LoadMySQLVariablesToRuntime()
			psql.SaveMySQLVariablesToDisk()
		}
		if proxy.Variables["MYSQL-MULTIPLEXING"] == "FALSE" && proxy.ClusterGroup.Conf.ProxysqlMultiplexing {
			psql.SetMySQLVariable("MYSQL-MULTIPLEXING", "TRUE")
			psql.LoadMySQLVariablesToRuntime()
			psql.SaveMySQLVariablesToDisk()
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
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
