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
	"errors"
	"fmt"
	"hash/crc64"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/crypto"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/myproxy"
	"github.com/signal18/replication-manager/state"
)

// Proxy defines a proxy
type Proxy struct {
	Id              string
	Type            string
	Host            string
	Port            string
	TunnelPort      int
	TunnelWritePort int
	Tunnel          bool
	User            string
	Pass            string
	WritePort       int
	ReadPort        int
	ReadWritePort   int
	ReaderHostgroup int
	WriterHostgroup int
	BackendsWrite   []Backend
	BackendsRead    []Backend
	Version         string
	InternalProxy   *myproxy.Server
}

type Backend struct {
	Host           string
	Port           string
	Status         string
	PrxName        string
	PrxStatus      string
	PrxConnections string
	PrxHostgroup   string
	PrxByteOut     string
	PrxByteIn      string
	PrxLatency     string
	PrxMaintenance bool
}

const (
	proxyMaxscale    string = "maxscale"
	proxyHaproxy     string = "haproxy"
	proxySqlproxy    string = "proxysql"
	proxySpider      string = "shardproxy"
	proxyExternal    string = "extproxy"
	proxyMysqlrouter string = "mysqlrouter"
	proxySphinx      string = "sphinx"
	proxyMyProxy     string = "myproxy"
)

type proxyList []*Proxy

