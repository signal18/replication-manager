// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The checkpoints a replication plugin writes are applied here (#2015), in order, to its
// target: for each table the keys to delete (in batches), then the rows with LOAD DATA
// LOCAL INFILE from memory, ColumnStore's bulk path. Each one applied is recorded in
// <name>.applied.json and its file removed, which frees the room the plugin waits for.
// A checkpoint that fails stays and is retried; the plugin pauses when they pile up.

const (
	replApplyEvery     = 2 * time.Second
	replApplyKeysBatch = 200
)

// applied is what a runner applied, for its state.
type replApplied struct {
	wire.Applied
	Err string
}

// applyReplCheckpoints runs until ctx ends: every replApplyEvery, the checkpoints after
// the last applied one, in order, stopping at the first failure.
func (cluster *Cluster) applyReplCheckpoints(ctx context.Context, r *replPluginRunner) {
	defer func() {
		if p := recover(); p != nil {
			r.applied.Store(&replApplied{Err: fmt.Sprintf("panic in the applier: %v", p)})
		}
	}()
	dir, appliedPath := wire.CheckpointDir(r.path), wire.AppliedPath(r.path)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(replApplyEvery):
		}
		done, err := readApplied(appliedPath)
		if err != nil {
			r.applied.Store(&replApplied{Err: err.Error()})
			continue
		}
		pending := pendingCheckpoints(dir, done.Seq)
		cur := &replApplied{Applied: done}
		for _, seq := range pending {
			if ctx.Err() != nil {
				return
			}
			gtid, err := cluster.applyReplCheckpoint(ctx, r, filepath.Join(dir, wire.CheckpointName(seq)))
			if err != nil {
				cur.Err = fmt.Sprintf("checkpoint %d: %s", seq, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication plugin %s of %s: %s", r.cfg.Plugin, cluster.Name, cur.Err)
				break
			}
			cur.Seq, cur.Err = seq, ""
			if gtid != "" {
				cur.GTID = gtid
			}
			if err := writeApplied(appliedPath, cur.Applied); err != nil {
				cur.Err = err.Error()
				break
			}
			os.Remove(filepath.Join(dir, wire.CheckpointName(seq)))
		}
		r.applied.Store(cur)
	}
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
	// parallel gunzip (read-ahead blocks decompressed while the tar is read), as the
	// splitdump restore
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
	if m.Wire != wire.WireVersion {
		return m, nil, fmt.Errorf("checkpoint of wire %d, this replication-manager wire %d", m.Wire, wire.WireVersion)
	}
	return m, files, nil
}

// applyReplCheckpoint applies one checkpoint to the target server and returns its GTID.
func (cluster *Cluster) applyReplCheckpoint(ctx context.Context, r *replPluginRunner, path string) (string, error) {
	m, files, err := readCheckpoint(path)
	if err != nil {
		return "", err
	}
	if len(m.Tables) == 0 {
		return m.GTID, nil
	}
	target := cluster.GetServerFromURL(fmt.Sprintf("%s:%d", r.cfg.Target.Host, r.cfg.Target.Port))
	if target == nil {
		return "", fmt.Errorf("target %s:%d is not a server of %s", r.cfg.Target.Host, r.cfg.Target.Port, cluster.Name)
	}
	db, err := target.GetNewDBConn()
	if err != nil {
		return "", err
	}
	defer db.Close()
	conn, err := db.Connx(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	logSQL := func(q string, err error) {
		cluster.LogSQL(q, err, target.URL, "ReplicationPlugin", config.LvlErr, "Replication plugin %s of %s, checkpoint %d: %s", r.cfg.Plugin, cluster.Name, m.Seq, err)
	}
	for _, t := range m.Tables {
		if t.Deletes != "" {
			var batch [][]interface{}
			flush := func() error {
				q, err := dbhelper.DeleteRowsByKey(ctx, conn, t.Schema, t.Table, t.KeyColumns, batch)
				if err != nil {
					logSQL(q, err)
				}
				batch = batch[:0]
				return err
			}
			sc := bufio.NewScanner(bytes.NewReader(files[t.Deletes]))
			sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
			for sc.Scan() {
				fields := wire.DecodeTSV(sc.Bytes())
				key := make([]interface{}, len(fields))
				for i, f := range fields {
					if f != nil {
						key[i] = f
					}
				}
				batch = append(batch, key)
				if len(batch) >= replApplyKeysBatch {
					if err := flush(); err != nil {
						return "", fmt.Errorf("delete in %s.%s: %w", t.Schema, t.Table, err)
					}
				}
			}
			if err := sc.Err(); err != nil {
				return "", err
			}
			if len(batch) > 0 {
				if err := flush(); err != nil {
					return "", fmt.Errorf("delete in %s.%s: %w", t.Schema, t.Table, err)
				}
			}
		}
		if t.Inserts != "" {
			name := fmt.Sprintf("replplugin-%s-%d-%s.%s", cluster.Name, m.Seq, t.Schema, t.Table)
			q, err := dbhelper.LoadDataLocal(ctx, conn, t.Schema, t.Table, name, files[t.Inserts])
			if err != nil {
				logSQL(q, err)
				return "", fmt.Errorf("load into %s.%s: %w", t.Schema, t.Table, err)
			}
		}
	}
	return m.GTID, nil
}
