package cluster

import (
	"fmt"
	"strconv"

	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/state"
)

// Proxy defines a proxy
type Proxy struct {
	Name          string
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
	proxySpider   string = "mdbproxy"
)

type proxyList []*Proxy

func (cluster *Cluster) newProxy(*Proxy) (*Proxy, error) {
	proxy := new(Proxy)

	return proxy, nil
}

func (cluster *Cluster) SetMaintenance(serverid string) {
	// Found server from ServerId

	intsrvid, _ := strconv.Atoi(serverid)
	server := cluster.GetServerFromId(uint(intsrvid))
	for _, p := range cluster.proxies {
		if cluster.master != nil {
			if p.Name == proxyMaxscale {

				m := maxscale.MaxScale{Host: p.Host, Port: p.Port, User: p.User, Pass: p.Pass}
				err := m.Connect()
				if err != nil {
					cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
				}
				err = m.SetServer(server.MxsServerName, "maintenance")
				if err != nil {
					cluster.LogPrintf("ERROR: Could not set  server %s in maintenance", err)
					m.Close()
				}
				m.Close()
			}
		}
	}
}
