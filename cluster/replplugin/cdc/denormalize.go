// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
	"strings"

	"github.com/go-mysql-org/go-mysql/client"
	gmysql "github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The denormalize mode (CDC_CORE.md "Denormalize"). An index is a denormalizing query run on
// the source and the denormalized table it fills on the target. Its source tables and their
// primary keys come from the query's result: the original table and column of each result
// column (the result metadata of the query run with LIMIT 0) and information_schema. The
// denormalized table carries the primary key columns of every source table; together they are
// its primary key (inner joins: none of them is NULL), and each source table's is indexed.
//
//   - Init: the denormalized table made from the description of the empty query result, then
//     filled from the query on a consistent snapshot of the source.
//   - Capture: per source table, the primary keys touched, nothing else: the
//     micro_checkpoint_pack. A deleted key is kept as deleted; inserted again it is back to
//     replace.
//   - Unpack: the denormalized rows whose source-table primary key columns hold a packed key are
//     deleted, and the query restricted to those keys gives the rows to write: the micro
//     checkpoint, applied as any checkpoint (to the destination, or the accumulation).
//
// The unpack reads the source as it is when it runs, past the pack: a change after the pack is
// read now and again at its own pack, which gives the same rows. That is also why the init
// needs no GTID: packs captured before it was done unpack after it on the current source.

const (
	defaultMaxKeysPerBatch = 10000
	// maxArgsPerQuery: the placeholders of one restricted query, under MariaDB's 65535
	maxArgsPerQuery       = 60000
	initRowsPerCheckpoint = 10000
)

// sourceTable is a source table of an index: its primary key, and the result columns that hold it.
type sourceTable struct {
	schema, table string
	pk            []string
	cols          []string
}

// indexMeta is an index resolved against the source.
type indexMeta struct {
	idx     wire.Index
	tables  []sourceTable
	key     []string // the denormalized table's primary key: every source table's cols
	columns []string // the result's columns, in order
	keyPos  []int    // the positions of key in columns
}

func (m *indexMeta) maxKeys() int {
	if m.idx.MaxKeysPerBatch > 0 {
		return m.idx.MaxKeysPerBatch
	}
	return defaultMaxKeysPerBatch
}

// lookups are the source tables' key columns, the denormalized table's indexes.
func (m *indexMeta) lookups() [][]string {
	out := make([][]string, len(m.tables))
	for i, t := range m.tables {
		out[i] = t.cols
	}
	return out
}

// indexID is an index's name and a checksum of its query and denormalized table: a change of
// either is a new index, its init runs again.
func indexID(idx wire.Index) string {
	return fmt.Sprintf("%s@%08x", idx.Name, crc32.ChecksumIEEE([]byte(idx.SQL+"\x00"+idx.Schema+"\x00"+idx.Table)))
}

// resolveIndexes resolves the indexes of cfg on the source and returns the source tables they
// read, as schema.table.
func resolveIndexes(ctx context.Context, cfg wire.Config, src *sqlx.DB) ([]*indexMeta, map[string]bool, error) {
	var metas []*indexMeta
	tables := map[string]bool{}
	names := map[string]bool{}
	for _, idx := range cfg.Indexes {
		if names[idx.Name] {
			return nil, nil, fmt.Errorf("index %s: two indexes of the same name", idx.Name)
		}
		names[idx.Name] = true
		m, err := resolveIndex(ctx, cfg, src, idx)
		if err != nil {
			return nil, nil, err
		}
		for _, t := range m.tables {
			tables[t.schema+"."+t.table] = true
		}
		metas = append(metas, m)
	}
	return metas, tables, nil
}

