// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// ColumnStore table rebalance (#2008): where a ColumnStore table's extents are (one
// dbroot per PM), how many dbroots the cluster has, and the statements of the rebuild
// that spreads a table over all of them: a copy under a table read lock, then an atomic
// rename. ColumnStore has no LOCK TABLES (MCS-2029) and refuses a same-engine ALTER
// (CAL0009); its redistributeData went away with mcsadmin.

// ColumnstoreTable is a ColumnStore table and its column extents per dbroot.
type ColumnstoreTable struct {
	Schema  string      `json:"schema"`
	Table   string      `json:"table"`
	Columns int         `json:"columns"` // columns, each with its own extents
	Bytes   int64       `json:"bytes"`   // data size of its extents
	Extents map[int]int `json:"extents"` // dbroot -> extents
}

// columnstoreConfigPath: ColumnStore's configuration on the UM, SystemConfig.DBRootCount
// counts every dbroot, an empty one of a PM just added too
const columnstoreConfigPath = "/etc/columnstore/Columnstore.xml"

// GetColumnstoreDBRootCount returns SystemConfig.DBRootCount of the server's
// Columnstore.xml, read with LOAD_FILE; 0 when the server cannot read it (no FILE
// privilege, secure_file_priv): the caller then counts the dbroots that hold extents.
func GetColumnstoreDBRootCount(db *sqlx.DB, myver *version.Version) (int, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return 0, "", ErrNoStorageEngine
	}
	query := "SELECT ExtractValue(LOAD_FILE('" + columnstoreConfigPath + "'), '/Columnstore/SystemConfig/DBRootCount')"
	var count sql.NullString
	if err := db.QueryRowx(query).Scan(&count); err != nil {
		return 0, query, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(count.String))
	if err != nil {
		return 0, query, nil
	}
	return n, query, nil
}

// GetColumnstoreTableExtents lists the ColumnStore tables, optionally of one schema and
// one table, with their column extents per dbroot. The columns come from
// information_schema.COLUMNSTORE_COLUMNS, then each column's extents from
// COLUMNSTORE_EXTENTS with OBJECT_ID = ?, one query per column: the view's full scan stops
// at ObjectIDManager::size(), the number of object ids in use, and ColumnStore reuses the
// ids a DROP frees, so once a table was dropped the newer objects are past that bound and
// missing from a join or a range (dev3: a rebuilt table's extents absent, editem showing
// them); OBJECT_ID IN (...) misses them too. Only the equality reads one object directly.
func GetColumnstoreTableExtents(db *sqlx.DB, myver *version.Version, schema, table string) ([]ColumnstoreTable, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT TABLE_SCHEMA, TABLE_NAME, OBJECT_ID FROM information_schema.COLUMNSTORE_COLUMNS " +
		"WHERE TABLE_SCHEMA NOT IN ('mysql','sys','information_schema','performance_schema','replication_manager_schema','calpontsys')"
	var args []any
	if schema != "" {
		query += " AND TABLE_SCHEMA = ?"
		args = append(args, schema)
	}
	if table != "" {
		query += " AND TABLE_NAME = ?"
		args = append(args, table)
	}
	query += " ORDER BY TABLE_SCHEMA, TABLE_NAME, OBJECT_ID"
	type column struct {
		Schema, Table string
		OID           int64
	}
	var columns []column
	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, query, err
	}
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.Schema, &c.Table, &c.OID); err != nil {
			rows.Close()
			return nil, query, err
		}
		columns = append(columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, query, err
	}
	var out []ColumnstoreTable
	extentQuery := "SELECT DBROOT, COALESCE(DATA_SIZE,0) FROM information_schema.COLUMNSTORE_EXTENTS WHERE OBJECT_ID = ?"
	for _, c := range columns {
		if n := len(out); n == 0 || out[n-1].Schema != c.Schema || out[n-1].Table != c.Table {
			out = append(out, ColumnstoreTable{Schema: c.Schema, Table: c.Table, Extents: map[int]int{}})
		}
		cur := &out[len(out)-1]
		cur.Columns++
		ext, err := db.Queryx(extentQuery, c.OID)
		if err != nil {
			return out, extentQuery, err
		}
		for ext.Next() {
			var dbroot int
			var bytes int64
			if err := ext.Scan(&dbroot, &bytes); err != nil {
				ext.Close()
				return out, extentQuery, err
			}
			cur.Extents[dbroot]++
			cur.Bytes += bytes
		}
		ext.Close()
		if err := ext.Err(); err != nil {
			return out, extentQuery, err
		}
	}
	return out, query, nil
}

