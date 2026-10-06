package cluster

import (
	"database/sql"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

func pgReplica(connection string) *ServerMonitor {
	return &ServerMonitor{
		DBVersion:    &version.Version{Flavor: "PostgreSQL", Major: 17},
		Replications: []dbhelper.SlaveStatus{{ConnectionName: sql.NullString{String: connection, Valid: true}}},
	}
}

// The PostgreSQL topology is the one the replicas report: physical standbys are WAL
// streaming, subscribers are logical replication, MariaDB replicas are neither.
func TestPostgresReplicationTopology(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{}}
	if got := cl.postgresReplicationTopology(); got != "" {
		t.Fatalf("no replica, no server: got %q", got)
	}
	// no replica: a PostgreSQL cluster keeps its declared topology, a MariaDB one does not
	cl.Conf.TopologyTarget = config.TopoMasterSlavePgStream
	cl.Servers = serverList{pgReplica(""), pgReplica("")}
	if got := cl.postgresReplicationTopology(); got != config.TopoMasterSlavePgStream {
		t.Fatalf("no replica, PostgreSQL servers: got %q", got)
	}
	cl.Servers = serverList{pgReplica(""), {DBVersion: &version.Version{Flavor: "MariaDB", Major: 11}}}
	if got := cl.postgresReplicationTopology(); got != "" {
		t.Fatalf("no replica, mixed servers: got %q", got)
	}
	cl.Conf.TopologyTarget = ""
	cl.slaves = serverList{pgReplica(dbhelper.PostgresStandbyConnectionName)}
	if got := cl.postgresReplicationTopology(); got != config.TopoMasterSlavePgStream {
		t.Fatalf("physical standby: got %q", got)
	}
	cl.slaves = serverList{pgReplica("alltables")}
	if got := cl.postgresReplicationTopology(); got != config.TopoMasterSlavePgLog {
		t.Fatalf("subscriber: got %q", got)
	}
	cl.slaves = serverList{{DBVersion: &version.Version{Flavor: "MariaDB", Major: 11}}}
	if got := cl.postgresReplicationTopology(); got != "" {
		t.Fatalf("MariaDB replica: got %q", got)
	}
}
