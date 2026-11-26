// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"strings"
	"testing"
)

// The staging table names: <schema>__<table>__r and __d, under 64 characters, distinct for
// two long names.
func TestStagingNames(t *testing.T) {
	if r, d := StagingNames("shop", "orders"); r != "shop__orders__r" || d != "shop__orders__d" {
		t.Fatalf("%s %s", r, d)
	}
	long := strings.Repeat("x", 60)
	r1, d1 := StagingNames(long, long+"a")
	r2, _ := StagingNames(long, long+"b")
	if len(r1) > 64 || len(d1) > 64 || r1 == r2 {
		t.Fatalf("long names: %s %s %s", r1, d1, r2)
	}
}
