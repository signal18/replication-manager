// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Capture sources (doc/implementation/cluster/CDC_SOURCES.md): a plugin whose source is not a
// MariaDB binlog brings a Source, and runs MainSource instead of Main. The core keeps
// everything else: the periods and their bounds, the checkpoints, the apply, its position, the
// accumulation and the status. The source turns its log into rows of the core's format (nil,
// string or bytes per column, in the destination's text) and says where each transaction ends,
// with its own position (State.GTID holds it: a log sequence number for PostgreSQL).

// Source is a capture of a source that is not a MariaDB binlog.
type Source interface {
	// Kind is the wire.Config.SourceKind it captures
	Kind() string
	// Prepare runs before the apply starts. When no initial load is done (sink.LoadDone false)
	// it makes and empties the destination's tables: the apply is not running yet, so no
	// checkpoint of an earlier attempt can land after the tables are emptied.
	Prepare(ctx context.Context, sink *Sink) error
	// Run captures into sink until ctx ends or it fails: the initial load when none is done
	// (Load then FinishLoad), then the changes from sink.Position. An error wrapping
	// ErrNeedsInit stops the feed until a new initial load.
	Run(ctx context.Context, sink *Sink) error
}

// Dropper is a Source that removes what it created on the source server for the feed
// (State.Owned, and nothing else): the plugin run with -drop, an explicit action of the
// operator when the feed is deleted.
type Dropper interface {
	Drop(ctx context.Context, cfg wire.Config, state wire.State) ([]string, error)
}

// ErrNeedsInit: the source cannot give the changes from the feed's position any more.
var ErrNeedsInit = errors.New("a new initial load is needed")

// Sink is what a Source writes into: the open period of the core.
type Sink struct {
	c *capture
}

// SinkTable is a source table as the core buffers it: its columns in the destination's order,
// and its primary key.
type SinkTable struct {
	t *schema.Table
}

// Config is the plugin's TOML.
func (s *Sink) Config() wire.Config { return s.c.cfg }

// State is the plugin's State as last written.
func (s *Sink) State() wire.State {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	st := s.c.state
	st.Tables = append([]string(nil), st.Tables...)
	st.Owned = append([]string(nil), st.Owned...)
	return st
}

// LoadDone: the initial load is done, the changes stream from Position.
func (s *Sink) LoadDone() bool { return s.State().RestoreDone }

// Position is the source position the feed restarts from (the last checkpoint written).
func (s *Sink) Position() string { return s.State().GTID }

// Applied is what the apply committed to the destination: a source acknowledges its log up
// to Applied().GTID only, so the source keeps what is captured but not applied yet.
func (s *Sink) Applied() wire.Applied {
	if s.c.ap == nil {
		return wire.Applied{}
	}
	s.c.ap.mu.Lock()
	defer s.c.ap.mu.Unlock()
	return s.c.ap.done
}

// Table describes a source table; key indexes columns. A new SinkTable for a table whose
// columns changed: the open period keeps the old one's rows.
func (s *Sink) Table(schemaName, table string, columns []string, key []int) *SinkTable {
	t := &schema.Table{Schema: schemaName, Name: table, PKColumns: append([]int(nil), key...)}
	for _, c := range columns {
		// text in, text out: the source converted the values; filterable for the keys
		t.Columns = append(t.Columns, schema.TableColumn{Name: c, Type: schema.TYPE_STRING, RawType: "varchar"})
	}
	return &SinkTable{t: t}
}

func rowSize(r []interface{}) int64 {
	n := int64(len(r))
	for _, v := range r {
		switch x := v.(type) {
		case string:
			n += int64(len(x))
		case []byte:
			n += int64(len(x))
		}
	}
	return n
}

func (s *Sink) change(t *SinkTable, fn func(b *tableBuf)) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cfg.Matches(t.t.Schema, t.t.Name) {
		c.st.Skipped++
		return nil
	}
	c.inTx = true
	fn(c.buf(t.t))
	return nil
}

// Insert adds a row of the current transaction.
func (s *Sink) Insert(t *SinkTable, r []interface{}) error {
	var err error
	s.change(t, func(b *tableBuf) {
		b.insert(row(r))
		s.c.st.Inserts++
		s.c.rows++
		err = s.c.addRowEvent(rowSize(r))
	})
	return err
}

