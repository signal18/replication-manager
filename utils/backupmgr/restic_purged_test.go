// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package backupmgr

import (
	"reflect"
	"sync"
	"testing"
)

// #2091: the snapshots a purge task deleted are the ones its fetch no longer lists; a list
// unchanged reports none
func TestNotifyPurgedReportsTheSnapshotsGone(t *testing.T) {
	repo := &ResticManager{Mutex: &sync.Mutex{}}
	repo.Backups = []BackupSnapshot{{Id: "a"}, {Id: "b"}, {Id: "c"}}
	var got []string
	repo.OnSnapshotsPurged = func(ids []string) { got = ids }
	before := repo.snapshotIDs()

	repo.notifyPurged(before) // the fetch skipped: the list as it was
	if got != nil {
		t.Fatalf("reported %v with the list unchanged", got)
	}
	repo.Backups = []BackupSnapshot{{Id: "b"}, {Id: "d"}}
	repo.notifyPurged(before)
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("reported %v, want [a c]", got)
	}
}
