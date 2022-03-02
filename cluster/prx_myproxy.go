package cluster

import (
	"database/sql"
	"strconv"

	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/myproxy"
	"github.com/spf13/pflag"
)

type MyProxyProxy struct {
	Proxy
}

// NewMyProxyProxy follows the same signature as the other Proxies for future compatibility, simply pass 0 and "" for the values not needed
func NewMyProxyProxy(placement int, cluster *Cluster, proxyHost string) *MyProxyProxy {
	prx := new(MyProxyProxy)
	prx.Type = config.ConstProxyMyProxy
	prx.Port = strconv.Itoa(cluster.Conf.MyproxyPort)
	prx.Host = "0.0.0.0"
	prx.ReadPort = cluster.Conf.MyproxyPort
	prx.WritePort = cluster.Conf.MyproxyPort
	prx.ReadWritePort = cluster.Conf.MyproxyPort
	prx.User = cluster.Conf.MyproxyUser
	prx.Pass = cluster.Conf.MyproxyPassword
	if prx.Host == "" {
		prx.Host = "repman." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
	}
	if prx.Name == "" {
		prx.Name = prx.Host
	}

	return prx
}

func (cluster *Cluster) initMyProxy(proxy *MyProxyProxy) {
	proxy.Init()
}

func (proxy *MyProxyProxy) BackendsStateChange() {
	return
}

func (proxy *MyProxyProxy) SetMaintenance(s *ServerMonitor) {
	return
}

func (proxy *MyProxyProxy) Refresh() error {
	return nil
}

func (proxy *MyProxyProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
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

func (proxy *MyProxyProxy) CertificatesReload() error {

	return nil
}