// Delete removes a row of the current transaction (its key columns at least).
func (s *Sink) Delete(t *SinkTable, r []interface{}) error {
	var err error
	s.change(t, func(b *tableBuf) {
		b.remove(row(r))
		s.c.st.Deletes++
		s.c.rows++
		err = s.c.addRowEvent(rowSize(r))
	})
	return err
}

// Update replaces old (its key columns at least) by r in the current transaction.
func (s *Sink) Update(t *SinkTable, old, r []interface{}) error {
	var err error
	s.change(t, func(b *tableBuf) {
		b.remove(row(old))
		b.insert(row(r))
		s.c.st.Updates++
		s.c.rows += 2
		err = s.c.addRowEvent(rowSize(old) + rowSize(r))
	})
	return err
}

// Commit ends the current transaction at the source position pos, committed at ts: a period
// due closes here.
func (s *Sink) Commit(pos string, ts time.Time) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inTx, c.txSize = false, 0
	c.gtid = pos
	if !ts.IsZero() {
		c.lastTS = uint32(ts.Unix())
	}
	if c.flushDue {
		return c.flushLocked()
	}
	return nil
}

// Load adds a row of the initial load (into a table made empty by Prepare: nothing to delete).
func (s *Sink) Load(t *SinkTable, r []interface{}) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.buf(t.t)
	b.loads = append(b.loads, row(r))
	c.st.Inserts++
	c.size += rowSize(r)
	if c.size >= c.maxSize {
		return c.flushLocked()
	}
	return nil
}

// LoadTableDone counts a table of the initial load copied.
func (s *Sink) LoadTableDone() {
	s.c.mu.Lock()
	s.c.st.InitialLoadTables++
	s.c.mu.Unlock()
}

// FinishLoad ends the initial load: its last rows written at the position pos the changes
// stream from, tables the tables carried.
func (s *Sink) FinishLoad(pos string, tables []string) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gtid = pos
	c.state.RestoreDone, c.state.Tables = true, tables
	c.st.InitialLoad = false
	if err := c.flushLocked(); err != nil {
		return err
	}
	c.state.GTID = pos
	return wire.WriteState(c.statePath, c.state)
}

// SetOwned records what the source created on the source server for the feed (and only
// that is ever removed, by Dropper or at the bound).
func (s *Sink) SetOwned(owned []string) error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Owned = append([]string(nil), owned...)
	return wire.WriteState(c.statePath, c.state)
}

// SetSource says what the source keeps for the feed, its bound and a warning ("" none).
func (s *Sink) SetSource(retainedMB, maxMB int64, warning string) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.SourceRetainedMB, c.st.SourceRetainedMaxMB, c.st.SourceWarning = retainedMB, maxMB, warning
}

// Ran records a statement the source ran on the source server that changes it (F11): in the
// status for replication-manager's log, and on stderr.
func (s *Sink) Ran(stmt string) {
	fmt.Fprintln(os.Stderr, "ran on the source: "+stmt)
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.SourceStatements = append(c.st.SourceStatements, stmt)
	if over := len(c.st.SourceStatements) - wire.MaxSourceStatements; over > 0 {
		c.st.SourceStatements = append([]string(nil), c.st.SourceStatements[over:]...)
	}
}

// Idle: nothing captured waits to be applied (no open transaction, no row buffered, no
// checkpoint pending): the source may then acknowledge its log up to Position.
func (s *Sink) Idle() bool {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inTx {
		return false
	}
	for _, b := range c.bufs {
		if !b.empty() {
			return false
		}
	}
	return wire.PendingBytes(c.w.dir) == 0
}

// Describe sets the status's description.
func (s *Sink) Describe(desc string) {
	s.c.mu.Lock()
	s.c.st.Description = desc
	s.c.mu.Unlock()
}

// Target opens the destination server.
func (s *Sink) Target() (*sqlx.DB, error) { return TargetDB(s.c.cfg) }

// Destination is the plugin's destination.
func (s *Sink) Destination() Destination { return s.c.ap.dest }

// PrepareTable makes schema.table on the target from the source's description cols (its
// primary key key) and empties it, for an initial load: a table of the same columns is emptied
// (Truncater, else every row deleted), a missing one created and made the destination's; a table
// of other columns is refused, never dropped (it may hold someone's rows). The SQL run is
// returned (F11).
func PrepareTable(ctx context.Context, db *sqlx.DB, conn *sqlx.Conn, dest Destination, schemaName, table string, cols []dbhelper.ColumnDef, key []string) (string, error) {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return emptyOrMake(ctx, db, conn, dest, schemaName, table, names, func(existing []string) (string, error) {
		if len(existing) > 0 {
			return "", fmt.Errorf("%s.%s exists on the target with other columns (%s), the source has (%s): it is never dropped, rename or drop it", schemaName, table, strings.Join(existing, ","), strings.Join(names, ","))
		}
		return dbhelper.CreateTableFromColumns(ctx, conn, schemaName, table, cols, key)
	})
}

