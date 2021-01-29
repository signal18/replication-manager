// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"hash/crc64"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/router/myproxy"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// Proxy defines a proxy
type Proxy struct {
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

type proxyList []*Proxy

func (cluster *Cluster) newProxyList() error {
	nbproxies := 0

	crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
	if cluster.Conf.MxsHost != "" && cluster.Conf.MxsOn {
		nbproxies += len(strings.Split(cluster.Conf.MxsHost, ","))
	}
	if cluster.Conf.HaproxyOn {
		nbproxies += len(strings.Split(cluster.Conf.HaproxyHosts, ","))
	}
	if cluster.Conf.MdbsProxyHosts != "" && cluster.Conf.MdbsProxyOn {
		nbproxies += len(strings.Split(cluster.Conf.MdbsProxyHosts, ","))
	}
	if cluster.Conf.ProxysqlOn {
		nbproxies += len(strings.Split(cluster.Conf.ProxysqlHosts, ","))
	}
	if cluster.Conf.MysqlRouterOn {
		nbproxies += len(strings.Split(cluster.Conf.MysqlRouterHosts, ","))
	}
	if cluster.Conf.SphinxOn {
		nbproxies += len(strings.Split(cluster.Conf.SphinxHosts, ","))
	}
	if cluster.Conf.ExtProxyOn {
		nbproxies++
	}
	// internal myproxy
	if cluster.Conf.MyproxyOn {
		nbproxies++
	}
	cluster.Proxies = make([]*Proxy, nbproxies)

	cluster.LogPrintf(LvlInfo, "Loading %d proxies", nbproxies)

	var ctproxy = 0
	var err error

	if cluster.Conf.MxsHost != "" && cluster.Conf.MxsOn {

		for k, proxyHost := range strings.Split(cluster.Conf.MxsHost, ",") {
			prx := new(Proxy)
			prx.Type = config.ConstProxyMaxscale
			prx.SetPlacement(k, cluster.Conf.ProvProxAgents, cluster.Conf.SlapOSMaxscalePartitions, cluster.Conf.MxsHostsIPV6)
			prx.Port = cluster.Conf.MxsPort
			prx.User = cluster.Conf.MxsUser
			prx.Pass = cluster.Conf.MxsPass
			if cluster.key != nil {
				p := crypto.Password{Key: cluster.key}
				p.CipherText = prx.Pass
				p.Decrypt()
				prx.Pass = p.PlainText
			}
			prx.ReadPort = cluster.Conf.MxsReadPort
			prx.WritePort = cluster.Conf.MxsWritePort
			prx.ReadWritePort = cluster.Conf.MxsReadWritePort
			prx.Name = proxyHost
			prx.Host = proxyHost
			if cluster.Conf.ProvNetCNI {
				prx.Host = prx.Host + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
			}
			prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			prx.ClusterGroup = cluster

			prx.SetDataDir()
			prx.SetServiceName(cluster.Name, prx.Name)
			cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)

			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.Conf.HaproxyOn {

		for k, proxyHost := range strings.Split(cluster.Conf.HaproxyHosts, ",") {
			prx := new(Proxy)
			prx.SetPlacement(k, cluster.Conf.ProvProxAgents, cluster.Conf.SlapOSHaProxyPartitions, cluster.Conf.HaproxyHostsIPV6)
			prx.Type = config.ConstProxyHaproxy
			prx.Port = strconv.Itoa(cluster.Conf.HaproxyAPIPort)
			prx.ReadPort = cluster.Conf.HaproxyReadPort
			prx.WritePort = cluster.Conf.HaproxyWritePort
			prx.ReadWritePort = cluster.Conf.HaproxyWritePort
			prx.Name = proxyHost
			prx.Host = proxyHost
			if cluster.Conf.ProvNetCNI {
				prx.Host = prx.Host + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
			}
			prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			prx.ClusterGroup = cluster
			prx.SetDataDir()
			prx.SetServiceName(cluster.Name, prx.Name)
			cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}

			ctproxy++
		}
	}
	if cluster.Conf.ExtProxyOn {
		prx := new(Proxy)
		prx.Type = config.ConstProxyExternal
		prx.Host, prx.Port = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
		prx.WritePort, _ = strconv.Atoi(prx.Port)
		prx.ReadPort = prx.WritePort
		prx.ReadWritePort = prx.WritePort
		if prx.Name == "" {
			prx.Name = prx.Host
		}
		prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
		prx.ClusterGroup = cluster
		prx.SetDataDir()
		prx.SetServiceName(cluster.Name, prx.Name)
		cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
		cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
		ctproxy++
	}
	if cluster.Conf.ProxysqlOn {

		for k, proxyHost := range strings.Split(cluster.Conf.ProxysqlHosts, ",") {

			prx := new(Proxy)
			prx.SetPlacement(k, cluster.Conf.ProvProxAgents, cluster.Conf.SlapOSProxySQLPartitions, cluster.Conf.ProxysqlHostsIPV6)
			prx.Type = config.ConstProxySqlproxy
			prx.Port = cluster.Conf.ProxysqlAdminPort
			prx.ReadWritePort, _ = strconv.Atoi(cluster.Conf.ProxysqlPort)
			prx.User = cluster.Conf.ProxysqlUser
			prx.Pass = cluster.Conf.ProxysqlPassword
			prx.ReaderHostgroup, _ = strconv.Atoi(cluster.Conf.ProxysqlReaderHostgroup)
			prx.WriterHostgroup, _ = strconv.Atoi(cluster.Conf.ProxysqlWriterHostgroup)
			prx.WritePort, _ = strconv.Atoi(cluster.Conf.ProxysqlPort)
			prx.ReadPort, _ = strconv.Atoi(cluster.Conf.ProxysqlPort)
			if cluster.key != nil {
				p := crypto.Password{Key: cluster.key}
				p.CipherText = prx.Pass
				p.Decrypt()
				prx.Pass = p.PlainText
			}
			prx.Name = proxyHost
			prx.Host = proxyHost
			if cluster.Conf.ProvNetCNI {
				if cluster.Conf.ClusterHead == "" {
					prx.Host = prx.Host + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
				} else {
					prx.Host = prx.Host + "." + cluster.Conf.ClusterHead + ".svc." + cluster.Conf.ProvOrchestratorCluster
				}
			}
			prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			prx.ClusterGroup = cluster
			prx.SetDataDir()
			prx.SetServiceName(cluster.Name, prx.Name)
			cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.Conf.MdbsProxyHosts != "" && cluster.Conf.MdbsProxyOn {
		for k, proxyHost := range strings.Split(cluster.Conf.MdbsProxyHosts, ",") {
			prx := new(Proxy)
			prx.SetPlacement(k, cluster.Conf.ProvProxAgents, cluster.Conf.SlapOSShardProxyPartitions, cluster.Conf.MdbsHostsIPV6)
			prx.Type = config.ConstProxySpider
			prx.Host, prx.Port = misc.SplitHostPort(proxyHost)
			prx.User, prx.Pass = misc.SplitPair(cluster.Conf.MdbsProxyCredential)
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)
			prx.Name = proxyHost
			if cluster.Conf.ProvNetCNI {
				if cluster.Conf.ClusterHead == "" {
					prx.Host = prx.Host + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
				} else {
					prx.Host = prx.Host + "." + cluster.Conf.ClusterHead + ".svc." + cluster.Conf.ProvOrchestratorCluster
				}
				prx.Port = "3306"
			}
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			prx.ClusterGroup = cluster
			prx.SetDataDir()
			prx.SetServiceName(cluster.Name, prx.Name)
			cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New MdbShardProxy proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.Conf.SphinxHosts != "" && cluster.Conf.SphinxOn {
		for k, proxyHost := range strings.Split(cluster.Conf.SphinxHosts, ",") {
			prx := new(Proxy)
			prx.SetPlacement(k, cluster.Conf.ProvProxAgents, cluster.Conf.SlapOSSphinxPartitions, cluster.Conf.SphinxHostsIPV6)
			prx.Type = config.ConstProxySphinx

			prx.Port = cluster.Conf.SphinxQLPort
			prx.User = ""
			prx.Pass = ""
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)
			prx.Name = proxyHost
			prx.Host = proxyHost
			if cluster.Conf.ProvNetCNI {
				prx.Host = prx.Host + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
			}
			prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			prx.ClusterGroup = cluster
			prx.SetDataDir()
			prx.SetServiceName(cluster.Name, prx.Name)
			cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New SphinxSearch proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.Conf.MyproxyOn {
		prx := new(Proxy)
		prx.Type = config.ConstProxyMyProxy
		prx.Port = strconv.Itoa(cluster.Conf.MyproxyPort)
		prx.Host = "0.0.0.0"
		prx.ReadPort = cluster.Conf.MyproxyPort
		prx.WritePort = cluster.Conf.MyproxyPort
		prx.ReadWritePort = cluster.Conf.MyproxyPort
		prx.User = cluster.Conf.MyproxyUser
		prx.Pass = cluster.Conf.MyproxyPassword
		if prx.Name == "" {
			prx.Name = prx.Host
		}
		prx.Id = "px" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
		if prx.Host == "" {
			prx.Host = "repman." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
		}
		prx.ClusterGroup = cluster
		prx.SetDataDir()
		prx.SetServiceName(cluster.Name, prx.Name)
		cluster.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.Type, prx.Host, prx.Port)
		cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
		ctproxy++
	}

	return nil
}

func (cluster *Cluster) newProxy(p *Proxy) (*Proxy, error) {
	proxy := new(Proxy)
	proxy = p
	proxy.State = stateSuspect
	return proxy, nil
}

func (cluster *Cluster) InjectProxiesTraffic() {
	var definer string
	// Found server from ServerId
	if cluster.GetMaster() != nil {
		for _, pr := range cluster.Proxies {
			if pr.Type == config.ConstProxySphinx || pr.Type == config.ConstProxyMyProxy {
				// Does not yet understand CREATE OR REPLACE VIEW
				continue
			}
			db, err := cluster.GetClusterThisProxyConn(pr)
			if err != nil {
				cluster.sme.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				if pr.Type == config.ConstProxyMyProxy {
					definer = "DEFINER = root@localhost"
				} else {
					definer = ""
				}
				_, err := db.Exec("CREATE OR REPLACE " + definer + " VIEW replication_manager_schema.pseudo_gtid_v as select '" + misc.GetUUID() + "' from dual")

				if err != nil {
					cluster.sme.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
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
			db, err := cluster.GetClusterThisProxyConn(pr)
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
			if cluster.GetMaster().ServerID == uint64(sid) || pr.Type == config.ConstProxySpider {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) SetProxyServerMaintenance(serverid uint64) {
	// Found server from ServerId
	for _, pr := range cluster.Proxies {
		server := cluster.GetServerFromId(serverid)
		if cluster.Conf.HaproxyOn && pr.Type == config.ConstProxyHaproxy {
			if cluster.Conf.HaproxyMode == "runtimeapi" {
				cluster.setMaintenanceHaproxy(pr, server)
			}
			if cluster.Conf.HaproxyMode == "standby" {
				cluster.initHaproxy(pr)
			}
		}
		if cluster.Conf.MxsOn && pr.Type == config.ConstProxyMaxscale {
			if cluster.GetMaster() != nil {
				cluster.setMaintenanceMaxscale(pr, server)
			}
		}
		if cluster.Conf.ProxysqlOn && pr.Type == config.ConstProxySqlproxy {
			if cluster.GetMaster() != nil {
				cluster.setMaintenanceProxysql(pr, server)
			}
		}
	}
	cluster.initConsul()
}

// called  by server monitor if state change
func (cluster *Cluster) backendStateChangeProxies() {
	cluster.initConsul()
}

// Used to monitor proxies call by main monitor loop
func (cluster *Cluster) refreshProxies(wcg *sync.WaitGroup) {
	defer wcg.Done()

	for _, pr := range cluster.Proxies {
		var err error
		if cluster.Conf.MxsOn && pr.Type == config.ConstProxyMaxscale {
			err = cluster.refreshMaxscale(pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == config.ConstProxySpider {
			err = cluster.refreshMdbsproxy(nil, pr)
		}
		if cluster.Conf.ProxysqlOn && pr.Type == config.ConstProxySqlproxy {
			err = cluster.refreshProxysql(pr)
		}
		if cluster.Conf.HaproxyOn && pr.Type == config.ConstProxyHaproxy {
			err = cluster.refreshHaproxy(pr)
		}
		if cluster.Conf.SphinxOn && pr.Type == config.ConstProxySphinx {
			err = cluster.refreshSphinx(pr)
		}
		if err == nil {
			pr.FailCount = 0
			pr.State = stateProxyRunning
			if pr.HasWaitStartCookie() {
				pr.DelWaitStartCookie()
			}
		} else {
			pr.FailCount++
			if pr.FailCount >= pr.ClusterGroup.Conf.MaxFail {
				if pr.FailCount == pr.ClusterGroup.Conf.MaxFail {
					pr.ClusterGroup.LogPrintf("INFO", "Declaring %s proxy as failed %s:%s %s", pr.Type, pr.Host, pr.Port, err)
				}
				pr.State = stateFailed
				pr.DelWaitStopCookie()
				pr.DelRestartCookie()
			} else {
				pr.State = stateSuspect
			}
		}
		if pr.PrevState != pr.State {
			pr.PrevState = pr.State
		}
		if cluster.Conf.GraphiteMetrics {
			cluster.SendProxyStats(pr)
		}
	}

}

func (cluster *Cluster) failoverProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "Failover Proxy Type: %s Host: %s Port: %s", pr.Type, pr.Host, pr.Port)
		if cluster.Conf.HaproxyOn && pr.Type == config.ConstProxyHaproxy {
			if cluster.Conf.HaproxyMode == "runtimeapi" {
				cluster.refreshHaproxy(pr)
			}
			if cluster.Conf.HaproxyMode == "standby" {
				cluster.initHaproxy(pr)
			}
		}
		if cluster.Conf.MxsOn && pr.Type == config.ConstProxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == config.ConstProxySpider {
			cluster.failoverMdbsproxy(nil, pr)
		}
		if cluster.Conf.ProxysqlOn && pr.Type == config.ConstProxySqlproxy {
			cluster.failoverProxysql(pr)
		}
	}
	cluster.initConsul()
}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "New proxy monitored: %s %s:%s", pr.Type, pr.Host, pr.Port)
		if cluster.Conf.HaproxyOn && pr.Type == config.ConstProxyHaproxy {
			cluster.initHaproxy(pr)
		}
		if cluster.Conf.MxsOn && pr.Type == config.ConstProxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == config.ConstProxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
		if cluster.Conf.ProxysqlOn && pr.Type == config.ConstProxySqlproxy {
			cluster.initProxysql(pr)
		}
		if cluster.Conf.MyproxyOn && pr.Type == config.ConstProxyMyProxy {
			cluster.initMyProxy(pr)
		}
	}
	cluster.initConsul()
}

func (cluster *Cluster) SendProxyStats(proxy *Proxy) error {
	graph, err := graphite.NewGraphite(cluster.Conf.GraphiteCarbonHost, cluster.Conf.GraphiteCarbonPort)
	if err != nil {
		return err
	}
	for _, wbackend := range proxy.BackendsWrite {
		var metrics = make([]graphite.Metric, 4)
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
