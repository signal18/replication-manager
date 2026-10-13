// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// replplugin-columnstore carries a source's row changes to a ColumnStore copy as statements
// (#2015): ColumnStore applies a replicated INSERT, but its row-level update and delete do
// nothing (ha_mcs_impl_update_row, ha_mcs_impl_delete_row), and a replica cannot turn row
// events into statements. It reads the source's binlog from a GTID (go-mysql canal, MariaDB
// compressed events included), and writes each source transaction as one target
// transaction: a multi-row INSERT, an UPDATE or a DELETE keyed by the source's primary key.
// Started and supervised by replication-manager with its TOML and its passwords in the
// environment; one wire.Status JSON line on stdout per period; its position in its State
// file, written after each committed transaction (at least once: a crash between the
// commit and the write replays that transaction).
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	_ "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// stateEvery: the State file is written at most once per period (and at exit)
const stateEvery = time.Second

type applier struct {
	canal.DummyEventHandler
	cfg       wire.Config
	statePath string
	target    *sql.DB

	mu        sync.Mutex
	tx        *sql.Tx
	st        wire.Status
	lastTS    uint32 // source time of the last committed transaction
	pendingTS uint32
	gtid      string
	savedGTID string
	savedAt   time.Time
	keyErrs   map[string]bool
}

func (a *applier) begin() (*sql.Tx, error) {
	if a.tx != nil {
		return a.tx, nil
	}
	tx, err := a.target.Begin()
	if err != nil {
		return nil, err
	}
	a.tx = tx
	return tx, nil
}

func (a *applier) exec(q string, args []interface{}) error {
	tx, err := a.begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(q, args...); err != nil {
		return fmt.Errorf("%s: %w", q, err)
	}
	return nil
}

// OnRow writes the rows of an event as statements in the open target transaction.
func (a *applier) OnRow(e *canal.RowsEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.Matches(e.Table.Schema, e.Table.Name) {
		a.st.Skipped += int64(len(e.Rows))
		return nil
	}
	a.pendingTS = e.Header.Timestamp
	switch e.Action {
	case canal.InsertAction:
		q, args := insertStatement(e.Table, e.Rows)
		if err := a.exec(q, args); err != nil {
			return err
		}
		a.st.Inserts += int64(len(e.Rows))
	case canal.UpdateAction, canal.DeleteAction:
		key, err := keyColumns(e.Table)
		if err != nil {
			// the table is skipped for updates and deletes, said once in the status
			name := e.Table.Schema + "." + e.Table.Name
			if !a.keyErrs[name] {
				a.keyErrs[name] = true
				a.st.Description = err.Error()
			}
			a.st.Skipped += int64(len(e.Rows))
			return nil
		}
		if e.Action == canal.UpdateAction {
			for i := 0; i+1 < len(e.Rows); i += 2 {
				q, args := updateStatement(e.Table, key, e.Rows[i], e.Rows[i+1])
				if err := a.exec(q, args); err != nil {
					return err
				}
				a.st.Updates++
			}
		} else {
			for _, row := range e.Rows {
				q, args := deleteStatement(e.Table, key, row)
				if err := a.exec(q, args); err != nil {
					return err
				}
				a.st.Deletes++
			}
		}
	}
	return nil
}

// OnXID commits the target transaction of the source transaction.
func (a *applier) OnXID(_ *replication.EventHeader, _ mysql.Position) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tx == nil {
		return nil
	}
	err := a.tx.Commit()
	a.tx = nil
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if a.pendingTS > 0 {
		a.lastTS = a.pendingTS
	}
	return nil
}

// OnPosSynced follows the source's GTID after each transaction and writes the State file,
// at most once per stateEvery.
func (a *applier) OnPosSynced(_ *replication.EventHeader, _ mysql.Position, set mysql.GTIDSet, force bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if set == nil || a.tx != nil {
		return nil
	}
	a.gtid = set.String()
	if force || time.Since(a.savedAt) >= stateEvery {
		return a.saveLocked()
	}
	return nil
}

