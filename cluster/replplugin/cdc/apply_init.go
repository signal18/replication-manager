// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The apply of the elements of the stream that are not the delta (CDC_CORE.md "The stream"):
// the -schema element makes or empties a destination table, the init's parts load it (by
// partition, on the pool), its last part records the init's GTID; a DDL element goes to the
// destination, or pauses the indexes it touches. All of it comes through the transport: the
// apply knows the source only by what the elements carry, and its own configuration (which
// index, where its tables go, how its destination is partitioned).

// initPartDeadline bounds the apply of one partition of an init part.
const initPartDeadline = 10 * time.Minute

// DDLMapper is a destination that maps a source DDL on a table of an index to its own DDL on
// the destination table; without it, the index is paused with the DDL as the reason.
type DDLMapper interface {
	MapDDL(ctx context.Context, conn *sqlx.Conn, ddl wire.DDLElement, schema, table string) error
}

// indexByID is the index of the apply's config of id; false when the config no longer has it.
func (a *applier) indexByID(id string) (wire.Index, bool) {
	for _, idx := range a.cfg.Indexes {
		if indexID(idx) == id {
			return idx, true
		}
	}
	return wire.Index{}, false
}

// copiesOf are the table-mode destinations of a source table (several indexes may copy it).
func (a *applier) copiesOf(schema, table string) []copyDest {
	var out []copyDest
	for _, d := range a.copies {
		if strings.EqualFold(d.srcSchema, schema) && strings.EqualFold(d.srcTable, table) {
			out = append(out, d)
		}
	}
	return out
}

// partitioned is the partitioned destination table schema.table of idx, nil when idx is not
// partitioned; read on the target once, then kept.
func (a *applier) partitioned(ctx context.Context, conn *sqlx.Conn, idx wire.Index, schema, table string) (*partitionedTable, error) {
	p, err := newPartitioning(idx)
	if err != nil || p == nil {
		return nil, err
	}
	k := strings.ToLower(schema + "." + table)
	if pt, ok := a.parts[k]; ok {
		return pt, nil
	}
	cols, q, err := dbhelper.GetColumnNamesConn(ctx, conn, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, q)
	}
	pos := slices.IndexFunc(cols, func(c string) bool { return strings.EqualFold(c, p.column) })
	if pos < 0 {
		return nil, fmt.Errorf("%s.%s has no column %s to partition by", schema, table, p.column)
	}
	pt, err := openPartitioned(ctx, conn, p, schema, table, pos)
	if err != nil {
		return nil, err
	}
	if a.parts == nil {
		a.parts = map[string]*partitionedTable{}
	}
	a.parts[k] = pt
	return pt, nil
}

// applySchema makes the destination table of a -schema element, or empties it: a table of
// the same columns is emptied; a denormalized table of other columns (its query changed) is
// dropped and made again; a table-mode destination of other columns is refused, never
// dropped. The init it starts is no longer done.
func (a *applier) applySchema(ctx context.Context, db *sqlx.DB, e wire.SchemaElement, done *wire.Applied) error {
	idx, ok := a.indexByID(e.Index)
	if !ok {
		return nil // an index no longer configured: its stream is skipped
	}
	conn, err := a.session(ctx, db)
	if err != nil {
		return err
	}
	defer conn.Close()
	if why := a.refuseEngine(idx); why != "" {
		return fmt.Errorf("index %s: %s", idx.Name, why)
	}
	cols := make([]dbhelper.ColumnDef, len(e.Columns))
	names := make([]string, len(e.Columns))
	for i, c := range e.Columns {
		cols[i] = dbhelper.ColumnDef{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Charset: c.Charset, Collation: c.Collation}
		names[i] = c.Name
	}
	p, err := newPartitioning(idx)
	if err != nil {
		return err
	}
	q, err := emptyOrMake(ctx, db, conn, a.dest, e.Schema, e.Table, names, func(existing []string) (string, error) {
		if len(existing) > 0 {
			if e.Mode == wire.IndexModeTable {
				return "", fmt.Errorf("%s.%s exists on the target with other columns (%s): it is never dropped, rename or drop it", e.Schema, e.Table, strings.Join(existing, ","))
			}
			if q, err := dbhelper.DropTableConn(ctx, conn, e.Schema, e.Table); err != nil {
				return q, err
			}
		}
		q, err := dbhelper.CreateTableFromColumnsIndexed(ctx, conn, e.Schema, e.Table, cols, e.Key, e.Indexes)
		if err != nil || p == nil {
			return q, err
		}
		pq, err := partitionTable(ctx, conn, p, e.Schema, e.Table, e.PartitionFrom)
		return q + "; " + pq, err
	})
	if err != nil {
		return fmt.Errorf("index %s: the destination %s.%s: %w (%s)", idx.Name, e.Schema, e.Table, err, q)
	}
	delete(a.parts, strings.ToLower(e.Schema+"."+e.Table))
	key := wire.InitKey(e.Index, e.Schema, e.Table)
	delete(done.Inits, key)
	delete(done.Paused, e.Index)
	done.Indexes = slices.DeleteFunc(done.Indexes, func(id string) bool { return id == e.Index })
	return nil
}

