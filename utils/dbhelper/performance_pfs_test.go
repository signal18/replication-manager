// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

// TestPfsQueriesSQL_MariaDBUsesDerivedJoin guards the #1651 fix: on MariaDB the digest
// capture must use the NON-correlated, materialized derived-table join — never the old
// per-digest correlated subquery that was O(digests × history) on MariaDB's unindexed PFS.
func TestPfsQueriesSQL_MariaDBUsesDerivedJoin(t *testing.T) {
	v, _ := version.NewVersionFromString("MariaDB", "11.4.7")
	q := pfsQueriesSQL(v)
	for _, want := range []string{"LEFT JOIN", "GROUP BY DIGEST", "events_statements_history_long", "H.sample_query"} {
		if !strings.Contains(q, want) {
			t.Errorf("MariaDB digest query is missing %q:\n%s", want, q)
		}
	}
	// The regression we must never bring back: a per-digest correlated lookup.
	if strings.Contains(q, "WHERE B.DIGEST = A.DIGEST") {
		t.Errorf("MariaDB digest query reintroduced the correlated subquery (issue #1651):\n%s", q)
	}
}

// TestPfsQueriesSQL_MySQLReadsSampleColumnNoJoin verifies MySQL/Percona 5.7+ reads the
// built-in QUERY_SAMPLE_TEXT column and never touches events_statements_history_long.
func TestPfsQueriesSQL_MySQLReadsSampleColumnNoJoin(t *testing.T) {
	v, _ := version.NewVersionFromString("MySQL", "8.0.36")
	q := pfsQueriesSQL(v)
	if !strings.Contains(q, "QUERY_SAMPLE_TEXT") {
		t.Errorf("MySQL digest query should read QUERY_SAMPLE_TEXT inline:\n%s", q)
	}
	for _, bad := range []string{"events_statements_history_long", "LEFT JOIN"} {
		if strings.Contains(q, bad) {
			t.Errorf("MySQL digest query must not contain %q (no join needed):\n%s", bad, q)
		}
	}
}

// TestPfsQueriesSQL_NilVersionDefaultsToMariaDB ensures an unknown/nil version falls back to
// the safe MariaDB path rather than assuming a QUERY_SAMPLE_TEXT column that may not exist.
func TestPfsQueriesSQL_NilVersionDefaultsToMariaDB(t *testing.T) {
	q := pfsQueriesSQL(nil)
	if !strings.Contains(q, "LEFT JOIN") || strings.Contains(q, "QUERY_SAMPLE_TEXT") {
		t.Errorf("nil version should default to the MariaDB derived-join path:\n%s", q)
	}
}
