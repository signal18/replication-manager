// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"testing"
	"time"
)

// #2091: the rules are read and written back the same way; a bad rule is refused
func TestParseArchivePurgeRules(t *testing.T) {
	rules, err := ParseArchivePurgeRules("trigger:binlogs keep-within=1d; trigger:snapback keep-last=1 keep-daily=2", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Tag != "trigger:binlogs" || rules[0].Policy.KeepWithin != "1d" || rules[1].Policy.KeepLast != 1 || rules[1].Policy.KeepDaily != 2 {
		t.Fatalf("rules %+v", rules)
	}
	if got := FormatArchivePurgeRules(rules); got != "trigger:binlogs keep-within=1d; trigger:snapback keep-last=1 keep-daily=2" {
		t.Fatalf("formatted %q", got)
	}
	for _, bad := range []string{
		"trigger:binlogs",                // keeps nothing
		"trigger:binlogs keep-last=x",    // not a count
		"trigger:binlogs keep-within=1w", // not a restic duration
		"trigger:binlogs keep-forever=1", // unknown key
		"keep-last=1",                    // no tag
		"* keep-last=1",                  // the default is backup-keep-* on the cluster
		"a keep-last=1; a keep-daily=1",  // twice
	} {
		if _, err := ParseArchivePurgeRules(bad, false); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if rules, err := ParseArchivePurgeRules("* keep-last=2", true); err != nil || rules[0].Tag != "*" {
		t.Fatalf("an archive's default rule: %v %v", rules, err)
	}
}

// #2091: an archive without purge-policies applies the cluster's; with them its own, a "*"
// rule replacing the default
func TestArchivePurgePolicies(t *testing.T) {
	conf := &Config{BackupKeepLast: 10, BackupKeepTagPolicies: "trigger:binlogs keep-within=1d"}
	rules, def, err := conf.ArchivePurgePolicies(&ArchiveConfig{Name: "a", Settings: map[string]interface{}{}})
	if err != nil || len(rules) != 1 || rules[0].Tag != "trigger:binlogs" || def.KeepLast != 10 {
		t.Fatalf("cluster's: %+v %+v %v", rules, def, err)
	}
	a := &ArchiveConfig{Name: "a", Settings: map[string]interface{}{ArchivePurgeSettingPolicies: "* keep-daily=3; trigger:snapback keep-last=1"}}
	rules, def, err = conf.ArchivePurgePolicies(a)
	if err != nil || len(rules) != 1 || rules[0].Tag != "trigger:snapback" || def.KeepDaily != 3 || def.KeepLast != 0 {
		t.Fatalf("archive's own: %+v %+v %v", rules, def, err)
	}
	a.Settings[ArchivePurgeSettingPolicies] = "trigger:snapback keep-last=1"
	if _, def, _ = conf.ArchivePurgePolicies(a); def.KeepLast != 10 {
		t.Fatalf("no * rule: the cluster's default, got %+v", def)
	}
}

// #2091: each policy counts its own snapshots: the binlog-only snapshots never push a backup
// out of keep-last
func TestKeptByPoliciesCountsApart(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var snaps []PurgeSnapshot
	// a backup every day for 5 days, 6 binlog-only snapshots between each two
	for d := 0; d < 5; d++ {
		day := now.AddDate(0, 0, -d)
		snaps = append(snaps, PurgeSnapshot{ID: fmt.Sprintf("b%d", d), Time: day, Tags: []string{"trigger:backup"}, Group: "g"})
		for h := 1; h <= 6; h++ {
			snaps = append(snaps, PurgeSnapshot{ID: fmt.Sprintf("l%d-%d", d, h), Time: day.Add(time.Duration(h) * time.Hour), Tags: []string{"trigger:binlogs"}, Group: "g"})
		}
	}
	snaps = append(snaps, PurgeSnapshot{ID: "old", Time: now.AddDate(0, 0, -30), Group: "g"}) // no trigger tag
	snaps = append(snaps, PurgeSnapshot{ID: "pinned", Time: now.AddDate(-1, 0, 0), Tags: []string{"keep"}, Group: "g"})
	snaps = append(snaps, PurgeSnapshot{ID: "other", Time: now.AddDate(0, 0, -40), Group: "h"}) // another group

	rules := []ArchivePurgeRule{{Tag: "trigger:binlogs", Policy: ArchivePurgePolicy{KeepWithin: "1d"}}}
	kept := KeptByPolicies(snaps, rules, ArchivePurgePolicy{KeepLast: 3}, []string{"keep"})

	for _, id := range []string{"b0", "b1", "b2", "pinned", "other"} {
		if !kept[id] {
			t.Errorf("%s not kept", id)
		}
	}
	for _, id := range []string{"b3", "b4", "old"} {
		if kept[id] {
			t.Errorf("%s kept beyond keep-last 3 of the backups", id)
		}
	}
	// the binlog-only ones within a day of the newest of them (day 0, 18:00): day 0's and day 1's from 18:00
	for id, want := range map[string]bool{"l0-1": true, "l0-6": true, "l1-6": true, "l1-5": false, "l2-6": false} {
		if kept[id] != want {
			t.Errorf("%s kept %v, want %v", id, kept[id], want)
		}
	}
}

// #2091: the periods count as restic forget: the newest snapshot of each of the last N periods
// holding one, keep-within-<period> every period within the duration of the newest
func TestKeptByPolicyPeriods(t *testing.T) {
	now := time.Date(2026, 10, 21, 23, 0, 0, 0, time.UTC)
	var snaps []PurgeSnapshot
	for d := 0; d < 10; d++ {
		if d == 3 {
			continue // a day with no snapshot is not a period
		}
		for _, h := range []int{0, 12} {
			snaps = append(snaps, PurgeSnapshot{ID: fmt.Sprintf("d%d-%d", d, h), Time: now.AddDate(0, 0, -d).Add(-time.Duration(h) * time.Hour)})
		}
	}
	kept := KeptByPolicies(snaps, nil, ArchivePurgePolicy{KeepDaily: 4}, nil)
	for id, want := range map[string]bool{"d0-0": true, "d0-12": false, "d1-0": true, "d2-0": true, "d4-0": true, "d5-0": false} {
		if kept[id] != want {
			t.Errorf("daily %s kept %v, want %v", id, kept[id], want)
		}
	}
	kept = KeptByPolicies(snaps, nil, ArchivePurgePolicy{KeepWithinDaily: "2d"}, nil)
	for id, want := range map[string]bool{"d0-0": true, "d1-0": true, "d2-0": true, "d2-12": false, "d4-0": false} {
		if kept[id] != want {
			t.Errorf("within-daily %s kept %v, want %v", id, kept[id], want)
		}
	}
	// an empty default keeps what it takes
	if kept = KeptByPolicies(snaps, nil, ArchivePurgePolicy{}, nil); len(kept) != len(snaps) {
		t.Fatalf("an empty policy deleted %d", len(snaps)-len(kept))
	}
}

// #2091: keeping one binlog-only snapshot is useless: under the trigger:binlogs policy, those
// taken since the last backup of their group are always kept, the older ones by the policy
func TestKeptByPoliciesBinlogsSinceLastBackup(t *testing.T) {
	backup := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	snaps := []PurgeSnapshot{
		{ID: "b-prev", Time: backup.AddDate(0, 0, -1), Tags: []string{"trigger:backup"}, Group: "g"},
		{ID: "l-before", Time: backup.Add(-time.Hour), Tags: []string{ArchivePurgeBinlogsTag}, Group: "g"},
		{ID: "b-last", Time: backup, Tags: []string{"trigger:backup"}, Group: "g"},
	}
	for h := 1; h <= 5; h++ {
		snaps = append(snaps, PurgeSnapshot{ID: fmt.Sprintf("l%d", h), Time: backup.Add(time.Duration(h) * time.Hour), Tags: []string{ArchivePurgeBinlogsTag}, Group: "g"})
	}
	rules := []ArchivePurgeRule{{Tag: ArchivePurgeBinlogsTag, Policy: ArchivePurgePolicy{KeepLast: 1}}}
	kept := KeptByPolicies(snaps, rules, ArchivePurgePolicy{KeepLast: 2}, nil)
	for h := 1; h <= 5; h++ {
		if !kept[fmt.Sprintf("l%d", h)] {
			t.Errorf("l%d, taken since the last backup, not kept", h)
		}
	}
	if kept["l-before"] {
		t.Error("a binlog-only snapshot before the last backup kept beyond its policy")
	}
	if !kept["b-prev"] || !kept["b-last"] {
		t.Error("backups not kept")
	}
}
