// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

// slowFS reads "y" into its buffer, after release closes when stuck; writes keep the data
// they were given, to check it is a copy
type slowFS struct {
	fuseutil.NotImplementedFileSystem
	stuck   atomic.Bool
	release chan struct{}
	written atomic.Value // []byte
}

func (f *slowFS) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) error {
	if f.stuck.Load() {
		<-f.release
	}
	for i := range op.Dst {
		op.Dst[i] = 'y'
	}
	op.BytesRead = len(op.Dst)
	return nil
}

// SetConnection is part of the fork's interface
func (f *slowFS) SetConnection(*fuse.Connection) {}

func (f *slowFS) WriteFile(ctx context.Context, op *fuseops.WriteFileOp) error {
	<-f.release
	f.written.Store(op.Data)
	return nil
}

// #2091: a request stuck on S3 is answered EIO at the deadline, reported late, and never
// writes into the FUSE server's buffers afterwards; a request in time is copied back whole
func TestDeadlineFS(t *testing.T) {
	var late atomic.Int32
	inner := &slowFS{release: make(chan struct{})}
	inner.stuck.Store(true)
	d := DeadlineFS{Fs: inner, Timeout: 100 * time.Millisecond, OnLate: func(string) { late.Add(1) }}

	buf := bytes.Repeat([]byte{'x'}, 8)
	start := time.Now()
	err := d.ReadFile(context.Background(), &fuseops.ReadFileOp{Dst: buf})
	if err != fuse.EIO || time.Since(start) > time.Second || late.Load() != 1 {
		t.Fatalf("a stuck read: err %v after %s, late %d", err, time.Since(start), late.Load())
	}
	close(inner.release) // the late read now runs, on its own buffer
	time.Sleep(50 * time.Millisecond)
	if !bytes.Equal(buf, bytes.Repeat([]byte{'x'}, 8)) {
		t.Fatalf("a late read wrote into the caller's buffer: %q", buf)
	}

	inner.stuck.Store(false)
	op := &fuseops.ReadFileOp{Dst: make([]byte, 4)}
	if err := d.ReadFile(context.Background(), op); err != nil || op.BytesRead != 4 || string(op.Dst) != "yyyy" {
		t.Fatalf("a read in time: %v %d %q", err, op.BytesRead, op.Dst)
	}

	// a late write keeps its own copy of the data, the caller's buffer can be reused at once
	inner2 := &slowFS{release: make(chan struct{})}
	d.Fs = inner2
	data := []byte("backup")
	if err := d.WriteFile(context.Background(), &fuseops.WriteFileOp{Data: data}); err != fuse.EIO {
		t.Fatalf("a stuck write: %v", err)
	}
	copy(data, "XXXXXX") // the FUSE server reuses its buffer
	close(inner2.release)
	time.Sleep(50 * time.Millisecond)
	if got, _ := inner2.written.Load().([]byte); string(got) != "backup" {
		t.Fatalf("the late write saw %q, want its own copy", got)
	}

	// a late vectored read gives its buffers back (its Callback) once it ends
	freed := make(chan struct{})
	inner3 := &vectorFS{release: make(chan struct{}), freed: freed}
	d.Fs = inner3
	if err := d.ReadFile(context.Background(), &fuseops.ReadFileOp{Size: 4}); err != fuse.EIO {
		t.Fatalf("a stuck vectored read: %v", err)
	}
	close(inner3.release)
	select {
	case <-freed:
	case <-time.After(time.Second):
		t.Fatal("the late read's buffers were never released")
	}
}

// vectorFS answers a vectored read with its own buffers, freed by the Callback
type vectorFS struct {
	fuseutil.NotImplementedFileSystem
	release chan struct{}
	freed   chan struct{}
}

func (f *vectorFS) SetConnection(*fuse.Connection) {}

func (f *vectorFS) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) error {
	<-f.release
	op.Data = [][]byte{[]byte("data")}
	op.BytesRead = 4
	op.Callback = func() { close(f.freed) }
	return nil
}
