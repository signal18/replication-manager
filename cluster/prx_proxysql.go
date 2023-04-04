package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type ProxySQLProxy struct {
	Proxy
}

func NewProxySQLProxy(placement int, cluster *Cluster, proxyHost string) *ProxySQLProxy {
	conf := cluster.Conf
	prx := new(ProxySQLProxy)
	prx.Name = proxyHost
	prx.Host = proxyHost
	prx.Type = config.ConstProxySqlproxy
	prx.Port = conf.ProxysqlAdminPort
	prx.ReadWritePort, _ = strconv.Atoi(conf.ProxysqlPort)
	prx.User = cluster.proxysqlUser
	prx.Pass = cluster.proxysqlPass
	prx.ReaderHostgroup, _ = strconv.Atoi(conf.ProxysqlReaderHostgroup)
	prx.WriterHostgroup, _ = strconv.Atoi(conf.ProxysqlWriterHostgroup)
	prx.WritePort, _ = strconv.Atoi(conf.ProxysqlPort)
	prx.ReadPort, _ = strconv.Atoi(conf.ProxysqlPort)

	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSProxySQLPartitions, conf.ProxysqlHostsIPV6)

	if conf.ProvNetCNI {
		if conf.ClusterHead == "" {
			prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
		} else {
			prx.Host = prx.Host + "." + conf.ClusterHead + ".svc." + conf.ProvOrchestratorCluster
		}
	}

	if cluster.key != nil {
		p := crypto.Password{Key: cluster.key}
		p.CipherText = prx.Pass
		p.Decrypt()
		prx.Pass = p.PlainText
	}

	return prx
}

func (proxy *ProxySQLProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.ProxysqlOn, "proxysql", false, "Use ProxySQL")
	flags.BoolVar(&conf.ProxysqlDebug, "proxysql-debug", false, "Extra info on monitoring backend")
	flags.BoolVar(&conf.ProxysqlSaveToDisk, "proxysql-save-to-disk", false, "Save proxysql change to sqllight")
	flags.StringVar(&conf.ProxysqlHosts, "proxysql-servers", "", "ProxySQL hosts")
	flags.StringVar(&conf.ProxysqlHostsIPV6, "proxysql-servers-ipv6", "", "ProxySQL extra IPV6 bind for interfaces")
	flags.StringVar(&conf.ProxysqlPort, "proxysql-port", "3306", "ProxySQL read/write proxy port")
	flags.StringVar(&conf.ProxysqlAdminPort, "proxysql-admin-port", "6032", "ProxySQL admin interface port")
	flags.StringVar(&conf.ProxysqlReaderHostgroup, "proxysql-reader-hostgroup", "1", "ProxySQL reader hostgroup")
	flags.StringVar(&conf.ProxysqlWriterHostgroup, "proxysql-writer-hostgroup", "0", "ProxySQL writer hostgroup")
	flags.StringVar(&conf.ProxysqlUser, "proxysql-user", "admin", "ProxySQL admin user")
	flags.StringVar(&conf.ProxysqlPassword, "proxysql-password", "admin", "ProxySQL admin password")
	flags.BoolVar(&conf.ProxysqlCopyGrants, "proxysql-bootstrap-users", true, "Copy users from master")
	flags.BoolVar(&conf.ProxysqlMultiplexing, "proxysql-multiplexing", false, "Multiplexing")
	flags.BoolVar(&conf.ProxysqlBootstrap, "proxysql-bootstrap", false, "Bootstrap ProxySQL backend servers and hostgroup")
	flags.BoolVar(&conf.ProxysqlBootstrapVariables, "proxysql-bootstrap-variables", false, "Bootstrap ProxySQL backend servers and hostgroup")
	flags.BoolVar(&conf.ProxysqlBootstrapHG, "proxysql-bootstrap-hostgroups", false, "Bootstrap ProxySQL hostgroups")
	flags.BoolVar(&conf.ProxysqlBootstrapQueryRules, "proxysql-bootstrap-query-rules", false, "Bootstrap Query rules into ProxySQL")
	flags.StringVar(&conf.ProxysqlBinaryPath, "proxysql-binary-path", "/usr/sbin/proxysql", "proxysql binary location")
}

