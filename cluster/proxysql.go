package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/proxysql"
)

func (cluster *Cluster) initProxysql(proxy *Proxy) {
	if cluster.conf.ProxysqlOn == false {
		return
	}

	psql := proxysql.ProxySQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
		WriterHG: fmt.Sprintf("%d", proxy.WritePort),
		ReaderHG: fmt.Sprintf("%d", proxy.ReadPort),
	}

	var err error
	err = psql.Connect()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		return
	}
	defer psql.Connection.Close()

	for _, s := range cluster.servers {
		switch s.State {
		case stateMaster:
			psql.SetWriter(s.Host)
		case stateSlave:
			psql.SetReader(s.Host)
		case stateFailed:
			psql.SetOfflineHard(s.Host)
		case stateUnconn:
			psql.SetOfflineHard(s.Host)
		}
	}
	psql.LoadServersToRuntime()
}

func (cluster *Cluster) refreshProxysql(proxy *Proxy) {
	if cluster.conf.ProxysqlOn == false {
		return
	}

	psql := proxysql.ProxySQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
		WriterHG: fmt.Sprintf("%d", proxy.WritePort),
		ReaderHG: fmt.Sprintf("%d", proxy.ReadPort),
	}

	var err error
	err = psql.Connect()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		return
	}
	defer psql.Connection.Close()

	var updated bool
	for _, s := range cluster.servers {
		switch s.State {
		case stateUnconn:
			psql.SetOfflineHard(s.Host)
			updated = true
		}
	}
	if updated {
		psql.LoadServersToRuntime()
	}
}
