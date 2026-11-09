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
	// the servers were just provisioned: a name may not resolve or a port not answer yet (seen
	// on a dev instance, MySQL 8.0.41: db3 not in DNS 36 s after the provision began). Each server
	// is tried again until it answers, within the budget of WaitDatabaseCanConn.
	step := time.Duration(cluster.Conf.MonitoringTicker) * time.Second
	if step <= 0 {
		step = 2 * time.Second
	}
	wait := time.Duration(cluster.Conf.MonitorWaitRetry) * step
	if wait <= 0 {
		wait = 2 * time.Minute
	}
	// a server still unreachable at the end was logged as a warning by its last attempt
	createReplicationUserOn(cluster.Servers, func(s *ServerMonitor, last bool) bool {
		return s.createReplicationUser(user, pass, last)
	}, wait, step)
}

// createReplicationUserOn calls attempt on every server, then again on those it asks to retry,
// every step until wait is spent; the last round is told it is the last. It returns the servers
// still asking for a retry at the end.
func createReplicationUserOn(servers []*ServerMonitor, attempt func(s *ServerMonitor, last bool) (retry bool), wait, step time.Duration) []*ServerMonitor {
	pending := make([]*ServerMonitor, 0, len(servers))
	for _, s := range servers {
		if s != nil {
			pending = append(pending, s)
		}
	}
	deadline := time.Now().Add(wait)
	for len(pending) > 0 {
		last := !time.Now().Add(step).Before(deadline)
		next := pending[:0:0]
		for _, s := range pending {
			if attempt(s, last) {
				next = append(next, s)
			}
		}
		pending = next
		if last || len(pending) == 0 {
			break
		}
		time.Sleep(step)
	}
	return pending
}

// createReplicationUser creates the replication user on this server, on a connection of its own
// (the session setting that keeps it out of the binary log must not reach the monitor's pool).
// It returns true when the server cannot be reached yet, or its engine is not known yet, so the
// caller tries again; an error from the server itself is final. Either failure is logged as a
// warning (at debug while retries remain) and leaves the bootstrap to go on: ERR00007 then names
// the server.
func (server *ServerMonitor) createReplicationUser(user, pass string, last bool) (retry bool) {
	cluster := server.ClusterGroup
	notYet := func(format string, args ...interface{}) bool {
		level := config.LvlDbg
		if last {
			level = config.LvlWarn
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, level, "Cannot create the replication user %s on %s: "+format, append([]interface{}{user, server.URL}, args...)...)
		return true
	}
	// the statements depend on the engine: a server never read yet is read, and one whose engine
	// is still unknown gets nothing rather than another engine's SQL (T23)
	if server.neverAnswered() {
		server.Refresh()
	}
	if server.neverAnswered() || server.DBVersion.Flavor == "" {
		return notYet("the server's engine is not known yet")
	}
	db, err := server.GetNewDBConn()
	if err != nil {
		return notYet("%s", err)
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
		return notYet("%s", err)
	}
	defer conn.Close()
	logs, err := dbhelper.CreateReplicationUser(ctx, conn, server.DBVersion, replicationUserHost, user, pass)
	cluster.LogSQL(logs, err, server.URL, "Provision", config.LvlErr, "Create the replication user: %s", err)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cannot create the replication user %s on %s: %s", user, server.URL, err)
		return false
	}
	// F11: what was done, where, the statements with the password masked
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication user %s created on %s, outside the binary log: %s", user, server.URL, logs)
	return false
}