// resolveIndex derives an index's source tables and keys from the query's result metadata on
// the source.
func resolveIndex(ctx context.Context, cfg wire.Config, src *sqlx.DB, idx wire.Index) (*indexMeta, error) {
	if idx.Name == "" || idx.SQL == "" || idx.Schema == "" || idx.Table == "" {
		return nil, fmt.Errorf("index %q: name, sql, slave_schema and slave_table are required", idx.Name)
	}
	conn, err := client.Connect(fmt.Sprintf("%s:%d", cfg.Source.Host, cfg.Source.Port), cfg.Source.User, os.Getenv(wire.EnvSourcePassword), "")
	if err != nil {
		return nil, fmt.Errorf("index %s: source: %w", idx.Name, err)
	}
	defer conn.Close()
	// the query itself: a derived table would hide the original tables of its columns
	res, err := conn.Execute(strings.TrimRight(strings.TrimSpace(idx.SQL), ";") + " LIMIT 0")
	if err != nil {
		return nil, fmt.Errorf("index %s: the denormalizing query with LIMIT 0 (it takes no LIMIT of its own): %w", idx.Name, err)
	}
	defer res.Close()
	return indexFromFields(ctx, src, idx, fieldsOf(res.Fields))
}

// field is what an index reads of a result column: its name, and its original table and column.
type field struct{ name, schema, orgTable, orgName string }

func fieldsOf(fs []*gmysql.Field) []field {
	out := make([]field, len(fs))
	for i, f := range fs {
		out[i] = field{name: string(f.Name), schema: string(f.Schema), orgTable: string(f.OrgTable), orgName: string(f.OrgName)}
	}
	return out
}

// primaryKeyOf reads a source table's primary key; a variable for the tests.
var primaryKeyOf = func(ctx context.Context, src *sqlx.DB, schema, table string) ([]string, error) {
	pk, q, err := dbhelper.GetPrimaryKeyColumns(ctx, src, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, q)
	}
	return pk, nil
}

// indexFromFields builds an index from its query's result columns; refused when a source
// table's primary key is not in the result, two columns share a name, or a table is read twice
// (joined twice: its keys could not be told apart).
func indexFromFields(ctx context.Context, src *sqlx.DB, idx wire.Index, fields []field) (*indexMeta, error) {
	m := &indexMeta{idx: idx}
	type origin struct{ schema, table string }
	byTable := map[origin]map[string]string{} // original column -> result column
	var order []origin
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.name] {
			return nil, fmt.Errorf("index %s: two result columns named %s: give each its own alias", idx.Name, f.name)
		}
		seen[f.name] = true
		m.columns = append(m.columns, f.name)
		if f.orgTable == "" {
			continue // an expression
		}
		o := origin{f.schema, f.orgTable}
		if _, ok := byTable[o]; !ok {
			byTable[o] = map[string]string{}
			order = append(order, o)
		}
		if prev, dup := byTable[o][f.orgName]; dup {
			return nil, fmt.Errorf("index %s: %s.%s.%s is returned twice (%s, %s): a table joined twice is not supported", idx.Name, o.schema, o.table, f.orgName, prev, f.name)
		}
		byTable[o][f.orgName] = f.name
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("index %s: the query reads no table", idx.Name)
	}
	for _, o := range order {
		if o.schema == "" {
			return nil, fmt.Errorf("index %s: table %s: write its schema in the query", idx.Name, o.table)
		}
		pk, err := primaryKeyOf(ctx, src, o.schema, o.table)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", idx.Name, err)
		}
		if len(pk) == 0 {
			return nil, fmt.Errorf("index %s: %s.%s has no primary key", idx.Name, o.schema, o.table)
		}
		t := sourceTable{schema: o.schema, table: o.table, pk: pk}
		for _, c := range pk {
			rc, ok := byTable[o][c]
			if !ok {
				return nil, fmt.Errorf("index %s: the query does not return %s.%s.%s, a primary key column of a table it reads: add it", idx.Name, o.schema, o.table, c)
			}
			t.cols = append(t.cols, rc)
			m.key = append(m.key, rc)
		}
		m.tables = append(m.tables, t)
	}
	for _, k := range m.key {
		for i, c := range m.columns {
			if c == k {
				m.keyPos = append(m.keyPos, i)
			}
		}
	}
	return m, nil
}

