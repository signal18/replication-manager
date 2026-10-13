// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Splitdump reload into a ColumnStore cluster (#2010): ColumnStore refuses most InnoDB DDL
// (any key, ENUM, SET, BIT, YEAR, BINARY, UUID, INET, generated columns, expression
// defaults, partitions, VARCHAR over 8000 bytes) and nothing in ColumnStore or MariaDB
// converts it. In the schema phase, once a table's dump CREATE TABLE ran as written, its
// ColumnStore DDL is generated from the target's information_schema, so no SQL parser:
// keys, foreign keys, partitions and table options disappear by construction. Safe rules,
// from the schema only (no data read): the integers widen because ColumnStore reserves two
// values of each type (TINYINT -128 and -127, TINYINT UNSIGNED 254 and 255, ... measured on
// dev3); NOT NULL goes from the string columns because ColumnStore stores '' as NULL.

// columnstoreStagingSuffix: the ColumnStore table created next to the InnoDB one, renamed
// into its place once created, so a failed CREATE leaves the InnoDB table to load into
const columnstoreStagingSuffix = "__mcs"

// columnstoreMaxStringBytes: ColumnStore's CHAR, VARCHAR and VARBINARY limit, in bytes
const columnstoreMaxStringBytes = 8000

var (
	numericLiteralRe     = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
	columnstoreCommentRe = regexp.MustCompile(`(?i)\b(autoincrement|compression)\b`)
)

// columnstoreConversion is the DDL of a table converted to ColumnStore and what it changed.
type columnstoreConversion struct {
	DDL     string
	Changes []string
}

// columnstoreStringTypes: the columns ColumnStore stores ” of as NULL
var columnstoreStringTypes = map[string]bool{
	"char": true, "varchar": true, "tinytext": true, "text": true, "mediumtext": true, "longtext": true,
	"tinyblob": true, "blob": true, "mediumblob": true, "longblob": true,
	"enum": true, "set": true, "binary": true, "varbinary": true, "uuid": true, "inet4": true, "inet6": true,
}

// columnstoreColumnType maps one column type to ColumnStore, with what it changed; an error
// when ColumnStore cannot hold it at all.
func columnstoreColumnType(c dbhelper.TableColumn) (string, []string, error) {
	typ := strings.ToLower(c.DataType)
	colType := strings.ToLower(c.ColumnType)
	unsigned := strings.Contains(colType, "unsigned")
	var notes []string
	if strings.Contains(colType, "zerofill") {
		colType = strings.TrimSpace(strings.ReplaceAll(colType, "zerofill", ""))
		notes = append(notes, "ZEROFILL removed")
	}
	withUnsigned := func(t string) string {
		if unsigned {
			return t + " unsigned"
		}
		return t
	}
	widen := func(t string) (string, []string, error) {
		notes = append(notes, fmt.Sprintf("%s widened to %s (ColumnStore reserves the 2 extreme values of each integer type)", c.ColumnType, t))
		return t, notes, nil
	}
	charset := ""
	if c.Charset.Valid && c.Charset.String != "" {
		charset = " CHARACTER SET " + c.Charset.String
		if c.Collation.Valid && c.Collation.String != "" {
			charset += " COLLATE " + c.Collation.String
		}
	}
	textFor := func(octets int64) string {
		switch {
		case octets <= 65535:
			return "text"
		case octets <= 16777215:
			return "mediumtext"
		}
		return "longtext"
	}
	switch typ {
	case "tinyint":
		return widen(withUnsigned("smallint"))
	case "smallint":
		return widen(withUnsigned("mediumint"))
	case "mediumint":
		return colType, notes, nil
	case "int", "integer":
		return widen(withUnsigned("bigint"))
	case "bigint":
		if unsigned {
			return widen("decimal(20,0) unsigned")
		}
		return widen("decimal(19,0)")
	case "decimal", "numeric":
		if c.Precision.Valid && c.Precision.Int64 > 38 {
			return "", nil, fmt.Errorf("%s: DECIMAL over 38 digits", c.ColumnType)
		}
		return colType, notes, nil
	case "float", "double", "real":
		return colType, notes, nil
	case "bit":
		t := "bigint unsigned"
		if c.Precision.Valid && c.Precision.Int64 >= 64 {
			t = "decimal(20,0) unsigned"
		}
		notes = append(notes, c.ColumnType+" becomes "+t)
		return t, notes, nil
	case "year":
		notes = append(notes, "YEAR becomes smallint")
		return "smallint", notes, nil
	case "char", "varchar":
		if c.OctetLength.Valid && c.OctetLength.Int64 > columnstoreMaxStringBytes {
			t := textFor(c.OctetLength.Int64)
			notes = append(notes, fmt.Sprintf("%s (%d bytes, over ColumnStore's %d) becomes %s", c.ColumnType, c.OctetLength.Int64, columnstoreMaxStringBytes, t))
			return t + charset, notes, nil
		}
		return colType + charset, notes, nil
	case "tinytext", "text", "mediumtext", "longtext":
		return typ + charset, notes, nil
	case "tinyblob", "blob", "mediumblob", "longblob":
		return typ, notes, nil
	case "enum", "set":
		n := int64(1)
		if c.MaxLength.Valid && c.MaxLength.Int64 > 0 {
			n = c.MaxLength.Int64
		}
		t := "varchar(" + strconv.FormatInt(n, 10) + ")"
		if c.OctetLength.Valid && c.OctetLength.Int64 > columnstoreMaxStringBytes {
			t = textFor(c.OctetLength.Int64)
		}
		notes = append(notes, strings.ToUpper(typ)+" becomes "+t)
		return t + charset, notes, nil
	case "binary", "varbinary":
		notes = append(notes, c.ColumnType+" becomes blob")
		return "blob", notes, nil
	case "uuid":
		notes = append(notes, "UUID becomes char(36)")
		return "char(36)", notes, nil
	case "inet6":
		notes = append(notes, "INET6 becomes varchar(39)")
		return "varchar(39)", notes, nil
	case "inet4":
		notes = append(notes, "INET4 becomes varchar(15)")
		return "varchar(15)", notes, nil
	case "date", "time", "datetime", "timestamp":
		return colType, notes, nil
	case "geometry", "point", "linestring", "polygon", "multipoint", "multilinestring", "multipolygon", "geometrycollection":
		return "", nil, fmt.Errorf("%s: ColumnStore has no spatial type", c.ColumnType)
	}
	return "", nil, fmt.Errorf("%s: type unknown to the ColumnStore conversion", c.ColumnType)
}

