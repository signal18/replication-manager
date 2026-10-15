// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file holds the SQL of the restore level of backup verification (#2023): the
// verification server's copy of a backup is dropped, counted and listed here.

package dbhelper

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// ContextExecer runs a statement: a *sqlx.DB pool, or a pinned *sqlx.Conn when the
// statement must run in that session (its sql_log_bin=0 on a replica a test restore reseeds).
type ContextExecer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// ErrBackupVerificationPostgreSQL: backup verification loads mysqldump backups, a
// PostgreSQL server is never a verification server.
var ErrBackupVerificationPostgreSQL = errors.New("backup verification loads MySQL/MariaDB dumps only, not on PostgreSQL")

// DropDatabaseIfExists drops a schema of the verification server before and after a test
// restore. PostgreSQL: ErrBackupVerificationPostgreSQL, nothing is sent.
func DropDatabaseIfExists(db ContextExecer, myver *version.Version, schema string) (string, error) {
	if myver.IsPostgreSQL() {
		return "", ErrBackupVerificationPostgreSQL
	}
	query := "DROP DATABASE IF EXISTS " + QuoteMySQLIdentifier(schema)
	_, err := db.ExecContext(context.Background(), query)
	return query, err
}

// UseDatabaseConn makes schema the current schema of a pinned connection of the
// verification server, before a splitdump file of that schema loads. PostgreSQL:
// ErrBackupVerificationPostgreSQL, nothing is sent.
func UseDatabaseConn(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema string) (string, error) {
	if myver.IsPostgreSQL() {
		return "", ErrBackupVerificationPostgreSQL
	}
	query := "USE " + QuoteMySQLIdentifier(schema)
	_, err := conn.ExecContext(ctx, query)
	return query, err
}

// ErrSetGtidPurgedNotMySQL: GTID_PURGED is MySQL's; MariaDB keeps its replica position in
// gtid_slave_pos, PostgreSQL has no GTID.
var ErrSetGtidPurgedNotMySQL = errors.New("GTID_PURGED is MySQL/Percona only")

// SetGtidPurged sets the GTID set a replica has executed, after a RESET MASTER, so that a
// replica reseeded from a backup resumes with MASTER_AUTO_POSITION from the backup's
// position. MySQL and Percona only: MariaDB and PostgreSQL get ErrSetGtidPurgedNotMySQL,
// nothing is sent.
func SetGtidPurged(db *sqlx.DB, myver *version.Version, gtidSet string) (string, error) {
	if myver.IsPostgreSQL() || myver.IsMariaDB() {
		return "", ErrSetGtidPurgedNotMySQL
	}
	query := "SET @@GLOBAL.GTID_PURGED = '" + strings.ReplaceAll(gtidSet, "'", "''") + "'"
	_, err := db.Exec(query)
	return query, err
}

// CountTableRows returns the exact number of rows of schema.table (COUNT(*), not the
// estimate of information_schema), canceled with ctx. PostgreSQL: ErrBackupVerificationPostgreSQL.
func CountTableRows(ctx context.Context, db *sqlx.DB, myver *version.Version, schema, table string) (int64, string, error) {
	if myver.IsPostgreSQL() {
		return 0, "", ErrBackupVerificationPostgreSQL
	}
	query := "SELECT COUNT(*) FROM " + QuoteMySQLIdentifier(schema) + "." + QuoteMySQLIdentifier(table)
	var n int64
	err := db.QueryRowxContext(ctx, query).Scan(&n)
	return n, query, err
}

// ErrGtidRetentionNotMySQL: gtid_purged is MySQL's, BINLOG_GTID_POS MariaDB's.
var ErrGtidRetentionNotMySQL = errors.New("GTID retention is read on MySQL/Percona (gtid_purged) or MariaDB (BINLOG_GTID_POS)")

// GtidPurgedSubsetOf tells whether every GTID the server purged from its binary logs is in
// gtidSet: a replica that executed gtidSet can resume from it with auto-position. MySQL and
// Percona only: ErrGtidRetentionNotMySQL elsewhere, nothing is sent.
func GtidPurgedSubsetOf(db *sqlx.DB, myver *version.Version, gtidSet string) (bool, string, error) {
	if myver.IsPostgreSQL() || myver.IsMariaDB() {
		return false, "", ErrGtidRetentionNotMySQL
	}
	query := "SELECT GTID_SUBSET(@@GLOBAL.gtid_purged, ?)"
	var ok int
	err := db.QueryRowx(query, gtidSet).Scan(&ok)
	return ok == 1, query, err
}

