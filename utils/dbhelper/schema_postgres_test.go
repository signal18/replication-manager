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
