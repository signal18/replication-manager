// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"strconv"

	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/state"
)

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
	//disable monitoring
	if cluster.conf.MxsMonitor == false {
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
				cluster.LogPrintf("ERROR", "MaxScale client could list monitors monitor:%s", err)
			}
			monitor = m.GetMonitor()
		}
		if monitor != "" {
			cmd := "shutdown monitor \"" + monitor + "\""
			cluster.LogPrintf("INFO: %s", cmd)
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