// refuseEngine: this destination takes the index's target_engine, or says why not.
func (a *applier) refuseEngine(idx wire.Index) string {
	if r, ok := a.dest.(EngineRefuser); ok {
		return r.RefuseEngine(idx.TargetEngineName())
	}
	return ""
}

// EngineRefuser is a destination that says which target_engine of an index it takes ("" it
// does).
type EngineRefuser interface {
	RefuseEngine(engine string) string
}

// applyInitPart loads one part of an init: its rows split by partition, each partition's
// rows (their keys deleted first: a part applied twice gives the same table) on a worker of
// the pool; the last part records the init's GTID.
func (a *applier) applyInitPart(ctx context.Context, db *sqlx.DB, m wire.Manifest, files map[string][]byte, done *wire.Applied) error {
	in := m.Init
	idx, ok := a.indexByID(in.Index)
	if !ok || done.Paused[in.Index] != "" {
		return nil
	}
	if len(m.Tables) > 0 {
		t := m.Tables[0]
		conn, err := a.session(ctx, db)
		if err != nil {
			return err
		}
		pt, err := a.partitioned(ctx, conn, idx, t.Schema, t.Table)
		if err == nil && pt != nil {
			var q string
			top, ok := pt.latest(files[t.Inserts])
			if q, err = pt.ensure(ctx, conn, time.Now().UTC(), top, ok); err != nil {
				err = fmt.Errorf("%w (%s)", err, q)
			}
		}
		conn.Close()
		if err != nil {
			return fmt.Errorf("index %s: %w", idx.Name, err)
		}
		groups, skipped := splitByPartition(files[t.Inserts], files[t.Deletes], pt, time.Now().UTC())
		a.countPurgeSkipped(skipped)
		jobs := map[string]func(context.Context) error{}
		for name, g := range groups {
			g := g
			jobs[name] = func(ctx context.Context) error {
				c, err := a.session(ctx, db)
				if err != nil {
					return err
				}
				defer c.Close()
				tf := wire.TableFiles{Schema: t.Schema, Table: t.Table, KeyColumns: t.KeyColumns, Deletes: "d", Inserts: "i", DeleteRows: g.n, InsertRows: g.n}
				q, err := a.dest.Apply(ctx, c, t.Schema, t.Table, tf, map[string][]byte{"d": g.keys, "i": g.rows}, fmt.Sprintf("replplugin-%d-%d-%s", os.Getpid(), m.Seq, name), nil)
				if err != nil {
					return fmt.Errorf("%w (%s)", err, q)
				}
				return nil
			}
		}
		if err := partitionErrors(runPartitions(ctx, a.cfg.Threads, initPartDeadline, jobs)); err != nil {
			return fmt.Errorf("index %s: init part %d of %s.%s: %w", idx.Name, in.Part, t.Schema, t.Table, err)
		}
	}
	if in.Last {
		if done.Inits == nil {
			done.Inits = map[string]string{}
		}
		done.Inits[wire.InitKey(in.Index, in.Schema, in.Table)] = in.GTID
		if !idx.TableMode() && !slices.Contains(done.Indexes, in.Index) {
			done.Indexes = append(done.Indexes, in.Index)
		}
	}
	return nil
}

// partRows is the rows of one partition of a part, and their keys.
type partRows struct {
	rows, keys []byte
	n          int64
}

// splitByPartition splits a part's rows and keys (line i of one is line i of the other) by
// the partition of each row; one group "" when the table is not partitioned. A row older than the
// purge horizon is left out and counted: it would land in the first partition.
func splitByPartition(rows, keys []byte, pt *partitionedTable, now time.Time) (map[string]*partRows, int64) {
	out := map[string]*partRows{}
	if pt == nil {
		if len(rows) > 0 {
			out[""] = &partRows{rows: rows, keys: keys, n: int64(bytes.Count(rows, []byte{'\n'}))}
		}
		return out, 0
	}
	var skipped int64
	for len(rows) > 0 {
		var r, k []byte
		r, rows = cutLine(rows)
		k, keys = cutLine(keys)
		fields := wire.DecodeTSV(r)
		var v interface{}
		if pt.pos < len(fields) && fields[pt.pos] != nil {
			v = fields[pt.pos]
		}
		if pt.p.purged(v, now) {
			skipped++
			continue
		}
		name := pt.p.route(v, pt.first)
		g, ok := out[name]
		if !ok {
			g = &partRows{}
			out[name] = g
		}
		g.rows = append(append(g.rows, r...), '\n')
		g.keys = append(append(g.keys, k...), '\n')
		g.n++
	}
	return out, skipped
}

