// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import "testing"

// the column types of the PostgreSQL capture's mapping pass, anything else is refused (T1)
func TestValidColumnType(t *testing.T) {
	for _, ok := range []string{"BIGINT", "INT", "DECIMAL(65,30)", "VARCHAR(49)", "CHAR(17)", "TINYINT(1)", "VARBINARY(3072)", "LONGBLOB", "LONGTEXT", "JSON", "UUID", "DATE", "DATETIME(6)", "DOUBLE", "INT UNSIGNED", "double precision",
		"ENUM('happy','sad')", "ENUM('it''s','a\\b','x,y')", "SET('a','b')"} {
		if !validColumnType(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "INT; DROP TABLE x", "INT -- x", "INT /* x */", "INT)", "VARCHAR(10))", "INT`", "ENUM('a')); DROP TABLE x; --", "ENUM('a'", "ENUM(a)", "INT NOT NULL DEFAULT (1)"} {
		if validColumnType(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
