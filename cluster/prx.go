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
	Id              string          `json:"id"`
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	Host            string          `json:"host"`
	Port            string          `json:"port"`
	TunnelPort      int             `json:"tunnelPort"`
	TunnelWritePort int             `json:"tunnelWritePort"`
	Tunnel          bool            `json:"tunnel"`
	User            string          `json:"user"`
	Pass            string          `json:"pass"`
	WritePort       int             `json:"writePort"`
	ReadPort        int             `json:"readPort"`
	ReadWritePort   int             `json:"readWritePort"`
	ReaderHostgroup int             `json:"readerHostGroup"`
	WriterHostgroup int             `json:"writerHostGroup"`
	BackendsWrite   []Backend       `json:"backendsWrite"`
	BackendsRead    []Backend       `json:"backendsRead"`
	Version         string          `json:"version"`
	InternalProxy   *myproxy.Server `json:"internalProxy"`
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
		for _, proxyHost := range strings.Split(cluster.Conf.MxsHost, ",") {
			cluster.LogPrintf(LvlInfo, "Loading Maxscale...")
			prx := new(Proxy)
			prx.Type = proxyMaxscale

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
			if cluster.Conf.ProvNetCNI {
				prx.Name = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
				prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
			} else {
				prx.Name = proxyHost
				prx.Host = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			}
			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.Conf.HaproxyOn {

		for _, proxyHost := range strings.Split(cluster.Conf.HaproxyHosts, ",") {

			cluster.LogPrintf(LvlInfo, "Loading HaProxy...")

			prx := new(Proxy)
			prx.Type = proxyHaproxy
			prx.Port = strconv.Itoa(cluster.Conf.HaproxyStatPort)

			prx.ReadPort = cluster.Conf.HaproxyReadPort
			prx.WritePort = cluster.Conf.HaproxyWritePort
			prx.ReadWritePort = cluster.Conf.HaproxyWritePort

			crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
			if cluster.Conf.ProvNetCNI {
				prx.Name = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
				prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
			} else {
				prx.Name = proxyHost
				prx.Host = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			}

			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}

			ctproxy++
		}
	}
	if cluster.Conf.ExtProxyOn {
		cluster.LogPrintf(LvlInfo, "Loading External Route...")
		prx := new(Proxy)
		prx.Type = proxyExternal
		prx.Host, prx.Port = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
		prx.WritePort, _ = strconv.Atoi(prx.Port)
		prx.ReadPort = prx.WritePort
		prx.ReadWritePort = prx.WritePort

		if prx.Name == "" {
			prx.Name = prx.Host
		}
		prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)

		cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
		ctproxy++
	}
	if cluster.Conf.ProxysqlOn {
		for _, proxyHost := range strings.Split(cluster.Conf.ProxysqlHosts, ",") {

			cluster.LogPrintf(LvlInfo, "Loading ProxySQL...")

			prx := new(Proxy)
			prx.Type = proxySqlproxy
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
			crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
			if cluster.Conf.ProvNetCNI {
				prx.Name = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
				prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
			} else {
				prx.Name = proxyHost
				prx.Host = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			}

			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.Conf.MdbsProxyHosts != "" && cluster.Conf.MdbsProxyOn {
		for _, proxyHost := range strings.Split(cluster.Conf.MdbsProxyHosts, ",") {
			cluster.LogPrintf(LvlInfo, "Loading MdbShardProxy...")
			prx := new(Proxy)
			prx.Type = proxySpider
			prx.Host, prx.Port = misc.SplitHostPort(proxyHost)
			prx.User, prx.Pass = misc.SplitPair(cluster.Conf.MdbsProxyUser)
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)

			if cluster.Conf.ProvNetCNI {
				prx.Name = proxyHost
				prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
				prx.Port = "3306"
			} else {
				prx.Name = prx.Host
			}
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)

			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New MdbShardProxy proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.Conf.SphinxHosts != "" && cluster.Conf.SphinxOn {
		for _, proxyHost := range strings.Split(cluster.Conf.SphinxHosts, ",") {
			cluster.LogPrintf(LvlInfo, "Loading SphinxSearch Proxy...")
			prx := new(Proxy)
			prx.Type = proxySphinx

			prx.Port = cluster.Conf.SphinxQLPort
			prx.User = ""
			prx.Pass = ""
			prx.ReadPort, _ = strconv.Atoi(prx.Port)
			prx.WritePort, _ = strconv.Atoi(prx.Port)
			prx.ReadWritePort, _ = strconv.Atoi(prx.Port)
			crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
			if cluster.Conf.ProvNetCNI {
				prx.Name = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
				prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
			} else {
				prx.Name = proxyHost
				prx.Host = proxyHost
				prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
			}

			cluster.Proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			cluster.LogPrintf(LvlDbg, "New SphinxSearch proxy created: %s %s", prx.Host, prx.Port)
			ctproxy++
		}
	}
	if cluster.Conf.MyproxyOn {
		cluster.LogPrintf(LvlInfo, "Loading MyProxy...")

		prx := new(Proxy)
		prx.Type = proxyMyProxy
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
		prx.Id = strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+prx.Name+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)
		if prx.Host == "" {
			prx.Host = prx.Id + ".svc." + cluster.Conf.ProvNetCNICluster
		}

		cluster.Proxies[ctproxy], err = cluster.newProxy(prx)

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
	var definer string
	// Found server from ServerId
	if cluster.GetMaster() != nil {
		for _, pr := range cluster.Proxies {
			if pr.Type == proxySphinx || pr.Type == proxyMyProxy {
				// Does not yet understand CREATE OR REPLACE VIEW
				continue
			}
			db, err := cluster.GetClusterThisProxyConn(pr)
			if err != nil {
				cluster.sme.AddState("ERR00050", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00050"], err), ErrFrom: "TOPO"})
			} else {
				if pr.Type == proxyMyProxy {
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
	for _, pr := range cluster.Proxies {
		if cluster.Conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.Conf.MxsOn && pr.Type == proxyMaxscale {
			//intsrvid, _ := strconv.Atoi(serverid)
			server := cluster.GetServerFromId(serverid)
			if cluster.GetMaster() != nil {
				cluster.setMaintenanceMaxscale(pr, server)
			}
		}
		if cluster.Conf.ProxysqlOn && pr.Type == proxySqlproxy {
			if cluster.GetMaster() != nil {
				server := cluster.GetServerFromId(serverid)
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
func (cluster *Cluster) refreshProxies() {

	for _, pr := range cluster.Proxies {
		if cluster.Conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.refreshMaxscale(pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == proxySpider {
			if cluster.GetStateMachine().GetHeartbeats()%20 == 0 {
				cluster.refreshMdbsproxy(nil, pr)
			}
		}
		if cluster.Conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.refreshProxysql(pr)
		}
		if cluster.Conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.refreshHaproxy(pr)
		}
		if cluster.Conf.SphinxOn && pr.Type == proxySphinx {
			cluster.refreshSphinx(pr)
		}
		if cluster.Conf.GraphiteMetrics {
			cluster.SendProxyStats(pr)
		}
	}

}

func (cluster *Cluster) failoverProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "Failover Proxy Type: %s Host: %s Port: %s", pr.Type, pr.Host, pr.Port)
		if cluster.Conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.Conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == proxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
		if cluster.Conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.failoverProxysql(pr)
		}
	}
	cluster.initConsul()
}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.Proxies {
		cluster.LogPrintf(LvlInfo, "Init Proxy Type: %s Host: %s Port: %s", pr.Type, pr.Host, pr.Port)
		if cluster.Conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.Conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.Conf.MdbsProxyOn && pr.Type == proxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
		if cluster.Conf.ProxysqlOn && pr.Type == proxySqlproxy {
			cluster.initProxysql(pr)
		}
		if cluster.Conf.MyproxyOn && pr.Type == proxyMyProxy {
			cluster.initMyProxy(pr)
		}
	}

	cluster.initConsul()
}

func (cluster *Cluster) GetClusterProxyConn() (*sqlx.DB, error) {
	if len(cluster.Proxies) == 0 {
		return nil, errors.New("No proxies defined")
	}
	prx := cluster.Proxies[0]

	params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)

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
	params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)
	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if cluster.Conf.MonitorWriteHeartbeatCredential != "" {
		dsn = cluster.Conf.MonitorWriteHeartbeatCredential + "@"
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
	for _, pr := range cluster.Proxies {
		if pr.Id == name {
			return pr
		}
	}
	return nil
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
