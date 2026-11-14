// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestAPIUserListWithPassword(t *testing.T) {
	got, ok := APIUserListWithPassword("admin:a,ops:b,dev:", "ops", "N3w")
	if !ok || got != "admin:a,ops:N3w,dev:" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := APIUserListWithPassword("admin:a", "ops", "x"); ok {
		t.Fatal("an absent user was found")
	}
	if !APIUserListHas(" admin:a, ops:b", "ops") || APIUserListHas("admin:a", "adm") {
		t.Fatal("APIUserListHas")
	}
}
