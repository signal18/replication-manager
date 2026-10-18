// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newRegisteredBackupTest registers a dump of the server's backup folder (#2108).
func newRegisteredBackupTest(t *testing.T) (*Cluster, *ServerMonitor, *backupmgr.BackupMetadata) {
	t.Helper()
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return c, server, m
}

// An archive push running alone (restic task queue) reads the artifacts: the discovery and a
// registration go on; the change check still waits for it (a push with
// backup-archive-remove-original removes its backup's file), and so does a reseed.
func TestDiscoveryAndRegisterDuringArchivePush(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	dir := server.GetMyBackupDirectory()
	other := filepath.Join(dir, "other.sql.gz")
	writeDiscoveryGzip(t, other, discoveryDump+discoveryDumpFooter)
	c.SetInResticLogicalBackupState(true)
	defer c.SetInResticLogicalBackupState(false)
	if cands, err := server.DiscoverBackups(); err != nil || len(cands) != 1 {
		t.Fatalf("discovery during a push: %d, %v", len(cands), err)
	}
	if _, err := registerAndWait(t, c, server, other, time.Time{}); err != nil {
		t.Fatalf("registration during a push: %v", err)
	}
	if !c.IsInBackup() {
		t.Fatal("a push no longer counts as a backup for the other guards")
	}
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("reseed during a push: %v", err)
	}
}
