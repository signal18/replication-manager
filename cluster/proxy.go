// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"hash/crc64"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
)

// Proxy defines a proxy
type Proxy struct {
	Id            string
	Type          string
	Host          string
	Port          string
	User          string
	Pass          string
	WritePort     int
	ReadPort      int
	ReadWritePort int
}

const (
	proxyMaxscale string = "maxscale"
	proxyHaproxy  string = "haproxy"
	proxySqlproxy string = "sqlproxy"
	proxySpider   string = "mdbsproxy"
)

type proxyList []*Proxy

func (cluster *Cluster) newProxyList() error {
	nbproxies := 0
	if cluster.conf.MxsHost != "" && cluster.conf.MxsOn {
		nbproxies += len(strings.Split(cluster.conf.MxsHost, ","))
	}
	if cluster.conf.HaproxyOn {
		nbproxies++
	}
	if cluster.conf.MdbsProxyHosts != "" && cluster.conf.MdbsProxyOn {
		nbproxies += len(strings.Split(cluster.conf.MdbsProxyHosts, ","))
	}

	cluster.proxies = make([]*Proxy, nbproxies)

	cluster.LogPrintf("INFO", "Loading %d proxies", nbproxies)

	var ctproxy = 0
	var err error
	if cluster.conf.MxsHost != "" && cluster.conf.MxsOn {
		for _, proxyHost := range strings.Split(cluster.conf.MxsHost, ",") {
			cluster.LogPrintf("INFO", "Loading Maxscale...")
			prx := new(Proxy)
			prx.Type = proxyMaxscale
			prx.Host = proxyHost
			prx.Port = cluster.conf.MxsPort
			prx.User = cluster.conf.MxsUser
			prx.Pass = cluster.conf.MxsPass
			prx.ReadPort = cluster.conf.MxsReadPort
			prx.WritePort = cluster.conf.MxsWritePort
			prx.ReadWritePort = cluster.conf.MxsReadWritePort

			crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
			prx.Id = strconv.FormatUint(crc64.Checksum([]byte(prx.Host+":"+strconv.Itoa(prx.WritePort)), crcTable), 10)

			cluster.proxies[ctproxy], err = cluster.newProxy(prx)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			ctproxy++
		}
	}
	if cluster.conf.HaproxyOn {

		for _, proxyHost := range strings.Split(cluster.conf.HaproxyHosts, ",") {

			cluster.LogPrintf("INFO", "Loading HaProxy...")

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
				cluster.LogPrintf("ERROR", "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}

			ctproxy++
		}
	}
	if cluster.conf.MdbsProxyHosts != "" && cluster.conf.MdbsProxyOn {
		for _, proxyHost := range strings.Split(cluster.conf.MdbsProxyHosts, ",") {
			cluster.LogPrintf("INFO", "Loading MdbShardProxy...")
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
				cluster.LogPrintf("ERROR", "Could not open connection to proxy %s %s: %s", prx.Host, prx.Port, err)
			}
			if cluster.conf.LogLevel > 1 {
				cluster.LogPrintf("DEBUG", "New MdbShardProxy proxy created: %s %s", prx.Host, prx.Port)
			}
			ctproxy++
		}
	}

	return nil
}

func (cluster *Cluster) newProxy(p *Proxy) (*Proxy, error) {
	proxy := new(Proxy)
	proxy = p
	return proxy, nil
}

func (cluster *Cluster) SetMaintenance(serverid string) {
	// Found server from ServerId
	for _, pr := range cluster.proxies {
		if cluster.conf.MxsOn && pr.Type == "maxscale" {
			intsrvid, _ := strconv.Atoi(serverid)
			server := cluster.GetServerFromId(uint(intsrvid))
			for _, p := range cluster.proxies {
				if cluster.master != nil {
					if p.Type == proxyMaxscale {
						m := maxscale.MaxScale{Host: pr.Host, Port: pr.Port, User: pr.User, Pass: pr.Pass}
						err := m.Connect()
						if err != nil {
							cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
						}
						err = m.SetServer(server.MxsServerName, "maintenance")
						if err != nil {
							cluster.LogPrintf("ERROR", "Could not set  server %s in maintenance", err)
							m.Close()
						}
						m.Close()
					}
				}
			}
		}
	}
}

func (cluster *Cluster) InjectTraffic() {
	// Found server from ServerId
	if cluster.master != nil {
		for _, pr := range cluster.proxies {
			db, err := cluster.GetClusterThisProxyConn(pr)
			if err != nil {
				cluster.LogPrintf("ERROR", "%s", err)
			} else {
				err := dbhelper.InjectTrx(db)
				if err != nil {
					cluster.LogPrintf("ERROR", "%s", err.Error())
				}
				db.Close()
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
			if cluster.GetStateMachine().GetHeartbeats()%60 == 0 {
				cluster.refreshMdbsproxy(nil, pr)
			}
		}
	}

}
func (cluster *Cluster) failoverProxies() {
	cluster.initProxies()
}

func (cluster *Cluster) initProxies() {
	for _, pr := range cluster.proxies {
		cluster.LogPrintf("INFO", "Init %s %s %s", pr.Type, pr.Host, pr.Port)
		if cluster.conf.HaproxyOn && pr.Type == proxyHaproxy {
			cluster.initHaproxy(nil, pr)
		}
		if cluster.conf.MxsOn && pr.Type == proxyMaxscale {
			cluster.initMaxscale(nil, pr)
		}
		if cluster.conf.MdbsProxyOn && pr.Type == proxySpider {
			cluster.initMdbsproxy(nil, pr)
		}
	}
}

func (cluster *Cluster) GetClusterProxyConn() (*sqlx.DB, error) {
	var proxyHost string
	var proxyPort string
	proxyHost = ""
	if cluster.conf.MxsOn {
		proxyHost = cluster.conf.MxsHost
		proxyPort = strconv.Itoa(cluster.conf.MxsWritePort)

	}
	if cluster.conf.HaproxyOn {
		proxyHost = "127.0.0.1"
		proxyPort = strconv.Itoa(cluster.conf.HaproxyWritePort)
	}

	_, err := dbhelper.CheckHostAddr(proxyHost)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", proxyHost)
		return nil, errmsg
	}

	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)

	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if proxyHost != "" {
		dsn += "tcp(" + proxyHost + ":" + proxyPort + ")/" + params
	}
	cluster.LogPrint(dsn)
	return sqlx.Open("mysql", dsn)

}

func (cluster *Cluster) GetClusterThisProxyConn(prx *Proxy) (*sqlx.DB, error) {

	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)
	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if prx.Host != "" {
		dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
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
