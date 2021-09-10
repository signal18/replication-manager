// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/sphinx"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type SphinxProxy struct {
	Proxy
}

func NewSphinxProxy(placement int, cluster *Cluster, proxyHost string) *SphinxProxy {
	conf := cluster.Conf
	prx := new(SphinxProxy)
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSSphinxPartitions, conf.SphinxHostsIPV6)
	prx.Type = config.ConstProxySphinx

	prx.Port = conf.SphinxQLPort
	prx.User = ""
	prx.Pass = ""
	prx.ReadPort, _ = strconv.Atoi(prx.GetPort())
	prx.WritePort, _ = strconv.Atoi(prx.GetPort())
	prx.ReadWritePort, _ = strconv.Atoi(prx.GetPort())
	prx.Name = proxyHost
	prx.Host = proxyHost
	if conf.ProvNetCNI {
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}

	return prx
}

func (proxy *SphinxProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.SphinxOn, "sphinx", false, "Turn on SphinxSearch detection")
	flags.StringVar(&conf.SphinxHosts, "sphinx-servers", "127.0.0.1", "SphinxSearch hosts")
	flags.StringVar(&conf.SphinxPort, "sphinx-port", "9312", "SphinxSearch API port")
	flags.StringVar(&conf.SphinxQLPort, "sphinx-sql-port", "9306", "SphinxSearch SQL port")
	if runtime.GOOS == "linux" {
		flags.StringVar(&conf.SphinxConfig, "sphinx-config", "/usr/share/replication-manager/shinx/sphinx.conf", "Path to sphinx config")
	}
	if runtime.GOOS == "darwin" {
		flags.StringVar(&conf.SphinxConfig, "sphinx-config", "/opt/replication-manager/share/sphinx/sphinx.conf", "Path to sphinx config")
	}
	flags.StringVar(&conf.SphinxHostsIPV6, "sphinx-servers-ipv6", "", "ipv6 bind address ")
}

func (proxy *SphinxProxy) Connect() (sphinx.SphinxSQL, error) {
	sphinx := sphinx.SphinxSQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
	}

	var err error
	err = sphinx.Connect()
	if err != nil {
		return sphinx, err
	}
	return sphinx, nil
}

func (proxy *SphinxProxy) Init() {
	cluster := proxy.ClusterGroup

	if cluster.Conf.SphinxOn == false {
		return
	}

	sphinx, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00058", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00058"], err), ErrFrom: "MON"})
		return
	}
	defer sphinx.Connection.Close()

}

func (proxy *SphinxProxy) BackendsStateChange() {
	return
}

func (proxy *SphinxProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	if cluster.Conf.SphinxOn == false {
		return nil
	}

	sphinx, err := proxy.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00058", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00058"], err), ErrFrom: "MON"})
		return err
	}
	defer sphinx.Connection.Close()
	proxy.Version = sphinx.GetVersion()

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	status, err := sphinx.GetStatus()
	var bke = Backend{
		Host:           cluster.Conf.ProvProxRouteAddr,
		Port:           cluster.Conf.ProvProxRoutePort,
		Status:         "UP",
		PrxName:        "",
		PrxStatus:      "UP",
		PrxConnections: status["CONNECTIONS"],
		PrxByteIn:      "0",
		PrxByteOut:     "0",
		PrxLatency:     status["AVG_QUERY_WALL"],
	}
	if err == nil {
		proxy.BackendsWrite = append(proxy.BackendsRead, bke)
	}
	return nil
}

func (proxy *SphinxProxy) SetMaintenance(s *ServerMonitor) {
	return
}

func (proxy *SphinxProxy) CertificatesReload() error {
	return nil
}
