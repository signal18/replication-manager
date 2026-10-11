// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// GetSystemCatalogAge returns, read at once on the server's own clock, how long ago its
// system catalogue was installed and how long ago it started, in seconds (#1979). Read
// live (the per-tick status keeps the uptime, but as seconds on MariaDB/MySQL and as the
// start epoch on PostgreSQL, and not on the clock the install time is read on).
//   - MariaDB 10.4+: CREATE_TIME of mysql.global_priv. The account table is Aria: its
//     CREATE_TIME is the datadir install and survives restarts (measured on dev3, 12.3 and
//     10.11); the InnoDB system tables report their last start instead.
//   - MariaDB before 10.4, MySQL: CREATE_TIME of mysql.user (MySQL 8 keeps it in the
//     data dictionary, measured on 8.0.41).
//   - PostgreSQL: the initdb time encoded in the high 32 bits of
//     pg_control_system().system_identifier (executable by PUBLIC, measured on 17), the
//     start from pg_postmaster_start_time().
func GetSystemCatalogAge(db *sqlx.DB, myver *version.Version) (installAge int64, startAge int64, query string, err error) {
	if myver != nil && myver.IsPostgreSQL() {
		query = "SELECT EXTRACT(EPOCH FROM now())::bigint - (system_identifier >> 32), EXTRACT(EPOCH FROM (now() - pg_postmaster_start_time()))::bigint FROM pg_control_system()"
		err = db.QueryRowx(query).Scan(&installAge, &startAge)
		return installAge, startAge, query, err
	}
	table := "user"
	if myver != nil && myver.IsMariaDB() && myver.GreaterEqual("10.4") {
		table = "global_priv"
	}
	query = "SELECT TIMESTAMPDIFF(SECOND, t.CREATE_TIME, NOW()), s.VARIABLE_VALUE FROM information_schema.tables t JOIN " + GetVariableSource(db, myver) + ".global_status s ON UPPER(s.VARIABLE_NAME) = 'UPTIME' WHERE t.table_schema = 'mysql' AND t.table_name = ?"
	var age sql.NullInt64
	var uptime string
	if err = db.QueryRowx(query, table).Scan(&age, &uptime); err != nil {
		return 0, 0, query, err
	}
	if !age.Valid {
		return 0, 0, query, errors.New("no CREATE_TIME for mysql." + table)
	}
	if startAge, err = strconv.ParseInt(uptime, 10, 64); err != nil {
		return 0, 0, query, err
	}
	return age.Int64, startAge, query, nil
}

// HasUserTables reports whether the server holds a table outside the system schemas and
// the schemas replication-manager's provisioning creates (#1979): a node with any is
// never restored automatically.
//   - MariaDB/MySQL: a base table outside mysql, sys, information_schema,
//     performance_schema and replication_manager_schema.
//   - PostgreSQL: a database besides postgres (ListDatabases), or a table in the
//     connected database outside pg_catalog, information_schema and
//     replication_manager_schema.
func HasUserTables(db *sqlx.DB, myver *version.Version) (bool, string, error) {
	var found bool
	if myver != nil && myver.IsPostgreSQL() {
		dbs, query, err := ListDatabases(db, myver)
		if err != nil {
			return false, query, err
		}
		for _, d := range dbs {
			if d != "postgres" {
				return true, query, nil
			}
		}
		query = "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema','replication_manager_schema'))"
		err = db.QueryRowx(query).Scan(&found)
		return found, query, err
	}
	query := "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('information_schema','mysql','performance_schema','sys','replication_manager_schema'))"
	err := db.QueryRowx(query).Scan(&found)
	return found, query, err
}