// withoutPurged is rows (a TSV file) without the rows older than the purge horizon, and their
// count.
func withoutPurged(rows []byte, pt *partitionedTable, now time.Time) ([]byte, int64) {
	if pt == nil {
		return rows, 0
	}
	if _, on := pt.p.keptFrom(now); !on {
		return rows, 0
	}
	var out []byte
	var skipped int64
	for len(rows) > 0 {
		var r []byte
		r, rows = cutLine(rows)
		if len(r) == 0 {
			continue
		}
		fields := wire.DecodeTSV(r)
		var v interface{}
		if pt.pos < len(fields) && fields[pt.pos] != nil {
			v = fields[pt.pos]
		}
		if pt.p.purged(v, now) {
			skipped++
			continue
		}
		out = append(append(out, r...), '\n')
	}
	return out, skipped
}

// countPurgeSkipped counts rows left out past the purge horizon.
func (a *applier) countPurgeSkipped(n int64) {
	if n == 0 {
		return
	}
	a.mu.Lock()
	a.purgeSkipped += n
	a.mu.Unlock()
}

// ranOnTarget records a statement the apply ran on the target that removed data (F11): the
// status carries it, replication-manager logs it.
func (a *applier) ranOnTarget(q string, dropped int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropped += int64(dropped)
	a.targetTotal++
	a.targetStmts = append(a.targetStmts, q)
	if over := len(a.targetStmts) - wire.MaxSourceStatements; over > 0 {
		a.targetStmts = append([]string(nil), a.targetStmts[over:]...)
	}
}

func cutLine(b []byte) ([]byte, []byte) {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[:i], b[i+1:]
	}
	return b, nil
}

// applyDDL hands a DDL element to the destination for each index it touches; an index whose
// destination cannot map it is paused with the DDL as the reason (F7: a state, never a guess).
func (a *applier) applyDDL(ctx context.Context, db *sqlx.DB, e wire.DDLElement, done *wire.Applied) error {
	touched := func(schema, table string) bool {
		return slices.ContainsFunc(e.Tables, func(t string) bool { return strings.EqualFold(t, schema+"."+table) })
	}
	pause := func(id, name, schema, table string, why error) {
		if done.Paused == nil {
			done.Paused = map[string]string{}
		}
		reason := fmt.Sprintf("index %s paused: the DDL %q on the source is not mapped to %s.%s", name, truncate(e.Statement, 200), schema, table)
		if why != nil {
			reason += ": " + why.Error()
		}
		done.Paused[id] = reason + "; rebuild the index"
	}
	apply := func(id, name, schema, table string) {
		if done.Paused[id] != "" {
			return
		}
		mapper, ok := a.dest.(DDLMapper)
		if !ok {
			pause(id, name, schema, table, nil)
			return
		}
		conn, err := a.session(ctx, db)
		if err == nil {
			err = mapper.MapDDL(ctx, conn, e, schema, table)
			conn.Close()
		}
		if err != nil {
			pause(id, name, schema, table, err)
		}
		delete(a.parts, strings.ToLower(schema+"."+table))
	}
	for _, d := range a.copies {
		if touched(d.srcSchema, d.srcTable) {
			apply(d.id, d.idx.Name, d.destSchema, d.destTable)
		}
	}
	for _, m := range a.indexes {
		for _, t := range m.tables {
			if touched(t.schema, t.table) {
				apply(indexID(m.idx), m.idx.Name, m.idx.Schema, m.idx.Table)
				break
			}
		}
	}
	return nil
}

// routeDelta turns the delta tables of a checkpoint into the destination's: a table-mode
// source table goes to its destination once its init is done and when the checkpoint is past
// the init's GTID (the rows of a checkpoint within it are in the init already); a table of the
// rules stays as it is.
func (a *applier) routeDelta(m wire.Manifest, done *wire.Applied) []wire.TableFiles {
	var out []wire.TableFiles
	for _, t := range m.Tables {
		copies := a.copiesOf(t.Schema, t.Table)
		if len(copies) == 0 || a.cfg.Matches(t.Schema, t.Table) {
			out = append(out, t)
		}
		for _, d := range copies {
			if done.Paused[d.id] != "" {
				continue
			}
			g, inited := done.Inits[d.initKey()]
			if !inited {
				continue // the init to come holds these rows
			}
			if within, err := coversKnownDomains(g, m.GTID); err == nil && within && m.GTID != "" {
				continue
			}
			c := t
			c.Schema, c.Table = d.destSchema, d.destTable
			out = append(out, c)
		}
	}
	return out
}

