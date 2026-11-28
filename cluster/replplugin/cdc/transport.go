// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"archive/tar"
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
)

// The transport is the one seam between the capture and the apply (CDC_CORE.md "The
// transport"): capture (the init's row stream, the binlog delta) -> micro checkpoint (the
// tar.gz: its manifest, sequence and GTID, the keys to delete, the rows to load) -> transport
// -> apply (the destination). The apply reads nothing of the capture but what the transport
// gives: the checkpoints, in sequence order. A checkpoint is written as a stream, never as a
// file path: an init streams each part into the transport while it reads the dump's chunks or
// the SELECT's ranges, a part held in memory up to its bound (T18), so a transport to an
// object store (S3, a later step: a CDC that travels across replication-manager clusters)
// needs no local spool. The local-disk queue below is the only transport today.
type Transport interface {
	// Create opens checkpoint seq: the tar.gz is written to the stream, Close makes it
	// visible whole, Abort drops it. It waits while the transport holds more than its bound
	// (the capture pauses: F3, never a dropped change)
	Create(seq int64) (CheckpointStream, error)
	// Pending lists the checkpoints after seq after, in order
	Pending(after int64) []int64
	// Open reads checkpoint seq
	Open(seq int64) (io.ReadCloser, error)
	// Ack: checkpoint seq is applied, the transport may free it
	Ack(seq int64) error
	// PendingBytes is what waits to be applied
	PendingBytes() int64
	// Clear drops every checkpoint waiting: those of an initial load that did not finish
	Clear() error
}

// CheckpointStream is one checkpoint being written into a transport.
type CheckpointStream interface {
	io.Writer
	Close() error
	Abort()
}

// publish writes checkpoint m with its files into tr.
func publish(tr Transport, m wire.Manifest, files map[string][]byte) error {
	w, err := tr.Create(m.Seq)
	if err != nil {
		return err
	}
	if err := encodeCheckpoint(w, m, files); err != nil {
		w.Abort()
		return err
	}
	return w.Close()
}

// readSeq reads checkpoint seq of tr.
func readSeq(tr Transport, seq int64) (wire.Manifest, map[string][]byte, error) {
	r, err := tr.Open(seq)
	if err != nil {
		return wire.Manifest{}, nil, err
	}
	defer r.Close()
	return decodeCheckpoint(r)
}

// encodeCheckpoint writes the tar.gz of m and its files to w, the manifest last.
func encodeCheckpoint(w io.Writer, m wire.Manifest, files map[string][]byte) error {
	m.Wire = wire.WireVersion
	// parallel gzip, 1 MB blocks over the CPUs: the gzip of a big period (the restore's
	// rows) was the slow part of a checkpoint
	gz := gzip.NewWriter(w)
	if err := gz.SetConcurrency(1<<20, runtime.NumCPU()); err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	for _, n := range names {
		if err := add(n, files[n]); err != nil {
			return err
		}
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := add(wire.ManifestName, mb); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// decodeCheckpoint reads a checkpoint's manifest and files.
func decodeCheckpoint(r io.Reader) (wire.Manifest, map[string][]byte, error) {
	var m wire.Manifest
	gz, err := gzip.NewReaderN(r, 1<<20, runtime.NumCPU())
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

// diskQueue is the local-disk transport: the checkpoints in dir (<name>.checkpoints next to
// the plugin's TOML), each written to a temporary file renamed whole, the writer waiting while
// those not yet applied exceed maxPending.
type diskQueue struct {
	dir        string
	maxPending int64
	paused     func(pending int64) // told while it waits, 0 when it ends; called with the capture's lock held: it must not take it
}

func (w *diskQueue) waitRoom() {
	for waited := false; ; waited = true {
		p := wire.PendingBytes(w.dir)
		if p <= w.maxPending {
			if waited && w.paused != nil {
				w.paused(0) // room again: the pause ends
			}
			return
		}
		if w.paused != nil {
			w.paused(p)
		}
		time.Sleep(time.Second)
	}
}

type diskStream struct {
	f          *os.File
	tmp, final string
}

func (s *diskStream) Write(b []byte) (int, error) { return s.f.Write(b) }
func (s *diskStream) Abort()                      { s.f.Close(); os.Remove(s.tmp) }
func (s *diskStream) Close() error {
	if err := s.f.Close(); err != nil {
		os.Remove(s.tmp)
		return err
	}
	return os.Rename(s.tmp, s.final)
}

func (w *diskQueue) Create(seq int64) (CheckpointStream, error) {
	w.waitRoom()
	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return nil, err
	}
	final := filepath.Join(w.dir, wire.CheckpointName(seq))
	f, err := os.OpenFile(final+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}
	return &diskStream{f: f, tmp: final + ".tmp", final: final}, nil
}

func (w *diskQueue) Pending(after int64) []int64 { return pendingCheckpoints(w.dir, after) }

func (w *diskQueue) Open(seq int64) (io.ReadCloser, error) {
	return os.Open(filepath.Join(w.dir, wire.CheckpointName(seq)))
}

func (w *diskQueue) Ack(seq int64) error {
	err := os.Remove(filepath.Join(w.dir, wire.CheckpointName(seq)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (w *diskQueue) PendingBytes() int64 { return wire.PendingBytes(w.dir) }

func (w *diskQueue) Clear() error {
	entries, _ := os.ReadDir(w.dir)
	for _, e := range entries {
		if _, ok := wire.ParseCheckpointName(e.Name()); ok {
			os.Remove(filepath.Join(w.dir, e.Name()))
		}
	}
	return nil
}

// pendingCheckpoints are the seqs of the checkpoint files of dir after after, in order.
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
