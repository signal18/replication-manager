// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type MaxscaleProxy struct {
	Proxy
}

func (cluster *Cluster) refreshMaxscale(proxy *MaxscaleProxy) error {
	return proxy.refresh()
}

func NewMaxscaleProxy(placement int, cluster *Cluster, proxyHost string) *MaxscaleProxy {
	conf := cluster.Conf
	prx := new(MaxscaleProxy)
	prx.Type = config.ConstProxyMaxscale
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSMaxscalePartitions, conf.MxsHostsIPV6)
	prx.Port = conf.MxsPort
	prx.User = conf.MxsUser
	prx.Pass = conf.MxsPass
	if cluster.key != nil {
		p := crypto.Password{Key: cluster.key}
		p.CipherText = prx.Pass
		p.Decrypt()
		prx.Pass = p.PlainText
	}
	prx.ReadPort = conf.MxsReadPort
	prx.WritePort = conf.MxsWritePort
	prx.ReadWritePort = conf.MxsReadWritePort
	prx.Name = proxyHost
	prx.Host = proxyHost
	if cluster.Conf.ProvNetCNI {
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}

	return prx
}

func (proxy *MaxscaleProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.MxsOn, "maxscale", false, "MaxScale proxy server is query for backend status")
	flags.BoolVar(&conf.CheckFalsePositiveMaxscale, "failover-falsepositive-maxscale", false, "Failover checks that maxscale detect failed master")
	flags.IntVar(&conf.CheckFalsePositiveMaxscaleTimeout, "failover-falsepositive-maxscale-timeout", 14, "Failover checks that maxscale detect failed master")
	flags.BoolVar(&conf.MxsBinlogOn, "maxscale-binlog", false, "Maxscale binlog server topolgy")
	flags.MarkDeprecated("maxscale-monitor", "Deprecate disable maxscale monitoring for 2 nodes cluster")
	flags.BoolVar(&conf.MxsDisableMonitor, "maxscale-disable-monitor", false, "Disable maxscale monitoring and fully drive server state")
	flags.StringVar(&conf.MxsGetInfoMethod, "maxscale-get-info-method", "maxadmin", "How to get infos from Maxscale maxinfo|maxadmin")
	flags.StringVar(&conf.MxsHost, "maxscale-servers", "", "MaxScale hosts ")
	flags.StringVar(&conf.MxsPort, "maxscale-port", "6603", "MaxScale admin port")
	flags.StringVar(&conf.MxsUser, "maxscale-user", "admin", "MaxScale admin user")
	flags.StringVar(&conf.MxsPass, "maxscale-pass", "mariadb", "MaxScale admin password")
	flags.IntVar(&conf.MxsWritePort, "maxscale-write-port", 3306, "MaxScale read-write port to leader")
	flags.IntVar(&conf.MxsReadPort, "maxscale-read-port", 3307, "MaxScale load balance read port to all nodes")
	flags.IntVar(&conf.MxsReadWritePort, "maxscale-read-write-port", 3308, "MaxScale load balance read port to all nodes")
	flags.IntVar(&conf.MxsMaxinfoPort, "maxscale-maxinfo-port", 3309, "MaxScale maxinfo plugin http port")
	flags.IntVar(&conf.MxsBinlogPort, "maxscale-binlog-port", 3309, "MaxScale maxinfo plugin http port")
	flags.BoolVar(&conf.MxsServerMatchPort, "maxscale-server-match-port", false, "Match servers running on same host with different port")
	flags.StringVar(&conf.MxsBinaryPath, "maxscale-binary-path", "/usr/sbin/maxscale", "Maxscale binary location")
	flags.StringVar(&conf.MxsHostsIPV6, "maxscale-servers-ipv6", "", "ipv6 bind address ")
}