func (cluster *Cluster) newProxyList() error {
	nbproxies := 0
	if cluster.conf.MxsHost != "" && cluster.conf.MxsOn {
		nbproxies += len(strings.Split(cluster.conf.MxsHost, ","))
	}
	if cluster.conf.HaproxyOn {
		nbproxies += len(strings.Split(cluster.conf.HaproxyHosts, ","))
	}
	if cluster.conf.MdbsProxyHosts != "" && cluster.conf.MdbsProxyOn {
		nbproxies += len(strings.Split(cluster.conf.MdbsProxyHosts, ","))
	}
	if cluster.conf.ProxysqlOn {
		nbproxies += len(strings.Split(cluster.conf.ProxysqlHosts, ","))
	}
	if cluster.conf.MysqlRouterOn {
		nbproxies += len(strings.Split(cluster.conf.MysqlRouterHosts, ","))
	}
	if cluster.conf.SphinxOn {
		nbproxies += len(strings.Split(cluster.conf.SphinxHosts, ","))
	}
	if cluster.conf.ExtProxyOn {
		nbproxies++
	}
	// internal myproxy
	if cluster.conf.MyproxyOn {
		nbproxies++
	}
	cluster.proxies = make([]*Proxy, nbproxies)

	cluster.LogPrintf(LvlInfo, "Loading %d proxies", nbproxies)

	var ctproxy = 0
	var err error
	if cluster.conf.MxsHost != "" && cluster.conf.MxsOn {
		for _, proxyHost := range strings.Split(cluster.conf.MxsHost, ",") {
			cluster.LogPrintf(LvlInfo, "Loading Maxscale...")
			prx := new(Proxy)
			prx.Type = proxyMaxscale
			prx.Host = proxyHost
			prx.Port = cluster.conf.MxsPort
			prx.User = cluster.conf.MxsUser
			prx.Pass = cluster.conf.MxsPass
			if cluster.key != nil {
				p := crypto.Password{Key: cluster.key}
				p.CipherText = prx.Pass
				p.Decrypt()
				prx.Pass = p.PlainText
			}
			prx.ReadPort = cluster.conf.MxsReadPort
			prx.WritePort = cluster.conf.MxsWritePort
			prx.ReadWritePort = cluster.conf.MxsReadWritePort

			crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)

			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.conf.HaproxyOn {

		for _, proxyHost := range strings.Split(cluster.conf.HaproxyHosts, ",") {

			cluster.LogPrintf(LvlInfo, "Loading HaProxy...")

			prx := new(Proxy)
			prx.Type = proxyHaproxy
			prx.Port = strconv.Itoa(cluster.conf.HaproxyStatPort)
			prx.Host = proxyHost
			prx.ReadPort = cluster.conf.HaproxyReadPort
			prx.WritePort = cluster.conf.HaproxyWritePort
			prx.ReadWritePort = cluster.conf.HaproxyWritePort

			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}

			ctproxy++
		}
	}
	if cluster.conf.ExtProxyOn {
		prx := new(Proxy)
		prx.Type = proxyExternal
		prx.Host, prx.Port = misc.SplitHostPort(cluster.conf.ExtProxyVIP)
		prx.WritePort, _ = strconv.Atoi(prx.Port)
		prx.ReadPort = prx.WritePort
		prx.ReadWritePort = prx.WritePort
		ctproxy++
	}
	if cluster.conf.ProxysqlOn {

		for _, proxyHost := range strings.Split(cluster.conf.ProxysqlHosts, ",") {

			cluster.LogPrintf(LvlInfo, "Loading ProxySQL...")

			prx := new(Proxy)
			prx.Type = proxySqlproxy
			prx.Port = cluster.conf.ProxysqlAdminPort
			prx.ReadWritePort, _ = strconv.Atoi(cluster.conf.ProxysqlPort)
			prx.Host = proxyHost
			prx.User = cluster.conf.ProxysqlUser
			prx.Pass = cluster.conf.ProxysqlPassword
			prx.ReaderHostgroup, _ = strconv.Atoi(cluster.conf.ProxysqlReaderHostgroup)
			prx.WriterHostgroup, _ = strconv.Atoi(cluster.conf.ProxysqlWriterHostgroup)
			prx.WritePort, _ = strconv.Atoi(cluster.conf.ProxysqlPort)
			prx.ReadPort, _ = strconv.Atoi(cluster.conf.ProxysqlPort)

			if cluster.key != nil {
				p := crypto.Password{Key: cluster.key}
				p.CipherText = prx.Pass
				p.Decrypt()
				prx.Pass = p.PlainText
			}
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}

			ctproxy++
		}

	}

	if cluster.conf.MdbsProxyHosts != "" && cluster.conf.MdbsProxyOn {
		for _, proxyHost := range strings.Split(cluster.conf.MdbsProxyHosts, ",") {
			cluster.LogPrintf(LvlInfo, "Loading MdbShardProxy...")
			prx := new(Proxy)
			prx.Type = proxySpider
			prx.Host, prx.Port = misc.SplitHostPort(proxyHost)
			prx.User, prx.Pass = misc.SplitPair(cluster.conf.MdbsProxyUser)
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New MdbShardProxy proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.conf.SphinxHosts != "" && cluster.conf.SphinxOn {
		for _, proxyHost := range strings.Split(cluster.conf.SphinxHosts, ",") {
			cluster.LogPrintf(LvlInfo, "Loading SphinxSearch Proxy...")
			prx := new(Proxy)
			prx.Type = proxySphinx
			prx.Host = proxyHost
			prx.Port = cluster.conf.SphinxQLPort
			prx.User = ""
			prx.Pass = ""
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New SphinxSearch proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.conf.MyproxyOn {
		cluster.LogPrintf(LvlInfo, "Loading MyProxy...")

		prx := new(Proxy)
		prx.Type = proxyMyProxy
		prx.Port = strconv.Itoa(cluster.conf.MyproxyPort)
		prx.Host = "0.0.0.0"
		prx.ReadPort = cluster.conf.MyproxyPort
		prx.WritePort = cluster.conf.MyproxyPort
		prx.ReadWritePort = cluster.conf.MyproxyPort
		prx.User = cluster.conf.MyproxyUser
		prx.Pass = cluster.conf.MyproxyPassword
		prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
		cluster.proxies[ctproxy], err = cluster.newProxy(prx)

		ctproxy++
	}

	return nil
}

func (cluster *Cluster) newProxy(p *Proxy) (*Proxy, error) {
	proxy := new(Proxy)
	proxy = p
	return proxy, nil
}

func (cluster *Cluster) InjectTraffic() {
	// Found server from ServerId
	if cluster.GetMaster() != nil {
		for _, pr := range cluster.proxies {
			db, err := cluster.GetClusterThisProxyConn(pr)
			if err != nil {
				cluster.sme.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				_, err := db.Exec("create or replace view replication_manager_schema.pseudo_gtid_v as select '" + misc.GetUUID() + "' from dual")

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
		for _, pr := range cluster.proxies {
			db, err := cluster.GetClusterThisProxyConn(pr)
			if err != nil {
				if cluster.IsVerbose() {
					cluster.LogPrintf(LvlErr, "Can't get a proxy connection: %s", err)
				}
				return false
			}
			defer db.Close()
			var sv map[string]string
			sv, err = dbhelper.GetVariables(db)
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
			if cluster.GetMaster().ServerID == uint(sid) || pr.Type == proxySpider {
				return true
			}
		}
	}
	return false
}

func (cluster *Cluster) SetProxyServerMaintenance(serverid uint) {
	// Found server from ServerId
	for _, pr := range cluster.proxies {
		if cluster.conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.conf.MxsOn && pr.Type == proxyMaxscale {
			//intsrvid, _ := strconv.Atoi(serverid)
			server := cluster.GetServerFromId(serverid)
			if cluster.GetMaster() != nil {
				cluster.setMaintenanceMaxscale(pr, server)
			}
		}
		if cluster.conf.ProxysqlOn && pr.Type == proxySqlproxy {
			if cluster.GetMaster() != nil {
				server := cluster.GetServerFromId(serverid)
				cluster.setMaintenanceProxysql(pr, server)
			}
		}
	}
}

func (cluster *Cluster) refreshProxies() {

	for _, pr := range cluster.proxies {
		if cluster.conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.refreshMaxscale(pr)
		}
		if cluster.conf.MdbsProxyOn && pr.Type == proxySpider {
			if cluster.GetStateMachine().GetHeartbeats()%20 == 0 {
				cluster.refreshMdbsproxy(nil, pr)
			}
		}
		if cluster.conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.refreshProxysql(pr)
		}
		if cluster.conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.refreshHaproxy(pr)
		}
		if cluster.conf.SphinxOn && pr.Type == proxySphinx {
			cluster.refreshSphinx(pr)
		}
		if cluster.conf.GraphiteMetrics {
			cluster.SendProxyStats(pr)
		}
	}

}

func (cluster *Cluster) failoverProxies() {
	for _, pr := range cluster.proxies {
		cluster.LogPrintf(LvlInfo, "Failover Proxy Type: %s Host: %s Port: %s", pr.Type, pr.Host, pr.Port)
		if cluster.conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.conf.MdbsProxyOn && pr.Type == proxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
		if cluster.conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.failoverProxysql(pr)
		}
	}
	cluster.initConsul()
}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.proxies {
		cluster.LogPrintf(LvlInfo, "Init Proxy Type: %s Host: %s Port: %s", pr.Type, pr.Host, pr.Port)
		if cluster.conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.conf.MdbsProxyOn && pr.Type == proxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
		if cluster.conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.initProxysql(pr)
		}
		if cluster.conf.MyproxyOn && pr.Type == proxyMyProxy {
			cluster.initMyProxy(pr)
		}
	}

	cluster.initConsul()
}

