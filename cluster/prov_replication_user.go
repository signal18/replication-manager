// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// replicationUserHost is the host part of the replication account: the replicas connect from
// their own addresses, which a provisioning does not know in advance.
const replicationUserHost = "%"

// createReplicationUser creates the replication user on every server repman just provisioned,
// before the replication bootstrap (#2360). The OpenSVC and Kubernetes database containers only
// get the root password: without this, a cluster whose replication user is not the database
// user never replicates (IO error 1045, ERR00007 on every server).
//
// Each server gets its own copy, outside its binary log (dbhelper.CreateReplicationUser), so any
// of them can be promoted by a failover. Only called from Bootstrap, the provisioning: never on a
// database repman did not create (F1). prov-db-create-replication-user turns it off (T14).
func (cluster *Cluster) createReplicationUser() {
	if !cluster.Conf.ProvDBCreateReplicationUser {
		return
	}
	user, pass := cluster.GetRplUser(), cluster.GetRplPass()
	if user == "" || user == cluster.GetDbUser() {
		return // the database user replicates: it exists on every server
	}
	for _, server := range cluster.Servers {
		if server != nil {
			server.createReplicationUser(user, pass)
		}
	}
}

// createReplicationUser creates the replication user on this server, on a connection of its own
// (the session setting that keeps it out of the binary log must not reach the monitor's pool).
// A failure is logged and leaves the bootstrap to go on: ERR00007 then names the server.
func (server *ServerMonitor) createReplicationUser(user, pass string) {
	cluster := server.ClusterGroup
	// the statements depend on the engine: a server never read yet is read once, and one whose
	// engine is still unknown gets nothing rather than another engine's SQL (T23)
	if server.neverAnswered() {
		server.Refresh()
	}
	if server.neverAnswered() || server.DBVersion.Flavor == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cannot create the replication user %s on %s: the server's engine is not known yet", user, server.URL)
		return
	}
	db, err := server.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cannot create the replication user %s on %s: %s", user, server.URL, err)
		return
	}
	defer db.Close()
	timeout := time.Duration(cluster.Conf.ExecTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := db.Connx(ctx)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cannot create the replication user %s on %s: %s", user, server.URL, err)
		return
	}
	defer conn.Close()
	logs, err := dbhelper.CreateReplicationUser(ctx, conn, server.DBVersion, replicationUserHost, user, pass)
	cluster.LogSQL(logs, err, server.URL, "Provision", config.LvlErr, "Create the replication user: %s", err)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cannot create the replication user %s on %s: %s", user, server.URL, err)
		return
	}
	// F11: what was done, where, the statements with the password masked
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication user %s created on %s, outside the binary log: %s", user, server.URL, logs)
}