// columnstoreColumnDefinition is one column of the generated DDL.
func columnstoreColumnDefinition(c dbhelper.TableColumn) (string, []string, error) {
	typ, notes, err := columnstoreColumnType(c)
	if err != nil {
		return "", nil, err
	}
	def := dbhelper.QuoteMySQLIdentifier(c.Name) + " " + typ
	extra := strings.ToLower(c.Extra)
	if strings.Contains(extra, "generated") {
		notes = append(notes, "generated column becomes a plain column holding its value")
	}
	if strings.Contains(extra, "auto_increment") {
		notes = append(notes, "AUTO_INCREMENT removed")
	}
	if !c.Nullable {
		if columnstoreStringTypes[strings.ToLower(c.DataType)] {
			notes = append(notes, "NOT NULL removed (ColumnStore stores '' as NULL)")
		} else {
			def += " NOT NULL"
		}
	}
	if d := strings.TrimSpace(c.Default.String); c.Default.Valid && d != "" && !strings.EqualFold(d, "NULL") && !strings.Contains(extra, "generated") {
		if strings.HasPrefix(d, "'") || numericLiteralRe.MatchString(d) {
			def += " DEFAULT " + d
		} else {
			notes = append(notes, "DEFAULT "+d+" removed (ColumnStore takes literal defaults only)")
		}
	}
	if strings.Contains(extra, "on update") {
		notes = append(notes, "ON UPDATE removed")
	}
	if cm := c.Comment; cm != "" {
		if columnstoreCommentRe.MatchString(cm) {
			notes = append(notes, "comment removed (ColumnStore reads autoincrement/compression in it)")
		} else {
			def += " COMMENT " + dbhelper.QuoteMySQLString(cm)
		}
	}
	for i, n := range notes {
		notes[i] = c.Name + ": " + n
	}
	return def, notes, nil
}

// columnstoreTableDDL builds the ColumnStore CREATE TABLE of schema.name from the columns,
// checks and options of a table; indexes and foreignKeys are only reported as removed. An
// error when a column cannot be held: the table then stays as it is.
func columnstoreTableDDL(schema, name string, cols []dbhelper.TableColumn, checks []dbhelper.TableCheck, opts dbhelper.TableOptions, indexes, foreignKeys []string) (columnstoreConversion, error) {
	var conv columnstoreConversion
	if len(cols) == 0 {
		return conv, fmt.Errorf("no column")
	}
	table, err := dbhelper.QuoteMySQLTableIdentifier(schema + "." + name)
	if err != nil {
		return conv, err
	}
	var lines []string
	for _, c := range cols {
		def, notes, err := columnstoreColumnDefinition(c)
		if err != nil {
			return conv, fmt.Errorf("column %s: %w", c.Name, err)
		}
		lines = append(lines, "  "+def)
		conv.Changes = append(conv.Changes, notes...)
	}
	for _, ck := range checks {
		lines = append(lines, "  CONSTRAINT "+dbhelper.QuoteMySQLIdentifier(ck.Name)+" CHECK ("+ck.Clause+")")
	}
	if len(indexes) > 0 {
		conv.Changes = append(conv.Changes, "indexes removed: "+strings.Join(indexes, ", "))
	}
	if len(foreignKeys) > 0 {
		conv.Changes = append(conv.Changes, "foreign keys removed: "+strings.Join(foreignKeys, ", "))
	}
	if opts.Partitioned {
		conv.Changes = append(conv.Changes, "partitioning removed")
	}
	ddl := "CREATE TABLE " + table + " (\n" + strings.Join(lines, ",\n") + "\n) ENGINE=Columnstore"
	if opts.Charset != "" {
		ddl += " DEFAULT CHARSET=" + opts.Charset
		if opts.Collation != "" {
			ddl += " COLLATE=" + opts.Collation
		}
	}
	if opts.Comment != "" {
		if columnstoreCommentRe.MatchString(opts.Comment) {
			conv.Changes = append(conv.Changes, "table comment removed (ColumnStore reads autoincrement/compression in it)")
		} else {
			ddl += " COMMENT=" + dbhelper.QuoteMySQLString(opts.Comment)
		}
	}
	conv.DDL = ddl
	return conv, nil
}