// emptyOrMake empties schema.table when it has exactly columns, else calls create (given the
// existing columns, none when missing) and hands the table made to the destination.
func emptyOrMake(ctx context.Context, db *sqlx.DB, conn *sqlx.Conn, dest Destination, schemaName, table string, columns []string, create func(existing []string) (string, error)) (string, error) {
	existing, _, _ := dbhelper.GetColumnNamesConn(ctx, conn, schemaName, table)
	if len(existing) > 0 && strings.Join(existing, ",") == strings.Join(columns, ",") {
		if tr, ok := dest.(Truncater); ok {
			return "TRUNCATE TABLE " + schemaName + "." + table, tr.Truncate(ctx, conn, schemaName, table)
		}
		return dbhelper.DeleteAllRows(ctx, conn, schemaName, table)
	}
	q, err := create(existing)
	if err != nil {
		return q, err
	}
	d, err := ReadTableDesc(db, schemaName, table)
	if err != nil {
		return q, err
	}
	if why := dest.Refuse(d); why != "" {
		return q, fmt.Errorf("the destination refuses %s.%s: %s", schemaName, table, why)
	}
	if err := dest.Create(ctx, conn, d); err != nil && !isMadeByRestore(err) {
		return q, fmt.Errorf("the destination's table %s.%s: %w", schemaName, table, err)
	}
	return q, nil
}

// clearCheckpoints removes the checkpoints waiting in dir: those of an initial load that did
// not finish, which a new initial load replaces.
func clearCheckpoints(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if _, ok := wire.ParseCheckpointName(e.Name()); ok {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// runSource runs the capture source src until it fails or a signal stops it.
func runSource(c *capture, ap *applier, src Source, sig <-chan os.Signal, tick func(), ticker *time.Ticker) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &Sink{c: c}
	c.mu.Lock()
	c.st.SourceKind = src.Kind()
	if !c.state.RestoreDone {
		c.st.InitialLoad = true
		c.st.Description = "initial load"
		// an initial load that did not finish: its checkpoints are replaced by the new one's
		clearCheckpoints(c.w.dir)
	}
	c.mu.Unlock()
	if err := src.Prepare(ctx, sink); err != nil {
		stopSource(c, err)
	}
	applyCtx, stopApply := context.WithCancel(context.Background())
	defer stopApply()
	go ap.run(applyCtx)
	done := make(chan error, 1)
	go func() { done <- src.Run(ctx, sink) }()
	for {
		select {
		case <-ticker.C:
			tick()
		case <-sig:
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			os.Exit(0) // what no checkpoint holds is read again from the State's position
		case err := <-done:
			if err == nil {
				err = fmt.Errorf("the source stream ended")
			}
			stopSource(c, err)
		}
	}
}

// stopSource ends the plugin on a source's error; ErrNeedsInit is recorded in the State, so
// replication-manager keeps the feed stopped until a new initial load.
func stopSource(c *capture, err error) {
	if errors.Is(err, ErrNeedsInit) {
		c.mu.Lock()
		c.state.NeedsInit, c.state.Reason = true, err.Error()
		werr := wire.WriteState(c.statePath, c.state)
		c.mu.Unlock()
		if werr != nil {
			fail(c, fmt.Errorf("%v; and its State: %w", err, werr))
		}
		c.status(wire.StateNeedsInit, err.Error())
		os.Exit(3)
	}
	fail(c, err)
}

// runDrop is a plugin run with -drop: the source removes what its State says it created on
// the source server for the feed, each statement printed (F11), and the State forgets them.
// 0 when done, 1 on an error.
func runDrop(cfg wire.Config, statePath string, src Source) int {
	d, ok := src.(Dropper)
	if !ok {
		fmt.Fprintln(os.Stderr, "this plugin created nothing on its source")
		return 1
	}
	state, err := wire.ReadState(statePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ran, err := d.Drop(context.Background(), cfg, state)
	for _, q := range ran {
		fmt.Println(q)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	state.Owned = nil
	if err := wire.WriteState(statePath, state); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
