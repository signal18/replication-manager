// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/maxscale"
	"github.com/signal18/replication-manager/state"
)

func (cluster *Cluster) refreshMaxscale(proxy *Proxy) {
	if cluster.conf.MxsOn == false {
		return
	}

	m := maxscale.MaxScale{Host: proxy.Host, Port: proxy.Port, User: proxy.User, Pass: proxy.Pass}
	if cluster.conf.MxsOn {
		err := m.Connect()
		if err != nil {
			cluster.sme.AddState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
			return
		}
	}
	for _, server := range cluster.servers {
		if cluster.conf.MxsGetInfoMethod == "maxinfo" {
			_, err := m.GetMaxInfoServers("http://" + proxy.Host + ":" + strconv.Itoa(cluster.conf.MxsMaxinfoPort) + "/servers")
			if err != nil {
				cluster.sme.AddState("ERR00020", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00020"], server.URL), ErrFrom: "MON"})
			}
			srvport, _ := strconv.Atoi(server.Port)
			server.MxsServerName, server.MxsServerStatus, server.MxsServerConnections = m.GetMaxInfoServer(server.Host, srvport, server.ClusterGroup.conf.MxsServerMatchPort)
		} else {
			_, err := m.ListServers()
			if err != nil {
				server.ClusterGroup.sme.AddState("ERR00019", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00019"], server.URL), ErrFrom: "MON"})
			} else {
				//		server.ClusterGroup.LogPrint("get MaxScale server list")
				var connections string
				server.MxsServerName, connections, server.MxsServerStatus = m.GetServer(server.IP, server.Port, server.ClusterGroup.conf.MxsServerMatchPort)
				server.MxsServerConnections, _ = strconv.Atoi(connections)
				//server.ClusterGroup.LogPrintf("INFO", "Affect for server %s, %s %s  ", server.IP, server.MxsServerName, server.MxsServerStatus)
			}
		}
	}
	m.Close()
}

func (cluster *Cluster) initMaxscale(oldmaster *ServerMonitor, proxy *Proxy) {
	if cluster.conf.MxsOn == false {
		return
	}

	m := maxscale.MaxScale{Host: proxy.Host, Port: proxy.Port, User: proxy.User, Pass: proxy.Pass}
	err := m.Connect()
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not connect to MaxScale:%s", err)
		return
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogPrintf("ERROR", "MaxScale server name undiscovered")
		return
	}

	var monitor string
	if cluster.conf.MxsGetInfoMethod == "maxinfo" {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("INFO", "Getting Maxscale monitor via maxinfo")
		}
		m.GetMaxInfoMonitors("http://" + cluster.conf.MxsHost + ":" + strconv.Itoa(cluster.conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoMonitor()

	} else {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("INFO", "Getting Maxscale monitor via maxadmin")
		}
		_, err := m.ListMonitors()
		if err != nil {
			cluster.LogPrintf("ERROR", "MaxScale client could not list monitors %s", err)
		}
		monitor = m.GetMonitor()
	}
	if monitor != "" && cluster.conf.MxsDisableMonitor == true {
		cmd := "shutdown monitor \"" + monitor + "\""
		cluster.LogPrintf("INFO", "Maxscale shutdown monitor: %s", cmd)
		err = m.ShutdownMonitor(monitor)
		if err != nil {
			cluster.LogPrintf("ERROR", "MaxScale client could not shutdown monitor:%s", err)
		}
		m.Response()
		if err != nil {
			cluster.LogPrintf("ERROR", "MaxScale client could not shutdown monitor:%s", err)
		}
	} else {
		cluster.sme.AddState("ERR00017", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00017"], ErrFrom: "TOPO"})
	}

	err = m.SetServer(cluster.master.MxsServerName, "master")
	if err != nil {
		cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
	}
	err = m.SetServer(cluster.master.MxsServerName, "running")
	if err != nil {
		cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
	}
	err = m.ClearServer(cluster.master.MxsServerName, "slave")
	if err != nil {
		cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
	}

	if cluster.conf.MxsBinlogOn == false {
		for _, s := range cluster.servers {
			if s != cluster.master {

				err = m.ClearServer(s.MxsServerName, "master")
				if err != nil {
					cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
				}

				if s.State != stateSlave {
					err = m.ClearServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
					}
					err = m.ClearServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
					}

				} else {
					err = m.SetServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
					}
					err = m.SetServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
					}

				}
			}
		}
		if oldmaster != nil {
			err = m.ClearServer(oldmaster.MxsServerName, "master")
			if err != nil {
				cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
			}

			if oldmaster.State != stateSlave {
				err = m.ClearServer(oldmaster.MxsServerName, "slave")
				if err != nil {
					cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
				}
				err = m.ClearServer(oldmaster.MxsServerName, "running")
				if err != nil {
					cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
				}
			} else {
				err = m.SetServer(oldmaster.MxsServerName, "slave")
				if err != nil {
					cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
				}
				err = m.SetServer(oldmaster.MxsServerName, "running")
				if err != nil {
					cluster.LogPrintf("ERROR", "MaxScale client could not send command:%s", err)
				}

			}
		}
	}
}
