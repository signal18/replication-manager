// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// Storage engine switch (#1999): the tables of one engine, an engine's support on the
// server, and the ALTER TABLE ... ENGINE that moves a table to another engine.

// ErrNoStorageEngine: PostgreSQL has no pluggable storage engine per table.
var ErrNoStorageEngine = errors.New("PostgreSQL has no storage engine per table")

var engineNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// EngineTable is a user table and what an engine switch needs to know about it.
type EngineTable struct {
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Engine     string `json:"engine"`
	Bytes      int64  `json:"bytes"`      // data + indexes, as information_schema reports them
	ForeignKey bool   `json:"foreignKey"` // a foreign key references this table or is declared on it
}

// GetTablesByEngine lists the user tables (system schemas and replication_manager_schema
// excluded) of an engine, "" for any, optionally of one schema and one table;
// replication_manager_schema named as the schema lists its tables.
func GetTablesByEngine(db *sqlx.DB, myver *version.Version, engine, schema, table string) ([]EngineTable, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT t.TABLE_SCHEMA, t.TABLE_NAME, COALESCE(t.ENGINE,''), COALESCE(t.DATA_LENGTH,0)+COALESCE(t.INDEX_LENGTH,0), " +
		"EXISTS (SELECT 1 FROM information_schema.REFERENTIAL_CONSTRAINTS r WHERE (r.CONSTRAINT_SCHEMA = t.TABLE_SCHEMA AND r.TABLE_NAME = t.TABLE_NAME) " +
		"OR (r.UNIQUE_CONSTRAINT_SCHEMA = t.TABLE_SCHEMA AND r.REFERENCED_TABLE_NAME = t.TABLE_NAME)) " +
		"FROM information_schema.TABLES t WHERE t.TABLE_TYPE = 'BASE TABLE' " +
		"AND t.TABLE_SCHEMA NOT IN ('mysql','sys','information_schema','performance_schema'"
	if schema != "replication_manager_schema" {
		query += ",'replication_manager_schema'"
	}
	query += ")"
	var args []any
	if engine != "" {
		query += " AND UPPER(t.ENGINE) = UPPER(?)"
		args = append(args, engine)
	}
	if schema != "" {
		query += " AND t.TABLE_SCHEMA = ?"
		args = append(args, schema)
	}
	if table != "" {
		query += " AND t.TABLE_NAME = ?"
		args = append(args, table)
	}
	query += " ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME"
	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	var out []EngineTable
	for rows.Next() {
		var t EngineTable
		if err := rows.Scan(&t.Schema, &t.Table, &t.Engine, &t.Bytes, &t.ForeignKey); err != nil {
			return out, query, err
		}
		out = append(out, t)
	}
	return out, query, rows.Err()
}

// GetEngineSupport returns the SUPPORT column of information_schema.ENGINES for an
// engine (YES, DEFAULT, NO, DISABLED), "" when the server does not know the engine.
func GetEngineSupport(db *sqlx.DB, myver *version.Version, engine string) (string, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return "", "", ErrNoStorageEngine
	}
	query := "SELECT SUPPORT FROM information_schema.ENGINES WHERE UPPER(ENGINE) = UPPER(?)"
	var support string
	err := db.QueryRowx(query, engine).Scan(&support)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return "", query, nil
	}
	return support, query, err
}

// AlterTableEngine moves schema.table to an engine: ALTER TABLE ... ENGINE=..., a copy
// of the table that blocks its writes for the duration. The engine name is checked
// (it cannot be a bound parameter).
func AlterTableEngine(db *sqlx.DB, myver *version.Version, schema, table, engine string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return "", ErrNoStorageEngine
	}
	if !engineNameRe.MatchString(engine) {
		return "", fmt.Errorf("invalid engine name %q", engine)
	}
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "ALTER TABLE " + quoted + " ENGINE=" + engine
	_, err = db.Exec(query)
	return query, err
}
