// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// replplugin-columnstore captures a source's carried tables as checkpoints (#2015), which
// replication-manager applies to a ColumnStore copy with LOAD DATA: ColumnStore takes no
// row UPDATE or DELETE from replication. Started before the child's seed, it reads on stdin
// the rows of the ColumnStore tables the restore parses from the parent's splitdump backup,
// then the restore's end, with the backup's GTID and the tables carried; only then it reads
// the source's binlog from that GTID (go-mysql canal, MariaDB compressed events included).
// Everything it receives is accumulated per period (batch_timeout_ms) and written as a
// checkpoint at a transaction boundary: per table the keys to delete and the rows to load.
// It pauses while the checkpoints not yet applied exceed max_pending_mb: its stdin is not
// read, the restore slows down; the binlog is not read. Supervised by replication-manager,
// its passwords in its environment; a wire.Status JSON line on stdout per period; its
// position in its State file, written after each checkpoint, so a restart re-reads what no
// checkpoint holds yet. A restart during the restore cannot resume it: a new seed is needed.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

type capture struct {
	canal.DummyEventHandler
	cfg       wire.Config
	statePath string
	c         *canal.Canal
	w         *checkpointWriter

	mu       sync.Mutex
	state    wire.State
	bufs     map[string]*tableBuf
	rows     int  // rows buffered in the period
	inTx     bool // between a transaction's first row and its XID
	flushDue bool // the period ended inside a transaction
	gtid     string
	lastTS   uint32 // source time of the last captured transaction
	st       wire.Status
	keyErrs  map[string]bool
}

func (c *capture) buf(t *schema.Table) *tableBuf {
	k := t.Schema + "." + t.Name
	b, ok := c.bufs[k]
	if !ok || b.t != t {
		// a new table, or its schema changed (a DDL): a new buffer is fine once the old
		// one was flushed; within a period the old buffer keeps its rows
		if ok && !b.empty() {
			return b
		}
		b = newTableBuf(t)
		c.bufs[k] = b
	}
	return b
}

func (c *capture) tsv(t *schema.Table, r []interface{}) row {
	out := make(row, len(t.Columns))
	for i := range t.Columns {
		if i < len(r) {
			out[i] = tsvField(&t.Columns[i], r[i])
		}
	}
	return out
}

// OnRow buffers the rows of an event in the period.
func (c *capture) OnRow(e *canal.RowsEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cfg.Matches(e.Table.Schema, e.Table.Name) {
		c.st.Skipped += int64(len(e.Rows))
		return nil
	}
	c.inTx = true
	c.lastTS = e.Header.Timestamp
	b := c.buf(e.Table)
	if b.keyErr != nil && e.Action != canal.InsertAction {
		// updates and deletes cannot be applied: said once, rows counted as skipped
		name := e.Table.Schema + "." + e.Table.Name
		if !c.keyErrs[name] {
			c.keyErrs[name] = true
			c.st.Description = b.keyErr.Error()
		}
		c.st.Skipped += int64(len(e.Rows))
		return nil
	}
	switch e.Action {
	case canal.InsertAction:
		for _, r := range e.Rows {
			b.insert(c.tsv(e.Table, r))
			c.st.Inserts++
		}
	case canal.DeleteAction:
		for _, r := range e.Rows {
			b.remove(c.tsv(e.Table, r))
			c.st.Deletes++
		}
	case canal.UpdateAction:
		for i := 0; i+1 < len(e.Rows); i += 2 {
			b.remove(c.tsv(e.Table, e.Rows[i]))
			b.insert(c.tsv(e.Table, e.Rows[i+1]))
			c.st.Updates++
		}
	}
	c.rows += len(e.Rows)
	return nil
}

func (c *capture) OnXID(_ *replication.EventHeader, _ mysql.Position) error {
	c.mu.Lock()
	c.inTx = false
	c.mu.Unlock()
	return nil
}

// OnPosSynced follows the GTID after each transaction, and closes the period there when its
// time is over.
func (c *capture) OnPosSynced(_ *replication.EventHeader, _ mysql.Position, set mysql.GTIDSet, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if set != nil {
		c.gtid = set.String()
	}
	if c.inTx {
		return nil
	}
	if c.flushDue {
		return c.flushLocked()
	}
	return nil
}

func (c *capture) OnDDL(_ *replication.EventHeader, _ mysql.Position, q *replication.QueryEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Description = "a DDL on the source is not applied to ColumnStore: " + truncate(string(q.Query), 200)
	return nil
}

