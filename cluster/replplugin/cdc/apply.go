// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// The apply: every applyEvery, the checkpoints after the last applied one, in order, to the
// destination, stopping at the first failure (retried at the next round). Each one applied
// is recorded in <name>.applied.json and its file removed, which frees the room the capture
// waits for. With merge_interval_ms, they go to the staging instead, merged at that period;
// a merge interval set back to 0 merges what the staging holds first.

const applyEvery = 2 * time.Second

type applier struct {
	dest Destination
	cfg  wire.Config
	path string // the plugin's TOML

	// the denormalize mode: the source the unpack queries, the indexes
	src     *sqlx.DB
	indexes []*indexMeta

	mu   sync.Mutex
	done wire.Applied
	err  string
	last *merged
}

// fill puts what the apply did into a status line.
func (a *applier) fill(st *wire.Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st.AppliedSeq, st.AppliedGTID, st.ApplyError = a.done.Seq, a.done.GTID, a.err
	st.IndexesReady = nil
	for _, id := range a.done.Indexes {
		st.IndexesReady = append(st.IndexesReady, strings.SplitN(id, "@", 2)[0])
	}
	st.PendingMB = wire.PendingBytes(wire.CheckpointDir(a.path)) >> 20
	if a.last != nil && a.cfg.MergeIntervalMs > 0 {
		st.MergedSeq, st.MergedGTID, st.MergeTookMs = a.last.Seq, a.last.GTID, a.last.TookMs
		if a.last.Seq > 0 {
			st.MergedAt = a.last.At.UTC().Format(time.RFC3339)
		}
		st.NextMergeAt = a.last.At.Add(time.Duration(a.cfg.MergeIntervalMs) * time.Millisecond).UTC().Format(time.RFC3339)
	}
}

func (a *applier) setErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.err = ""
		return
	}
	a.err = err.Error()
}

// TargetDB opens the plugin's target with the password of its environment.
func TargetDB(cfg wire.Config) (*sqlx.DB, error) {
	return endpointDB(cfg.Target, os.Getenv(wire.EnvTargetPassword))
}

// SourceDB opens the plugin's source with the password of its environment (the denormalize
// mode's queries).
func SourceDB(cfg wire.Config) (*sqlx.DB, error) {
	return endpointDB(cfg.Source, os.Getenv(wire.EnvSourcePassword))
}

func endpointDB(e wire.Endpoint, password string) (*sqlx.DB, error) {
	mc := mysql.NewConfig()
	mc.User = e.User
	mc.Passwd = password
	mc.Net = "tcp"
	mc.Addr = fmt.Sprintf("%s:%d", e.Host, e.Port)
	mc.Timeout = 10 * time.Second
	mc.AllowNativePasswords = true
	mc.ParseTime = false
	db, err := sqlx.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	// never a pooled connection: each one leaves with its session (sql_log_bin=0, MIXED) and,
	// after a cancellation, maybe a transaction
	db.SetMaxIdleConns(0)
	return db, nil
}

// session opens a connection to the target with the destination's session set.
func (a *applier) session(ctx context.Context, db *sqlx.DB) (*sqlx.Conn, error) {
	conn, err := db.Connx(ctx)
	if err != nil {
		return nil, err
	}
	for _, q := range a.dest.Session() {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			conn.Close()
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	return conn, nil
}

// run applies until ctx ends; a panic is said and the apply starts again a round later.
func (a *applier) run(ctx context.Context) {
	for ctx.Err() == nil {
		a.runOnce(ctx)
		select {
		case <-ctx.Done():
		case <-time.After(applyEvery):
		}
	}
}

func (a *applier) runOnce(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			a.setErr(fmt.Errorf("panic in the apply: %v", p))
		}
	}()
	db, err := TargetDB(a.cfg)
	if err != nil {
		a.setErr(err)
		return
	}
	defer db.Close()
	dir, appliedPath, mergedPath := wire.CheckpointDir(a.path), wire.AppliedPath(a.path), wire.MergedPath(a.path)
	done, err := readApplied(appliedPath)
	if err != nil {
		a.setErr(err)
		return
	}
	if p, ok := a.dest.(Positioner); ok {
		// committed with the rows: past applied.json after a crash between the two
		if pos, err := a.position(ctx, db, p); err != nil {
			a.setErr(err)
			return
		} else if pos.Seq > done.Seq {
			done.Seq, done.GTID = pos.Seq, pos.GTID
			if err := writeApplied(appliedPath, done); err != nil {
				a.setErr(err)
				return
			}
		}
	}
	a.mu.Lock()
	a.done = done
	a.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(applyEvery):
		}
		// a merge interval set back to 0: the staging merged once, it would be lost otherwise
		if a.cfg.MergeIntervalMs == 0 {
			if _, err := os.Stat(mergedPath); err == nil {
				if err := a.merge(ctx, db); err != nil {
					a.setErr(fmt.Errorf("last merge of the staging before the merge interval 0: %w", err))
					continue
				}
				os.Remove(mergedPath)
			}
		}
		var failed error
		if err := a.initIndexes(ctx, db, &done, appliedPath); err != nil {
			a.setErr(err)
			continue
		}
		for _, seq := range pendingCheckpoints(dir, done.Seq) {
			if ctx.Err() != nil {
				return
			}
			gtid, err := a.applyOne(ctx, db, filepath.Join(dir, wire.CheckpointName(seq)))
			if err != nil {
				failed = fmt.Errorf("checkpoint %d: %w", seq, err)
				break
			}
			done.Seq = seq
			if gtid != "" {
				done.GTID = gtid
			}
			if err := writeApplied(appliedPath, done); err != nil {
				failed = err
				break
			}
			os.Remove(filepath.Join(dir, wire.CheckpointName(seq)))
			a.mu.Lock()
			a.done = done
			a.mu.Unlock()
		}
		if failed == nil && a.cfg.MergeIntervalMs > 0 {
			if err := a.mergeWhenDue(ctx, db, done); err != nil {
				failed = fmt.Errorf("merge of the staging: %w", err)
			}
		}
		a.setErr(failed)
	}
}

