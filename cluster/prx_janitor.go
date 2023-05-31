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

type ProxyJanitor struct {
	Proxy
}

func NewProxyJanitor(placement int, cluster *Cluster, proxyHost string) *ProxyJanitor {
	conf := cluster.Conf
	prx := new(ProxyJanitor)
	prx.Name = proxyHost
	prx.Host = proxyHost
	prx.Type = config.ConstProxyJanitor
	prx.Port = conf.ProxyJanitorAdminPort
	prx.ReadWritePort, _ = strconv.Atoi(conf.ProxyJanitorPort)
	prx.User = cluster.Conf.ProxyJanitorUser
	prx.Pass = cluster.Conf.GetDecryptedValue("proxyjanitor-password")
	prx.WritePort, _ = strconv.Atoi(conf.ProxyJanitorPort)
	prx.ReadPort, _ = strconv.Atoi(conf.ProxyJanitorPort)

	//	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSProxyJanitorPartitions, conf.ProxyJanitorHostsIPV6)

	/*	if conf.ProvNetCNI {
		if conf.ClusterHead == "" {
			prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
		} else {
			prx.Host = prx.Host + "." + conf.ClusterHead + ".svc." + conf.ProvOrchestratorCluster
		}
	}*/

	prx.Pass = cluster.Conf.GetDecryptedPassword("proxyjanitor-password", prx.Pass)

	return prx
}

func (proxy *ProxyJanitor) AddFlags(flags *pflag.FlagSet, conf *config.Config) {

	flags.BoolVar(&conf.ProxyJanitorDebug, "proxyjanitor-debug", false, "Extra info on monitoring backend")
	flags.StringVar(&conf.ProxyJanitorHosts, "proxyjanitor-servers", "", "ProxyJanitor hosts")
	flags.StringVar(&conf.ProxyJanitorHostsIPV6, "proxyjanitor-servers-ipv6", "", "ProxyJanitor extra IPV6 bind for interfaces")
	flags.StringVar(&conf.ProxyJanitorPort, "proxyjanitor-port", "3306", "ProxyJanitor read/write proxy port")
	flags.StringVar(&conf.ProxyJanitorAdminPort, "proxyjanitor-admin-port", "6032", "ProxyJanitor admin interface port")
	flags.StringVar(&conf.ProxyJanitorUser, "proxyjanitor-user", "external", "ProxyJanitor admin user")
	flags.StringVar(&conf.ProxyJanitorPassword, "proxyjanitor-password", "admin", "ProxyJanitor admin password")
	flags.StringVar(&conf.ProxyJanitorBinaryPath, "proxyjanitor-binary-path", "/usr/sbin/proxysql", "proxysql binary location")
}

func (proxy *ProxyJanitor) Connect() (proxysql.ProxySQL, error) {
	psql := proxysql.ProxySQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
		WriterHG: "0",
	}
	var err error
	err = psql.Connect()
	if err != nil {
		return psql, err
	}
	psql.GetHostgroupFromJanitorDomain(proxy.GetJanitorDomain())

	return psql, nil
}

func (proxy *ProxyJanitor) UseSSL() string {
	UseSSL := "0"
	if proxy.ClusterGroup.Configurator.HaveDBTag("ssl") {
		UseSSL = "1"
	}
	return UseSSL
}

func (proxy *ProxyJanitor) AddShardProxy(shardproxy *MariadbShardProxy) {
	cluster := proxy.ClusterGroup

	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()
	psql.AddShardServer(misc.Unbracket(shardproxy.Host), shardproxy.Port, proxy.UseSSL())
}

func (proxy *ProxyJanitor) AddQueryRulesProxysql(rules []proxysql.QueryRule) error {
	cluster := proxy.ClusterGroup

	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return err
	}
	defer psql.Connection.Close()
	err = psql.AddQueryRules(rules)
	return err
}

func (proxy *ProxyJanitor) Init() {
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProxyJanitorHosts == "" {
		return
	}

	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	/*	if cluster.Conf.ProxysqlBootstrapHG {
		psql.AddHostgroups(cluster.Name)
	} */
	//	proxy.Refresh()
	//	return
	for _, s := range cluster.Proxies {
		if s.GetType() != config.ConstProxyJanitor {
			if s.GetState() == stateUnconn || s.IsIgnored() {
				cluster.LogPrintf(LvlErr, "ProxyJanitor add backend %s as offline (%s)", s.GetURL(), err)
				err = psql.AddOfflineServer(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort()), proxy.UseSSL())
				if err != nil {
					cluster.LogPrintf(LvlErr, "ProxyJanitor could not add backend %s as offline (%s)", s.GetURL(), err)
				}
			} else {
				//weight string, max_replication_lag string, max_connections string, compression string
				err = psql.AddServerAsWriter(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort()), proxy.UseSSL())

				if cluster.Conf.LogLevel > 2 || cluster.Conf.ProxysqlDebug {
					cluster.LogPrintf(LvlWarn, "ProxyJanitor init backend  %s with state %s ", s.GetURL(), s.GetState())
				}
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not load servers to runtime (%s)", err)
	}
	psql.SaveServersToDisk()

}