func (c *capture) String() string { return "replplugin-columnstore" }

// flushLocked writes the period as a checkpoint at the current GTID, then the State. Its
// wait for room blocks the binlog reading: the pause.
func (c *capture) flushLocked() error {
	c.flushDue = false
	tables, files := renderTables(c.bufs)
	if len(tables) == 0 {
		if c.gtid != "" && c.gtid != c.state.GTID {
			c.state.GTID = c.gtid
			return wire.WriteState(c.statePath, c.state)
		}
		return nil
	}
	seq := c.state.Seq + 1
	if err := c.w.write(wire.Manifest{Seq: seq, GTID: c.gtid, Tables: tables}, files); err != nil {
		return fmt.Errorf("checkpoint %d: %w", seq, err)
	}
	c.state.Seq, c.state.GTID = seq, c.gtid
	if err := wire.WriteState(c.statePath, c.state); err != nil {
		return err
	}
	c.bufs, c.rows = map[string]*tableBuf{}, 0
	return nil
}

// tick ends the period: flushed now between transactions, else at the next transaction end.
func (c *capture) tick() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inTx {
		c.flushDue = true
		return nil
	}
	return c.flushLocked()
}

// status prints one wire.Status line.
func (c *capture) status(state, errText string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statusLocked(state, errText)
}

func (c *capture) statusLocked(state, errText string) {
	c.st.Wire = wire.WireVersion
	c.st.Time = time.Now().UTC().Format(time.RFC3339)
	c.st.State = state
	c.st.Error = errText
	c.st.GTID = c.state.GTID
	if c.lastTS > 0 {
		c.st.LagSeconds = time.Now().Unix() - int64(c.lastTS)
	}
	b, _ := json.Marshal(c.st)
	fmt.Println(string(b))
}

// restore reads the restore's frames on stdin (#2015): the rows of each INSERT go into the
// period, loaded into the table the restore created; at the restore's end the period is
// written with the backup's GTID and started is closed, the binlog streams from there.
// Reading waits while a checkpoint waits for room (c.mu is held): the restore's writes
// block, the restore slows down.
func (c *capture) restore(stdin io.Reader, started chan<- struct{}) error {
	br := bufio.NewReaderSize(stdin, 1<<20)
	for {
		in, payload, err := wire.ReadInput(br)
		if err == io.EOF {
			return fmt.Errorf("the restore's input ended before its end")
		}
		if err != nil {
			return err
		}
		switch in.Type {
		case wire.InputRestoreRows:
			if err := c.restoreRows(in.Schema, in.Table, payload); err != nil {
				return fmt.Errorf("restore of %s.%s: %w", in.Schema, in.Table, err)
			}
		case wire.InputRestoreDone:
			if in.GTID == "" {
				return fmt.Errorf("the restore's end has no GTID")
			}
			c.mu.Lock()
			c.cfg.Rules = wire.RulesOf(in.Tables)
			c.gtid = in.GTID
			c.state.RestoreDone, c.state.Tables = true, in.Tables
			err := c.flushLocked() // the last restored rows, with the GTID, and the State
			if err == nil {
				err = wire.WriteState(c.statePath, c.state)
			}
			c.mu.Unlock()
			if err != nil {
				return err
			}
			close(started)
			return nil
		default:
			return fmt.Errorf("unknown input %q", in.Type)
		}
	}
}

