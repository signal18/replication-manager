package dbhelper

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

func TestPostgresAnalyzeQuery(t *testing.T) {
	q, err := postgresAnalyzeQuery(`public.my"table`)
	if err != nil || q != `ANALYZE "public"."my""table"` {
		t.Fatalf("%q %v", q, err)
	}
	for _, bad := range []string{"", "noschema", "a.b.c", ".t", "s."} {
		if _, err := postgresAnalyzeQuery(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

// The PostgreSQL table list reads real sizes from pg_class and returns the fourteen
// columns the scan expects, in order.
func TestPostgresTablesQuery(t *testing.T) {
	v, _ := version.NewVersion("PostgreSQL", 17, 11, 0)
	q := tablesQueryAll(v)
	for _, want := range []string{"pg_table_size", "pg_indexes_size", "reltuples", "pg_namespace"} {
		if !strings.Contains(q, want) {
			t.Fatalf("missing %s in %s", want, q)
		}
	}
	sel := q[strings.Index(q, "SELECT")+6 : strings.Index(q, "FROM pg_class")]
	depth, cols := 0, 1
	for _, r := range sel {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				cols++
			}
		}
	}
	if cols != 14 {
		t.Fatalf("%d columns selected, the scan reads 14", cols)
	}
	m, _ := version.NewVersion("MariaDB", 11, 8, 0)
	if strings.Contains(tablesQueryAll(m), "pg_class") {
		t.Fatal("MariaDB keeps the information_schema query")
	}
}

// The streaming standby status returns every column the logical replication status
// returns (the monitor reads both into the same structure), and neither it nor the
// position query calls a function PostgreSQL refuses during recovery.
func TestPostgresStandbyStatusQueryShape(t *testing.T) {
	for _, col := range []string{"Connection_name", "Master_Host", "Master_Port", "Master_User", "Master_Log_File",
		"Read_Master_Log_Pos", "Relay_Master_Log_File", "Slave_IO_Running", "Slave_SQL_Running", "Exec_Master_Log_Pos",
		"Seconds_Behind_Master", "Last_IO_Errno", "Last_SQL_Errno", "Last_SQL_Error", "Master_Server_Id", "Using_Gtid",
		"Gtid_IO_Pos", "Gtid_Slave_Pos", "Slave_Heartbeat_Period", "Slave_SQL_Running_State"} {
		if !strings.Contains(postgresStandbyStatusQuery, `"`+col+`"`) {
			t.Fatalf("column %s missing from the standby status", col)
		}
	}
	for _, q := range []string{postgresStandbyStatusQuery, postgresMasterStatusQuery} {
		for _, refused := range []string{"pg_walfile_name", "pg_current_wal_lsn() AS"} {
			if strings.Contains(q, refused) && refused == "pg_walfile_name" {
				t.Fatalf("%s cannot be executed during recovery", refused)
			}
		}
	}
	if !strings.Contains(postgresStandbyStatusQuery, "pg_is_in_recovery()") || !strings.Contains(postgresStandbyStatusQuery, "pg_stat_wal_receiver") {
		t.Fatal("the standby status reads the WAL receiver, on a server in recovery only")
	}
	for _, col := range []string{`"File"`, `"Position"`} {
		if !strings.Contains(postgresMasterStatusQuery, col) {
			t.Fatalf("column %s missing from the position query", col)
		}
	}
}
