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
	"errors"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (server *ServerMonitor) WaitSyncToMaster(master *ServerMonitor) {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Waiting for slave %s to sync", server.URL)
	if server.DBVersion.Flavor == "MariaDB" {
		logs, err := dbhelper.MasterWaitGTID(server.Conn, master.GTIDBinlogPos.Sprint(), 30)
		cluster.LogSQL(logs, err, server.URL, "MasterFailover", LvlErr, "Failed MasterWaitGTID, %s", err)

	} else {
		logs, err := dbhelper.MasterPosWait(server.Conn, server.DBVersion, master.BinaryLogFile, master.BinaryLogPos, 30, cluster.Conf.MasterConn)
		cluster.LogSQL(logs, err, server.URL, "MasterFailover", LvlErr, "Failed MasterPosWait, %s", err)
	}

	if cluster.Conf.LogLevel > 2 {
		server.LogReplPostion()
	}
}

func (server *ServerMonitor) WaitDatabaseStart() error {
	cluster := server.ClusterGroup
	exitloop := 0
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Waiting database start on %s", server.URL)
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.GetConf().MonitoringTicker*1000))
	for int64(exitloop) < cluster.GetConf().MonitorWaitRetry {
		select {
		case <-ticker.C:

			exitloop++

			var err error
			wg := new(sync.WaitGroup)
			wg.Add(1)
			go server.Ping(wg)
			wg.Wait()
			err = server.Refresh()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Waiting state refresh on %s failed with error %s ", server.URL, err)

			if cluster.GetTopology() == topoMultiMasterWsrep {
				if !server.IsConnected() {
					err = errors.New("Not yet connected")
				}
				/*	} else { */

			}
			if err == nil {

				exitloop = 9999999
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Waiting state running on %s failed with error %s ", server.URL, err)
			}
		}
	}
	if exitloop == 9999999 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Waiting state running reach on %s", server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Wait state running on %s", server.URL)
		return errors.New("Failed to wait running database server")
	}
	return nil
}
