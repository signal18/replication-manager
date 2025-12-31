// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"fmt"
	"strconv"

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
	// Validate numeric value before using
	if err := ValidateNumeric(size); err != nil {
		return "", fmt.Errorf("invalid relay_log_space_limit value: %w", err)
	}

	// Convert to int64 for type-safe query execution
	sizeValue, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid relay_log_space_limit value: %w", err)
	}

	query := "SET GLOBAL relay_log_space_limit = ?"
	_, err = db.Exec(query, sizeValue)
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
	// Validate numeric value before using
	if err := ValidateNumeric(timeout); err != nil {
		return "", fmt.Errorf("invalid timeout value: %w", err)
	}
	// NOTE: SET STATEMENT cannot use parameterized queries, but value is validated as numeric
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
	// Convert to int64 for type-safe query execution
	connValue, err := strconv.ParseInt(connections, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid max_connections value: %w", err)
	}

	query := "SET GLOBAL max_connections = ?"
	_, err = db.Exec(query, connValue)
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
