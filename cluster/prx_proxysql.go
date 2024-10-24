package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
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
	prx.User = cluster.Conf.ProxysqlUser
	prx.Pass = cluster.Conf.Secrets["proxysql-password"].Value
	prx.ReaderHostgroup, _ = strconv.Atoi(conf.ProxysqlReaderHostgroup)
	prx.WriterHostgroup, _ = strconv.Atoi(conf.ProxysqlWriterHostgroup)
	prx.WritePort, _ = strconv.Atoi(conf.ProxysqlPort)
	prx.ReadPort, _ = strconv.Atoi(conf.ProxysqlPort)

	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSProxySQLPartitions, conf.ProxysqlHostsIPV6, conf.ProxysqlJanitorWeights)

	if conf.ProvNetCNI {
		if conf.ClusterHead == "" {
			prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
		} else {
			prx.Host = prx.Host + "." + conf.ClusterHead + ".svc." + conf.ProvOrchestratorCluster
		}
	}

	//prx.Pass = cluster.GetDecryptedPassword("proxysql-password", prx.Pass)

	return prx
}

func (proxy *ProxySQLProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.ProxysqlOn, "proxysql", false, "Use ProxySQL")
	flags.BoolVar(&conf.ProxysqlDebug, "proxysql-debug", true, "Extra info on monitoring backend")
	flags.IntVar(&conf.ProxysqlLogLevel, "proxysql-log-level", 1, "Extra info on monitoring backend")
	flags.BoolVar(&conf.ProxysqlSaveToDisk, "proxysql-save-to-disk", false, "Save proxysql change to sqllight")
	flags.StringVar(&conf.ProxysqlHosts, "proxysql-servers", "", "ProxySQL hosts")
	flags.StringVar(&conf.ProxysqlHostsIPV6, "proxysql-servers-ipv6", "", "ProxySQL extra IPV6 bind for interfaces")
	flags.StringVar(&conf.ProxysqlJanitorWeights, "proxysql-janitor-weights", "100", "Weight of each proxysql host inside janitor proxy")
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
		Weight:   proxy.Weight,
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
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL add backend %s as offline (%s)", s.URL, err)
			err = psql.AddOfflineServer(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add backend %s as offline (%s)", s.URL, err)
			}
		} else {
			//weight string, max_replication_lag string, max_connections string, compression string

			if s.IsLeader() {
				err = psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add writer %s (%s) ", s.URL, err)
				}
				if cluster.Configurator.HasProxyReadLeader() {
					err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
					}
				}
			} else if s.State == stateSlave {
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}
			}
			// if cluster.Conf.LogLevel > 2 || cluster.Conf.ProxysqlDebug {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlWarn, "ProxySQL init backend  %s with state %s ", s.URL, s.State)
			// }

		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
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
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return err
	}
	defer psql.Connection.Close()
	err = psql.ReloadTLS()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Reload TLS failed %s", err)
		return err
	}
	return nil
}

func (proxy *ProxySQLProxy) Failover() {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}

	defer psql.Connection.Close()
	for _, s := range cluster.Servers {
		if s.State == stateUnconn || s.IsIgnored() {
			err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Failover ProxySQL could not set server %s offline (%s)", s.URL, err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlInfo, "Failover ProxySQL set server %s offline", s.URL)
			}
		}
		if s.IsMaster() && !s.IsRelay && cluster.oldMaster != nil {
			err = psql.ReplaceWriter(misc.Unbracket(s.Host), s.Port, misc.Unbracket(cluster.oldMaster.Host), cluster.oldMaster.Port, cluster.Configurator.HasProxyReadLeader())
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Failover ProxySQL could not set server %s Master (%s)", s.URL, err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlInfo, "Failover ProxySQL set server %s master", s.URL)
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Failover ProxySQL could not load servers to runtime (%s)", err)
	}
	if proxy.ClusterGroup.Conf.ProxysqlSaveToDisk {
		err = psql.SaveServersToDisk()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Failover ProxySQL could not save servers to disk (%s)", err)
		}
	}

}