func (a *applier) OnDDL(_ *replication.EventHeader, _ mysql.Position, q *replication.QueryEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Description = "a DDL on the source is not applied to ColumnStore: " + truncate(string(q.Query), 200)
	return nil
}

func (a *applier) String() string { return "replplugin-columnstore" }

func (a *applier) saveLocked() error {
	if a.gtid == "" || a.gtid == a.savedGTID {
		return nil
	}
	if err := wire.WriteState(a.statePath, wire.State{GTID: a.gtid}); err != nil {
		return fmt.Errorf("state %s: %w", a.statePath, err)
	}
	a.savedGTID, a.savedAt = a.gtid, time.Now()
	return nil
}

// status prints one wire.Status line.
func (a *applier) status(state, errText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Wire = wire.WireVersion
	a.st.Time = time.Now().UTC().Format(time.RFC3339)
	a.st.State = state
	a.st.Error = errText
	a.st.GTID = a.gtid
	if a.lastTS > 0 {
		a.st.LagSeconds = time.Now().Unix() - int64(a.lastTS)
	}
	b, _ := json.Marshal(a.st)
	fmt.Println(string(b))
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

func fail(a *applier, err error) {
	if a != nil {
		a.status(wire.StateError, err.Error())
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
	start := state.GTID
	if start == "" {
		start = cfg.StartGTID
	}
	if start == "" {
		fail(nil, fmt.Errorf("no start_gtid and no state: where to start is unknown"))
	}
	gset, err := mysql.ParseGTIDSet(mysql.MariaDBFlavor, start)
	if err != nil {
		fail(nil, fmt.Errorf("GTID %q: %w", start, err))
	}

	target, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/?interpolateParams=true&charset=utf8mb4&parseTime=false",
		cfg.Target.User, os.Getenv(wire.EnvTargetPassword), cfg.Target.Host, cfg.Target.Port))
	if err != nil {
		fail(nil, err)
	}
	target.SetMaxOpenConns(1)
	if err := target.Ping(); err != nil {
		fail(nil, fmt.Errorf("target %s:%d: %w", cfg.Target.Host, cfg.Target.Port, err))
	}

	a := &applier{cfg: cfg, statePath: statePath, target: target, gtid: start, savedGTID: state.GTID, keyErrs: map[string]bool{}}

	ccfg := canal.NewDefaultConfig()
	ccfg.Addr = fmt.Sprintf("%s:%d", cfg.Source.Host, cfg.Source.Port)
	ccfg.User = cfg.Source.User
	ccfg.Password = os.Getenv(wire.EnvSourcePassword)
	ccfg.Flavor = mysql.MariaDBFlavor
	if cfg.ServerID > 0 {
		ccfg.ServerID = cfg.ServerID
	}
	ccfg.Dump.ExecutionPath = "" // no dump: the target was seeded, the plugin starts at its GTID
	ccfg.IncludeTableRegex = includeRegex(&cfg)
	c, err := canal.NewCanal(ccfg)
	if err != nil {
		fail(a, fmt.Errorf("source %s: %w", ccfg.Addr, err))
	}
	c.SetEventHandler(a)

	a.status(wire.StateStarting, "")
	done := make(chan error, 1)
	go func() { done <- c.StartFromGTID(gset) }()

	ticker := time.NewTicker(time.Duration(cfg.BatchTimeoutMs) * time.Millisecond)
	defer ticker.Stop()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			err := a.saveLocked()
			a.mu.Unlock()
			if err != nil {
				fail(a, err)
			}
			a.status(wire.StateRunning, "")
		case <-sig:
			c.Close()
			a.mu.Lock()
			if a.tx != nil {
				a.tx.Rollback()
				a.tx = nil
			}
			err := a.saveLocked()
			a.mu.Unlock()
			if err != nil {
				fail(a, err)
			}
			a.status(wire.StateRunning, "")
			os.Exit(0)
		case err := <-done:
			a.mu.Lock()
			if a.tx != nil {
				a.tx.Rollback()
				a.tx = nil
			}
			a.saveLocked()
			a.mu.Unlock()
			if err == nil {
				err = fmt.Errorf("the source stream ended")
			}
			fail(a, err)
		}
	}
}