// applyOne applies one checkpoint and returns its GTID: to the destination, or to the
// staging in one InnoDB transaction.
func (a *applier) applyOne(ctx context.Context, db *sqlx.DB, path string) (string, error) {
	m, files, err := readCheckpoint(path)
	if err != nil {
		return "", err
	}
	if len(m.Tables) == 0 && len(m.Packs) == 0 {
		return m.GTID, nil
	}
	conn, err := a.session(ctx, db)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if len(m.Packs) > 0 {
		// the unpack: each index's micro checkpoints join the checkpoint's tables
		for _, meta := range a.indexes {
			tables, f, err := unpack(ctx, a.src, conn, meta, m, files, a.cfg.MergeIntervalMs > 0)
			if err != nil {
				return "", err
			}
			m.Tables = append(m.Tables, tables...)
			for k, v := range f {
				files[k] = v
			}
		}
		if len(m.Tables) == 0 {
			return m.GTID, nil
		}
	}
	name := func(t wire.TableFiles) string {
		return fmt.Sprintf("replplugin-%d-%d-%s.%s", os.Getpid(), m.Seq, t.Schema, t.Table)
	}
	if a.cfg.MergeIntervalMs > 0 {
		return m.GTID, stage(ctx, conn, a.dest, m, files, name)
	}
	p, transactional := a.dest.(Positioner)
	if transactional {
		if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
			return "", err
		}
	}
	for _, t := range m.Tables {
		if q, err := a.dest.Apply(ctx, conn, t.Schema, t.Table, t, files, name(t), nil); err != nil {
			if transactional {
				conn.ExecContext(context.Background(), "ROLLBACK")
			}
			return "", fmt.Errorf("%w (%s)", err, q)
		}
	}
	if transactional {
		if err := p.SavePosition(ctx, conn, a.plugin(), wire.Applied{Seq: m.Seq, GTID: m.GTID}); err != nil {
			conn.ExecContext(context.Background(), "ROLLBACK")
			return "", fmt.Errorf("the position in the destination: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return "", err
		}
	}
	return m.GTID, nil
}

// plugin is the plugin's name, its TOML's: what a Positioner keys its position by.
func (a *applier) plugin() string {
	return strings.TrimSuffix(filepath.Base(a.path), filepath.Ext(a.path))
}

// position reads the position a Positioner destination committed.
func (a *applier) position(ctx context.Context, db *sqlx.DB, p Positioner) (wire.Applied, error) {
	conn, err := a.session(ctx, db)
	if err != nil {
		return wire.Applied{}, err
	}
	defer conn.Close()
	pos, err := p.Position(ctx, conn, a.plugin())
	if err != nil {
		return pos, fmt.Errorf("the position in the destination: %w", err)
	}
	return pos, nil
}