// packBuf is one source table's part of the open micro_checkpoint_pack.
type packBuf struct {
	t       *schema.Table
	replace map[string]row
	delete  map[string]row
}

func newPackBuf(t *schema.Table) *packBuf {
	return &packBuf{t: t, replace: map[string]row{}, delete: map[string]row{}}
}

func (b *packBuf) keyOf(r []interface{}) row {
	k := make(row, len(b.t.PKColumns))
	for i, p := range b.t.PKColumns {
		if p < len(r) {
			k[i] = tsvField(&b.t.Columns[p], r[p])
		}
	}
	return k
}

func (b *packBuf) touch(k row) {
	s := keyString(k)
	delete(b.delete, s)
	b.replace[s] = k
}

func (b *packBuf) remove(k row) {
	s := keyString(k)
	delete(b.replace, s)
	b.delete[s] = k
}

func (b *packBuf) empty() bool { return len(b.replace) == 0 && len(b.delete) == 0 }

// pack records the rows of an event of a source table: insert, update or delete, the
// update's before and after images in turn.
func (b *packBuf) pack(action string, rows [][]interface{}) {
	switch action {
	case "insert":
		for _, r := range rows {
			b.touch(b.keyOf(r))
		}
	case "delete":
		for _, r := range rows {
			b.remove(b.keyOf(r))
		}
	case "update":
		for i := 0; i+1 < len(rows); i += 2 {
			before, after := b.keyOf(rows[i]), b.keyOf(rows[i+1])
			if keyString(before) != keyString(after) {
				b.remove(before) // its primary key changed: the old key is gone
			}
			b.touch(after)
		}
	}
}

