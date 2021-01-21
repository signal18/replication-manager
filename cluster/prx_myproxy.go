package cluster

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/router/myproxy"
)

type MyProxyProxy struct {
	Proxy
}

func (cluster *Cluster) initMyProxy(proxy *MyProxyProxy) {
	proxy.Init()
}

func (proxy *MyProxyProxy) Init() {
	if proxy.InternalProxy != nil {
		proxy.InternalProxy.Close()
	}
	cluster := proxy.ClusterGroup
	db, err := sql.Open("mysql", cluster.master.DSN)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not connect to Master for MyProxy %s", err)
		return
	}
	proxy.InternalProxy, _ = myproxy.NewProxyServer("0.0.0.0:"+proxy.GetPort(), proxy.GetUser(), proxy.GetPass(), db)
	go proxy.InternalProxy.Run()
}
