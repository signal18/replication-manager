// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Group Replication, the multi-master-grprep topology of repl-mysql-mysql-native: its own code
// where the mechanism differs from a binlog channel. A member's link is its membership of the
// group, not a channel the monitor reads (no row: nothing joins server.Replications, no new
// discovery): starting it is START GROUP_REPLICATION, promoting it is
// group_replication_set_as_primary, bootstrapping the group is the first member's start with
// group_replication_bootstrap_group. The statements and the log lines are the ones srv.go and
// srv_set.go sent before (the rejoin, the bootstrap, the switchover). Galera
// (multi-master-wsrep of repl-mariadb-mariadb-native) has no such object: its members send no
// link statement on a failover (every member writes) and its SST account is the
// configurator's; its bootstrap moves into the plugin with the others (step 5).

// groupReplicationName names a member's membership of the group.
const groupReplicationName = "group_replication"

// groupReplication is a member's membership of the group, under the MySQL plugin's manifest.
type groupReplication struct {
	replicationBase
}

// GroupReplication is the server's membership of its group (Group Replication).
func (server *ServerMonitor) GroupReplication() *groupReplication {
	return &groupReplication{replicationBase{manifest: mysqlNativeManifest, target: server, name: groupReplicationName}}
}

// setAllowlist names the cluster's servers as the hosts allowed to join, before group
// replication starts (#2164). A failure is logged: the start goes on.
func (r *groupReplication) setAllowlist() {
	server := r.target
	cluster := server.ClusterGroup
	logs, err := dbhelper.SetGroupReplicationAllowlist(server.Conn, server.DBVersion, cluster.GetGroupReplicationWhiteList())
	cluster.LogSQL(logs, err, server.URL, "SetGroupReplicationAllowlist", config.LvlErr, "Group replication allowlist can't be set on server %s: %s", server.URL, err)
}

// bootstrapGroup starts the group on its first member (BootstrapReplication, prov.go).
func (r *groupReplication) bootstrapGroup() error {
	server := r.target
	r.setAllowlist()
	logs, err := dbhelper.BootstrapGroupReplication(server.Conn, server.DBVersion)
	cluster := server.ClusterGroup
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", config.LvlErr, "Group Replication can't be bootstrap on server %s :%s ", server.URL, err)
		return err
	}
	return nil
}

// Start joins the group (the bootstrap of the other members, the rejoin); reason is not used,
// a membership is not recorded as a channel is.
func (r *groupReplication) Start(reason string) (string, error) {
	server := r.target
	r.setAllowlist()
	logs, err := dbhelper.StartGroupReplication(server.Conn, server.DBVersion)
	cluster := server.ClusterGroup
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", config.LvlErr, "Group Replication can't be joined on server %s :%s ", server.URL, err)
		return logs, err
	}
	return logs, nil
}

// Promote makes the member the group's PRIMARY (group_replication_set_as_primary), the
// switchover's: the group waits for the old PRIMARY's transactions and makes it read-only
// itself.
func (r *groupReplication) Promote(reason string) (string, error) {
	server := r.target
	logs, err := dbhelper.SetGroupReplicationPrimary(server.Conn, server.DBVersion)
	server.GetCluster().LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not set server a primary")
	return logs, err
}

var _ Replication = (*groupReplication)(nil)
