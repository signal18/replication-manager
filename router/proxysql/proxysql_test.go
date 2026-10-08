package proxysql

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func newMock(t *testing.T, flavor string) (*ProxySQL, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &ProxySQL{Flavor: flavor, Connection: sqlx.NewDb(db, "mysql"), WriterHG: "0", ReaderHG: "1", Weight: "1"}, mock
}

func expectExec(mock sqlmock.Sqlmock, stmt string) {
	mock.ExpectExec("^" + regexp.QuoteMeta(stmt) + "$").WillReturnResult(sqlmock.NewResult(0, 1))
}

// The same call writes the objects of its flavor; an unset flavor is MySQL.
func TestFlavorObjects(t *testing.T) {
	cases := []struct {
		flavor, servers, module, monitorVar string
	}{
		{"", "mysql_servers", "MYSQL", "mysql-monitor_writer_is_also_reader"},
		{FlavorMySQL, "mysql_servers", "MYSQL", "mysql-monitor_writer_is_also_reader"},
		{FlavorPgSQL, "pgsql_servers", "PGSQL", "pgsql-monitor_writer_is_also_reader"},
	}
	for _, c := range cases {
		t.Run(c.flavor, func(t *testing.T) {
			psql, mock := newMock(t, c.flavor)
			expectExec(mock, "REPLACE INTO "+c.servers+" (hostgroup_id,hostname, port,use_ssl,weight) VALUES('0','db1','5432','0','1')")
			expectExec(mock, "UPDATE "+c.servers+" SET status='ONLINE', hostgroup_id='1' WHERE  hostname='db1' AND port='5432' AND hostgroup_id in ('1','0')")
			expectExec(mock, "LOAD "+c.module+" SERVERS TO RUNTIME")
			expectExec(mock, "SAVE "+c.module+" SERVERS TO DISK")
			expectExec(mock, "SET "+c.monitorVar+" = 1")
			if err := psql.AddServerAsWriter("db1", "5432", "0"); err != nil {
				t.Fatal(err)
			}
			if err := psql.SetReader("db1", "5432"); err != nil {
				t.Fatal(err)
			}
			if err := psql.LoadServersToRuntime(); err != nil {
				t.Fatal(err)
			}
			if err := psql.SaveServersToDisk(); err != nil {
				t.Fatal(err)
			}
			if err := psql.SetMonitorIsAlsoWriter(true); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// ProxySQL's PostgreSQL monitor cannot distinguish a logical subscriber from
// a writer, so replication-manager must retain hostgroup placement itself.
func TestPgSQLDoesNotAddReplicationHostgroups(t *testing.T) {
	psql, mock := newMock(t, FlavorPgSQL)
	if err := psql.AddHostgroups("test"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// pgsql_servers has no gtid_port: copying a reader to the writer hostgroup
// must not name it.
func TestCopyReaderToWriterColumns(t *testing.T) {
	pgCols := "hostname, port, status, weight, compression, max_connections, max_replication_lag, use_ssl, max_latency_ms"
	myCols := "hostname, port, gtid_port, status, weight, compression, max_connections, max_replication_lag, use_ssl, max_latency_ms"
	for flavor, want := range map[string]string{
		FlavorPgSQL: "REPLACE INTO pgsql_servers (hostgroup_id, " + pgCols + ") SELECT '0', " + pgCols + " FROM pgsql_servers WHERE  hostgroup_id = '1' AND hostname = 'db1' AND port = '5432'",
		FlavorMySQL: "REPLACE INTO mysql_servers (hostgroup_id, " + myCols + ") SELECT '0', " + myCols + " FROM mysql_servers WHERE  hostgroup_id = '1' AND hostname = 'db1' AND port = '5432'",
	} {
		psql, mock := newMock(t, flavor)
		expectExec(mock, want)
		if err := psql.CopyReaderToWriter("db1", "5432"); err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
	}
}

// The query rules name the database in schemaname (MySQL) or database
// (PostgreSQL); the rule read back keeps one field for both.
func TestQueryRulesDatabaseColumn(t *testing.T) {
	psql, mock := newMock(t, FlavorPgSQL)
	mock.ExpectQuery("^" + regexp.QuoteMeta("select rule_id,active,username,database AS schemaname,digest,match_digest,match_pattern, destination_hostgroup,mirror_hostgroup,multiplex,apply from runtime_pgsql_query_rules") + "$").
		WillReturnRows(sqlmock.NewRows([]string{"rule_id", "active", "username", "schemaname", "digest", "match_digest", "match_pattern", "destination_hostgroup", "mirror_hostgroup", "multiplex", "apply"}).
			AddRow(1, 1, nil, "app", nil, "^SELECT", nil, 1, nil, nil, 1))
	rules, err := psql.GetQueryRulesRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].SchemaName.String != "app" {
		t.Fatalf("rules = %+v", rules)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStatsConnectionPool(t *testing.T) {
	psql, mock := newMock(t, FlavorPgSQL)
	mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT hostgroup, status, ConnUsed, Bytes_data_sent , Bytes_data_recv , Latency_us FROM stats.stats_pgsql_connection_pool WHERE hostgroup='0' AND srv_host='db1' AND srv_port='5432'") + "$").
		WillReturnRows(sqlmock.NewRows([]string{"hostgroup", "status", "ConnUsed", "Bytes_data_sent", "Bytes_data_recv", "Latency_us"}).AddRow("0", "ONLINE", 2, 10, 20, 300))
	hg, status, _, _, _, _, err := psql.GetStatsForHostWrite("db1", "5432")
	if err != nil || hg != "0" || status != "ONLINE" {
		t.Fatalf("hg=%q status=%q err=%v", hg, status, err)
	}
}
