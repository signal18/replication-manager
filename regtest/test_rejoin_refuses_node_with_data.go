// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"fmt"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

const refuseProbeSchema = "regtest_refuse_restore"

// TestRejoinRefusesNodeWithData is the F1 proof of #1979: an automatic restore never
// reaches a node that holds data. A replica gets a user schema of its own (not binlogged),
// loses its replication channel and restarts, so it comes back StandAlone with no crash
// record: with autoseed on, the rejoin path would reseed it. It must be refused
// (restore-refused, WARN0237) and the schema must still be there. The replica is then
// re-attached and the schema dropped.
func (regtest *RegTest) TestRejoinRefusesNodeWithData(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	orch := cl.GetOrchestrator()
	if (orch != config.ConstOrchestratorOpenSVC && orch != config.ConstOrchestratorKubernetes) || !tlsTopology(cl) || !cl.Conf.Autorejoin {
		tlsLogf(cl, "Skipping RejoinRefusesNodeWithData: needs a provisioned master-slave cluster with autorejoin")
		return true
	}
	master, slave := cl.GetMaster(), cl.GetSlaves()[0]
	if slave.DBVersion == nil || slave.DBVersion.IsPostgreSQL() {
		tlsLogf(cl, "Skipping RejoinRefusesNodeWithData: MariaDB/MySQL replica needed")
		return true
	}
	savedAutoseed := cl.Conf.Autoseed
	cl.Conf.Autoseed = true
	defer func() { cl.Conf.Autoseed = savedAutoseed }()

	// A user schema on this replica only: the data an automatic restore must never touch.
	if err := tlsExecOneConn(slave, "SET SESSION sql_log_bin=0", "CREATE DATABASE IF NOT EXISTS "+refuseProbeSchema, "CREATE TABLE IF NOT EXISTS "+refuseProbeSchema+".t (id INT PRIMARY KEY)"); err != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: %s", err)
		return false
	}
	defer tlsExecOneConn(slave, "SET SESSION sql_log_bin=0", "DROP DATABASE IF EXISTS "+refuseProbeSchema)

	// No replication channel, then a restart: StandAlone, no crash record, the autoseed path.
	slave.StopSlave()
	if _, err := slave.ResetSlave(); err != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: reset slave: %s", err)
		return false
	}
	if err := cl.StopDatabaseService(slave); err != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: stop: %s", err)
		return false
	}
	if err := cl.WaitDatabaseFailed(slave); err != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: not failed: %s", err)
		return false
	}
	if err := cl.StartDatabaseService(slave); err != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: start: %s", err)
		return false
	}
	refused := tlsWait(4*time.Minute, func() error {
		if !cl.StateMachine.IsInState(state.BuildStateKey("WARN0237", slave.URL)) {
			return fmt.Errorf("WARN0237 not open on %s (state %s)", slave.URL, slave.State)
		}
		return nil
	})

	// Whatever happened, put the replica back under the master.
	defer func() {
		if _, err := slave.SetReplicationGTIDSlavePosFromServer(master); err != nil {
			tlsLogf(cl, "RejoinRefusesNodeWithData: re-attach %s: %s", slave.URL, err)
		}
		slave.StartSlave()
	}()
	if refused != nil {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: the automatic restore was not refused: %s", refused)
		return false
	}
	var n int
	if err := slave.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", refuseProbeSchema).Scan(&n); err != nil || n != 1 {
		tlsLogf(cl, "FAIL RejoinRefusesNodeWithData: the replica's own data is gone (%d table, %v)", n, err)
		return false
	}
	tlsLogf(cl, "PASS RejoinRefusesNodeWithData: restore refused on %s, its data kept", slave.URL)
	return true
}

// tlsExecOneConn runs statements on one pinned connection (session settings apply).
func tlsExecOneConn(s *cluster.ServerMonitor, stmts ...string) error {
	if s.Conn == nil {
		return fmt.Errorf("no connection to %s", s.URL)
	}
	conn, err := s.Conn.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}