func (c *capture) restoreRows(schemaName, tableName string, stmt []byte) error {
	table, err := c.c.GetTable(schemaName, tableName)
	if err != nil {
		return fmt.Errorf("its schema on the source: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.buf(table)
	n := 0
	err = parseInsert(stmt, func(v []dumpValue) error {
		r := make(row, len(table.Columns))
		for i := range table.Columns {
			if i < len(v) {
				r[i] = dumpField(&table.Columns[i], v[i])
			}
		}
		b.loads = append(b.loads, r)
		n++
		return nil
	})
	c.st.Inserts += int64(n)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fail(c *capture, err error) {
	if c != nil {
		c.status(wire.StateError, err.Error())
	} else {
		b, _ := json.Marshal(wire.Status{Wire: wire.WireVersion, Time: time.Now().UTC().Format(time.RFC3339), State: wire.StateError, Error: err.Error()})
		fmt.Println(string(b))
	}
	os.Exit(1)
}

func main() {
	configPath := flag.String("config", "", "the plugin's TOML (<cluster datadir>/replication-plugins/<name>.toml)")
	flag.Parse()
	if *configPath == "" {
		fail(nil, fmt.Errorf("-config is required"))
	}
	var cfg wire.Config
	if _, err := toml.DecodeFile(*configPath, &cfg); err != nil {
		fail(nil, fmt.Errorf("config %s: %w", *configPath, err))
	}
	if v := os.Getenv(wire.EnvWireVersion); v != "" && v != strconv.Itoa(wire.WireVersion) {
		fail(nil, fmt.Errorf("replication-manager speaks wire %s, this plugin wire %d", v, wire.WireVersion))
	}
	if cfg.BatchTimeoutMs <= 0 {
		cfg.BatchTimeoutMs = 10000
	}
	statePath := wire.StatePath(*configPath)
	state, err := wire.ReadState(statePath)
	if err != nil {
		fail(nil, fmt.Errorf("state %s: %w", statePath, err))
	}
	if !state.RestoreDone && state.Seq > 0 {
		fail(nil, fmt.Errorf("a restore was interrupted after %d checkpoints: the child must be seeded again", state.Seq))
	}
	if state.RestoreDone && len(cfg.Rules) == 0 {
		cfg.Rules = wire.RulesOf(state.Tables)
	}

	c := &capture{cfg: cfg, statePath: statePath, state: state, bufs: map[string]*tableBuf{}, keyErrs: map[string]bool{}, gtid: state.GTID}
	c.w = &checkpointWriter{dir: wire.CheckpointDir(*configPath), maxPending: mbBytes(cfg.MaxPendingMB), paused: func(p int64) {
		c.mu.Lock()
		c.st.Description = fmt.Sprintf("paused: %d MB of checkpoints wait to be applied (max_pending_mb %d)", p>>20, mbBytes(cfg.MaxPendingMB)>>20)
		c.mu.Unlock()
	}}

	ccfg := canal.NewDefaultConfig()
	ccfg.Addr = fmt.Sprintf("%s:%d", cfg.Source.Host, cfg.Source.Port)
	ccfg.User = cfg.Source.User
	ccfg.Password = os.Getenv(wire.EnvSourcePassword)
	ccfg.Flavor = mysql.MariaDBFlavor
	if cfg.ServerID > 0 {
		ccfg.ServerID = cfg.ServerID
	}
	ccfg.Dump.ExecutionPath = "" // no dump: the restore's rows come on stdin
	// the tables carried are known at the restore's end: filtered in OnRow. canal keeps only
	// a table an include matches, the excludes then removing some: an exclude alone
	// excludes every table
	ccfg.IncludeTableRegex = []string{`^.*\..*$`}
	ccfg.ExcludeTableRegex = []string{`^mysql\..*$`} // replication_manager_schema's load-test tables are carried (#2015)
	cc, err := canal.NewCanal(ccfg)
	if err != nil {
		fail(c, fmt.Errorf("source %s: %w", ccfg.Addr, err))
	}
	c.c = cc
	cc.SetEventHandler(c)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	c.status(wire.StateStarting, "")
	ticker := time.NewTicker(time.Duration(cfg.BatchTimeoutMs) * time.Millisecond)
	defer ticker.Stop()

	started := make(chan struct{})
	if state.RestoreDone {
		close(started)
	} else {
		c.mu.Lock()
		c.st.Description = "waiting for the restore's rows"
		c.mu.Unlock()
		restored := make(chan error, 1)
		go func() { restored <- c.restore(os.Stdin, started) }()
	restoring:
		for {
			select {
			case err := <-restored:
				if err != nil {
					fail(c, err)
				}
				break restoring
			case <-ticker.C:
				if err := c.tick(); err != nil {
					fail(c, err)
				}
				c.status(wire.StateRunning, "")
			case <-sig:
				os.Exit(0)
			}
		}
		c.mu.Lock()
		c.st.Description = ""
		c.mu.Unlock()
	}
	<-started
	gset, err := mysql.ParseGTIDSet(mysql.MariaDBFlavor, c.state.GTID)
	if err != nil {
		fail(c, fmt.Errorf("GTID %q: %w", c.state.GTID, err))
	}
	done := make(chan error, 1)
	go func() { done <- cc.StartFromGTID(gset) }()
	for {
		select {
		case <-ticker.C:
			if err := c.tick(); err != nil {
				fail(c, err)
			}
			c.status(wire.StateRunning, "")
		case <-sig:
			cc.Close()
			os.Exit(0) // what no checkpoint holds is read again from the State's GTID
		case err := <-done:
			if err == nil {
				err = fmt.Errorf("the source stream ended")
			}
			fail(c, err)
		}
	}
}
