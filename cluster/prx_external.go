// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/pflag"
)

type ExternalProxy struct {
	Proxy
}

func NewExternalProxy(placement int, cluster *Cluster, proxyHost string) *ExternalProxy {
	prx := new(ExternalProxy)
	prx.Type = config.ConstProxyExternal
	prx.Host, prx.Port = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
	prx.WritePort, _ = strconv.Atoi(prx.GetPort())
	prx.ReadPort = prx.WritePort
	prx.ReadWritePort = prx.WritePort
	if prx.Name == "" {
		prx.Name = prx.Host
	}
	return prx
}

func (proxy *ExternalProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {

	flags.BoolVar(&conf.ExtProxyOn, "extproxy", false, "External proxy can be used to specify a route manage with external scripts")
	flags.StringVar(&conf.ExtProxyVIP, "extproxy-address", "", "Network address when route is manage via external script,  host:[port] format")

}

func (proxy *ExternalProxy) Init() {

}

func (proxy *ExternalProxy) Refresh() error {
	return nil
}

func (proxy *ExternalProxy) Failover() {
	proxy.Init()
}

func (proxy *ExternalProxy) BackendsStateChange() {
	proxy.Init()
}

func (proxy *ExternalProxy) SetMaintenance(s *ServerMonitor) {
	proxy.Init()
}