// ensureDeltaPartitions adds, before the checkpoint's transaction (a DDL commits), the
// partitions its rows need on every partitioned destination it writes, and leaves out the rows
// older than a destination's purge horizon (a late row never undoes a purge); it returns the
// tables to apply.
func (a *applier) ensureDeltaPartitions(ctx context.Context, conn *sqlx.Conn, tables []wire.TableFiles, files map[string][]byte) ([]wire.TableFiles, error) {
	now := time.Now().UTC()
	out := make([]wire.TableFiles, 0, len(tables))
	for _, t := range tables {
		idx, ok := a.indexOfDest(t.Schema, t.Table)
		if !ok {
			out = append(out, t)
			continue
		}
		pt, err := a.partitioned(ctx, conn, idx, t.Schema, t.Table)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", idx.Name, err)
		}
		if pt == nil {
			out = append(out, t)
			continue
		}
		if t.Inserts != "" {
			if rows, skipped := withoutPurged(files[t.Inserts], pt, now); skipped > 0 {
				// a file of its own: another destination of the same rows has its own horizon
				name := t.Inserts + "@" + t.Schema + "." + t.Table
				files[name], t.Inserts, t.InsertRows = rows, name, t.InsertRows-skipped
				a.countPurgeSkipped(skipped)
				if len(rows) == 0 {
					t.Inserts = ""
				}
			}
		}
		top, ok := pt.latest(files[t.Inserts])
		if q, err := pt.ensure(ctx, conn, now, top, ok); err != nil {
			return nil, fmt.Errorf("index %s: %w (%s)", idx.Name, err, q)
		}
		out = append(out, t)
	}
	return out, nil
}

// indexOfDest is the index whose destination schema.table is.
func (a *applier) indexOfDest(schema, table string) (wire.Index, bool) {
	for _, d := range a.copies {
		if strings.EqualFold(d.destSchema, schema) && strings.EqualFold(d.destTable, table) {
			return d.idx, true
		}
	}
	for _, idx := range a.cfg.Indexes {
		if !idx.TableMode() && strings.EqualFold(idx.Schema, schema) && strings.EqualFold(idx.Table, table) {
			return idx, true
		}
	}
	return wire.Index{}, false
}

// maintainPartitions keeps the partitions of every partitioned destination whose init is
// done up to the horizon (partitions_ahead periods after the current one), and drops those past
// its purge (partition_purge_after_days): never during an init (a destination whose init is not
// applied is not maintained), never the last one nor the one being written.
func (a *applier) maintainPartitions(ctx context.Context, db *sqlx.DB, done *wire.Applied) error {
	type dest struct {
		idx           wire.Index
		schema, table string
	}
	var dests []dest
	for _, d := range a.copies {
		if _, ok := done.Inits[d.initKey()]; ok && d.idx.PartitionColumn != "" {
			dests = append(dests, dest{d.idx, d.destSchema, d.destTable})
		}
	}
	for _, idx := range a.cfg.Indexes {
		if !idx.TableMode() && idx.PartitionColumn != "" && slices.Contains(done.Indexes, indexID(idx)) {
			dests = append(dests, dest{idx, idx.Schema, idx.Table})
		}
	}
	if len(dests) == 0 {
		return nil
	}
	conn, err := a.session(ctx, db)
	if err != nil {
		return err
	}
	defer conn.Close()
	var errs []string
	for _, d := range dests {
		pt, err := a.partitioned(ctx, conn, d.idx, d.schema, d.table)
		if err == nil && pt != nil {
			now := time.Now().UTC()
			var q string
			if q, err = pt.ensure(ctx, conn, now, 0, false); err != nil {
				err = fmt.Errorf("%w (%s)", err, q)
			} else if q, dropped, perr := pt.purge(ctx, conn, now); perr != nil {
				err = fmt.Errorf("the purge: %w (%s)", perr, q)
			} else if len(dropped) > 0 {
				a.ranOnTarget(q, len(dropped))
			}
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("index %s: the partitions ahead of %s.%s: %s", d.idx.Name, d.schema, d.table, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// pausedError says the paused indexes, in order; nil when none.
func pausedError(done wire.Applied) error {
	if len(done.Paused) == 0 {
		return nil
	}
	ids := make([]string, 0, len(done.Paused))
	for id := range done.Paused {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		out = append(out, done.Paused[id])
	}
	return fmt.Errorf("%s", strings.Join(out, "; "))
}
