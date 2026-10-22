// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"
)

// A backup directory left at a snapshot (WARN0255) or a failed snapback (WARN0256) offers a
// snapback to its latest snapshot and a release; a running snapback or one back at its latest
// offers nothing.
func TestStateRemediationSnapback(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	const srv = "db1:3306"
	cl.setSnapback(srv, &SnapbackState{Server: srv, Archive: "archive-local-1", Snapshot: "aaaaaaaa11", Head: "bbbbbbbb22", Status: SnapbackDone})
	plan := cl.GetStateRemediationPlan()
	if len(plan.Remediations) != 1 {
		t.Fatalf("remediations %+v", plan.Remediations)
	}
	e := plan.Remediations[0]
	if e.ErrKey != "WARN0255" || e.Server != srv || !e.AutoFixable || len(e.Fixes) != 2 {
		t.Fatalf("entry %+v", e)
	}
	if !strings.HasSuffix(e.Fixes[0].URL, "/archive-actions/snapback/bbbbbbbb22") || e.Fixes[0].Type != "server_action" {
		t.Fatalf("snapback to the latest: %+v", e.Fixes[0])
	}
	if !strings.Contains(e.Fixes[1].URL, "/archive-actions/snapback-release/") {
		t.Fatalf("release: %+v", e.Fixes[1])
	}

	cl.setSnapback(srv, &SnapbackState{Server: srv, Archive: "archive-local-1", Snapshot: "aaaaaaaa11", Head: "bbbbbbbb22", Status: SnapbackFailed, Error: "boom"})
	if p := cl.GetStateRemediationPlan(); len(p.Remediations) != 1 || p.Remediations[0].ErrKey != "WARN0256" {
		t.Fatalf("failed snapback: %+v", p.Remediations)
	}
	for _, s := range []*SnapbackState{
		{Server: srv, Snapshot: "aaaaaaaa11", Head: "bbbbbbbb22", Status: SnapbackRunning},
		{Server: srv, Snapshot: "bbbbbbbb22", Head: "bbbbbbbb22", Status: SnapbackDone},
	} {
		cl.setSnapback(srv, s)
		if p := cl.GetStateRemediationPlan(); len(p.Remediations) != 0 {
			t.Fatalf("%s at %s: nothing to remediate, got %+v", s.Status, s.Snapshot, p.Remediations)
		}
	}
}