func (proxy *MaxscaleProxy) refresh() error {
	cluster := proxy.ClusterGroup
	if cluster.Conf.MxsOn == false {
		return nil
	}
	var m maxscale.MaxScale
	if proxy.Tunnel {
		m = maxscale.MaxScale{Host: "localhost", Port: strconv.Itoa(proxy.TunnelPort), User: proxy.User, Pass: proxy.Pass}
	} else {
		m = maxscale.MaxScale{Host: proxy.Host, Port: proxy.Port, User: proxy.User, Pass: proxy.Pass}
	}

	if cluster.Conf.MxsOn {
		err := m.Connect()
		if err != nil {
			cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
			cluster.sme.CopyOldStateFromUnknowServer(proxy.Name)
			return err
		}
	}
	proxy.BackendsWrite = nil
	for _, server := range cluster.Servers {

		var bke = Backend{
			Host:    server.Host,
			Port:    server.Port,
			Status:  server.State,
			PrxName: server.URL,
		}

		if cluster.Conf.MxsGetInfoMethod == "maxinfo" {
			_, err := m.GetMaxInfoServers("http://" + proxy.Host + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/servers")
			if err != nil {
				cluster.sme.AddState("ERR00020", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00020"], server.URL), ErrFrom: "MON", ServerUrl: proxy.Name})
			}
			srvport, _ := strconv.Atoi(server.Port)
			mxsConnections := 0
			bke.PrxName, bke.PrxStatus, mxsConnections = m.GetMaxInfoServer(server.Host, srvport, server.ClusterGroup.Conf.MxsServerMatchPort)
			bke.PrxConnections = strconv.Itoa(mxsConnections)
			server.MxsServerStatus = bke.PrxStatus
			server.MxsServerName = bke.PrxName

		} else {
			_, err := m.ListServers()
			if err != nil {
				server.ClusterGroup.sme.AddState("ERR00019", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00019"], server.URL), ErrFrom: "MON", ServerUrl: proxy.Name})
			} else {

				if proxy.Tunnel {

					bke.PrxName, bke.PrxStatus, bke.PrxConnections = m.GetServer(server.Host, server.Port, server.ClusterGroup.Conf.MxsServerMatchPort)
					server.MxsServerStatus = bke.PrxStatus
					server.MxsServerName = bke.PrxName

				} else {
					bke.PrxName, bke.PrxStatus, bke.PrxConnections = m.GetServer(server.Host, server.Port, server.ClusterGroup.Conf.MxsServerMatchPort)
					server.MxsServerStatus = bke.PrxStatus
					server.MxsServerName = bke.PrxName
				}
				//server.ClusterGroup.LogPrintf("INFO", "Affect for server %s, %s %s  ", server.IP, server.MxsServerName, server.MxsServerStatus)
			}
		}
		proxy.BackendsWrite = append(proxy.BackendsWrite, bke)
	}
	m.Close()
	return nil
}

func (cluster *Cluster) initMaxscale(proxy DatabaseProxy) {
	proxy.Init()
}

func (proxy *MaxscaleProxy) Init() {
	cluster := proxy.ClusterGroup
	if cluster.Conf.MxsOn == false {
		return
	}

	var m maxscale.MaxScale
	if proxy.Tunnel {
		m = maxscale.MaxScale{Host: "localhost", Port: strconv.Itoa(proxy.TunnelPort), User: proxy.User, Pass: proxy.Pass}
	} else {
		m = maxscale.MaxScale{Host: proxy.Host, Port: proxy.Port, User: proxy.User, Pass: proxy.Pass}
	}
	err := m.Connect()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not connect to MaxScale:%s", err)
		return
	}
	defer m.Close()
	if cluster.GetMaster().MxsServerName == "" {
		return
	}

	var monitor string
	if cluster.Conf.MxsGetInfoMethod == "maxinfo" {
		cluster.LogPrintf(LvlDbg, "Getting Maxscale monitor via maxinfo")
		m.GetMaxInfoMonitors("http://" + cluster.Conf.MxsHost + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoMonitor()

	} else {
		cluster.LogPrintf(LvlDbg, "Getting Maxscale monitor via maxadmin")
		_, err := m.ListMonitors()
		if err != nil {
			cluster.LogPrintf(LvlErr, "MaxScale client could not list monitors %s", err)
		}
		monitor = m.GetMonitor()
	}
	if monitor != "" && cluster.Conf.MxsDisableMonitor == true {
		cmd := "shutdown monitor \"" + monitor + "\""
		cluster.LogPrintf(LvlInfo, "Maxscale shutdown monitor: %s", cmd)
		err = m.ShutdownMonitor(monitor)
		if err != nil {
			cluster.LogPrintf(LvlErr, "MaxScale client could not shutdown monitor:%s", err)
		}
		m.Response()
		if err != nil {
			cluster.LogPrintf(LvlErr, "MaxScale client could not shutdown monitor:%s", err)
		}
	} else {
		cluster.sme.AddState("ERR00017", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00017"], ErrFrom: "TOPO", ServerUrl: proxy.Name})
	}

	err = m.SetServer(cluster.GetMaster().MxsServerName, "master")
	if err != nil {
		cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
	}
	err = m.SetServer(cluster.GetMaster().MxsServerName, "running")
	if err != nil {
		cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
	}
	err = m.ClearServer(cluster.GetMaster().MxsServerName, "slave")
	if err != nil {
		cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
	}

	if cluster.Conf.MxsBinlogOn == false {
		for _, s := range cluster.Servers {
			if s != cluster.GetMaster() {

				err = m.ClearServer(s.MxsServerName, "master")
				if err != nil {
					cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
				}

				if s.State != stateSlave {
					err = m.ClearServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
					}
					err = m.ClearServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
					}

				} else {
					err = m.SetServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
					}
					err = m.SetServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogPrintf(LvlErr, "MaxScale client could not send command:%s", err)
					}

				}
			}
		}
	}
}

func (cluster *Cluster) setMaintenanceMaxscale(pr DatabaseProxy, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

func (proxy *MaxscaleProxy) BackendsStateChange() {
	return
}

func (pr *MaxscaleProxy) SetMaintenance(server *ServerMonitor) {
	cluster := pr.ClusterGroup
	if cluster.GetMaster() != nil {
		return
	}
	if cluster.Conf.MxsOn {
		return
	}
	m := maxscale.MaxScale{Host: pr.Host, Port: pr.Port, User: pr.User, Pass: pr.Pass}
	err := m.Connect()
	if err != nil {
		cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
	}
	if server.IsMaintenance {
		err = m.SetServer(server.MxsServerName, "maintenance")
	} else {
		err = m.ClearServer(server.MxsServerName, "maintenance")
	}
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set server %s in maintenance", err)
		m.Close()
	}
	m.Close()
}

// Failover for MaxScale simply calls Init
func (prx *MaxscaleProxy) Failover() {
	prx.Init()
}
