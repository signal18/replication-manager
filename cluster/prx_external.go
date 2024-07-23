// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type ExternalProxy struct {
	Proxy
}

func NewExternalProxy(placement int, cluster *Cluster, proxyHost string) *ExternalProxy {
	prx := new(ExternalProxy)

	prx.Type = config.ConstProxyExternal
	prx.Host, prx.Port = misc.SplitHostPort(cluster.Conf.ExtProxyVIP)
	prx.User = cluster.GetDbUser()
	prx.Pass = cluster.GetDbPass()
	prx.WritePort, _ = strconv.Atoi(prx.GetPort())
	prx.ReadPort = prx.WritePort
	prx.ReadWritePort = prx.WritePort
	if prx.Name == "" {
		prx.Name = prx.Host
	}
	prx.ShardProxy, _ = cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, true, "")
	return prx
}

func (proxy *ExternalProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {

	flags.BoolVar(&conf.ExtProxyOn, "extproxy", false, "External proxy can be used to specify a route manage with external scripts")
	flags.StringVar(&conf.ExtProxyVIP, "extproxy-address", "", "Network address when route is manage via external script,  host:[port] format")

}

func (proxy *ExternalProxy) Init() {

}

func (proxy *ExternalProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	if proxy.ShardProxy == nil {
		//proxy.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxy,config.LvlErr, "Sharding proxy refresh no database monitor yet initialize")
		proxy.ClusterGroup.StateMachine.AddState("ERR00086", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(proxy.ClusterGroup.GetErrorList()["ERR00086"]), ErrFrom: "PROXY", ServerUrl: proxy.GetURL()})
		return errors.New("Sharding proxy refresh no database monitor yet initialize")
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go proxy.ShardProxy.Ping(wg)
	wg.Wait()

	err := proxy.ShardProxy.Refresh()
	if err != nil {
		//proxy.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModProxy,config.LvlErr, "Sharding proxy refresh error (%s)", err)
		return err
	}
	proxy.Version = proxy.ShardProxy.Variables.Get("VERSION")
	proxyByteOut := proxy.ShardProxy.Status.Get("BYTES_SENT")
	proxyByteIn := proxy.ShardProxy.Status.Get("BYTES_RECEIVED")
	proxyCnx := proxy.ShardProxy.Status.Get("THREADS_CREATED")

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil
	srv := cluster.GetMaster()
	if srv != nil {
		var bke = Backend{
			Host:           srv.Host,
			Port:           srv.Port,
			PrxName:        srv.Host + ":" + srv.Port,
			Status:         srv.State,
			PrxConnections: proxyCnx,
			PrxByteIn:      proxyByteOut,
			PrxByteOut:     proxyByteIn,
			PrxStatus:      "ONLINE",
			PrxHostgroup:   "WRITE",
		}

		//PrxLatency:     strconv.Itoa(proxysqlLatency),

		proxy.BackendsWrite = append(proxy.BackendsWrite, bke)

		var bkeread = Backend{
			Host:           srv.Host,
			Port:           srv.Port,
			PrxName:        srv.Host + ":" + proxy.Port,
			Status:         srv.State,
			PrxConnections: proxyCnx,
			PrxByteIn:      proxyByteOut,
			PrxByteOut:     proxyByteIn,
			PrxStatus:      "ONLINE",
			PrxHostgroup:   "READ",
		}
		proxy.BackendsRead = append(proxy.BackendsRead, bkeread)
	}
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
