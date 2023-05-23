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
	Id              string               `json:"id"`
	Name            string               `json:"name"`
	Type            string               `json:"type"`
	Host            string               `json:"host"`
	HostIPV6        string               `json:"hostIPV6"`
	Port            string               `json:"port"`
	TunnelPort      int                  `json:"tunnelPort"`
	TunnelWritePort int                  `json:"tunnelWritePort"`
	Tunnel          bool                 `json:"tunnel"`
	User            string               `json:"user"`
	Pass            string               `json:"-"`
	WritePort       int                  `json:"writePort"`
	ReadPort        int                  `json:"readPort"`
	ReadWritePort   int                  `json:"readWritePort"`
	ReaderHostgroup int                  `json:"readerHostGroup"`
	WriterHostgroup int                  `json:"writerHostGroup"`
	BackendsWrite   []Backend            `json:"backendsWrite"`
	BackendsRead    []Backend            `json:"backendsRead"`
	Version         string               `json:"version"`
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
	SetCredential(credential string)

	GetFailCount() int
	SetFailCount(c int)

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

	SetMaintenanceHaproxy(server *ServerMonitor)

	IsFilterInTags(filter string) bool
	IsDown() bool

	GetProxyConfig() string
	// GetInitContainer(collector opensvc.Collector) string
	GetBindAddress() string
	GetBindAddressExtraIPV6() string
	GetUseSSL() string
	GetUseCompression() string
	GetDatadir() string
	GetConfigDatadir() string
	GetConfigConfigdir() string
	GetEnv() map[string]string
	GetConfigProxyModule(variable string) string
	SendStats() error

	OpenSVCGetProxyDefaultSection() map[string]string

	SetSuspect()

	SetID()
	SetDataDir()
	SetServiceName(namespace string)

	SetProvisionCookie() error
	SetUnprovisionCookie() error
	SetReprovCookie() error
	SetRestartCookie() error
	SetWaitStartCookie() error
	SetWaitStopCookie() error

	HasProvisionCookie() bool
	HasUnprovisionCookie() bool
	HasReprovCookie() bool
	HasRestartCookie() bool
	HasWaitStartCookie() bool
	HasWaitStopCookie() bool
	HasDNS() bool

	DelProvisionCookie() error
	DelUnprovisionCookie() error
	DelReprovisionCookie() error
	DelRestartCookie() error
	DelWaitStartCookie() error
	DelWaitStopCookie() error

	RotateProxyPasswords(password string)
}