func (cluster *Cluster) GetClusterProxyConn() (*sqlx.DB, error) {
	if len(cluster.proxies) == 0 {
		return nil, errors.New("No proxies defined")
	}
	prx := cluster.proxies[0]

	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)

	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if prx.Host != "" {
		dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
	} else {

		return nil, errors.New("No proxies definition")
	}
	conn, err := sqlx.Open("mysql", dsn)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get a proxy %s connection: %s", dsn, err)
	}
	return conn, err

}

func (cluster *Cluster) GetClusterThisProxyConn(prx *Proxy) (*sqlx.DB, error) {
	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)
	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if cluster.conf.MonitorWriteHeartbeatCredential != "" {
		dsn = cluster.conf.MonitorWriteHeartbeatCredential + "@"
	}

	if prx.Host != "" {
		if prx.Tunnel {
			dsn += "tcp(localhost:" + strconv.Itoa(prx.TunnelWritePort) + ")/" + params
		} else {
			dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
		}
	}
	return sqlx.Open("mysql", dsn)

}

func (cluster *Cluster) GetProxyFromName(name string) *Proxy {
	for _, pr := range cluster.proxies {
		if pr.Id == name {
			return pr
		}
	}
	return nil
}

func (cluster *Cluster) SendProxyStats(proxy *Proxy) error {
	graph, err := graphite.NewGraphite(cluster.conf.GraphiteCarbonHost, cluster.conf.GraphiteCarbonPort)
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