func (proxy *ProxySQLProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	// if cluster.Conf.LogLevel > 9 {
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "ProxySQL port : %s, user %s, pass %s\n", proxy.Port, proxy.User, proxy.Pass)
	// }
	if cluster.Conf.ProxysqlOn == false {
		return nil
	}

	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		cluster.StateMachine.CopyOldStateFromUnknowServer(proxy.Name)
		return err
	}
	defer psql.Connection.Close()
	proxy.Version = psql.GetVersion()

	var updated bool
	bkWriters := make([]Backend, 0)
	bkReaders := make([]Backend, 0)

	for _, s := range cluster.Servers {
		isBackendWriter := true
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

		if err == nil {
			bkWriters = append(bkWriters, bke)
		} else {
			isBackendWriter = false
		}

		IsBackendReader := true
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
			bkReaders = append(bkReaders, bkeread)
		} else {
			IsBackendReader = false
		}

		// nothing should be done if no bootstrap
		if cluster.Conf.ProxysqlBootstrap && cluster.IsDiscovered() {
			// if ProxySQL and replication-manager states differ, resolve the conflict
			if bke.PrxStatus == "OFFLINE_HARD" && s.State == stateSlave && !s.IsIgnored() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL setting online as reader rejoining server %s", s.URL)
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.SetState("ERR00069", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00069"], s.URL, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}

				// Drop old status as writer
				if IsBackendReader {
					err = psql.DropWriter(misc.Unbracket(s.Host), s.Port)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not drop slave in writer in %s (%s)", s.URL, err)
					}
				}
				updated = true
			} else if s.IsSlaveOrSync() && s.IsMaintenance && IsBackendReader && bkeread.PrxStatus == "ONLINE" {
				// if server is slave, and maintenance  set offline Soft in ProxySQL
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL set %s as OFFLINE_SOFT from reader group cause by maintenance ", s.URL)
				err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.SetState("ERR00094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00094"], proxy.GetURL(), s.URL, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			} else if s.State == stateUnconn && bke.PrxStatus == "ONLINE" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL setting writer offline standalone server %s", s.URL)
				err = psql.SetOffline(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.SetState("ERR00070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true

			} else if s.State == stateUnconn && bkeread.PrxStatus == "ONLINE" && IsBackendReader {
				// if server is Standalone, and reader shunned it in ProxySQL
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlInfo, "Monitor ProxySQL setting reader offline standalone server %s", s.URL)
				err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.SetState("ERR00070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00070"], err, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true

			} else if s.IsLeader() && (s.PrevState == stateUnconn || s.PrevState == stateFailed || (len(proxy.BackendsWrite) == 0 || !isBackendWriter)) {
				// if the master comes back from a previously failed or standalone state, reintroduce it in
				// the appropriate HostGroup

				if psql.ExistAsWriterOrOffline(misc.Unbracket(s.Host), s.Port) {
					err = psql.SetOnline(misc.Unbracket(s.Host), s.Port)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "Monitor ProxySQL setting online failed server %s: %s", s.URL, err.Error())
					}
				} else {
					//scenario restart with failed leader
					err = psql.AddServerAsWriter(misc.Unbracket(s.Host), s.Port, proxy.UseSSL())
					if err != nil {
						cluster.SetState("ERR00071", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00071"], proxy.Name, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
					}
				}
				updated = true

			} else if s.IsLeader() && !IsBackendReader && (cluster.Configurator.HasProxyReadLeader() || (cluster.Configurator.HasProxyReadLeaderNoSlave() && (cluster.HasNoValidSlave() || !proxy.HasAvailableReader()))) {
				// Add  leader in reader group if not found and setup
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL add leader in reader group in %s", s.URL)
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}

				if cluster.Conf.ProxysqlBootstrapVariables {
					// This is needed for preventing proxySQL removing leader as reader
					psql.SetMonitorIsAlsoWriter(true)
				}
				updated = true
			} else if s.IsLeader() && IsBackendReader && !cluster.Configurator.HasProxyReadLeader() { // Drop the leader in reader group if not found and setup

				// Cancel leader remove because only leader is a valid leader
				if !cluster.Configurator.HasProxyReadLeaderNoSlave() || (cluster.Configurator.HasProxyReadLeaderNoSlave() && proxy.CountAvailableReaders() > 1 && proxy.HasLeaderInReader()) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL Drop the leader in reader group from %s", s.URL)
					err = psql.DropReader(misc.Unbracket(s.Host), s.Port)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not drop reader in %s (%s)", s.URL, err)
					}

					if cluster.Conf.ProxysqlBootstrapVariables {
						// This is needed for preventing proxySQL keeping leader as reader
						psql.SetMonitorIsAlsoWriter(false)
					}
					updated = true
				}
			} else if s.IsSlaveOrSync() && s.State == stateSlave && !s.IsIgnored() && (s.PrevState == stateUnconn || s.PrevState == stateFailed) {
				err = psql.SetReader(misc.Unbracket(s.Host), s.Port)
				// if cluster.Conf.ProxysqlDebug {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL setting reader standalone server %s", s.URL)
				// }
				if err != nil {
					cluster.SetState("ERR00072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00072"], proxy.Name, s.URL, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
				}
				updated = true
			} else if s.IsSlaveOrSync() && !IsBackendReader && !s.IsIgnored() {
				err = psql.AddServerAsReader(misc.Unbracket(s.Host), s.Port, "1", strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxReplicationLag), strconv.Itoa(s.ClusterGroup.Conf.PRXServersBackendMaxConnections), strconv.Itoa(misc.Bool2Int(s.ClusterGroup.Conf.PRXServersBackendCompression)), proxy.UseSSL())
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not add reader %s (%s)", s.URL, err)
				}
				updated = true
			} else if s.IsSlaveOrSync() && isBackendWriter {
				// Drop slave from writer HG if exists
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlDbg, "Monitor ProxySQL drop slave in writer group from %s", s.URL)
				err = psql.DropWriter(misc.Unbracket(s.Host), s.Port)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not drop slave in writer in %s (%s)", s.URL, err)
				}
				updated = true
			}
		} //if bootstrap

		// //Set the alert if proxysql status is OFFLINE_SOFT
		if (bke.PrxStatus == "OFFLINE_SOFT" || bkeread.PrxStatus == "OFFLINE_SOFT") && !s.IsMaintenance {
			cluster.SetState("ERR00091", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00091"], proxy.Name, s.URL), ErrFrom: "PRX", ServerUrl: proxy.Name})
			// s.SwitchMaintenance()
		}

		if !cluster.Conf.ProxysqlBootstrap && s.IsLeader() && !IsBackendReader && (cluster.Configurator.HasProxyReadLeader() || (cluster.Configurator.HasProxyReadLeaderNoSlave() && (cluster.HasNoValidSlave() || !proxy.HasAvailableReader()))) {
			cluster.SetState("ERR00093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00093"], proxy.Name), ErrFrom: "PRX", ServerUrl: proxy.Name})
		}

		// load the grants
		if s.IsMaster() && cluster.Conf.ProxysqlCopyGrants {
			myprxusermap, _, err := dbhelper.GetProxySQLUsers(psql.Connection)
			if err != nil {
				cluster.SetState("ERR00053", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00053"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
			}
			uniUsers := make(map[string]*dbhelper.Grant)
			dupUsers := make(map[string]string)

			for _, u := range s.Users.ToNewMap() {
				user, ok := uniUsers[u.User+":"+u.Password]
				if ok {
					dupUsers[user.User] = user.User
					cluster.SetState("ERR00057", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00057"], user.User), ErrFrom: "MON", ServerUrl: proxy.Name})
				} else {
					if u.Password != "" && u.Password != "invalid" {
						if u.User != cluster.GetDbUser() {
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
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlInfo, "Add ProxySQL user %s ", user.User)
					err := psql.AddUser(user.User, user.Password)
					if err != nil {
						cluster.SetState("ERR00054", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00054"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
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

	proxy.BackendsWrite = bkWriters
	proxy.BackendsRead = bkReaders

	if updated {
		err = psql.LoadServersToRuntime()
		if err != nil {
			cluster.SetState("ERR00095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00095"], proxy.GetURL(), err), ErrFrom: "PRX", ServerUrl: proxy.Name})
		} else {
			err = psql.SaveServersToDisk()
			if err != nil {
				cluster.SetState("ERR00096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00096"], proxy.GetURL(), err), ErrFrom: "PRX", ServerUrl: proxy.Name})
			}
		}
	}
	proxy.QueryRules, err = psql.GetQueryRulesRuntime()
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
	}
	proxy.Variables, err = psql.GetVariables()
	if err != nil {
		cluster.SetState("WARN0098", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0098"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
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

func (proxy *ProxySQLProxy) CountAvailableReaders() (n int) {
	for _, b := range proxy.BackendsRead {
		if b.PrxStatus == "ONLINE" {
			n++
		}
	}
	return n
}

func (proxy *ProxySQLProxy) HasLeaderInReader() bool {
	for _, b := range proxy.BackendsRead {
		if b.Host == proxy.GetCluster().master.Host && b.Port == proxy.GetCluster().master.Port && b.PrxStatus == "ONLINE" {
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
		cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxySQL,config.LvlErr, "ProxySQL set maintenance cancel for server %s:%s as proxysql as no leader", s.Host, s.Port)
			return
	} */
	if !cluster.Conf.ProxysqlOn {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL set maintenance cancel for server %s:%s as proxysql off in config", s.Host, s.Port)
		return
	}

	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	if s.IsMaintenance {
		err = psql.SetOfflineSoft(misc.Unbracket(s.Host), s.Port)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not set %s:%s as offline_soft (%s)", s.Host, s.Port, err)
		}
	} else {
		err = psql.SetOnlineSoft(misc.Unbracket(s.Host), s.Port)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not set %s:%s as online (%s)", s.Host, s.Port, err)
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not load servers to runtime (%s)", err)
	}
}

func (proxy *ProxySQLProxy) RotateMonitoringPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	vars, err := psql.GetVariables()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not get mysql variables (%s)", err)
	}
	mon_user := vars["MYSQL-MONITOR_USERNAME"]
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxySQL,LvlInfo, "RotationMonitorPasswords user %s", user)
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxySQL,LvlInfo, "RotationMonitorPasswords dbUser %s", cluster.dbUser)
	err = psql.SetMySQLVariable("mysql-monitor_password", password)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not set mysql variables (%s)", err)
	}

	if mon_user != strings.ToUpper(cluster.GetDbUser()) {
		err = psql.SetMySQLVariable("mysql-monitor_username", cluster.GetDbUser())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not set mysql variables (%s)", err)
		}
	}
	err = psql.LoadMySQLVariablesToRuntime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not load varibles to runtime (%s)", err)
	}

	err = psql.SaveMySQLVariablesToDisk()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not save admin variables to disk (%s)", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlInfo, "Password rotation is done for the proxySQL monitor")
}

func (proxy *ProxySQLProxy) RotateProxyPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	err = psql.SetMySQLVariable("admin-admin_credentials", proxy.User+":"+password)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not set mysql variables (%s)", err)
	}

	err = psql.LoadAdminVariablesToRuntime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not load admin variables to runtime (%s)", err)
	}

	err = psql.SaveAdminVariablesToDisk()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not save admin variables to disk (%s)", err)
	}
	proxy.Pass = password
}

func (proxy *ProxySQLProxy) Shutdown() {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.SetState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	err = psql.Shutdown()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxySQL, config.LvlErr, "ProxySQL could not shutdown (%s)", err)
	}
}
