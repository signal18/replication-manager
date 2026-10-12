// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "testing"

// #1991: every grant table file is recognised, data and definition, whatever the
// chunk or compression; other mysql tables and user schemas are not.
func TestIsMysqlAccountTableFile(t *testing.T) {
	for name, want := range map[string]bool{
		"mysql.user.sql.gz":               true,
		"mysql.user-schema-view.sql.gz":   true,
		"mysql.global_priv.00000.sql.gz":  true,
		"mysql.global_priv-schema.sql.gz": true,
		"mysql.db.sql.zst":                true,
		"mysql.tables_priv.sql":           true,
		"mysql.columns_priv.sql.gz":       true,
		"mysql.procs_priv.sql.gz":         true,
		"mysql.proxies_priv.sql.gz":       true,
		"mysql.roles_mapping.sql.gz":      true,
		"mysql.role_edges.sql.gz":         true,
		"mysql.default_roles.sql.gz":      true,
		"mysql.global_grants.sql.gz":      true,
		"mysql.password_history.sql.gz":   true,
		"mysql.time_zone.sql.gz":          false,
		"mysql.proc.sql.gz":               false,
		"mysql.dbx.sql.gz":                false,
		"mysql-schema-create.sql.gz":      false,
		"shop.user.sql.gz":                false,
		"metadata":                        false,
		"mysql.users_backup.sql.gz":       false,
	} {
		if got := isMysqlAccountTableFile(name); got != want {
			t.Errorf("isMysqlAccountTableFile(%q) = %t, want %t", name, got, want)
		}
	}
}
