package cluster

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/myproxy"
	"github.com/spf13/pflag"
)

type MyProxyProxy struct {
	Proxy
}

func (cluster *Cluster) initMyProxy(proxy *MyProxyProxy) {
	proxy.Init()
}

func (proxy *MyProxyProxy) AddFlags(flags *pflag.FlagSet, conf config.Config) {
	flags.BoolVar(&conf.MyproxyOn, "myproxy", false, "Use Internal Proxy")
	flags.IntVar(&conf.MyproxyPort, "myproxy-port", 4000, "Internal proxy read/write port")
	flags.StringVar(&conf.MyproxyUser, "myproxy-user", "admin", "Myproxy user")
	flags.StringVar(&conf.MyproxyPassword, "myproxy-password", "repman", "Myproxy password")
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
