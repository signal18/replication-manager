// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"testing"
	"time"

	"github.com/PlakarKorp/kloset/locate"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

func TestParseRetention(t *testing.T) {
	r, err := parseRetention("2y5m7d3h")
	if err != nil {
		t.Fatal(err)
	}
	if r != (retention{years: 2, months: 5, days: 7, hours: 3}) {
		t.Fatalf("got %+v", r)
	}
	if r, _ := parseRetention("2w"); r.days != 14 {
		t.Fatalf("2w: got %+v", r)
	}
	for _, bad := range []string{"", "7", "d", "7x", "-1d", "7d "} {
		if _, err := parseRetention(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// items are one snapshot an hour for n hours back from ref, newest first.
func hourlyItems(ref time.Time, n int) []locate.Item {
	items := make([]locate.Item, n)
	for i := range items {
		var id objects.MAC
		id[0], id[1] = byte(i), byte(i>>8)
		items[i] = locate.Item{ItemID: id, Timestamp: ref.Add(-time.Duration(i) * time.Hour)}
	}
	return items
}

func TestKeptByPolicy(t *testing.T) {
	ref := time.Date(2026, 10, 17, 12, 30, 0, 0, time.UTC)
	items := hourlyItems(ref, 24*10) // ten days, hourly

	if _, err := keptByPolicy(items, &wire.Policy{}); err == nil {
		t.Fatal("empty policy: no error")
	}

	kept, err := keptByPolicy(items, &wire.Policy{KeepLast: 3})
	if err != nil || len(kept) != 3 {
		t.Fatalf("keepLast 3: %d kept, %v", len(kept), err)
	}

	// one per calendar day over the last 3 days (ref's day included)
	kept, err = keptByPolicy(items, &wire.Policy{KeepDaily: 3})
	if err != nil || len(kept) != 3 {
		t.Fatalf("keepDaily 3: %d kept, %v", len(kept), err)
	}

	// everything of the last 5 hours, ref included
	kept, err = keptByPolicy(items, &wire.Policy{KeepWithin: "5h"})
	if err != nil || len(kept) != 6 {
		t.Fatalf("keepWithin 5h: %d kept, %v", len(kept), err)
	}

	// the window is relative to the newest snapshot, not now: old archives keep theirs
	old := hourlyItems(ref.AddDate(-1, 0, 0), 48)
	kept, err = keptByPolicy(old, &wire.Policy{KeepDaily: 1})
	if err != nil || len(kept) != 1 {
		t.Fatalf("old keepDaily 1: %d kept, %v", len(kept), err)
	}
}
