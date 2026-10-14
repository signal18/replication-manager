// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// A replica of a ColumnStore cluster holds its schema only (#2021): the rows are on the
// dbroots every server shares, and ColumnStore writes no DML of its tables to the binary
// log. A dump reloaded into a replica drops and recreates the shared tables of every
// server (its DROP TABLE runs outside the replication thread): its seed is the schema
// replicated from the master instead, a rebootstrap.

// isColumnstoreReplica: a server of a columnstore cluster that is not its master.
func (server *ServerMonitor) isColumnstoreReplica() bool {
	cluster := server.ClusterGroup
	if cluster == nil || !cluster.HasConfigTopoColumnstore() {
		return false
	}
	master := cluster.GetMaster()
	return master == nil || master.URL != server.URL
}

// refuseColumnstoreReplicaDump: the error of a dump about to be reloaded into a
// ColumnStore replica, nil for any other server.
func (server *ServerMonitor) refuseColumnstoreReplicaDump() error {
	if !server.isColumnstoreReplica() {
		return nil
	}
	return fmt.Errorf("%s is a replica of ColumnStore cluster %s: a dump reloaded into it drops and recreates the tables every server shares; its schema is replicated from the master (rebootstrap)", server.URL, server.ClusterGroup.Name)
}

// columnstoreSchemaDiff: the tables (schema.table engine) on the master only and on the
// replica only, system schemas and replication_manager_schema left out.
func columnstoreSchemaDiff(master, replica []dbhelper.EngineTable) (masterOnly, replicaOnly int) {
	key := func(t dbhelper.EngineTable) string { return t.Schema + "." + t.Table + " " + t.Engine }
	onMaster := make(map[string]bool, len(master))
	for _, t := range master {
		onMaster[key(t)] = true
	}
	onReplica := make(map[string]bool, len(replica))
	for _, t := range replica {
		onReplica[key(t)] = true
		if !onMaster[key(t)] {
			replicaOnly++
		}
	}
	for k := range onMaster {
		if !onReplica[k] {
			masterOnly++
		}
	}
	return masterOnly, replicaOnly
}

// RebootstrapColumnstoreReplica points a ColumnStore replica at the master from where its
// schema stands:
//   - no table (a new server): from the origin, the replica replays every DDL, which the
//     replication thread applies to its own definitions only; refused (WARN0245) when the
//     master's oldest binary log does not start at the origin, the DDL before it is gone;
//   - the master's tables: from the master's current position;
//   - other tables: refused (WARN0246), nothing changed.
func (server *ServerMonitor) RebootstrapColumnstoreReplica() error {
	cluster := server.ClusterGroup
	if !server.isColumnstoreReplica() {
		return fmt.Errorf("%s is not a replica of a ColumnStore cluster", server.URL)
	}
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("no master found")
	}
	if server.Conn == nil || master.Conn == nil {
		return fmt.Errorf("no connection to %s or to the master %s", server.URL, master.URL)
	}
	masterTables, logs, err := dbhelper.GetTablesByEngine(master.Conn, master.DBVersion, "", "", "")
	if err != nil {
		cluster.LogSQL(logs, err, master.URL, "Rebootstrap", config.LvlErr, "Could not list the tables of %s: %s", master.URL, err)
		return err
	}
	replicaTables, logs, err := dbhelper.GetTablesByEngine(server.Conn, server.DBVersion, "", "", "")
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rebootstrap", config.LvlErr, "Could not list the tables of %s: %s", server.URL, err)
		return err
	}

	if len(replicaTables) > 0 {
		masterOnly, replicaOnly := columnstoreSchemaDiff(masterTables, replicaTables)
		if masterOnly > 0 || replicaOnly > 0 {
			cluster.SetState("WARN0246", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0246"], server.URL, master.URL, masterOnly, replicaOnly), ErrFrom: "JOB", ServerUrl: server.URL})
			return fmt.Errorf("the schema of %s differs from the master %s (%d tables on the master only, %d on the replica only)", server.URL, master.URL, masterOnly, replicaOnly)
		}
		if _, err := server.StopSlave(); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rebootstrap of ColumnStore replica %s: its tables are the master's, replicating from the master's current position", server.URL)
		if err := server.ChangeMasterTo(master, "SLAVE_POS"); err != nil {
			return err
		}
		_, err := server.SetReadOnly()
		return err
	}

	file, gtidList, logs, err := dbhelper.GetFirstBinaryLogGtidList(master.Conn, master.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, master.URL, "Rebootstrap", config.LvlErr, "Could not read the oldest binary log of %s: %s", master.URL, err)
		return err
	}
	if gtidList != "" {
		cluster.SetState("WARN0245", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0245"], server.URL, master.URL, file, gtidList), ErrFrom: "JOB", ServerUrl: server.URL})
		return fmt.Errorf("the oldest binary log %s of the master %s starts after %s: the schema of %s cannot be replicated", file, master.URL, gtidList, server.URL)
	}
	if _, err := server.StopSlave(); err != nil {
		return err
	}
	logs, err = dbhelper.SetGTIDSlavePos(server.Conn, "")
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rebootstrap", config.LvlErr, "Could not reset the GTID position of %s: %s", server.URL, err)
		return err
	}
	opt := cluster.GetChangeMasterBaseOptForSlave(server, master, server.IsDelayed)
	opt.Mode = "SLAVE_POS"
	logs, err = server.ChangeMaster(opt)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rebootstrap", config.LvlErr, "Could not point %s at the master %s: %s", server.URL, master.URL, err)
		return err
	}
	if _, err := server.StartSlave(); err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rebootstrap of ColumnStore replica %s: replicating the schema of %s from the origin", server.URL, master.URL)
	_, err = server.SetReadOnly()
	return err
}
