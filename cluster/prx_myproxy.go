package cluster

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/myproxy"
)

func (cluster *Cluster) initMyProxy(proxy *Proxy) {
	if proxy.InternalProxy != nil {
		proxy.InternalProxy.Close()
	}
	db, err := sql.Open("mysql", cluster.master.DSN)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not connect to Master for MyProxy %s", err)
		return
	}
	proxy.InternalProxy, _ = myproxy.StartProxyServer("0.0.0.0:4000", db)
	go proxy.InternalProxy.Run()
}