// renderPacks renders the open pack into a manifest's packs and their files.
func renderPacks(packs map[string]*packBuf) ([]wire.PackTable, map[string][]byte) {
	names := make([]string, 0, len(packs))
	for n := range packs {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []wire.PackTable
	files := map[string][]byte{}
	render := func(set map[string]row) []byte {
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf []byte
		for _, k := range keys {
			buf = wire.EncodeTSV(buf, set[k])
		}
		return buf
	}
	for i, n := range names {
		b := packs[n]
		if b.empty() {
			continue
		}
		cols := make([]string, len(b.t.PKColumns))
		for j, p := range b.t.PKColumns {
			cols[j] = b.t.Columns[p].Name
		}
		pt := wire.PackTable{Schema: b.t.Schema, Table: b.t.Name, KeyColumns: cols, Keys: int64(len(b.replace) + len(b.delete))}
		if len(b.replace) > 0 {
			pt.Replace = fmt.Sprintf("p%d.replace.tsv", i)
			files[pt.Replace] = render(b.replace)
		}
		if len(b.delete) > 0 {
			pt.Delete = fmt.Sprintf("p%d.delete.tsv", i)
			files[pt.Delete] = render(b.delete)
		}
		out = append(out, pt)
	}
	return out, files
}

// decodeKeys reads a pack file's keys.
func decodeKeys(b []byte) [][]interface{} {
	var out [][]interface{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		fields := wire.DecodeTSV([]byte(line))
		k := make([]interface{}, len(fields))
		for i, f := range fields {
			if f != nil {
				k[i] = f
			}
		}
		out = append(out, k)
	}
	return out
}

// keyBatches splits the packed keys of meta's source tables into the conditions of one
// restricted query each: max_keys_per_batch keys and maxArgsPerQuery values at most. Keys are
// split across batches, never dropped.
func keyBatches(meta *indexMeta, m wire.Manifest, files map[string][]byte) [][]dbhelper.KeyCondition {
	var batches [][]dbhelper.KeyCondition
	var cur []dbhelper.KeyCondition
	keys, args := 0, 0
	for _, t := range meta.tables {
		for _, p := range m.Packs {
			if !strings.EqualFold(p.Schema, t.schema) || !strings.EqualFold(p.Table, t.table) {
				continue
			}
			all := append(decodeKeys(files[p.Replace]), decodeKeys(files[p.Delete])...)
			for start := 0; start < len(all); {
				room := meta.maxKeys() - keys
				if r := (maxArgsPerQuery - args) / len(t.cols); r < room {
					room = r
				}
				if room <= 0 {
					batches = append(batches, cur)
					cur, keys, args = nil, 0, 0
					continue
				}
				end := start + room
				if end > len(all) {
					end = len(all)
				}
				cur = append(cur, dbhelper.KeyCondition{Columns: t.cols, Keys: all[start:end]})
				keys += end - start
				args += (end - start) * len(t.cols)
				start = end
			}
		}
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// unpack turns the micro_checkpoint_pack of m into the micro checkpoints of index meta, one per
// batch of keys, as the tables of a checkpoint and their files: the keys of the denormalized
// rows the packed keys touch (on the target, and in the accumulation when there is one), then
// the query restricted to the same keys on the source gives the rows to write, whose keys are
// deleted too (a micro checkpoint applied twice gives the same table).
func unpack(ctx context.Context, src *sqlx.DB, target *sqlx.Conn, meta *indexMeta, m wire.Manifest, files map[string][]byte, staging bool) ([]wire.TableFiles, map[string][]byte, error) {
	var tables []wire.TableFiles
	out := map[string][]byte{}
	for i, conds := range keyBatches(meta, m, files) {
		t, f, err := unpackBatch(ctx, src, target, meta, conds, staging, fmt.Sprintf("%s-%d-%d", meta.idx.Name, m.Seq, i))
		if err != nil {
			return nil, nil, err
		}
		tables = append(tables, t)
		for k, v := range f {
			out[k] = v
		}
	}
	return tables, out, nil
}

func unpackBatch(ctx context.Context, src *sqlx.DB, target *sqlx.Conn, meta *indexMeta, conds []dbhelper.KeyCondition, staging bool, name string) (wire.TableFiles, map[string][]byte, error) {
	seen := map[string]bool{}
	var dels []byte
	addKey := func(k []interface{}) {
		s := string(wire.EncodeTSV(nil, k))
		if !seen[s] {
			seen[s] = true
			dels = wire.EncodeTSV(dels, k)
		}
	}
	type place struct{ schema, table string }
	where := []place{{meta.idx.Schema, meta.idx.Table}}
	if staging {
		rows, _ := StagingNames(meta.idx.Schema, meta.idx.Table)
		where = append(where, place{StagingSchema, rows})
	}
	for _, c := range conds {
		for _, w := range where {
			keys, q, err := dbhelper.SelectKeysWhere(ctx, target, w.schema, w.table, meta.key, c)
			if err != nil {
				if w.schema == StagingSchema && isNoSuchTable(err) {
					continue // nothing accumulated for it yet
				}
				return wire.TableFiles{}, nil, fmt.Errorf("index %s: the rows touched: %w (%s)", meta.idx.Name, err, q)
			}
			for _, k := range keys {
				addKey(k)
			}
		}
	}
	query, args, err := dbhelper.RestrictedQuery(meta.idx.SQL, conds)
	if err != nil {
		return wire.TableFiles{}, nil, err
	}
	rows, err := src.QueryContext(ctx, query, args...)
	if err != nil {
		return wire.TableFiles{}, nil, fmt.Errorf("index %s: the denormalizing query restricted to the packed keys: %w", meta.idx.Name, err)
	}
	defer rows.Close()
	ins, n, err := scanRows(rows, meta, func(r, k []interface{}, out []byte) []byte {
		addKey(k)
		return wire.EncodeTSV(out, r)
	}, 0, nil)
	if err != nil {
		return wire.TableFiles{}, nil, fmt.Errorf("index %s: %w", meta.idx.Name, err)
	}
	t := wire.TableFiles{Schema: meta.idx.Schema, Table: meta.idx.Table, KeyColumns: meta.key, DeleteRows: int64(len(seen)), InsertRows: n}
	files := map[string][]byte{}
	if len(dels) > 0 {
		t.Deletes = "d-" + name + ".deletes.tsv"
		files[t.Deletes] = dels
	}
	if len(ins) > 0 {
		t.Inserts = "d-" + name + ".inserts.tsv"
		files[t.Inserts] = ins
	}
	return t, files, nil
}

// scanRows reads rows, gives each row and its key to fn, which appends to the buffer; every
// flushAt rows (0: never) flush is given the buffer and its row count.
func scanRows(rows *sql.Rows, meta *indexMeta, fn func(r, k []interface{}, out []byte) []byte, flushAt int64, flush func([]byte, int64) error) ([]byte, int64, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var out []byte
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, n, err
		}
		r := make([]interface{}, len(vals))
		for i, v := range vals {
			if v != nil {
				r[i] = append([]byte(nil), v...)
			}
		}
		k := make([]interface{}, len(meta.keyPos))
		for i, p := range meta.keyPos {
			k[i] = r[p]
		}
		out = fn(r, k, out)
		if n++; flushAt > 0 && n%flushAt == 0 {
			if err := flush(out, flushAt); err != nil {
				return nil, n, err
			}
			out = nil
		}
	}
	return out, n, rows.Err()
}

// initIndex fills an index's denormalized table from the query on a consistent snapshot of the
// source, applied by apply in parts of initRowsPerCheckpoint rows. The table is made first from
// the description of the empty query result, the destination then makes it its own; an
// existing table of the same columns is emptied instead (Truncater, or a DELETE).
func initIndex(ctx context.Context, src, tdb *sqlx.DB, tconn *sqlx.Conn, dest Destination, meta *indexMeta, apply func(wire.TableFiles, map[string][]byte) error) error {
	if _, err := emptyOrMake(ctx, tdb, tconn, dest, meta.idx.Schema, meta.idx.Table, meta.columns, func([]string) (string, error) {
		return dbhelper.CreateDenormalizedTable(ctx, tconn, meta.idx.Schema, meta.idx.Table, meta.idx.SQL, meta.key, meta.lookups())
	}); err != nil {
		return fmt.Errorf("index %s: the denormalized table %s.%s: %w", meta.idx.Name, meta.idx.Schema, meta.idx.Table, err)
	}
	sconn, err := src.Connx(ctx)
	if err != nil {
		return err
	}
	defer sconn.Close()
	if _, err := sconn.ExecContext(ctx, "START TRANSACTION WITH CONSISTENT SNAPSHOT"); err != nil {
		return err
	}
	defer sconn.ExecContext(context.Background(), "ROLLBACK")
	rows, err := sconn.QueryContext(ctx, "SELECT * FROM ("+meta.idx.SQL+") AS d")
	if err != nil {
		return fmt.Errorf("index %s: the denormalizing query: %w", meta.idx.Name, err)
	}
	defer rows.Close()
	part := 0
	write := func(b []byte, n int64) error {
		part++
		t := wire.TableFiles{Schema: meta.idx.Schema, Table: meta.idx.Table, KeyColumns: meta.key, Inserts: fmt.Sprintf("init-%s-%d.inserts.tsv", meta.idx.Name, part), InsertRows: n}
		return apply(t, map[string][]byte{t.Inserts: b})
	}
	rest, n, err := scanRows(rows, meta, func(r, _ []interface{}, out []byte) []byte {
		return wire.EncodeTSV(out, r)
	}, initRowsPerCheckpoint, write)
	if err != nil {
		return fmt.Errorf("index %s: the denormalizing query: %w", meta.idx.Name, err)
	}
	if len(rest) > 0 {
		return write(rest, n%initRowsPerCheckpoint)
	}
	return nil
}

// isMadeByRestore: a destination whose tables the seed's restore makes (ColumnStore) refuses
// Create; its denormalized table stays InnoDB.
func isMadeByRestore(err error) bool {
	return err != nil && strings.Contains(err.Error(), "made by the seed's restore")
}

// isNoSuchTable: MariaDB error 1146.
func isNoSuchTable(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1146
}
