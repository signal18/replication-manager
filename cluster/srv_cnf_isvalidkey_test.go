// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "testing"

// #2278: a preserved/delta variable with an empty or malformed name must never be
// written, because "loose_" + "" = "loose_=<value>" is a fatal ambiguous option that
// stops mariadbd from starting.
func TestIsWritableConfigKey(t *testing.T) {
	cases := map[string]bool{
		"":                        false, // empty: the bug (loose_= )
		"loose_":                  false, // loose_ prefix with nothing after
		"loose-":                  false,
		";set @s=1":               false, // attacker SQL-ish junk
		"loose_;set global x=1":   false,
		"a b":                     false, // space
		"=true":                   false,
		"optimizer_switch":        true,
		"binlog_format":           true,
		"innodb_buffer_pool_size": true,
		"loose_binlog_format":     true, // valid after stripping loose_
		"loose-gtid-mode":         true,
		"gtid-strict-mode":        true,
	}
	for name, want := range cases {
		if got := isWritableConfigKey(name); got != want {
			t.Errorf("isWritableConfigKey(%q) = %v, want %v", name, got, want)
		}
	}
}
