// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
)

const provisionReplicationUserWait = 10 * time.Minute

// TestProvisionReplicationUser (#2360): a cluster whose replication user is not the database
// user replicates after a provisioning from scratch. The cluster is unprovisioned and provisioned
// again (Bootstrap, as the provision API does), then:
//   - every server holds the replication user with the replication grant (each node can be
//     promoted by a failover);
//   - every replica runs its IO and SQL threads, and ERR00007 is not open;
//   - no replica holds a GTID of its own: the account was created outside the binary log
//     (MariaDB: no GTID with the replica's server_id; MySQL: none with its server_uuid).
//
// Skipped when replication runs as the database user (nothing to create) or when the
// orchestrator does not provision (onpremise).
func (regtest *RegTest) TestProvisionReplicationUser(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	rpl := cl.GetRplUser()
	if rpl == "" || rpl == cl.GetDbUser() {
		tlsLogf(cl, "Skipping Provision Replication User: replication runs as the database user")
		return true
	}
	switch cl.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes, config.ConstOrchestratorLocalhost:
	default:
		tlsLogf(cl, "Skipping Provision Replication User: orchestrator %s does not provision", cl.Conf.ProvOrchestrator)
		return true
	}

	tlsLogf(cl, "Provision Replication User: unprovisioning %s", cl.Name)
	if err := cl.Unprovision(); err != nil {
		tlsLogf(cl, "FAIL Provision Replication User: unprovision: %s", err)
		return false
	}
	if err := cl.WaitClusterStop(); err != nil {
		tlsLogf(cl, "FAIL Provision Replication User: the cluster did not stop: %s", err)
		return false
	}
	tlsLogf(cl, "Provision Replication User: provisioning %s", cl.Name)
	if err := cl.Bootstrap(); err != nil {
		tlsLogf(cl, "FAIL Provision Replication User: provisioning: %s", err)
		return false
	}

	var why string
	for deadline := time.Now().Add(provisionReplicationUserWait); time.Now().Before(deadline); time.Sleep(10 * time.Second) {
		if why = provisionReplicationUserMiss(cl, rpl); why == "" {
			tlsLogf(cl, "PASS Provision Replication User: %s exists on every server, the replicas replicate, no replica holds a GTID of its own", rpl)
			return true
		}
	}
	tlsLogf(cl, "FAIL Provision Replication User: after %s: %s", provisionReplicationUserWait, why)
	return false
}

// provisionReplicationUserMiss returns what is not yet as expected, "" when all is.
func provisionReplicationUserMiss(cl *cluster.Cluster, rpl string) string {
	master := cl.GetMaster()
	if master == nil {
		return "no master"
	}
	slaves := cl.GetSlaves()
	if len(slaves) == 0 {
		return "no replica"
	}
	for _, s := range cl.Servers {
		if s == nil || s.Conn == nil || s.IsDown() {
			return "server down: " + serverURL(s)
		}
		// the grant as a replica connecting to s would see it
		peer := master
		if s == master {
			peer = slaves[0]
		}
		ip, _ := misc.GetIPSafe(misc.Unbracket(peer.Host))
		priv, _, err := dbhelper.GetPrivileges(s.Conn, rpl, peer.Host, ip, s.DBVersion)
		if err != nil || priv.Repl_slave_priv != "Y" {
			return "no replication grant for " + rpl + " on " + s.URL
		}
	}
	for _, s := range slaves {
		if s.IsReplicationBroken() {
			return "replication not running on " + s.URL
		}
		if s.DBVersion.IsMariaDB() && s.GTIDBinlogPos != nil {
			for _, id := range s.GTIDBinlogPos.GetServerIDs() {
				if id == s.ServerID {
					return "replica " + s.URL + " holds a GTID of its own: " + s.GTIDBinlogPos.Sprint()
				}
			}
		}
		if s.DBVersion.IsMySQLOrPercona() {
			if uuid := strings.ToLower(s.Variables.Get("SERVER_UUID")); uuid != "" && strings.Contains(strings.ToLower(s.GTIDExecuted), uuid) {
				return "replica " + s.URL + " holds a GTID of its own: " + s.GTIDExecuted
			}
		}
	}
	if cl.StateMachine.IsInState("ERR00007") {
		return "ERR00007 open"
	}
	return ""
}

func serverURL(s *cluster.ServerMonitor) string {
	if s == nil {
		return "<nil>"
	}
	return s.URL
}
