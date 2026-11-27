// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The PostgreSQL native plugin, repl-postgresql-postgresql-native: the streaming standby
// (master-slave-pg-stream, physical) and the publication and subscription (master-slave-pg-logical,
// logical). Its rows are the subscriptions and the standby's WAL receiver (readNativeLinks).
// Its own code where the mechanism differs from the binlog: the promotion is pg_promote(). Its
// manifest lists what PostgreSQL does: no copy or apply apart, no skip of an event, no position
// set nor waited for here (the failover's wait reads the primary's LSN, postgresWaitSync), no
// option and no credential on the link: those verbs answer ErrReplicationNotSupported, nothing
// sent. Start, Stop, ResetPositions, Remove and ChangeSource are the common native verbs, whose
// PostgreSQL statements (ALTER SUBSCRIPTION, DROP SUBSCRIPTION, CREATE SUBSCRIPTION) are
// dbhelper's.

const ReplicationPluginPostgreSQLNative = "repl-postgresql-postgresql-native"

var postgresqlNativeManifest = ReplicationManifest{
	Name: ReplicationPluginPostgreSQLNative, Type: string(ReplicationKindNative),
	Source: []string{"PostgreSQL"}, Target: []string{"PostgreSQL"},
	Topologies: []ReplicationTopology{
		{Name: config.TopoMasterSlavePgStream, Transport: ReplicationTransportPhysical, Consistency: ReplicationConsistencyAsync},
		{Name: config.TopoMasterSlavePgLog, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	},
	Actions: []string{
		ReplicationActionRemove, ReplicationActionStatus,
		ReplicationActionStart, ReplicationActionStop,
		ReplicationActionResetPositions, ReplicationActionChangeSource,
		ReplicationActionPromote, ReplicationActionRejoin,
	},
}

type postgresReplicationPlugin struct {
	unsupportedReplicationPlugin
}

func (p *postgresReplicationPlugin) Manifest() ReplicationManifest { return postgresqlNativeManifest }

func (p *postgresReplicationPlugin) newReplication(base replicationBase) Replication {
	return &postgresReplication{nativeReplication{base}}
}

// Bootstrap: step 5 (the publication then each subscription, today in prov.go).
func (p *postgresReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, ReplicationPluginPostgreSQLNative)
}

// Create: step 5, with the bootstrap.
func (p *postgresReplicationPlugin) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionCreate, name, ReplicationPluginPostgreSQLNative)
}

// postgresReplication is a subscription or a standby's WAL receiver.
type postgresReplication struct {
	nativeReplication
}

// Promote is pg_promote() (PostgresPromote): the standby leaves recovery, the statement the
// failover, the switchover and its rollback sent (cluster_fail_postgres.go). read_only is the
// caller's, as for every plugin.
func (r *postgresReplication) Promote(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.PostgresPromote(server.Conn, postgresPromoteWaitSeconds)
}
