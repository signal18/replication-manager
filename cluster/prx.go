// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/router/myproxy"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

// Proxy defines a proxy
type Proxy struct {
	DatabaseProxy
	Id              string               `json:"id" groups:"apps"`
	Name            string               `json:"name" groups:"apps"`
	Type            string               `json:"type" groups:"apps"`
	Host            string               `json:"host"groups:"apps"`
	HostIPV6        string               `json:"hostIPV6"`
	Port            string               `json:"port" groups:"apps"`
	TunnelPort      int                  `json:"tunnelPort"`
	TunnelWritePort int                  `json:"tunnelWritePort"`
	Tunnel          bool                 `json:"tunnel"`
	User            string               `json:"-"`
	Pass            string               `json:"-"`
	WritePort       int                  `json:"writePort" groups:"apps"`
	ReadPort        int                  `json:"readPort" groups:"apps"`
	ReadWritePort   int                  `json:"readWritePort" groups:"apps"`
	ReaderHostgroup int                  `json:"readerHostGroup"`
	WriterHostgroup int                  `json:"writerHostGroup"`
	BackendsWrite   []Backend            `json:"backendsWrite"`
	BackendsRead    []Backend            `json:"backendsRead"`
	Version         string               `json:"version" groups:"apps"`
	InternalProxy   *myproxy.Server      `json:"internalProxy"`
	ShardProxy      *ServerMonitor       `json:"shardProxy"`
	ClusterGroup    *Cluster             `json:"-"`
	Datadir         string               `json:"datadir"`
	QueryRules      []proxysql.QueryRule `json:"queryRules"`
	State           string               `json:"state"`
	PrevState       string               `json:"prevState"`
	FailCount       int                  `json:"failCount"`
	SlapOSDatadir   string               `json:"slaposDatadir"`
	Process         *os.Process          `json:"process"`
	Variables       map[string]string    `json:"-"`
	ServiceName     string               `json:"serviceName"`
	Agent           string               `json:"agent"`
	WorkingAgent    string               `json:"workingAgent"`
	Weight          string               `json:"weight"`
	IsStaging       bool                 `json:"isStaging"`
	Lock            sync.Mutex
}

type DatabaseProxy interface {
	SetCluster(c *Cluster)
	AddFlags(flags *pflag.FlagSet, conf *config.Config)
	Init()
	Refresh() error
	Failover()
	SetMaintenance(server *ServerMonitor)
	BackendsStateChange()
	GetType() string
	CertificatesReload() error
	IsRunning() bool
	IsIgnored() bool
	SetCredential(credential string)

	GetFailCount() int
	SetFailCount(c int)
	DelLock()
	SetLock()
	GetAgent() string
	GetName() string
	GetHost() string
	GetPort() string
	GetURL() string
	GetWritePort() int
	GetReadWritePort() int
	GetReadPort() int
	GetId() string
	GetState() string
	SetState(v string)
	GetUser() string
	GetPass() string
	GetServiceName() string
	GetOrchestrator() string

	GetPrevState() string

	SetPrevState(state string)
	GetCluster() *Cluster
	GetClusterConnection() (*sqlx.DB, error)
	GetWorkingAgent() string
	GetWorkingOrchestratorNode() error

	SetMaintenanceHaproxy(server *ServerMonitor)

	IsFilterInTags(filter string) bool
	IsDown() bool
	GetProxyConfig() error
	GetJanitorWeight() string
	// GetInitContainer(collector opensvc.Collector) string
	GetBindAddress() string
	GetBindAddressExtraIPV6() string
	GetUseSSL() string
	GetUseCompression() string
	GetDatadir() string
	GetConfigDatadir() string
	GetConfigConfigdir() string
	GetEnv() map[string]string
	GetSshEnv() string
	GetConfigProxyModule(variable string) string
	FetchStats()

	OpenSVCGetProxyDefaultSection() map[string]string

	SetSuspect()

	SetID()
	SetDataDir()
	SetServiceName(namespace string)
	SetStaging(staging bool)
	IsInStaging() bool

	SetProvisionCookie() error
	SetUnprovisionCookie() error
	SetReprovCookie() error
	SetRestartCookie() error
	SetWaitStartCookie() error
	SetWaitStopCookie() error
	SetConfigCookie() error
	SetConfigRefreshCookie() error
	SetNoConfigFetchCookie() error

	HasProvisionCookie() bool
	HasUnprovisionCookie() bool
	HasReprovCookie() bool
	HasRestartCookie() bool
	HasWaitStartCookie() bool
	HasWaitStopCookie() bool
	HasConfigCookie() bool
	HasConfigRefreshCookie() bool
	HasNoConfigFetchCookie() bool
	HasDNS() bool

	DelProvisionCookie() error
	DelUnprovisionCookie() error
	DelReprovisionCookie() error
	DelRestartCookie() error
	DelWaitStartCookie() error
	DelWaitStopCookie() error
	DelConfigCookie() error
	DelConfigRefreshCookie() error
	DelNoConfigFetchCookie() error

	RotateProxyPasswords(password string)
}

