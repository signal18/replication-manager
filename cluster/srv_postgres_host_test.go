package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A host entry written host:port/database is a PostgreSQL instance: its database survives
// the url rewritten without it (orchestrator domain appended, credential rotation) and its
// connection string is the PostgreSQL one, whatever topology the cluster declares.
func TestPostgreSQLHostKeepsDatabaseAndDriver(t *testing.T) {
	c := &Cluster{Name: "pgtest", Conf: &config.Config{Timeout: 1, DNSTimeout: 1}}
	s := &ServerMonitor{ClusterGroup: c, PostgressDB: "postgres", postgresDeclared: true}
	if !s.IsPostgreSQLHost() {
		t.Fatal("a declared database makes the server a PostgreSQL host")
	}
	s.SetCredential("127.0.0.1:5432", "postgres", "secret")
	if s.PostgressDB != "postgres" {
		t.Fatalf("database lost when the url carries none: %q", s.PostgressDB)
	}
	if !strings.Contains(s.DSN, "dbname=postgres") || !strings.Contains(s.DSN, "host=127.0.0.1") || strings.Contains(s.DSN, "@tcp(") {
		t.Fatalf("PostgreSQL connection string expected: %q", strings.ReplaceAll(s.DSN, "secret", "<hidden>"))
	}
	s.SetCredential("127.0.0.1:5432/other", "postgres", "secret")
	if s.PostgressDB != "other" {
		t.Fatalf("a url that names a database sets it: %q", s.PostgressDB)
	}

	// a MariaDB server of a cluster without PostgreSQL topology keeps the MySQL driver
	m := &ServerMonitor{ClusterGroup: c}
	if m.IsPostgreSQLHost() {
		t.Fatal("no database in the host entry, no PostgreSQL topology: MySQL driver")
	}
	m.SetCredential("127.0.0.1:3306", "root", "secret")
	if !strings.Contains(m.DSN, "@tcp(127.0.0.1:3306)") {
		t.Fatalf("MySQL connection string expected: %q", strings.ReplaceAll(m.DSN, "secret", "<hidden>"))
	}

	// the cluster-wide PostgreSQL topology flags still decide for every server
	c.Conf.MasterSlavePgLogical = true
	if !m.IsPostgreSQLHost() {
		t.Fatal("replication-master-slave-pg-logical makes every server a PostgreSQL host")
	}
}
