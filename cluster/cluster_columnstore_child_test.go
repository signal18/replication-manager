// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"reflect"
	"testing"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

// #2015: the plugin carries what the restore sent it, replication_manager_schema's load-test
// tables included, never replication-manager's own tables (belair-mcs 2026-10-15: 112 tables
// sent, 21 carried).
func TestColumnstoreCarried(t *testing.T) {
	got := columnstoreCarried([]dbhelper.EngineTable{
		{Schema: "repl_test", Table: "pk_int"},
		{Schema: "replication_manager_schema", Table: "sbtest1"},
		{Schema: "replication_manager_schema", Table: "heartbeat"},
		{Schema: "mysql", Table: "user"},
		{Schema: "replication_manager_schema", Table: "order_line1"},
	})
	want := []string{"repl_test.pk_int", "replication_manager_schema.order_line1", "replication_manager_schema.sbtest1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