type Backend struct {
	Host           string `json:"host"`
	Port           string `json:"port"`
	Status         string `json:"status"`
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
		}
	}
	if cluster.Conf.HaproxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.HaproxyHosts, ",") {
			prx := NewHaproxyProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
		}
	}
	if cluster.Conf.ExtProxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.ExtProxyVIP, ",") {
			prx := NewExternalProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
		}

	}
	if cluster.Conf.ProxysqlOn {
		for k, proxyHost := range strings.Split(cluster.Conf.ProxysqlHosts, ",") {
			prx := NewProxySQLProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
		}
	}
	if cluster.Conf.MdbsProxyHosts != "" && cluster.Conf.MdbsProxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.MdbsProxyHosts, ",") {
			prx := NewMariadbShardProxy(k, cluster, proxyHost)
			cluster.AddProxy(prx)
			cluster.LogPrintf(LvlDbg, "New MdbShardProxy proxy created: %s %s", prx.GetHost(), prx.GetPort())
		}
	}
	if cluster.Conf.SphinxHosts != "" && cluster.Conf.SphinxOn {
		for k, proxyHost := range strings.Split(cluster.Conf.SphinxHosts, ",") {
			prx := NewSphinxProxy(k, cluster, proxyHost)

			cluster.AddProxy(prx)
			cluster.LogPrintf(LvlDbg, "New SphinxSearch proxy created: %s %s", prx.GetHost(), prx.GetPort())
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

	cluster.LogPrintf(LvlInfo, "Loaded %d proxies", len(cluster.Proxies))

	return nil
}

func (cluster *Cluster) InjectProxiesTraffic() {
	var definer string
	// Found server from ServerId
	if cluster.GetMaster() != nil {
		for _, pr := range cluster.Proxies {
			if pr.GetType() == config.ConstProxySphinx || pr.GetType() == config.ConstProxyMyProxy {
				// Does not yet understand CREATE OR REPLACE VIEW
				continue
			}
			db, err := pr.GetClusterConnection()
			if err != nil {
				cluster.StateMachine.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				if pr.GetType() == config.ConstProxyMyProxy {
					definer = "DEFINER = root@localhost"
				} else {
					definer = ""
				}
				_, err := db.Exec("CREATE OR REPLACE " + definer + " VIEW replication_manager_schema.pseudo_gtid_v as select '" + misc.GetUUID() + "' from dual")

				if err != nil {
					cluster.StateMachine.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
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
				if cluster.IsVerbose() {
					cluster.LogPrintf(LvlErr, "Can't get a proxy connection: %s", err)
				}
				return false
			}
			defer db.Close()
			var sv map[string]string
			sv, _, err = dbhelper.GetVariables(db, cluster.GetMaster().DBVersion)
			if err != nil {
				if cluster.IsVerbose() {
					cluster.LogPrintf(LvlErr, "Can't get variables: %s", err)
				}
				return false
			}
			var sid uint64
			sid, err = strconv.ParseUint(sv["SERVER_ID"], 10, 64)
			if err != nil {
				if cluster.IsVerbose() {
					cluster.LogPrintf(LvlErr, "Can't form proxy server_id convert: %s", err)
				}
				return false
			}
			if cluster.IsVerbose() {
				cluster.LogPrintf(LvlInfo, "Proxy compare master: %d %d", cluster.GetMaster().ServerID, uint(sid))
			}
			if cluster.GetMaster().ServerID == uint64(sid) || pr.GetType() == config.ConstProxySpider {
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
		cluster.LogPrintf(LvlInfo, "Notify server %s in maintenance in Proxy Type: %s Host: %s Port: %s", server.URL, pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.SetMaintenance(server)
	}
}

// called  by server monitor if state change
func (cluster *Cluster) backendStateChangeProxies() {
	for _, pr := range cluster.Proxies {
		pr.BackendsStateChange()
	}
}

// Used to monitor proxies call by main monitor loop
func (cluster *Cluster) refreshProxies(wcg *sync.WaitGroup) {
	defer wcg.Done()
	for _, pr := range cluster.Proxies {
		if pr != nil {
			var err error
			err = pr.Refresh()
			if err == nil {
				pr.SetFailCount(0)
				pr.SetState(stateProxyRunning)
				if pr.HasWaitStartCookie() {
					pr.DelWaitStartCookie()
					pr.DelProvisionCookie()
				}
			} else {
				pr.SetFailCount(pr.GetFailCount() + 1)
				// TODO: Can pr.ClusterGroup be different from cluster *Cluster? code doesn't imply it. if not change to
				// cl, err := pr.GetClusterConnection()
				// cl.Conf.MaxFail
				if pr.GetFailCount() >= cluster.Conf.MaxFail {
					if pr.GetFailCount() == cluster.Conf.MaxFail {
						cluster.LogPrintf("INFO", "Declaring %s proxy as failed %s:%s %s", pr.GetType(), pr.GetHost(), pr.GetPort(), err)
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
				pr.SetPrevState(pr.GetState())
			}
			if cluster.Conf.GraphiteMetrics {
				pr.SendStats()
			}
		}
	}
}

func (cluster *Cluster) failoverProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "Failover Proxy Type: %s Host: %s Port: %s", pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.Failover()
	}

}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "New proxy monitored: %s %s:%s", pr.GetType(), pr.GetHost(), pr.GetPort())
		pr.Init()
	}
}

func (cluster *Cluster) SendProxyStats(proxy DatabaseProxy) error {
	return proxy.SendStats()
}

func (proxy *Proxy) SendStats() error {
	cluster := proxy.ClusterGroup
	graph, err := graphite.NewGraphite(cluster.Conf.GraphiteCarbonHost, cluster.Conf.GraphiteCarbonPort)
	if err != nil {
		return err
	}
	for _, wbackend := range proxy.BackendsWrite {
		var metrics = make([]graphite.Metric, 4)

		// TODO: clarify what this replacer does and what the purpose is
		replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-", ":", "-")
		server := "rw-" + replacer.Replace(wbackend.PrxName)
		metrics[0] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_send", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix())
		metrics[1] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_received", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix())
		metrics[2] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.connections", proxy.Type, proxy.Id, server), wbackend.PrxConnections, time.Now().Unix())
		metrics[3] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.latency", proxy.Type, proxy.Id, server), wbackend.PrxLatency, time.Now().Unix())
		graph.SendMetrics(metrics)
	}
	for _, wbackend := range proxy.BackendsRead {
		var metrics = make([]graphite.Metric, 4)
		replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-", ":", "-")
		server := "ro-" + replacer.Replace(wbackend.PrxName)
		metrics[0] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_send", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix())
		metrics[1] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.bytes_received", proxy.Type, proxy.Id, server), wbackend.PrxByteOut, time.Now().Unix())
		metrics[2] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.connections", proxy.Type, proxy.Id, server), wbackend.PrxConnections, time.Now().Unix())
		metrics[3] = graphite.NewMetric(fmt.Sprintf("proxy.%s%s.%s.latency", proxy.Type, proxy.Id, server), wbackend.PrxLatency, time.Now().Unix())
		graph.SendMetrics(metrics)
	}

	graph.Disconnect()

	return nil
}
