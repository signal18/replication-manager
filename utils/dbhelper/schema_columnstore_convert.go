// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// Splitdump reload into a ColumnStore cluster (#2010): what the conversion of an InnoDB
// table reads of it on the target (its columns, checks, indexes, foreign keys, partitions,
// options) and the statements that put the generated ColumnStore table in its place.

// TableColumn is a column as information_schema.COLUMNS describes it.
type TableColumn struct {
	Name          string         `json:"name"`
	DataType      string         `json:"dataType"`   // int, varchar, enum...
	ColumnType    string         `json:"columnType"` // int(10) unsigned, enum('a','b')...
	Nullable      bool           `json:"nullable"`
	Default       sql.NullString `json:"-"`     // the literal SQL, 'quoted' for a string, NULL, or an expression()
	Extra         string         `json:"extra"` // auto_increment, on update ..., VIRTUAL GENERATED...
	Charset       sql.NullString `json:"-"`
	Collation     sql.NullString `json:"-"`
	MaxLength     sql.NullInt64  `json:"-"` // CHARACTER_MAXIMUM_LENGTH
	OctetLength   sql.NullInt64  `json:"-"` // CHARACTER_OCTET_LENGTH
	Precision     sql.NullInt64  `json:"-"` // NUMERIC_PRECISION (bit: its width)
	Scale         sql.NullInt64  `json:"-"`
	GenerationExp sql.NullString `json:"-"`
	Comment       string         `json:"comment"`
}

// GetTableColumns returns the columns of schema.table in their order.
func GetTableColumns(db *sqlx.DB, myver *version.Version, schema, table string) ([]TableColumn, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_DEFAULT, EXTRA, CHARACTER_SET_NAME, COLLATION_NAME, " +
		"CHARACTER_MAXIMUM_LENGTH, CHARACTER_OCTET_LENGTH, NUMERIC_PRECISION, NUMERIC_SCALE, GENERATION_EXPRESSION, COALESCE(COLUMN_COMMENT,'') " +
		"FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION"
	rows, err := db.Queryx(query, schema, table)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	var out []TableColumn
	for rows.Next() {
		var c TableColumn
		if err := rows.Scan(&c.Name, &c.DataType, &c.ColumnType, &c.Nullable, &c.Default, &c.Extra, &c.Charset, &c.Collation,
			&c.MaxLength, &c.OctetLength, &c.Precision, &c.Scale, &c.GenerationExp, &c.Comment); err != nil {
			return out, query, err
		}
		out = append(out, c)
	}
	return out, query, rows.Err()
}

// TableCheck is a CHECK constraint, of the table or of one column.
type TableCheck struct {
	Name   string `json:"name"`
	Clause string `json:"clause"`
}

// GetTableChecks returns the CHECK constraints of schema.table.
func GetTableChecks(db *sqlx.DB, myver *version.Version, schema, table string) ([]TableCheck, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT CONSTRAINT_NAME, CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ? ORDER BY CONSTRAINT_NAME"
	rows, err := db.Queryx(query, schema, table)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	var out []TableCheck
	for rows.Next() {
		var c TableCheck
		if err := rows.Scan(&c.Name, &c.Clause); err != nil {
			return out, query, err
		}
		out = append(out, c)
	}
	return out, query, rows.Err()
}

// GetTableIndexNames returns the names of the indexes of schema.table, PRIMARY included.
func GetTableIndexNames(db *sqlx.DB, myver *version.Version, schema, table string) ([]string, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME"
	var out []string
	err := db.Select(&out, query, schema, table)
	return out, query, err
}

// TableOptions is a table's engine, what the conversion keeps of its options, and whether it is partitioned.
type TableOptions struct {
	Engine      string
	Charset     string
	Collation   string
	Comment     string
	Partitioned bool
}

// GetTableOptions returns the engine, default charset, collation, comment and partitioning of schema.table.
func GetTableOptions(db *sqlx.DB, myver *version.Version, schema, table string) (TableOptions, string, error) {
	var o TableOptions
	if myver != nil && myver.IsPostgreSQL() {
		return o, "", ErrNoStorageEngine
	}
	query := "SELECT COALESCE(t.ENGINE,''), COALESCE(c.CHARACTER_SET_NAME,''), COALESCE(t.TABLE_COLLATION,''), COALESCE(t.TABLE_COMMENT,''), " +
		"EXISTS (SELECT 1 FROM information_schema.PARTITIONS p WHERE p.TABLE_SCHEMA = t.TABLE_SCHEMA AND p.TABLE_NAME = t.TABLE_NAME AND p.PARTITION_NAME IS NOT NULL) " +
		"FROM information_schema.TABLES t LEFT JOIN information_schema.COLLATIONS c ON c.COLLATION_NAME = t.TABLE_COLLATION " +
		"WHERE t.TABLE_SCHEMA = ? AND t.TABLE_NAME = ?"
	err := db.QueryRowx(query, schema, table).Scan(&o.Engine, &o.Charset, &o.Collation, &o.Comment, &o.Partitioned)
	return o, query, err
}

// CreateTableFromDDL runs a CREATE TABLE statement generated by replication-manager (the
// ColumnStore conversion builds it from quoted identifiers and the server's own column
// types and literals: no user input).
func CreateTableFromDDL(ctx context.Context, conn *sqlx.Conn, ddl string) (string, error) {
	if !strings.HasPrefix(ddl, "CREATE TABLE ") {
		return ddl, fmt.Errorf("not a CREATE TABLE statement")
	}
	_, err := conn.ExecContext(ctx, ddl)
	return ddl, err
}

// GetTableForeignKeyNames returns the foreign keys declared on schema.table.
func GetTableForeignKeyNames(db *sqlx.DB, myver *version.Version, schema, table string) ([]string, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT CONSTRAINT_NAME FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ? ORDER BY CONSTRAINT_NAME"
	var out []string
	err := db.Select(&out, query, schema, table)
	return out, query, err
}

// DropTableConn drops schema.table on a pinned connection (its session settings apply:
// FOREIGN_KEY_CHECKS=0 during a restore).
func DropTableConn(ctx context.Context, conn *sqlx.Conn, schema, table string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "DROP TABLE " + quoted
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// RenameTableConn renames schema.from to schema.to on a pinned connection.
func RenameTableConn(ctx context.Context, conn *sqlx.Conn, schema, from, to string) (string, error) {
	src, err := QuoteMySQLTableIdentifier(schema + "." + from)
	if err != nil {
		return "", err
	}
	dst, err := QuoteMySQLTableIdentifier(schema + "." + to)
	if err != nil {
		return "", err
	}
	query := "RENAME TABLE " + src + " TO " + dst
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// SetChannelReplicateIgnoreTable sets replicate_ignore_table of one replication channel
// (MariaDB multi-source: the per-connection filter of @@default_master_connection), on a
// pinned connection of db; the channel must be stopped. tables: schema.table, "" clears it.
func SetChannelReplicateIgnoreTable(ctx context.Context, db *sqlx.DB, myver *version.Version, channel string, tables []string) (string, error) {
	if myver != nil && !myver.IsMariaDB() {
		return "", fmt.Errorf("per-channel replicate_ignore_table needs MariaDB")
	}
	conn, err := db.Connx(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	q1 := "SET @@default_master_connection = " + QuoteMySQLString(channel)
	if _, err := conn.ExecContext(ctx, q1); err != nil {
		return q1, err
	}
	q2 := "SET GLOBAL replicate_ignore_table = " + QuoteMySQLString(strings.Join(tables, ","))
	_, err = conn.ExecContext(ctx, q2)
	return q1 + "; " + q2, err
}
