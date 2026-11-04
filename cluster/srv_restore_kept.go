// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"slices"
	"strings"

	"github.com/signal18/replication-manager/utils/state"
)

// The tables a logical restore kept as the server has them instead of the backup's: MySQL 8
// refuses any DDL on the tables of the mysql schema it owns (3554, the InnoDB statistics
// tables), so the restore keeps the server's own (execSplitdumpSingle). The server then
// differs from the backup there; WARN0280 says so, tick after tick, until its next restore
// or reseed clears the list: a state, not a log line (F7).

// markRestoreKeptTable records schema.table as kept by the running restore.
func (server *ServerMonitor) markRestoreKeptTable(table string) {
	server.restoreKeptMu.Lock()
	defer server.restoreKeptMu.Unlock()
	if !slices.Contains(server.restoreKept, table) {
		server.restoreKept = append(server.restoreKept, table)
	}
}

// clearRestoreKeptTables forgets the tables of the previous restore: a new restore or reseed
// starts.
func (server *ServerMonitor) clearRestoreKeptTables() {
	server.restoreKeptMu.Lock()
	defer server.restoreKeptMu.Unlock()
	server.restoreKept = nil
}

// checkRestoreKeptTables runs with the server refresh, every tick: WARN0280 while the last
// restore kept a table as the server has it.
func (server *ServerMonitor) checkRestoreKeptTables() {
	server.restoreKeptMu.Lock()
	kept := slices.Clone(server.restoreKept)
	server.restoreKeptMu.Unlock()
	if len(kept) == 0 {
		return
	}
	cluster := server.ClusterGroup
	cluster.SetState("WARN0280", state.State{ErrType: "WARNING", ErrFrom: "JOB", ServerUrl: server.URL,
		ErrDesc: fmt.Sprintf(clusterError["WARN0280"], server.URL, strings.Join(kept, ", "))})
}
