// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #2091: a backup directory not at its latest snapshot locks its backups; the lock survives a
// restart, and a snapback running when replication-manager stopped reads as failed
func TestSnapbackLock(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	const srv = "db1:3306"
	if err := cl.snapbackLock(srv); err != nil {
		t.Fatalf("no snapback, no lock: %v", err)
	}
	cl.setSnapback(srv, &SnapbackState{Server: srv, Archive: "archive-local-1", Snapshot: "aaaaaaaa11", Head: "bbbbbbbb22", Status: SnapbackDone})
	if err := cl.snapbackLock(srv); !errors.Is(err, ErrSnapbackLocked) {
		t.Fatalf("at an older snapshot: %v", err)
	}
	if err := cl.snapbackLock("db2:3306"); err != nil {
		t.Fatalf("another server is not locked: %v", err)
	}

	// read again by a new process
	cl2 := newCatalogueTestCluster(t)
	cl2.WorkingDir = cl.WorkingDir
	if err := cl2.snapbackLock(srv); !errors.Is(err, ErrSnapbackLocked) {
		t.Fatalf("the lock is kept across a restart: %v", err)
	}

	cl.setSnapback(srv, &SnapbackState{Server: srv, Snapshot: "aaaaaaaa11", Head: "bbbbbbbb22", Status: SnapbackRunning})
	cl3 := newCatalogueTestCluster(t)
	cl3.WorkingDir = cl.WorkingDir
	if s := cl3.snapback(srv); s == nil || s.Status != SnapbackFailed {
		t.Fatalf("a snapback interrupted by a stop reads as failed: %+v", s)
	}

	cl.setSnapback(srv, &SnapbackState{Server: srv, Snapshot: "bbbbbbbb22", Head: "bbbbbbbb22", Status: SnapbackDone})
	if err := cl.snapbackLock(srv); err != nil {
		t.Fatalf("back at its latest snapshot: %v", err)
	}
	cl.setSnapback(srv, nil)
	if len(cl.Snapbacks()) != 0 {
		t.Fatal("a released snapback leaves the list")
	}
}

// #2091: a snapback needs an attached archive
func TestSnapbackRefused(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.Conf.BackupArchiveName = ""
	if _, err := cl.Snapback("aaaaaaaa"); !errors.Is(err, ErrSnapbackRefused) {
		t.Fatalf("no archive attached: %v", err)
	}
}

// #2091: the restored content moves into the backup directory whole; emptying a directory
// keeps the directory
func TestSnapbackMoveContent(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(src, "mydumper.1"), 0750)
	os.WriteFile(filepath.Join(src, "mydumper.1", "metadata"), []byte("m"), 0640)
	os.WriteFile(filepath.Join(src, "mydumper.1.meta.json"), []byte("{}"), 0640)
	if err := moveContent(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "mydumper.1", "metadata")); err != nil || string(b) != "m" {
		t.Fatalf("moved folder: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "mydumper.1.meta.json")); err != nil {
		t.Fatalf("moved file: %v", err)
	}
	if err := copyTree(filepath.Join(dst, "mydumper.1"), filepath.Join(src, "copy")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "copy", "metadata")); string(b) != "m" {
		t.Fatalf("copied tree: %q", b)
	}
	if err := emptyDir(dst); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dst); err != nil || len(entries) != 0 {
		t.Fatalf("emptied: %v %v", entries, err)
	}
}
