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
	"strconv"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (server *ServerMonitor) HasInstallPlugin(name string) bool {
	val, ok := server.Plugins[name]
	if !ok {
		return false
	}
	if val.Status == "ACTIVE" {
		return true
	}
	return false
}

// check if node see same master as the passed list
func (server *ServerMonitor) HasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err != nil {
			return false
		}
		ssserver, err := server.GetSlaveStatus(server.ReplicationSourceName)
		if err != nil {
			return false
		}
		if sssib.MasterServerID != ssserver.MasterServerID {
			return false
		}
	}
	return true
}

func (sl serverList) checkAllSlavesRunning() bool {
	if len(sl) == 0 {
		return false
	}
	for _, s := range sl {
		ss, sserr := s.GetSlaveStatus(s.ReplicationSourceName)
		if sserr != nil {
			return false
		}
		if ss.SlaveSQLRunning.String != "Yes" || ss.SlaveIORunning.String != "Yes" {
			return false
		}
	}
	return true
}

/* Check Consistency parameters on server */
func (server *ServerMonitor) IsAcid() bool {
	syncBin, _ := dbhelper.GetVariableByName(server.Conn, "SYNC_BINLOG")
	logFlush, _ := dbhelper.GetVariableByName(server.Conn, "INNODB_FLUSH_LOG_AT_TRX_COMMIT")
	if syncBin == "1" && logFlush == "1" {
		return true
	}
	return false
}

func (server *ServerMonitor) HasSlaves(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err == nil {
			if server.ServerID == sssib.MasterServerID && sl.ServerID != server.ServerID {
				return true
			}
		}
	}
	return false
}

func (server *ServerMonitor) HasCycling() bool {
	currentSlave := server
	searchServerID := server.ServerID

	for range server.ClusterGroup.Servers {
		currentMaster, _ := server.ClusterGroup.GetMasterFromReplication(currentSlave)
		if currentMaster != nil {
			//	server.ClusterGroup.LogPrintf("INFO", "Cycling my current master id :%d me id:%d", currentMaster.ServerID, currentSlave.ServerID)
			if currentMaster.ServerID == searchServerID {
				return true
			} else {
				currentSlave = currentMaster
			}
		} else {
			return false
		}
	}
	return false
}

// IsDown() returns true is the server is Failed or Suspect or or auth error
func (server *ServerMonitor) IsDown() bool {
	if server.State == stateFailed || server.State == stateSuspect || server.State == stateErrorAuth {
		return true
	}
	return false
}

// IFailed() returns true is the server is Failed or auth error
func (server *ServerMonitor) IsFailed() bool {
	if server.State == stateFailed || server.State == stateErrorAuth {
		return true
	}
	return false
}

func (server *ServerMonitor) IsReplicationBroken() bool {
	if server.IsSQLThreadRunning() == false || server.IsIOThreadRunning() == false {
		return true
	}
	return false
}

func (server *ServerMonitor) HasGTIDReplication() bool {
	if server.DBVersion.IsMySQLOrPercona() && server.HaveMySQLGTID == false {
		return false
	} else if server.DBVersion.IsMariaDB() && server.DBVersion.Major == 5 {
		return false
	}
	return true
}

func (server *ServerMonitor) HasReplicationIssue() bool {
	ret := server.CheckReplication()
	if ret == "Running OK" || ((ret == "NOT OK, IO Connecting" || server.IsIOThreadRunning() == false) && server.ClusterGroup.GetMaster() == nil) {
		return false
	}
	return true
}

func (server *ServerMonitor) IsIgnored() bool {
	return server.Ignored
}

func (server *ServerMonitor) IsReadOnly() bool {
	return server.HaveReadOnly
}

func (server *ServerMonitor) IsReadWrite() bool {
	return server.HaveReadOnly
}

func (server *ServerMonitor) IsIOThreadRunning() bool {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveIORunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) IsSQLThreadRunning() bool {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveSQLRunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) IsPrefered() bool {
	return server.Prefered
}

func (server *ServerMonitor) IsMaster() bool {
	master := server.ClusterGroup.GetMaster()
	if master == nil {
		return false
	}
	if master.Id == server.Id {
		return true
	}
	return false
}

func (server *ServerMonitor) IsMySQL() bool {
	return server.DBVersion.IsMySQL()
}

func (server *ServerMonitor) IsMariaDB() bool {
	if server.DBVersion == nil {
		return true
	}
	return server.DBVersion.IsMariaDB()
}

func (server *ServerMonitor) HasSuperReadOnly() bool {
	return server.DBVersion.IsMySQLOrPercona57()
}

func (server *ServerMonitor) HasHighNumberSlowQueries() bool {
	if server.Variables["LONG_QUERY_TIME"] == "0" || server.Variables["LONG_QUERY_TIME"] == "0.000010" {
		return false
	}
	slowquerynow, _ := strconv.ParseInt(server.Status["SLOW_QUERIES"], 10, 64)
	slowquerybefore, _ := strconv.ParseInt(server.PrevStatus["SLOW_QUERIES"], 10, 64)
	if server.MonitorTime-server.PrevMonitorTime > 0 {
		qpssecond := (slowquerynow - slowquerybefore) / (server.MonitorTime - server.PrevMonitorTime)
		if qpssecond > 20 {
			return true
		}
	}
	return false

}
