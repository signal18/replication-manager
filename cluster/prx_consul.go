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
	"strconv"
	"strings"

	"github.com/micro/go-micro/registry"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/spf13/pflag"
)

type ConsulProxy struct {
	Proxy
}

func (proxy *ConsulProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.RegistryConsul, "registry-consul", false, "Register write and read SRV DNS to consul")
	flags.BoolVar(&conf.RegistryConsulDebug, "registry-consul-debug", false, "Register write and read SRV DNS to consul")
	flags.IntVar(&conf.RegistryConsulLogLevel, "registry-consul-log-level", 0, "Register write and read SRV DNS to consul")
	flags.StringVar(&conf.RegistryConsulCredential, "registry-consul-credential", ":", "Consul credential user:password")
	flags.StringVar(&conf.RegistryConsulToken, "registry-consul-token", "", "Consul Token")
	flags.StringVar(&conf.RegistryConsulHosts, "registry-servers", "127.0.0.1", "Comma-separated list of registry addresses")
	flags.StringVar(&conf.RegistryConsulJanitorWeights, "registry-consul-weights", "100", "Weight of each proxysql host inside janitor proxy")
}

func NewConsulProxy(placement int, cluster *Cluster, proxyHost string) *ConsulProxy {
	conf := cluster.Conf
	prx := new(ConsulProxy)
	prx.Name = proxyHost
	prx.Host = proxyHost
	prx.Type = config.ConstProxyConsul
	prx.Port = conf.ProxysqlAdminPort
	prx.ReadWritePort, _ = strconv.Atoi(conf.ProxysqlPort)
	prx.User, prx.Pass = misc.SplitPair(conf.RegistryConsulCredential)
	prx.ReaderHostgroup, _ = strconv.Atoi(conf.ProxysqlReaderHostgroup)
	prx.WriterHostgroup, _ = strconv.Atoi(conf.ProxysqlWriterHostgroup)
	prx.WritePort, _ = strconv.Atoi(conf.ProxysqlPort)
	prx.ReadPort, _ = strconv.Atoi(conf.ProxysqlPort)

	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSProxySQLPartitions, conf.ProxysqlHostsIPV6, conf.RegistryConsulJanitorWeights)

	if conf.ProvNetCNI {
		if conf.ClusterHead == "" {
			prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
		} else {
			prx.Host = prx.Host + "." + conf.ClusterHead + ".svc." + conf.ProvOrchestratorCluster
		}
	}

	prx.Pass = cluster.Conf.GetDecryptedPassword("registry-consul-credential", prx.Pass)

	return prx
}

func (proxy *ConsulProxy) Init() {
	cluster := proxy.ClusterGroup
	var opt registry.Options
	//opt := consul.DefaultConfig()
	if cluster.Conf.RegistryConsul == false || cluster.IsActive() == false {
		return
	}
	opt.Addrs = strings.Split(cluster.Conf.RegistryConsulHosts, ",")
	//DefaultRegistry()
	//opt := registry.DefaultRegistry
	reg := registry.NewRegistry()

	if cluster.GetMaster() != nil {

		port, _ := strconv.Atoi(cluster.GetMaster().Port)
		writesrv := map[string][]*registry.Service{
			"write": []*registry.Service{
				{
					Name:    "write_" + cluster.GetName(),
					Version: "0.0.0",
					Nodes: []*registry.Node{
						{
							Id:      "write_" + cluster.GetName(),
							Address: cluster.GetMaster().Host,
							Port:    port,
						},
					},
				},
			},
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Register consul master ID %s with host %s", "write_"+cluster.GetName(), cluster.GetMaster().URL)
		delservice, err := reg.GetService("write_" + cluster.GetName())
		if err != nil {
			for _, service := range delservice {

				if err := reg.Deregister(service); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Unexpected deregister error: %v", err)
				}
			}
		}
		//reg := registry.NewRegistry()
		for _, v := range writesrv {
			for _, service := range v {

				if err := reg.Register(service); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Unexpected register error: %v", err)
				}

			}
		}

	}

	for _, srv := range cluster.Servers {
		var readsrv registry.Service
		readsrv.Name = "read_" + cluster.GetName()
		readsrv.Version = "0.0.0"
		var readnodes []*registry.Node
		var node registry.Node
		node.Id = srv.Id
		node.Address = srv.Host
		port, _ := strconv.Atoi(srv.Port)
		node.Port = port
		readnodes = append(readnodes, &node)
		readsrv.Nodes = readnodes

		if err := reg.Deregister(&readsrv); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Unexpected consul deregister error for server %s: %v", srv.URL, err)
		}
		if srv.State != stateFailed && srv.State != stateMaintenance && srv.State != stateUnconn {
			if (srv.IsSlave && srv.HasReplicationIssue() == false) || (srv.IsMaster() && cluster.Conf.PRXServersReadOnMaster) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlInfo, "Register consul read service  %s %s", srv.Id, srv.URL)
				if err := reg.Register(&readsrv); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, LvlErr, "Unexpected consul register error for server %s: %v", srv.URL, err)
				}
			}
		}
	}

}

func (proxy *ConsulProxy) Refresh() error {
	return nil
}

func (proxy *ConsulProxy) Failover() {
	proxy.Init()
}

func (proxy *ConsulProxy) BackendsStateChange() {
	proxy.Init()
}

func (proxy *ConsulProxy) SetMaintenance(s *ServerMonitor) {
	proxy.Init()
}
