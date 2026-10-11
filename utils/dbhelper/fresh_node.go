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

// HasUserData reports whether the server holds any user schema (#1979): a node with one
// is never restored automatically. A schema counts whatever it holds (tables, sequences,
// views, routines, events), so an empty user schema is user data too.
//   - MariaDB/MySQL: a schema of ListDatabases (system schemas excluded) other than
//     replication_manager_schema, the one replication-manager's provisioning creates.
//     information_schema only lists what the reading account may see: the caller must
//     prove that account sees everything first (global SELECT).
//   - PostgreSQL: a database besides postgres (ListDatabases, templates excluded), or a
//     table in the connected database outside pg_catalog, information_schema and
//     replication_manager_schema. The catalogs are not filtered by privileges.
func HasUserData(db *sqlx.DB, myver *version.Version) (bool, string, error) {
	dbs, query, err := ListDatabases(db, myver)
	if err != nil {
		return false, query, err
	}
	pg := myver != nil && myver.IsPostgreSQL()
	for _, d := range dbs {
		if d == "replication_manager_schema" || (pg && d == "postgres") {
			continue
		}
		return true, query, nil
	}
	if !pg {
		return false, query, nil
	}
	var found bool
	query = "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema','replication_manager_schema'))"
	err = db.QueryRowx(query).Scan(&found)
	return found, query, err
}
