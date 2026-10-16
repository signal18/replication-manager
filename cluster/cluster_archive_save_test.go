// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"testing"
)

// #2077: the save that migrates a cluster replaces its legacy archive keys with the list.
// The diff emits one event per archive setting, and none for the legacy keys leaving the
// file: a peer replaying their unset would reset the values the list now carries. The
// connection to archive-local-1 is a plain key.
func TestArchiveMigrationSaveEvents(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)

	oldData := []byte("[saved-c1]\ntitle = \"c1\" \nbackup-archive-mode = \"restic-local\"\nbackup-restic = true\nbackup-restic-local-repository = \"/data/restic\"\nfoo = \"a\"\n")
	newData := []byte("[saved-c1]\ntitle = \"c1\" \nfoo = \"a\"\nbackup-archive-name = \"archive-local-1\"\n\n[[saved-c1.archive]]\nname = \"archive-local-1\"\nplugin = \"archive-restic-local\"\nlocal-repository = \"/data/restic\"\n")

	cl.emitConfigChangeEvents(oldData, newData, "saved-c1")

	got := map[string]ConfigChangeEvent{}
	for _, ev := range readEventLog(t, cl) {
		got[ev.Key] = ev
	}
	want := map[string]string{
		"backup-archive-name":                "archive-local-1",
		"archive.archive-local-1.name":             "archive-local-1",
		"archive.archive-local-1.plugin":           "archive-restic-local",
		"archive.archive-local-1.local-repository": "/data/restic",
	}
	if len(got) != len(want) {
		t.Fatalf("events %+v, want %v", got, want)
	}
	for k, v := range want {
		if ev, ok := got[k]; !ok || ev.Action != "set" || ev.Value != v {
			t.Errorf("event %s: %+v, want set %q", k, got[k], v)
		}
	}
}

// #2077: one setting changed in one archive is one event, keyed by the archive's name
func TestArchiveSettingChangeIsOneEvent(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	base := "[saved-c1]\ntitle = \"c1\" \n\n[[saved-c1.archive]]\nname = \"archive-local-1\"\nplugin = \"archive-restic-local\"\ntags = \"%s\"\n\n[[saved-c1.archive]]\nname = \"archive-s3-1\"\nplugin = \"archive-restic-s3\"\naws-bucket = \"bk\"\n"
	oldData := []byte(fmt.Sprintf(base, "a"))
	newData := []byte(fmt.Sprintf(base, "b"))
	cl.emitConfigChangeEvents(oldData, newData, "saved-c1")
	events := readEventLog(t, cl)
	if len(events) != 1 || events[0].Key != "archive.archive-local-1.tags" || events[0].Value != "b" {
		t.Fatalf("events %+v, want one set archive.archive-local-1.tags=b", events)
	}
}
