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
			err = psql.SetWriter(s.Host)
			if err != nil {
				cluster.LogPrintf("ERROR", "ProxySQL could not set writer (%s)", err)
			}
		case stateSlave:
			err = psql.SetReader(s.Host)
			if err != nil {
				cluster.LogPrintf("ERROR", "ProxySQL could not set reader (%s)", err)
			}
		case stateFailed:
			err = psql.SetOfflineHard(s.Host)
			if err != nil {
				cluster.LogPrintf("ERROR", "ProxySQL could not set offline (%s)", err)
			}
		case stateUnconn:
			err = psql.SetOfflineHard(s.Host)
			if err != nil {
				cluster.LogPrintf("ERROR", "ProxySQL could not set offline (%s)", err)
			}
		}
	}
	err = psql.LoadServersToRuntime()
	if err != nil {
		cluster.LogPrintf("ERROR", "ProxySQL could not load servers to runtime (%s)", err)
	}
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
			err = psql.SetOfflineHard(s.Host)
			if err != nil {
				cluster.LogPrintf("ERROR", "ProxySQL could not set offline (%s)", err)
			}
			updated = true
		}
	}
	if updated {
		err = psql.LoadServersToRuntime()
		if err != nil {
			cluster.LogPrintf("ERROR", "ProxySQL could not load servers to runtime (%s)", err)
		}
	}
}