func (proxy *ProxyJanitor) CertificatesReload() error {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
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

func (proxy *ProxyJanitor) Failover() {

}

func (proxy *ProxyJanitor) GetJanitorDomain() string {
	cluster := proxy.ClusterGroup
	return cluster.Name + "." + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone + "." + cluster.Conf.Cloud18Domain
}

func (proxy *ProxyJanitor) Refresh() error {
	//return nil
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProxyJanitorHosts == "" {
		return errors.New("No proxy janitor hosts defined")
	}

	if cluster.Conf.LogLevel > 9 {
		cluster.LogPrintf(LvlDbg, "ProxyJanitor port : %s, user %s, pass %s\n", proxy.Port, proxy.User, proxy.Pass)
	}

	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		cluster.StateMachine.CopyOldStateFromUnknowServer(proxy.Name)
		return err
	}
	defer psql.Connection.Close()
	proxy.Version = psql.GetVersion()

	var updated bool
	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	for _, s := range cluster.Proxies {
		if s.GetType() != config.ConstProxyJanitor {
			isFoundBackendWrite := true
			proxysqlHostgroup, proxysqlServerStatus, proxysqlServerConnections, proxysqlByteOut, proxysqlByteIn, proxysqlLatency, err := psql.GetStatsForHostWrite(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort()))
			var bke = Backend{
				Host:           s.GetHost(),
				Port:           strconv.Itoa(s.GetWritePort()),
				Status:         s.GetState(),
				PrxName:        s.GetURL(),
				PrxStatus:      proxysqlServerStatus,
				PrxConnections: strconv.Itoa(proxysqlServerConnections),
				PrxByteIn:      strconv.Itoa(proxysqlByteOut),
				PrxByteOut:     strconv.Itoa(proxysqlByteIn),
				PrxLatency:     strconv.Itoa(proxysqlLatency),
				PrxHostgroup:   proxysqlHostgroup,
			}

			if err != nil {
				isFoundBackendWrite = false
			} else {
				proxy.BackendsWrite = append(proxy.BackendsWrite, bke)
			}

			// nothing should be done if no bootstrap
			if cluster.IsDiscovered() {
				// if ProxyJanitor and replication-manager states differ, resolve the conflict

				// if proxy writer set offline in ProxyJanitor

				if s.GetPrevState() == stateUnconn || s.GetPrevState() == stateFailed || (len(proxy.BackendsWrite) == 0 || !isFoundBackendWrite) {
					// if the proxy comes back from a previously failed or standalone state, reintroduce it in
					// the appropriate HostGroup

					cluster.StateMachine.AddState("ERR00071", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00071"], err, s.GetURL()), ErrFrom: "PRX", ServerUrl: proxy.Name})
					if psql.ExistAsWriterOrOffline(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort())) {
						err = psql.SetOnline(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort()))
						if err != nil {
							cluster.LogPrintf(LvlErr, "Monitor ProxyJanitor setting online failed proxy %s", s.GetURL())
						}
					} else {
						//scenario restart with failed leader
						err = psql.AddServerAsWriter(misc.Unbracket(s.GetHost()), strconv.Itoa(s.GetWritePort()), proxy.UseSSL())
					}
					updated = true

				}
			} //if bootstrap
		}

	} //end for each proxy
	// load the grants
	s := proxy.GetCluster().GetMaster()
	if s != nil {
		myprxusermap, _, err := dbhelper.GetProxySQLUsers(psql.Connection)
		if err != nil {
			cluster.StateMachine.AddState("ERR00053", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00053"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
		}
		uniUsers := make(map[string]dbhelper.Grant)
		dupUsers := make(map[string]string)

		for _, u := range s.Users {
			if !strings.Contains(u.User, proxy.GetJanitorDomain()) {

				user, ok := s.Users["'"+u.User+"@"+proxy.GetJanitorDomain()+"'@'"+u.Host+"'"]
				if !ok {
					//		cluster.LogPrintf(LvlErr, "lookup %s %s%v", u.User, proxy.GetJanitorDomain(), s.Users)
					// create domain user in master
					logs, err := dbhelper.DuplicateUserPassword(s.Conn, s.DBVersion, u.User, u.Host, u.User+"@"+proxy.GetJanitorDomain())
					cluster.LogSQL(logs, err, cluster.master.URL, "Add Janitor user to leader", LvlDbg, "Refresh ProxyJanitor")

				}
				user, ok = uniUsers[u.User+":"+u.Password]
				if ok {
					dupUsers[user.User] = user.User
					cluster.StateMachine.AddState("ERR00057", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00057"], user.User), ErrFrom: "MON", ServerUrl: proxy.Name})
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
		}
		changedUser := false
		for _, user := range uniUsers {
			if _, ok := myprxusermap[user.User+"@"+proxy.GetJanitorDomain()+":"+user.Password]; !ok {
				cluster.LogPrintf(LvlInfo, "Add ProxyJanitor user %s ", user.User+"@"+proxy.GetJanitorDomain())
				err := psql.AddUser(user.User+"@"+proxy.GetJanitorDomain(), user.Password)
				psql.AddFastRouting(user.User+"@"+proxy.GetJanitorDomain(), "replication_manager_schema", strconv.FormatUint(proxy.GetCluster().GetUniqueId(), 10))

				if err != nil {
					cluster.StateMachine.AddState("ERR00054", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00054"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
				} else {
					changedUser = true
				}
			}
		}
		if changedUser {
			psql.SaveMySQLUsersToDisk()
		}
	}

	if updated {
		err = psql.LoadServersToRuntime()
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxyJanitor could not load servers to runtime (%s)", err)
		} else {
			err = psql.SaveServersToDisk()
		}
	}
	/*proxy.QueryRules, err = psql.GetQueryRulesRuntime()
	if err != nil {
		cluster.StateMachine.AddState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
	}*/
	proxy.Variables, err = psql.GetVariables()
	if err != nil {
		cluster.StateMachine.AddState("WARN0098", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0098"], err), ErrFrom: "MON", ServerUrl: proxy.Name})
	}

	if proxy.Variables["MYSQL-MULTIPLEXING"] == "TRUE" {
		psql.SetMySQLVariable("MYSQL-MULTIPLEXING", "FALSE")
		psql.LoadMySQLVariablesToRuntime()
		psql.SaveMySQLVariablesToDisk()

	}

	return nil
}

func (proxy *ProxyJanitor) HasAvailableReader() bool {
	for _, b := range proxy.BackendsRead {
		if b.PrxStatus == "ONLINE" {
			return true
		}
	}
	return false
}

func (proxy *ProxyJanitor) HasLeaderInReader() bool {
	for _, b := range proxy.BackendsRead {
		if b.Host == proxy.GetCluster().master.Host && b.Port == proxy.GetCluster().master.Port {
			return true
		}
	}
	return false
}

func (proxy *ProxyJanitor) BackendsStateChange() {
	//proxy.Refresh()
}

func (proxy *ProxyJanitor) SetMaintenance(s *ServerMonitor) {

}

func (proxy *ProxyJanitor) RotateMonitoringPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	vars, err := psql.GetVariables()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not get mysql variables (%s)", err)
	}
	mon_user := vars["MYSQL-MONITOR_USERNAME"]
	//cluster.LogPrintf(LvlInfo, "RotationMonitorPasswords user %s", user)
	//cluster.LogPrintf(LvlInfo, "RotationMonitorPasswords dbUser %s", cluster.dbUser)
	err = psql.SetMySQLVariable("mysql-monitor_password", password)
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not set mysql variables (%s)", err)
	}

	if mon_user != strings.ToUpper(cluster.GetDbUser()) {
		err = psql.SetMySQLVariable("mysql-monitor_username", cluster.GetDbUser())
		if err != nil {
			cluster.LogPrintf(LvlErr, "ProxyJanitor could not set mysql variables (%s)", err)
		}
	}
	err = psql.LoadMySQLVariablesToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not load varibles to runtime (%s)", err)
	}

	err = psql.SaveMySQLVariablesToDisk()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not save admin variables to disk (%s)", err)
	}

	cluster.LogPrintf(LvlInfo, "Password rotation is done for the proxySQL monitor")
}

func (proxy *ProxyJanitor) RotateProxyPasswords(password string) {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	err = psql.SetMySQLVariable("admin-admin_credentials", proxy.User+":"+password)
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not set mysql variables (%s)", err)
	}

	err = psql.LoadAdminVariablesToRuntime()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not load admin variables to runtime (%s)", err)
	}

	err = psql.SaveAdminVariablesToDisk()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not save admin variables to disk (%s)", err)
	}

}

func (proxy *ProxyJanitor) Shutdown() {
	cluster := proxy.ClusterGroup
	psql, err := proxy.Connect()
	if err != nil {
		cluster.StateMachine.AddState("ERR00051", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00051"], err), ErrFrom: "MON"})
		return
	}
	defer psql.Connection.Close()

	err = psql.Shutdown()
	if err != nil {
		cluster.LogPrintf(LvlErr, "ProxyJanitor could not shutdown (%s)", err)
	}
}
