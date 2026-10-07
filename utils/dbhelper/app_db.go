// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// The database and user of a linked application (app-db-auto-create, #1870), on MySQL and
// MariaDB through the existing helpers and on PostgreSQL through its own statements (#1908):
// a PostgreSQL application gets ONE role (no user@host) that owns its database.

package dbhelper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// QuotePostgresIdentifier quotes an identifier the PostgreSQL way (double quotes doubled).
func QuotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// QuotePostgresLiteral quotes a string literal the PostgreSQL way (single quotes doubled).
func QuotePostgresLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// PostgresCreateRoleSQL is the login role of an application with its password.
func PostgresCreateRoleSQL(user, password string) string {
	return "CREATE ROLE " + QuotePostgresIdentifier(user) + " WITH LOGIN PASSWORD " + QuotePostgresLiteral(password)
}

// PostgresSetRolePasswordSQL changes the password of an application's role.
func PostgresSetRolePasswordSQL(user, password string) string {
	return "ALTER ROLE " + QuotePostgresIdentifier(user) + " WITH PASSWORD " + QuotePostgresLiteral(password)
}

// PostgresCreateDatabaseSQL is the application's database; the ownership goes to the role in
// the grant step, once the role exists, so the flow keeps its order (database, user, grants).
func PostgresCreateDatabaseSQL(database string) string {
	return "CREATE DATABASE " + QuotePostgresIdentifier(database)
}

// PostgresGrantDatabaseSQL gives the role everything on its database: the ownership (which
// also covers the public schema since PostgreSQL 15) and the explicit privileges.
func PostgresGrantDatabaseSQL(database, user string) []string {
	return []string{
		"ALTER DATABASE " + QuotePostgresIdentifier(database) + " OWNER TO " + QuotePostgresIdentifier(user),
		"GRANT ALL PRIVILEGES ON DATABASE " + QuotePostgresIdentifier(database) + " TO " + QuotePostgresIdentifier(user),
	}
}

// ListDatabases is the user databases of a server: pg_database without the templates on
// PostgreSQL, the schemas of GetSchemas elsewhere.
func ListDatabases(db *sqlx.DB, myver *version.Version) ([]string, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		out := []string{}
		query := "SELECT datname FROM pg_catalog.pg_database WHERE datistemplate = false ORDER BY datname"
		if err := db.Select(&out, query); err != nil {
			return nil, query, fmt.Errorf("could not list databases: %w", err)
		}
		return out, query, nil
	}
	return GetSchemas(db)
}

// appPasswordMask replaces the password in a logged statement. The password is
// masked in every spelling the statement can carry: raw, the SQL literal with its
// quotes doubled, and the backslash-escaped form.
const appPasswordMask = "*.*"

// MaskAppPassword returns the statement with the password masked, for the log. An
// empty password masks nothing (ReplaceAll would insert the mask between every byte).
func MaskAppPassword(statement, password string) string {
	if password == "" {
		return statement
	}
	out := statement
	for _, spelling := range []string{
		strings.ReplaceAll(strings.ReplaceAll(password, "\\", "\\\\"), "'", "\\'"),
		strings.ReplaceAll(password, "'", "''"),
		password,
	} {
		if spelling != "" {
			out = strings.ReplaceAll(out, spelling, appPasswordMask)
		}
	}
	return out
}

// CreateAppUser creates the application's user (MySQL user@host) or role (PostgreSQL).
// The returned statement is the LOGGED one: the password is masked.
func CreateAppUser(db *sqlx.DB, myver *version.Version, host, user, password string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		q := PostgresCreateRoleSQL(user, password)
		_, err := db.Exec(q)
		return PostgresCreateRoleSQL(user, appPasswordMask), err
	}
	logs, err := CreateUser(db, myver, host, user, password)
	return MaskAppPassword(logs, password), err
}

// SetAppUserPassword sets the password of the application's user or role. The returned
// statement is the LOGGED one: the password is masked.
func SetAppUserPassword(db *sqlx.DB, myver *version.Version, host, user, password string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		q := PostgresSetRolePasswordSQL(user, password)
		_, err := db.Exec(q)
		return PostgresSetRolePasswordSQL(user, appPasswordMask), err
	}
	logs, err := SetUserPassword(db, myver, host, user, password)
	return MaskAppPassword(logs, password), err
}

// CreateAppDatabase creates the application's database (a schema on MySQL and MariaDB).
func CreateAppDatabase(db *sqlx.DB, myver *version.Version, database string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		q := PostgresCreateDatabaseSQL(database)
		_, err := db.Exec(q)
		return q, err
	}
	return CreateDatabaseIfNotExists(db, database)
}

// GrantAppDatabase gives the application's user or role everything on its database.
func GrantAppDatabase(ctx context.Context, db *sqlx.DB, connx *sqlx.Conn, myver *version.Version, host, user, database string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		var logs []string
		for _, q := range PostgresGrantDatabaseSQL(database, user) {
			logs = append(logs, q)
			if _, err := db.ExecContext(ctx, q); err != nil {
				return strings.Join(logs, "; "), err
			}
		}
		return strings.Join(logs, "; "), nil
	}
	return SetUserGrants(ctx, connx, myver, host, user, fmt.Sprintf("ALL PRIVILEGES ON %s.*", QuoteMySQLIdentifier(database)))
}
