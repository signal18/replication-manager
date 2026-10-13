// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// replplugin-columnstore captures a source's carried tables as checkpoints (#2015), which
// replication-manager applies to a ColumnStore copy with LOAD DATA: ColumnStore takes no
// row UPDATE or DELETE from replication. It starts from an existing splitdump backup of the
// source, no dump of its own: every data file of a carried table becomes a checkpoint, the
// last one carrying the backup's GTID. Then it reads the source's binlog from that GTID
// (go-mysql canal, MariaDB compressed events included) and writes a checkpoint per period,
// at a transaction boundary: the keys to delete and the rows to load per table. It pauses
// while the checkpoints not yet applied exceed max_pending_mb, the splitdump and the
// binlog alike. Started and supervised by replication-manager, its passwords in its
// environment; a wire.Status JSON line on stdout per period; its position in its State file,
// written after each checkpoint, so a restart re-reads what no checkpoint holds yet.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/splitdump"
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

// OnPosSynced follows the GTID after each transaction, and closes the period there when it
// is due (its time is over, or batch_size rows).
func (c *capture) OnPosSynced(_ *replication.EventHeader, _ mysql.Position, set mysql.GTIDSet, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if set != nil {
		c.gtid = set.String()
	}
	if c.inTx {
		return nil
	}
	if c.flushDue || (c.cfg.BatchSize > 0 && c.rows >= c.cfg.BatchSize) {
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

// snapshot turns the carried tables of the splitdump backup into checkpoints, a data file
// each, then a last one carrying the backup's GTID. Files already done (State) are skipped.
func (c *capture) snapshot() error {
	dir := c.cfg.StartBackup
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("splitdump backup %s: %w", dir, err)
	}
	done := map[string]bool{}
	for _, f := range c.state.SnapshotFiles {
		done[f] = true
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || splitdump.IsSchemaFile(n) || splitdump.IsPostFile(n) || splitdump.IsMysqlSystemAll(n) {
			continue
		}
		if !strings.HasSuffix(n, ".sql.gz") && !strings.HasSuffix(n, ".sql") {
			continue
		}
		s, t := splitdump.SchemaFromFilename(n), splitdump.TableFromFilename(n)
		if s == "" || t == "" || !c.cfg.Matches(s, t) || done[n] {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s, t := splitdump.SchemaFromFilename(n), splitdump.TableFromFilename(n)
		table, err := c.c.GetTable(s, t)
		if err != nil {
			return fmt.Errorf("schema of %s.%s on the source: %w", s, t, err)
		}
		b := newTableBuf(table)
		if err := readDumpFile(filepath.Join(dir, n), func(v []dumpValue) error {
			r := make(row, len(table.Columns))
			for i := range table.Columns {
				if i < len(v) {
					r[i] = dumpField(&table.Columns[i], v[i])
				}
			}
			b.list = append(b.list, r) // a fresh table: rows to load, no key to delete
			return nil
		}); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
		var ins []byte
		for _, r := range b.list {
			ins = wire.EncodeTSV(ins, r)
		}
		seq := c.state.Seq + 1
		tf := wire.TableFiles{Schema: s, Table: t, KeyColumns: b.keyNames(), InsertRows: int64(len(b.list))}
		files := map[string][]byte{}
		if len(b.list) > 0 {
			tf.Inserts = "0.inserts.tsv"
			files[tf.Inserts] = ins
		}
		if err := c.w.write(wire.Manifest{Seq: seq, Snapshot: true, Tables: []wire.TableFiles{tf}}, files); err != nil {
			return fmt.Errorf("checkpoint %d: %w", seq, err)
		}
		c.mu.Lock()
		c.state.Seq = seq
		c.state.SnapshotFiles = append(c.state.SnapshotFiles, n)
		c.st.Inserts += int64(len(b.list))
		err = wire.WriteState(c.statePath, c.state)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		c.status(wire.StateRunning, "")
	}
	seq := c.state.Seq + 1
	if err := c.w.write(wire.Manifest{Seq: seq, Snapshot: true, GTID: c.cfg.StartGTID}, nil); err != nil {
		return fmt.Errorf("checkpoint %d: %w", seq, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Seq, c.state.GTID, c.state.SnapshotDone, c.state.SnapshotFiles = seq, c.cfg.StartGTID, true, nil
	c.gtid = c.cfg.StartGTID
	return wire.WriteState(c.statePath, c.state)
}

func readDumpFile(path string, fn func([]dumpValue) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	return forEachInsert(r, fn)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// includeRegex: canal's table filter, "schema\.table" of the rules
func includeRegex(cfg *wire.Config) []string {
	var out []string
	for _, r := range cfg.Rules {
		if len(r.Tables) == 0 {
			out = append(out, regexp.QuoteMeta(r.Schema)+`\..*`)
			continue
		}
		for _, t := range r.Tables {
			if t == "*" {
				out = append(out, regexp.QuoteMeta(r.Schema)+`\..*`)
			} else {
				out = append(out, regexp.QuoteMeta(r.Schema)+`\.`+regexp.QuoteMeta(t))
			}
		}
	}
	return out
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
	if len(cfg.Rules) == 0 {
		fail(nil, fmt.Errorf("no rule: nothing to carry"))
	}
	if cfg.BatchTimeoutMs <= 0 {
		cfg.BatchTimeoutMs = 10000
	}
	statePath := wire.StatePath(*configPath)
	state, err := wire.ReadState(statePath)
	if err != nil {
		fail(nil, fmt.Errorf("state %s: %w", statePath, err))
	}
	if !state.SnapshotDone && (cfg.StartBackup == "" || cfg.StartGTID == "") {
		fail(nil, fmt.Errorf("no start_backup or start_gtid: where to start is unknown"))
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
	ccfg.Dump.ExecutionPath = "" // no dump: the splitdump backup is the start
	ccfg.IncludeTableRegex = includeRegex(&cfg)
	cc, err := canal.NewCanal(ccfg)
	if err != nil {
		fail(c, fmt.Errorf("source %s: %w", ccfg.Addr, err))
	}
	c.c = cc
	cc.SetEventHandler(c)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	c.status(wire.StateStarting, "")
	if !state.SnapshotDone {
		snap := make(chan error, 1)
		go func() { snap <- c.snapshot() }()
		select {
		case err := <-snap:
			if err != nil {
				fail(c, err)
			}
		case <-sig:
			os.Exit(0)
		}
	}
	gset, err := mysql.ParseGTIDSet(mysql.MariaDBFlavor, c.state.GTID)
	if err != nil {
		fail(c, fmt.Errorf("GTID %q: %w", c.state.GTID, err))
	}
	done := make(chan error, 1)
	go func() { done <- cc.StartFromGTID(gset) }()

	ticker := time.NewTicker(time.Duration(cfg.BatchTimeoutMs) * time.Millisecond)
	defer ticker.Stop()
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