func (proxy *ProxySQLProxy) Connect() (proxysql.ProxySQL, error) {
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

func (cluster *Cluster) AddShardProxy(proxysql *ProxySQLProxy, shardproxy *MariadbShardProxy) {
	proxysql.AddShardProxy(shardproxy)
}

func (proxy *ProxySQLProxy) UseSSL() string {
	UseSSL := "0"
	if proxy.ClusterGroup.Configurator.HaveDBTag("ssl") {
		UseSSL = "1"
	}
	return UseSSL
}

func (proxy *ProxySQLProxy) AddShardProxy(shardproxy *MariadbShardProxy) {
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProxysqlOn == false {
		return
	}
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()
	psql.AddShardServer(misc.Unbracket(shardproxy.Host), shardproxy.Port, proxy.UseSSL())
}

func (proxy *ProxySQLProxy) AddQueryRulesProxysql(rules []proxysql.QueryRule) error {
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProxysqlOn == false {
		return errors.New("No proxysql enable in config")
	}
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return err
	}
	defer psql.Connection.Close()
	err = psql.AddQueryRules(rules)
	return err
}

func (proxy *ProxySQLProxy) Init() {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.ProxysqlBootstrap || !cluster.Conf.ProxysqlOn {
		return
	}

	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	if cluster.Conf.ProxysqlBootstrapHG {
		psql.AddHostgroups(cluster.Name)
	}
	//	proxy.Refresh()
	//	return
	for _, s := range cluster.Servers {

		if s.State == stateUnconn || s.IsIgnored() {
			cluster.LogPrintf(LvlErr, "ProxySQL add backend %s as offline (%s)", s.URL, err)
			err = psql.AddOfflineServer(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
			if err != nil {
				cluster.LogPrintf(LvlErr, "ProxySQL could not add backend %s as offline (%s)", s.URL, err)
			}
		} else {
			//weight string, max_replication_lag string, max_connections string, compression string

			if s.IsLeader() {
				err = psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL could not add writer %s (%s) ", s.URL, err)
				}
				if cluster.Configurator.HasProxyReadLeader() {
					err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
					if err != nil {
						cluster.LogPrintf(LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
					}
				}
			} else if s.State == stateSlave {
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}
			}
			if cluster.Conf.LogLevel > 2 || cluster.Conf.ProxysqlDebug {
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

func (cluster *Cluster) failoverProxysql(proxy *ProxySQLProxy) {
	proxy.Failover()
}

func (proxy *ProxySQLProxy) CertificatesReload() error {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return err
	}
	defer psql.Connection.Close()
	err = psql.ReloadTLS()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Reload TLS failed %s", err)
		return err
	}
	return nil
}

func (proxy *ProxySQLProxy) Failover() {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
		if s.IsMaster() && !s.IsRelay && cluster.oldMaster != nil {
			err = psql.ReplaceWriter(misc.Unbracket(s.Host), s.Port, misc.Unbracket(cluster.oldMaster.Host), cluster.oldMaster.Port, cluster.Configurator.HasProxyReadLeader(), proxy.UseSSL())
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

func (proxy *ProxySQLProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProxysqlOn == false {
		return nil
	}

	psql, err := proxy.Connect()
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
		isFoundBackendWrite := true
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
			isFoundBackendWrite = false
		} else {
			proxy.BackendsWrite = append(proxy.BackendsWrite, bke)
		}
		isFoundBackendRead := true
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
		} else {
			isFoundBackendRead = false
		}

		// nothing should be done if no bootstrap
		if cluster.Conf.ProxysqlBootstrap {

			// if ProxySQL and replication-manager states differ, resolve the conflict
			if bke.PrxStatus == "OFFLINE_HARD" && s.State == stateSlave && !s.IsIgnored() {
				if cluster.Conf.ProxysqlDebug {
					cluster.LogPrintf(LvlInfo, "Monitor ProxySQL setting online as reader rejoining server %s", s.URL)
				}
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.sme.AddState("ERR00069", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00069"], s.URL, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			}

			if s.IsSlaveOrSync() && s.IsMaintenance && isFoundBackendRead && bke.PrxStatus == "ONLINE" {
				if cluster.Conf.ProxysqlDebug {
					cluster.LogPrintf(LvlInfo, "Monitor ProxySQL  replicat %s Offline SOFT from reader group cause by maintenance ", s.URL)
				}
				err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
				updated = true
			}

			// if server is Standalone, and writer set offline in ProxySQL
			if s.State == stateUnconn && bke.PrxStatus == "ONLINE" {
				cluster.LogPrintf(LvlInfo, "Monitor ProxySQL setting writer offline standalone server %s", s.URL)
				err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.sme.AddState("ERR00070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})

				}
				updated = true

			} else if s.State == stateUnconn && bkeread.PrxStatus == "ONLINE" && isFoundBackendRead {
				// if server is Standalone, and reader shunned it in ProxySQL
				cluster.LogPrintf(LvlInfo, "Monitor ProxySQL setting reader offline standalone server %s", s.URL)
				err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.sme.AddState("ERR00070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true

			} else if s.IsLeader() && (s.PrevState == stateUnconn || s.PrevState == stateFailed || (len(proxy.BackendsWrite) == 0 || !isFoundBackendWrite)) {
				// if the master comes back from a previously failed or standalone state, reintroduce it in
				// the appropriate HostGroup

				cluster.LogPrintf(LvlInfo, "Monitor ProxySQL setting online failed server %s", s.URL)
				if psql.ExistAsWriterOrOffline(misc.Unbracket(s.Host), s.Port) {
					err = psql.SetOnline(misc.Unbracket(s.Host), s.Port)
					if err != nil {
						cluster.sme.AddState("ERR00071", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
					}
				} else {
					//scenario restart with failed leader
					err = psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
				}
				updated = true

			} else if s.IsLeader() && !isFoundBackendRead && (cluster.Configurator.HasProxyReadLeader() || (cluster.Configurator.HasProxyReadLeaderNoSlave() && cluster.HasNoValidSlave())) {
				// Add  leader in reader group if not found and setup
				if cluster.Conf.ProxysqlDebug {
					cluster.LogPrintf(LvlInfo, "Monitor ProxySQL add leader in reader group in %s", s.URL)
				}
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}
				updated = true
			} else if s.IsLeader() && isFoundBackendRead && (!cluster.Configurator.HasProxyReadLeader()) {
				// Drop the leader in reader group if not found and setup
				// Cancel learder remove because no valid reader
				if !cluster.Configurator.HasProxyReadLeaderNoSlave() || (cluster.Configurator.HasProxyReadLeaderNoSlave() && !cluster.HasNoValidSlave()) {
					if cluster.Conf.ProxysqlDebug {
						cluster.LogPrintf(LvlInfo, "Monitor ProxySQL Drop the leader in reader group from %s", s.URL)
					}
					err = psql.DropReader(misc.Unbracket(s.Host), s.Port)
					if err != nil {
						cluster.LogPrintf(LvlErr, "ProxySQL could not drop reader in %s (%s)", s.URL, err)
					}
					updated = true
				}
			} else if s.IsSlaveOrSync() && !s.IsIgnored() && (s.PrevState == stateUnconn || s.PrevState == stateFailed) {
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				if cluster.Conf.ProxysqlDebug {
					cluster.LogPrintf(LvlInfo, "Monitor ProxySQL setting reader standalone server %s", s.URL)
				}
				if err != nil {
					cluster.sme.AddState("ERR00072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00072"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			} else if s.IsSlaveOrSync() && !isFoundBackendRead && !s.IsIgnored() {
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				updated = true
			}
		} //if bootstrap

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
			changedUser := false
			for _, user := range uniUsers {
				if _, ok := myprxusermap[user.User+":"+user.Password]; !ok {
					cluster.LogPrintf(LvlInfo, "Add ProxySQL user %s ", user.User)
					err := psql.AddUser(user.User, user.Password)
					if err != nil {
						cluster.sme.AddState("ERR00054", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00054"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
					} else {
						changedUser = true
					}
				}
			}
			if changedUser {
				psql.SaveMySQLUsersToDisk()
			}
		}
	} //end for each server

	if updated {
		err = psql.LoadServersToRuntime()
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
		} else {
			err = psql.SaveServersToDisk()
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

func (proxy *ProxySQLProxy) HasAvailableReader() bool {
	for _, b := range proxy.BackendsRead {
		if b.PrxStatus == "ONLINE" {
			return true
		}
	}
	return false
}

func (proxy *ProxySQLProxy) HasLeaderInReader() bool {
	for _, b := range proxy.BackendsRead {
		if b.Host == proxy.GetCluster().master.Host && b.Port == proxy.GetCluster().master.Port {
			return true
		}
	}
	return false
}

func (cluster *Cluster) setMaintenanceProxysql(proxy *ProxySQLProxy, s *ServerMonitor) {
	proxy.SetMaintenance(s)
}

func (proxy *ProxySQLProxy) BackendsStateChange() {
	proxy.Refresh()
}

func (proxy *ProxySQLProxy) SetMaintenance(s *ServerMonitor) {
	cluster := proxy.ClusterGroup
	// TODO ? check if needed
	/*	if cluster.GetMaster() != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL set maintenance cancel for server %s:%s as proxysql as no leader", s.Host, s.Port)
			return
	} */
	if !cluster.Conf.ProxysqlOn {
		cluster.LogPrintf(LvlErr, "ProxySQL set maintenance cancel for server %s:%s as proxysql off in config", s.Host, s.Port)
		return
	}

	psql, err := proxy.Connect()
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
		err = psql.SetOnlineSoft(misc.Unbracket(s.Host), s.Port)
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not set %s:%s as online (%s)", s.Host, s.Port, err)
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
}

func (proxy *ProxySQLProxy) RotateMonitoringPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	vars, err := psql.GetVariables()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not get mysql variables (%s)", err)
	}
	mon_user := vars["MYSQL-MONITOR_USERNAME"]
	//cluster.LogPrintf(LvlInfo, "RotationMonitorPasswords user %s", user)
	//cluster.LogPrintf(LvlInfo, "RotationMonitorPasswords dbUser %s", cluster.dbUser)
	err = psql.SetMySQLVariable("mysql-monitor_password", password)
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not set mysql variables (%s)", err)
	}

	if mon_user != strings.ToUpper(cluster.dbUser) {
		err = psql.SetMySQLVariable("mysql-monitor_username", cluster.dbUser)
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxySQL could not set mysql variables (%s)", err)
		}
	}
	err = psql.LoadMySQLVariablesToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load varibles to runtime (%s)", err)
	}

	err = psql.SaveMySQLVariablesToDisk()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not save admin variables to disk (%s)", err)
	}

	cluster.LogPrintf(LvlInfo, "Password rotation is done for the proxySQL monitor")
}

func (proxy *ProxySQLProxy) RotationAdminPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	err = psql.SetMySQLVariable("admin-admin_credentials", proxy.User+":"+password)
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not set mysql variables (%s)", err)
	}

	err = psql.LoadAdminVariablesToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not load admin variables to runtime (%s)", err)
	}

	err = psql.SaveAdminVariablesToDisk()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxySQL could not save admin variables to disk (%s)", err)
	}

}
