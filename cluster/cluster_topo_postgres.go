// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// postgresReplicationTopology names the topology OBSERVED on PostgreSQL replicas, or "" when
// the replicas are not PostgreSQL: WAL streaming when the replicas are physical standbys
// (their status comes from the WAL receiver), logical replication when they follow through
// subscriptions. It is detected from what the servers report, never copied from the target,
// so a cluster declared for one and running the other still raises the topology mismatch.
func (cluster *Cluster) postgresReplicationTopology() string {
	standbys, subscribers := 0, 0
	for _, sl := range cluster.slaves {
		if sl == nil || sl.DBVersion == nil || !sl.DBVersion.IsPostgreSQL() {
			return ""
		}
		if postgresIsPhysicalStandbyStatus(sl.Replications) {
			standbys++
		} else {
			subscribers++
		}
	}
	switch {
	case standbys > 0 && subscribers == 0:
		return config.TopoMasterSlavePgStream
	case subscribers > 0 && standbys == 0:
		return config.TopoMasterSlavePgLog
	}
	return ""
}

// postgresIsPhysicalStandbyStatus tells a WAL receiver status from a subscription status.
func postgresIsPhysicalStandbyStatus(replications []dbhelper.SlaveStatus) bool {
	for _, r := range replications {
		if r.ConnectionName.String == dbhelper.PostgresStandbyConnectionName {
			return true
		}
	}
	return false
}