type Backend struct {
	Host           string `json:"host"`
	Port           string `json:"port"`
	Status         string `json:"status"`
	Svname         string `json:"svname"`
	PrxName        string `json:"prxName"`
	PrxStatus      string `json:"prxStatus"`
	PrxConnections string `json:"prxConnections"`
	PrxHostgroup   string `json:"prxHostgroup"`
	PrxByteOut     string `json:"prxByteOut"`
	PrxByteIn      string `json:"prxByteIn"`
	PrxLatency     string `json:"prxLatency"`
	PrxMaintenance bool   `json:"prxMaintenance"`
}

type proxyList []DatabaseProxy

func (cluster *Cluster) newProxyList() error {
	cluster.Proxies = make([]DatabaseProxy, 0)

	if cluster.Conf.MxsHost != "" && cluster.Conf.MxsOn {
		for k, proxyHost := range strings.Split(cluster.Conf.MxsHost, ",") {
			prx := NewMaxscaleProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New Maxscale proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.HaproxyHosts != "" && cluster.Conf.HaproxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.HaproxyHosts, ",") {
			prx := NewHaproxyProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New HA Proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.ExtProxyVIP != "" && cluster.Conf.ExtProxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.ExtProxyVIP, ",") {
			prx := NewExternalProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New external proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.ProxysqlHosts != "" && cluster.Conf.ProxysqlOn {
		for k, proxyHost := range strings.Split(cluster.Conf.ProxysqlHosts, ",") {
			prx := NewProxySQLProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New ProxySQL proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.ProxyJanitorHosts != "" {
		for k, proxyHost := range strings.Split(cluster.Conf.ProxyJanitorHosts, ",") {
			prx := NewProxyJanitor(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New ProxyJanitor proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.MdbsProxyHosts != "" && cluster.Conf.MdbsProxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.MdbsProxyHosts, ",") {
			prx := NewMariadbShardProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New MdbShardProxy proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.SphinxHosts != "" && cluster.Conf.SphinxOn {
		for k, proxyHost := range strings.Split(cluster.Conf.SphinxHosts, ",") {
			prx := NewSphinxProxy(k, cluster, proxyHost)

			cluster.AddProxy(prx)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "New SphinxSearch proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.MyproxyOn {
		prx := NewMyProxyProxy(0, cluster, "")
		cluster.AddProxy(prx)
	}

	if cluster.Conf.RegistryConsul {
		prx := NewConsulProxy(0, cluster, "")
		cluster.AddProxy(prx)
	}

	stagingList := strings.Split(cluster.Conf.StagingProxyHosts, ",")
	for _, pr := range cluster.Proxies {
		if pr != nil && slices.Contains(stagingList, pr.GetName()) {
			pr.SetStaging(true)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Loaded %d proxies", len(cluster.Proxies))

	return nil
}

func (cluster *Cluster) InjectProxiesTraffic() {
	var definer string
	// Found server from ServerId
	if cluster.Conf.TopologyStaging && cluster.Conf.TestInjectTrafficStaging {
		for _, pr := range cluster.Proxies {
			if pr.GetType() == config.ConstProxySphinx || pr.GetType() == config.ConstProxyMyProxy || !pr.IsInStaging() { // Traffic for staging only
				// Does not yet understand CREATE OR REPLACE VIEW
				continue
			}
			db, err := pr.GetClusterConnection()
			if err != nil {
				cluster.SetState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				if pr.GetType() == config.ConstProxyMyProxy {
					definer = "DEFINER = root@localhost"
				} else {
					definer = ""
				}
				_, err := db.Exec("CREATE OR REPLACE " + definer + " VIEW replication_manager_schema.pseudo_gtid_v as select '" + misc.GetUUID() + "' from dual")

				if err != nil {
					cluster.SetState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
					db.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")

				}
				db.Close()
			}
		}
	}

	if cluster.GetMaster() != nil && (cluster.Conf.TestInjectTraffic || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat) {
		for _, pr := range cluster.Proxies {
			if pr.GetType() == config.ConstProxySphinx || pr.GetType() == config.ConstProxyMyProxy || (cluster.Conf.TopologyStaging && pr.IsInStaging()) { // skip staging proxy
				// Does not yet understand CREATE OR REPLACE VIEW
				continue
			}
			db, err := pr.GetClusterConnection()
			if err != nil {
				cluster.SetState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				if pr.GetType() == config.ConstProxyMyProxy {
					definer = "DEFINER = root@localhost"
				} else {
					definer = ""
				}
				_, err := db.Exec("CREATE OR REPLACE " + definer + " VIEW replication_manager_schema.pseudo_gtid_v as select '" + misc.GetUUID() + "' from dual")

				if err != nil {
					cluster.SetState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
					db.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")

				}
				db.Close()
			}
		}
	}
}

func (cluster *Cluster) IsProxyEqualMaster() bool {
	// Found server from ServerId
	if cluster.GetMaster() != nil {
		for _, pr := range cluster.Proxies {
			db, err := pr.GetClusterConnection()
			if err != nil {
				// if cluster.IsVerbose() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Can't get a proxy connection: %s", err)
				// }
				return false
			}
			defer db.Close()
			var sv map[string]string
			sv, _, err = dbhelper.GetVariables(db, cluster.GetMaster().DBVersion)
			if err != nil {
				// if cluster.IsVerbose() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Can't get variables: %s", err)
				// }
				return false
			}
			var sid uint64
			sid, err = strconv.ParseUint(sv["SERVER_ID"], 10, 64)
			if err != nil {
				// if cluster.IsVerbose() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Can't form proxy server_id convert: %s", err)
				// }
				return false
			}
			// if cluster.IsVerbose() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Proxy compare master: %d %d", cluster.GetMaster().ServerID, uint(sid))
			// }
			if cluster.GetMaster() != nil && cluster.GetMaster().ServerID == uint64(sid) || pr.GetType() == config.ConstProxySpider {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) SetProxyServerMaintenance(serverid uint64) {
	// Found server from ServerId
	server := cluster.GetServerFromId(serverid)
	for _, pr := range cluster.Proxies {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Notify server %s in maintenance in Proxy Type: %s Host: %s Port: %s", server.URL, pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.SetMaintenance(server)
	}
}

// called  by server monitor if state change
func (cluster *Cluster) backendStateChangeProxies() {
	for _, pr := range cluster.Proxies {
		//	pr.SetLock()
		pr.BackendsStateChange()
		//	pr.DelLock()
	}
}

// Used to monitor proxies call by main monitor loop
func (cluster *Cluster) refreshProxies(wcg *sync.WaitGroup) {
	defer wcg.Done()
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "Refresh proxy start")
	// }
	for _, pr := range cluster.Proxies {
		if pr != nil {
			//	pr.SetLock()

			if err := pr.Refresh(); err == nil {
				pr.SetFailCount(0)
				pr.SetState(stateProxyRunning)
				if pr.HasWaitStartCookie() {
					pr.DelWaitStartCookie()
				}
			} else {
				pr.SetFailCount(pr.GetFailCount() + 1)
				// TODO: Can pr.ClusterGroup be different from cluster *Cluster? code doesn't imply it. if not change to
				// cl, err := pr.GetClusterConnection()
				// cl.Conf.MaxFail
				if pr.GetFailCount() >= cluster.Conf.MaxFail {
					if pr.GetFailCount() == cluster.Conf.MaxFail {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, "INFO", "Declaring %s proxy as failed %s:%s %s", pr.GetType(), pr.GetHost(), pr.GetPort(), err)
					}
					pr.SetState(stateFailed)
					pr.DelWaitStopCookie()
					pr.DelRestartCookie()
					pr.DelUnprovisionCookie()
				} else {
					pr.SetState(stateSuspect)
				}
			}
			if pr.GetPrevState() != pr.GetState() {
				cluster.BashScriptPrxServersChangeState(pr, pr.GetState(), pr.GetPrevState())
				pr.SetPrevState(pr.GetState())
			}
			if cluster.Conf.GraphiteMetrics {
				pr.FetchStats()
			}
			//	pr.DelLock()
		}
	}
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "Refresh proxy end")
	// }
}

func (cluster *Cluster) failoverProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Failover Proxy Type: %s Host: %s Port: %s", pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.Failover()
	}

}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "New proxy monitored: %s %s:%s", pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.Init()
	}
}

func (proxy *Proxy) FetchStats() {
	metrics := make([]graphite.Metric, 0)

	for _, wbackend := range proxy.BackendsWrite {
		/*
			This replacer is for graphite metric title, and will replace unwanted string from hostname.
			Replace [`?] with empty string
			Replace spaces and / with underscore _
			Replace [.()<'":] with dash -
			Result will be clean hostname ie from 172.18.0.2:3306 to 172-18-0-2-3306
		*/
		replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-", ":", "-")
		server := "rw-" + replacer.Replace(wbackend.PrxName)
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_send", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_received", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.connections", proxy.Type, proxy.Id, server), wbackend.PrxConnections, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.latency", proxy.Type, proxy.Id, server), wbackend.PrxLatency, time.Now().Unix()))
	}
	for _, wbackend := range proxy.BackendsRead {
		replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-", ":", "-")
		server := "ro-" + replacer.Replace(wbackend.PrxName)
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_send", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_received", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.connections", proxy.Type, proxy.Id, server), wbackend.PrxConnections, time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.latency", proxy.Type, proxy.Id, server), wbackend.PrxLatency, time.Now().Unix()))
	}

	proxy.GetCluster().AddMetrics(metrics)
}

func (proxy *Proxy) GetWorkingOrchestratorNode() error {
	cluster := proxy.GetCluster()
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return nil
	}

	srvname := cluster.Name + "/svc/" + proxy.GetName()

	svc := cluster.OpenSVCConnect()
	agents, err := svc.GetServiceNodeFromState(srvname)
	if err != nil {
		return fmt.Errorf("unable to get database agent from OpenSVC: %v", err)
	}

	if len(agents) == 0 {
		return fmt.Errorf("no database agents found for service %s", srvname)
	}

	if !slices.Contains(agents, proxy.GetAgent()) {
		proxy.WorkingAgent = agents[0]
	} else {
		proxy.WorkingAgent = proxy.GetAgent()
	}

	return nil
}