// initIndexes runs the init of each index not done yet (applied.json's Indexes, by indexID):
// its denormalized table filled from the query on a consistent snapshot of the source, directly
// in the destination (the accumulation is for the changes). An index left out of the config is
// forgotten, so put back it starts again from its init; a changed query or table is a new
// index, its init runs again.
func (a *applier) initIndexes(ctx context.Context, db *sqlx.DB, done *wire.Applied, appliedPath string) error {
	var kept []string
	for _, id := range done.Indexes {
		for _, m := range a.indexes {
			if indexID(m.idx) == id {
				kept = append(kept, id)
			}
		}
	}
	for _, meta := range a.indexes {
		if slices.Contains(kept, indexID(meta.idx)) {
			continue
		}
		conn, err := a.session(ctx, db)
		if err != nil {
			return err
		}
		err = initIndex(ctx, a.src, db, conn, a.dest, meta, func(t wire.TableFiles, files map[string][]byte) error {
			q, err := a.dest.Apply(ctx, conn, t.Schema, t.Table, t, files, fmt.Sprintf("replplugin-%d-%s", os.Getpid(), t.Inserts), nil)
			if err != nil {
				return fmt.Errorf("%w (%s)", err, q)
			}
			return nil
		})
		conn.Close()
		if err != nil {
			return fmt.Errorf("init of index %s: %w", meta.idx.Name, err)
		}
		kept = append(kept, indexID(meta.idx))
	}
	if !slices.Equal(kept, done.Indexes) {
		done.Indexes = kept
		if err := writeApplied(appliedPath, *done); err != nil {
			return err
		}
		a.mu.Lock()
		a.done = *done
		a.mu.Unlock()
	}
	return nil
}

// mergeWhenDue merges the staging once the merge interval passed since the last merge (the
// first counted from the first call); done is what the staging holds.
func (a *applier) mergeWhenDue(ctx context.Context, db *sqlx.DB, done wire.Applied) error {
	path := wire.MergedPath(a.path)
	last, err := readMerged(path)
	if err != nil {
		return err
	}
	if last == nil {
		last = &merged{At: time.Now()}
		if err := writeMerged(path, *last); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.last = last
	a.mu.Unlock()
	if time.Since(last.At) < time.Duration(a.cfg.MergeIntervalMs)*time.Millisecond {
		return nil
	}
	next := merged{Seq: last.Seq, GTID: last.GTID, At: time.Now()}
	if done.Seq != last.Seq {
		start := time.Now()
		if err := a.merge(ctx, db); err != nil {
			return err
		}
		next = merged{Seq: done.Seq, GTID: done.GTID, At: time.Now(), TookMs: time.Since(start).Milliseconds()}
	}
	a.mu.Lock()
	a.last = &next
	a.mu.Unlock()
	return writeMerged(path, next)
}

// merge merges the staging of every carried table (the State's) into the destination.
func (a *applier) merge(ctx context.Context, db *sqlx.DB) error {
	st, err := wire.ReadState(wire.StatePath(a.path))
	if err != nil {
		return err
	}
	conn, err := a.session(ctx, db)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader, err := db.Connx(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	// the carried tables and every table the staging holds (a table no longer carried keeps
	// its staged rows until they are merged)
	tables, err := stagedTables(ctx, reader, st.Tables)
	if err != nil {
		return err
	}
	return mergeAll(ctx, conn, reader, tables)
}

func readApplied(path string) (wire.Applied, error) {
	var a wire.Applied
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}

func writeApplied(path string, a wire.Applied) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// nextSeqBase is the last checkpoint number used: the State's, the applied one's, or a file's
// still waiting, the highest.
func nextSeqBase(stateSeq int64, appliedPath, dir string) int64 {
	seq := stateSeq
	if a, err := readApplied(appliedPath); err == nil && a.Seq > seq {
		seq = a.Seq
	}
	for _, s := range pendingCheckpoints(dir, seq) {
		if s > seq {
			seq = s
		}
	}
	return seq
}

// pendingCheckpoints are the seqs of the checkpoint files after after, in order.
func pendingCheckpoints(dir string, after int64) []int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var seqs []int64
	for _, e := range entries {
		if seq, ok := wire.ParseCheckpointName(e.Name()); ok && seq > after {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

// readCheckpoint reads a checkpoint's manifest and files.
func readCheckpoint(path string) (wire.Manifest, map[string][]byte, error) {
	var m wire.Manifest
	f, err := os.Open(path)
	if err != nil {
		return m, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReaderN(f, 1<<20, runtime.NumCPU())
	if err != nil {
		return m, nil, err
	}
	defer gz.Close()
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return m, nil, err
		}
		files[h.Name] = b
	}
	mb, ok := files[wire.ManifestName]
	if !ok {
		return m, nil, fmt.Errorf("no %s", wire.ManifestName)
	}
	if err := json.Unmarshal(mb, &m); err != nil {
		return m, nil, err
	}
	// wire 1 wrote the same checkpoints (replication-manager applied them): kept after an upgrade
	if m.Wire != 1 && m.Wire != wire.WireVersion {
		return m, nil, fmt.Errorf("checkpoint of wire %d, this plugin wire %d", m.Wire, wire.WireVersion)
	}
	return m, files, nil
}
