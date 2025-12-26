// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func SetInnoDBLockMonitor(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_status_output=ON"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return logs, err
	}
	query = "SET GLOBAL innodb_status_output_locks=ON"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func UnsetInnoDBLockMonitor(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_status_output_locks=0"
	logs := query
	_, err := db.Exec(query)

	if err != nil {
		return logs, err
	}
	query = "SET GLOBAL innodb_status_output=0"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetRelayLogSpaceLimit(db *sqlx.DB, size string) (string, error) {
	query := "SET GLOBAL relay_log_space_limit=" + size
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetSyncBinlog(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL sync_binlog=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetSyncInnodb(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_flush_log_at_trx_commit=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func FlushTables(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH TABLES")
	return "FLUSH TABLES", err
}

func FlushTablesNoLog(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH NO_WRITE_TO_BINLOG TABLES")
	return "FLUSH NO_WRITE_TO_BINLOG TABLES", err
}

func MariaDBFlushTablesNoLogTimeout(db *sqlx.DB, timeout string) (string, error) {
	query := "SET STATEMENT max_statement_time=" + timeout + " FOR FLUSH NO_WRITE_TO_BINLOG TABLES"
	_, err := db.Exec(query)
	//MySQL does not support DML timeout only SELECT
	return query, err
}

func FlushTablesWithReadLock(db *sqlx.DB, myver *version.Version) (string, error) {
	query := "FLUSH NO_WRITE_TO_BINLOG TABLES WITH READ LOCK"
	_, err := db.Exec(query)
	return query, err
}

func UnlockTables(db *sqlx.DB) (string, error) {
	query := "UNLOCK TABLES"
	_, err := db.Exec(query)
	return query, err
}

func SetMaxConnections(db *sqlx.DB, connections string, myver *version.Version) (string, error) {

	query := "SET GLOBAL max_connections=" + connections
	_, err := db.Exec(query)
	return query, err
}

func SetReadOnly(db *sqlx.DB, flag bool) (string, error) {
	if flag == true {
		query := "SET GLOBAL read_only=1"
		_, err := db.Exec(query)
		return query, err
	} else {
		query := "SET GLOBAL read_only=0"
		_, err := db.Exec(query)
		return query, err
	}
}

func SetSuperReadOnly(db *sqlx.DB, flag bool) (string, error) {
	if flag == true {
		_, err := db.Exec("SET GLOBAL super_read_only=1")
		return "SET GLOBAL super_read_only=1", err
	} else {
		_, err := db.Exec("SET GLOBAL super_read_only=0")
		return "SET GLOBAL super_read_only=0", err
	}
}

