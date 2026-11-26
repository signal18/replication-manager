// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The accumulation (CDC_CORE.md): with a merge interval, each checkpoint goes at once to an
// InnoDB staging on the target, per carried table a rows table (the rows to put, by key: a
// later change of a key replaces the earlier one) and a keys table (every key deleted or
// replaced), in one InnoDB transaction: a checkpoint staged twice gives the same staging.
// Every merge interval the staging is merged into the destination in bulk, per table in one
// destination transaction: the staged keys deleted in batches of explicit keys (a
// cross-engine DELETE ... IN (SELECT ...) of the staging crashed a columnar engine's server),
// the staged rows inserted; only then the staging is purged, in one InnoDB transaction.
// There is no transaction across the two engines: a merge stopped anywhere is run again whole
// and gives the same table (the keys deleted again, the same rows inserted again).

const (
	// StagingSchema holds the staging tables on the target
	StagingSchema = "replication_manager_staging"
	// mergeKeysBatch: the keys of one DELETE of a merge
	mergeKeysBatch = 1000
)

// merged is the last merge of the staging, <name>.merged.json.
type merged struct {
	Seq    int64     `json:"seq"`
	GTID   string    `json:"gtid"`
	At     time.Time `json:"at"`
	TookMs int64     `json:"tookMs"`
}

// StagingNames are the rows and keys tables of schema.table in the staging: <schema>__<table>
// with __r and __d, the base shortened with its checksum when a name would pass 64 characters.
func StagingNames(schema, table string) (rows, keys string) {
	base := schema + "__" + table
	if len(base) > 61 {
		base = fmt.Sprintf("%s_%08x", base[:52], crc32.ChecksumIEEE([]byte(schema+"."+table)))
	}
	return base + "__r", base + "__d"
}

// stage applies checkpoint m to the staging on conn: the staging tables created when missing
// (a DDL, before the transaction), then in one transaction each table's keys deleted from its
// rows table and added to its keys table, its rows loaded.
func stage(ctx context.Context, conn *sqlx.Conn, dest Destination, m wire.Manifest, files map[string][]byte, name func(wire.TableFiles) string) error {
	for _, t := range m.Tables {
		rows, keys := StagingNames(t.Schema, t.Table)
		if q, err := dbhelper.CreateStagingTables(ctx, conn, nil, StagingSchema, rows, keys, t.Schema, t.Table, t.KeyColumns); err != nil {
			return fmt.Errorf("staging of %s.%s: %w (%s)", t.Schema, t.Table, err, q)
		}
	}
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return err
	}
	for _, t := range m.Tables {
		rows, keys := StagingNames(t.Schema, t.Table)
		addKeys := func(batch [][]interface{}) error {
			_, err := dbhelper.InsertIgnoreKeys(ctx, conn, StagingSchema, keys, t.KeyColumns, batch)
			return err
		}
		if _, err := dest.Apply(ctx, conn, StagingSchema, rows, t, files, name(t), addKeys); err != nil {
			rollbackFresh(conn)
			return err
		}
	}
	_, err := conn.ExecContext(ctx, "COMMIT")
	return err
}

// rollbackFresh rolls back with a context of its own: the caller's may be the cancelled one.
func rollbackFresh(conn *sqlx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn.ExecContext(ctx, "ROLLBACK")
}

// stagedTables are the carried tables and those the staging holds (its rows tables name
// their table in their comment), each once.
func stagedTables(ctx context.Context, reader *sqlx.Conn, carried []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, t := range carried {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	comments, _, err := dbhelper.GetTableCommentsBySuffix(ctx, reader, StagingSchema, "__r")
	if err != nil {
		return nil, err
	}
	for _, cm := range comments {
		if t, ok := strings.CutPrefix(cm, stagingCommentPrefix); ok && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

// stagingCommentPrefix starts the comment of a rows table: the table it stages follows
const stagingCommentPrefix = "replication-manager staging of "

func readMerged(path string) (*merged, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m merged
	return &m, json.Unmarshal(b, &m)
}

func writeMerged(path string, m merged) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mergeAll merges the staging of every carried table into the destination: conn the
// destination's session, reader a second connection reading the staged keys.
func mergeAll(ctx context.Context, conn, reader *sqlx.Conn, tables []string) error {
	for _, name := range tables {
		schema, table, ok := strings.Cut(name, ".")
		if !ok {
			continue
		}
		if err := mergeTable(ctx, conn, reader, schema, table); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func mergeTable(ctx context.Context, conn, reader *sqlx.Conn, schema, table string) error {
	rows, keys := StagingNames(schema, table)
	keyColumns, _, err := dbhelper.GetColumnNamesConn(ctx, reader, StagingSchema, keys)
	if err != nil {
		return err
	}
	if len(keyColumns) == 0 {
		return nil // nothing staged for it yet
	}
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return err
	}
	rollback := func(err error) error {
		rollbackFresh(conn)
		return err
	}
	if q, err := dbhelper.ForEachKeyBatch(ctx, reader, StagingSchema, keys, keyColumns, mergeKeysBatch, func(batch [][]interface{}) error {
		_, err := dbhelper.DeleteRowsByKey(ctx, conn, schema, table, keyColumns, batch)
		return err
	}); err != nil {
		return rollback(fmt.Errorf("delete of the staged keys: %w (%s)", err, q))
	}
	// the columns both tables have: the destination changed by hand after a DDL on the source
	// (WARN0341) keeps merging, a column it lacks is left out, one the staging lacks takes its
	// default
	cols, _, err := dbhelper.GetCommonColumnNames(ctx, reader, StagingSchema, rows, schema, table)
	if err != nil {
		return rollback(err)
	}
	if q, err := dbhelper.InsertSelectColumns(ctx, conn, schema, table, StagingSchema, rows, cols); err != nil {
		return rollback(fmt.Errorf("insert of the staged rows: %w (%s)", err, q))
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	// the destination holds the staging: purged in one transaction (a rows table emptied
	// without its keys table would delete those rows at the next merge)
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return err
	}
	for _, t := range []string{keys, rows} {
		if _, err := dbhelper.DeleteAllRows(ctx, conn, StagingSchema, t); err != nil {
			return rollback(err)
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