// CreateTableLike creates schema.newTable with the definition of schema.table.
func CreateTableLike(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema, table, newTable string) (string, error) {
	src, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	dst, err := QuoteMySQLTableIdentifier(schema + "." + newTable)
	if err != nil {
		return "", err
	}
	query := "CREATE TABLE " + dst + " LIKE " + src
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// CopyTableRows copies every row of schema.table into schema.newTable.
func CopyTableRows(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema, table, newTable string) (string, error) {
	src, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	dst, err := QuoteMySQLTableIdentifier(schema + "." + newTable)
	if err != nil {
		return "", err
	}
	query := "INSERT INTO " + dst + " SELECT * FROM " + src
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// LockTableForRead takes FLUSH TABLES schema.table WITH READ LOCK on a pinned connection,
// waiting at most lockWaitSeconds for the running writes: the writers wait until
// UnlockTablesConn on the same connection, the readers do not. It works on ColumnStore,
// where LOCK TABLES does not.
func LockTableForRead(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema, table string, lockWaitSeconds int) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	if lockWaitSeconds > 0 {
		q := "SET SESSION lock_wait_timeout = " + strconv.Itoa(lockWaitSeconds)
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return q, err
		}
	}
	query := "FLUSH NO_WRITE_TO_BINLOG TABLES " + quoted + " WITH READ LOCK"
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// UnlockTablesConn releases the table locks of a pinned connection (UnlockTables: any connection of a pool).
func UnlockTablesConn(ctx context.Context, conn *sqlx.Conn) (string, error) {
	query := "UNLOCK TABLES"
	_, err := conn.ExecContext(ctx, query)
	return query, err
}

// SwapTables renames schema.table to schema.oldTable and schema.newTable to schema.table
// in one statement: the clients never see the table missing.
func SwapTables(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema, table, newTable, oldTable string) (string, error) {
	names := make([]string, 3)
	for i, t := range []string{table, oldTable, newTable} {
		q, err := QuoteMySQLTableIdentifier(schema + "." + t)
		if err != nil {
			return "", err
		}
		names[i] = q
	}
	query := fmt.Sprintf("RENAME TABLE %s TO %s, %s TO %s", names[0], names[1], names[2], names[0])
	_, err := conn.ExecContext(ctx, query)
	return query, err
}

// DropTable drops schema.table if it exists.
func DropTable(ctx context.Context, db *sqlx.DB, myver *version.Version, schema, table string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "DROP TABLE IF EXISTS " + quoted
	_, err = db.ExecContext(ctx, query)
	return query, err
}

// GetConnectionID returns the thread id of a pinned connection.
func GetConnectionID(ctx context.Context, conn *sqlx.Conn) (int64, string, error) {
	query := "SELECT CONNECTION_ID()"
	var id int64
	err := conn.QueryRowxContext(ctx, query).Scan(&id)
	return id, query, err
}

// IsWaitingForMetadataLock: the thread id waits for a metadata lock (its statement queued
// behind a table lock); false when the thread runs anything else or is gone.
func IsWaitingForMetadataLock(db *sqlx.DB, myver *version.Version, id int64) (bool, string, error) {
	query := "SELECT COALESCE(STATE,'') FROM information_schema.PROCESSLIST WHERE ID = ?"
	var st string
	err := db.QueryRowx(query, id).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return false, query, nil
	}
	return strings.Contains(strings.ToLower(st), "metadata lock"), query, err
}

// GetColumnstoreAutoincrementTables returns the schema.table names, optionally of one schema
// and one table, with a ColumnStore autoincrement column, declared by a column comment
// "autoincrement" or a table comment "autoincrement=col[,start]". ColumnStore keeps the next
// value in its catalog: a copy of the rows restarts it, and the next insert reuses an id
// (dev3: 1, 2, 3 copied, the next insert got 1). COLUMNSTORE_COLUMNS.IS_AUTOINCREMENT
// cannot tell, it reports 1 for every column.
func GetColumnstoreAutoincrementTables(db *sqlx.DB, myver *version.Version, schema, table string) (map[string]bool, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return nil, "", ErrNoStorageEngine
	}
	query := "SELECT t.TABLE_SCHEMA, t.TABLE_NAME FROM information_schema.TABLES t WHERE UPPER(t.ENGINE) = 'COLUMNSTORE' " +
		"AND (LOWER(t.TABLE_COMMENT) LIKE '%autoincrement=%' OR EXISTS (SELECT 1 FROM information_schema.COLUMNS c " +
		"WHERE c.TABLE_SCHEMA = t.TABLE_SCHEMA AND c.TABLE_NAME = t.TABLE_NAME AND LOWER(c.COLUMN_COMMENT) LIKE 'autoincrement%'))"
	var args []any
	if schema != "" {
		query += " AND t.TABLE_SCHEMA = ?"
		args = append(args, schema)
	}
	if table != "" {
		query += " AND t.TABLE_NAME = ?"
		args = append(args, table)
	}
	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var s, t string
		if err := rows.Scan(&s, &t); err != nil {
			return out, query, err
		}
		out[s+"."+t] = true
	}
	return out, query, rows.Err()
}
