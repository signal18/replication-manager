// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"reflect"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The direct reseed loads without --force (#2365): with it, a load whose connection dropped at
// its first statement failed every statement after it and the client still exited 0.
func TestWithoutClientForce(t *testing.T) {
	cases := []struct {
		in      string
		kept    []string
		dropped bool
	}{
		{"--force --batch", []string{"--batch"}, true},
		{"--batch -f", []string{"--batch"}, true},
		{"--batch", []string{"--batch"}, false},
		{"", []string{""}, false},
		{"--batch --force-something", []string{"--batch", "--force-something"}, false},
	}
	for _, c := range cases {
		kept, dropped := withoutClientForce(strings.Split(c.in, " "))
		if !reflect.DeepEqual(kept, c.kept) || dropped != c.dropped {
			t.Errorf("withoutClientForce(%q) = %q, %v; want %q, %v", c.in, kept, dropped, c.kept, c.dropped)
		}
	}
}

// A client error of a load is logged at ERROR, every other line of its output at debug.
func TestMysqlClientLineLevel(t *testing.T) {
	cases := map[string]string{
		"ERROR 2006 (HY000) at line 1003296: Server has gone away":               config.LvlErr,
		"  ERROR 1062 (23000) at line 12: Duplicate entry '1' for key 'PRIMARY'": config.LvlErr,
		"LOCK TABLES `time_zone` WRITE":                                          config.LvlDbg,
		"('mysql','gtid_slave_pos','2026-11-09 22:03:14',4,1,0),":                config.LvlDbg,
		"Warning (Code 1287): 'SQL_LOG_BIN' is deprecated":                       config.LvlDbg,
	}
	for line, want := range cases {
		if got := mysqlClientLineLevel(line); got != want {
			t.Errorf("mysqlClientLineLevel(%q) = %s, want %s", line, got, want)
		}
	}
}