// BinlogGtidPos returns the GTID position at the start of the binary log file of a MariaDB
// server: "" when it has none. MariaDB only: ErrGtidRetentionNotMySQL elsewhere.
func BinlogGtidPos(db *sqlx.DB, myver *version.Version, file string) (string, string, error) {
	if !myver.IsMariaDB() {
		return "", "", ErrGtidRetentionNotMySQL
	}
	query := "SELECT BINLOG_GTID_POS(?, 4)"
	var pos sql.NullString
	err := db.QueryRowx(query, file).Scan(&pos)
	return pos.String, query, err
}

// Schema object kinds of GetSchemaObjectNames.
const (
	SchemaObjectTable     = "table"
	SchemaObjectView      = "view"
	SchemaObjectProcedure = "procedure"
	SchemaObjectFunction  = "function"
	SchemaObjectTrigger   = "trigger"
	SchemaObjectEvent     = "event"
)

// GetSchemaObjectNames returns the names of a schema's tables, views, procedures,
// functions, triggers and events, by kind (SchemaObject*). PostgreSQL:
// ErrBackupVerificationPostgreSQL.
func GetSchemaObjectNames(db *sqlx.DB, myver *version.Version, schema string) (map[string][]string, string, error) {
	if myver.IsPostgreSQL() {
		return nil, "", ErrBackupVerificationPostgreSQL
	}
	query := "SELECT CASE WHEN TABLE_TYPE = 'VIEW' THEN 'view' ELSE 'table' END, TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? " +
		"UNION ALL SELECT LOWER(ROUTINE_TYPE), ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? " +
		"UNION ALL SELECT 'trigger', TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? " +
		"UNION ALL SELECT 'event', EVENT_NAME FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ?"
	rows, err := db.Queryx(query, schema, schema, schema, schema)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	objects := make(map[string][]string)
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return nil, query, err
		}
		objects[kind] = append(objects[kind], name)
	}
	return objects, query, rows.Err()
}

// The table of replication_manager_schema that marks a server as a verification server:
// its existence is the mark, one row per cluster that marked it.
const (
	VerificationMarkSchema = "replication_manager_schema"
	VerificationMarkTable  = "backup_verification_server"
)

// HasVerificationMark tells whether the server holds the verification server mark
// (replication_manager_schema.backup_verification_server). PostgreSQL:
// ErrBackupVerificationPostgreSQL.
func HasVerificationMark(db *sqlx.DB, myver *version.Version) (bool, string, error) {
	if myver.IsPostgreSQL() {
		return false, "", ErrBackupVerificationPostgreSQL
	}
	query := "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?"
	var n int
	err := db.QueryRowx(query, VerificationMarkSchema, VerificationMarkTable).Scan(&n)
	return n > 0, query, err
}

// MarkVerificationServer marks the server as a verification server for cluster: the
// schemas a backup holds may be dropped there. by says who marked it. It returns the last
// statement sent. PostgreSQL: ErrBackupVerificationPostgreSQL, nothing is sent.
func MarkVerificationServer(db *sqlx.DB, myver *version.Version, cluster, by string) (string, error) {
	if myver.IsPostgreSQL() {
		return "", ErrBackupVerificationPostgreSQL
	}
	table := QuoteMySQLIdentifier(VerificationMarkSchema) + "." + QuoteMySQLIdentifier(VerificationMarkTable)
	for _, query := range []string{
		"CREATE DATABASE IF NOT EXISTS " + QuoteMySQLIdentifier(VerificationMarkSchema),
		"CREATE TABLE IF NOT EXISTS " + table + " (cluster VARCHAR(255) NOT NULL PRIMARY KEY, marked_by VARCHAR(255) NOT NULL, marked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB",
	} {
		if _, err := db.Exec(query); err != nil {
			return query, err
		}
	}
	query := "REPLACE INTO " + table + " (cluster, marked_by, marked_at) VALUES (?, ?, NOW())"
	_, err := db.Exec(query, cluster, by)
	return query, err
}