// convertTableToColumnstore puts a ColumnStore table in the place of schema.table, an InnoDB
// table the restore just created, on the restore's connection: the ColumnStore table is
// created as table__mcs, then the InnoDB one dropped and table__mcs renamed. A table that is
// not InnoDB, or that ColumnStore cannot hold, stays as it is with a warning: the restore
// goes on. Only an error after the InnoDB table was dropped is returned.
func (server *ServerMonitor) convertTableToColumnstore(ctx context.Context, conn *sqlx.Conn, schema, table string) error {
	cluster := server.ClusterGroup
	name := schema + "." + table
	logf := func(level, format string, args ...any) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, level, format, args...)
	}
	keep := func(format string, args ...any) error {
		logf(config.LvlWarn, "ColumnStore conversion of %s on %s skipped, the table stays as it is: %s", name, server.URL, fmt.Sprintf(format, args...))
		return nil
	}
	db, ver := server.Conn, server.DBVersion
	opts, _, err := dbhelper.GetTableOptions(db, ver, schema, table)
	if err != nil {
		return keep("%s", err)
	}
	if !strings.EqualFold(opts.Engine, "InnoDB") {
		logf(config.LvlInfo, "ColumnStore conversion of %s on %s: engine %s, left as it is", name, server.URL, opts.Engine)
		return nil
	}
	cols, _, err := dbhelper.GetTableColumns(db, ver, schema, table)
	if err != nil {
		return keep("%s", err)
	}
	checks, _, err := dbhelper.GetTableChecks(db, ver, schema, table)
	if err != nil {
		return keep("%s", err)
	}
	indexes, _, err := dbhelper.GetTableIndexNames(db, ver, schema, table)
	if err != nil {
		return keep("%s", err)
	}
	fks, _, err := dbhelper.GetTableForeignKeyNames(db, ver, schema, table)
	if err != nil {
		return keep("%s", err)
	}
	if len(table)+len(columnstoreStagingSuffix) > 64 {
		return keep("the name leaves no room for the suffix %s", columnstoreStagingSuffix)
	}
	staging := table + columnstoreStagingSuffix
	conv, err := columnstoreTableDDL(schema, staging, cols, checks, opts, indexes, fks)
	if err != nil {
		return keep("%s", err)
	}
	logSQL := func(query string, err error) {
		cluster.LogSQL(query, err, server.URL, "ColumnstoreConversion", config.LvlErr, "ColumnStore conversion of %s on %s: %s", name, server.URL, err)
	}
	q, err := dbhelper.CreateTableFromDDL(ctx, conn, conv.DDL)
	logSQL(q, err)
	if err != nil {
		if q, derr := dbhelper.DropTableConn(ctx, conn, schema, staging); derr == nil {
			logSQL(q, nil)
		}
		return keep("%s", err)
	}
	q, err = dbhelper.DropTableConn(ctx, conn, schema, table)
	logSQL(q, err)
	if err != nil {
		q, _ := dbhelper.DropTableConn(ctx, conn, schema, staging)
		logSQL(q, nil)
		return keep("%s", err)
	}
	q, err = dbhelper.RenameTableConn(ctx, conn, schema, staging, table)
	logSQL(q, err)
	if err != nil {
		return fmt.Errorf("ColumnStore conversion of %s: the InnoDB table is dropped and %s.%s could not take its name: %w", name, schema, staging, err)
	}
	if len(conv.Changes) == 0 {
		logf(config.LvlInfo, "ColumnStore conversion of %s on %s: done, no change to its columns", name, server.URL)
	} else {
		logf(config.LvlInfo, "ColumnStore conversion of %s on %s: done; %s", name, server.URL, strings.Join(conv.Changes, "; "))
	}
	return nil
}
