// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const sampleMountinfo = `3593 5649 0:356 / /var/lib/replication-manager/backups/dev3 rw,nosuid,nodev,relatime - fuse.geesefs repman:2/signal18/dev3 rw,user_id=103,group_id=103
3594 5649 0:357 / /var/lib/replication-manager/backups/my\040cluster rw - fuse.geesefs b rw
3595 5649 0:358 / /mnt/other rw - fuse.sshfs h rw
3596 5649 8:1 / /var/lib/replication-manager rw - ext4 /dev/sda1 rw
3597 5649 0:359 / /var/lib/replication-manager-old rw - fuse.geesefs b rw`

// The FUSE mounts under the working directory, with their connection, a mount point with a space
// decoded; neither another file system nor a FUSE mount elsewhere nor a sibling directory
func TestFuseMountsUnder(t *testing.T) {
	got := fuseMountsUnder(sampleMountinfo, "/var/lib/replication-manager/")
	want := []fuseMount{
		{point: "/var/lib/replication-manager/backups/dev3", conn: "356"},
		{point: "/var/lib/replication-manager/backups/my cluster", conn: "357"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts %+v, want %+v", got, want)
	}
}

// Only a connection whose requests stay waiting at both reads is aborted: one with nothing
// waiting, or answered meanwhile, is a live server
func TestAbortStuckOnlyWaitingTwice(t *testing.T) {
	mounts := []fuseMount{{point: "/nonexistent/a", conn: "1"}, {point: "/nonexistent/b", conn: "2"}, {point: "/nonexistent/c", conn: "3"}}
	reads := map[string][]int{"1": {2, 2}, "2": {0, 0}, "3": {1, 0}}
	waiting := func(conn string) (int, error) {
		r := reads[conn]
		n := r[0]
		reads[conn] = r[1:]
		return n, nil
	}
	var aborted []string
	abort := func(conn string) error { aborted = append(aborted, conn); return nil }
	got, err := abortStuck(mounts, time.Millisecond, waiting, abort)
	if err != nil || !reflect.DeepEqual(got, []string{"/nonexistent/a"}) || !reflect.DeepEqual(aborted, []string{"1"}) {
		t.Fatalf("aborted %v (connections %v), err %v; want only /nonexistent/a", got, aborted, err)
	}

	// nothing waiting: no second read, no wait
	calls := 0
	none := func(string) (int, error) { calls++; return 0, nil }
	if got, err := abortStuck(mounts, time.Hour, none, abort); got != nil || err != nil || calls != len(mounts) {
		t.Fatalf("idle mounts: %v %v, %d reads", got, err, calls)
	}

	// no write access to fusectl: said so
	stuckReads := func(string) (int, error) { return 1, nil }
	refused := func(string) error { return errors.New("read-only file system") }
	if _, err := abortStuck(mounts[:1], time.Millisecond, stuckReads, refused); err == nil {
		t.Fatal("a refused abort is not reported")
	}
}
