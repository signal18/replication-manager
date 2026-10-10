// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestGetTagsForLog(t *testing.T) {
	tests := []struct {
		module int
		want   string
	}{
		{ConstLogModGeneral, "general"},
		{ConstLogModWriterElection, "election"},
		{ConstLogModSST, "sst"},
		{ConstLogModHeartBeat, "heartbeat"},
		{ConstLogModConfigLoad, "conf"},
		{ConstLogModGit, "git"},
		{ConstLogModBackupStream, "backup"},
		{ConstLogModOrchestrator, "orchestrator"},
		{ConstLogModVault, "vault"},
		{ConstLogModTopology, "topology"},
		{ConstLogModProxy, "proxy"},
		{ConstLogModProxySQL, "proxysql"},
		{ConstLogModHAProxy, "haproxy"},
		{ConstLogModProxyJanitor, "prxjanitor"},
		{ConstLogModMaxscale, "maxscale"},
		{ConstLogModGraphite, "graphite"},
		{ConstLogModPurge, "purge"},
		{ConstLogModTask, "job"},
		{ConstLogModRestic, "restic"},
		{ConstLogModMailer, "mailer"},
		{ConstLogModSupport, "support"},
		{ConstLogModExternalScript, "externalscript"},
		{ConstLogModStats, "stats"},
		{ConstLogModSQL, "sql"},
		{ConstLogModApp, "app"},
		{ConstLogModDbErrors, "errorlog"},
		{ConstLogModDbSlowquery, "slowquery"},
		{ConstLogModDbOptimize, "optimize"},
		{ConstLogModDbAudit, "auditlog"},
		{ConstLogModPlugin, "plugin"},
		{ConstLogModMaintenance, "maintenance"},
		{ConstLogModArbitration, "arbitration"},
		{ConstLogModDbSqlErrors, "sqlerrorlog"},
		{ConstLogModUncategorized, "uncategorized"},
	}

	for _, tt := range tests {
		if got := GetTagsForLog(tt.module); got != tt.want {
			t.Errorf("GetTagsForLog(%d) = %q, want %q", tt.module, got, tt.want)
		}
	}
}

// TestModuleFromTag round-trips GetTagsForLog's own tag vocabulary back to
// module ids, since ModuleFromTag is used to reconstruct HttpMessage.Module
// from on-disk log history where only the tag string survives.
func TestModuleFromTag(t *testing.T) {
	for _, tt := range []struct {
		module int
		tag    string
	}{
		{ConstLogModGeneral, "general"},
		{ConstLogModWriterElection, "election"},
		{ConstLogModSST, "sst"},
		{ConstLogModHeartBeat, "heartbeat"},
		{ConstLogModConfigLoad, "conf"},
		{ConstLogModGit, "git"},
		{ConstLogModBackupStream, "backup"},
		{ConstLogModOrchestrator, "orchestrator"},
		{ConstLogModVault, "vault"},
		{ConstLogModTopology, "topology"},
		{ConstLogModProxy, "proxy"},
		{ConstLogModProxySQL, "proxysql"},
		{ConstLogModHAProxy, "haproxy"},
		{ConstLogModProxyJanitor, "prxjanitor"},
		{ConstLogModMaxscale, "maxscale"},
		{ConstLogModGraphite, "graphite"},
		{ConstLogModPurge, "purge"},
		{ConstLogModTask, "job"},
		{ConstLogModRestic, "restic"},
		{ConstLogModMailer, "mailer"},
		{ConstLogModSupport, "support"},
		{ConstLogModExternalScript, "externalscript"},
		{ConstLogModStats, "stats"},
		{ConstLogModSQL, "sql"},
		{ConstLogModApp, "app"},
		{ConstLogModDbErrors, "errorlog"},
		{ConstLogModDbSlowquery, "slowquery"},
		{ConstLogModDbOptimize, "optimize"},
		{ConstLogModDbAudit, "auditlog"},
		{ConstLogModDbSqlErrors, "sqlerrorlog"},
		{ConstLogModPlugin, "plugin"},
		{ConstLogModMaintenance, "maintenance"},
		{ConstLogModArbitration, "arbitration"},
		{ConstLogModUncategorized, "uncategorized"},
	} {
		if got := ModuleFromTag(tt.tag); got != tt.module {
			t.Errorf("ModuleFromTag(%q) = %d, want %d", tt.tag, got, tt.module)
		}
	}

	// Unknown/empty tags fall back to uncategorized, never to general — a
	// gap in the tag vocabulary must not silently look like a real general
	// message.
	for _, unknown := range []string{"", "not-a-real-tag"} {
		if got := ModuleFromTag(unknown); got != ConstLogModUncategorized {
			t.Errorf("ModuleFromTag(%q) = %d, want ConstLogModUncategorized (%d)", unknown, got, ConstLogModUncategorized)
		}
	}
}

// TestIsTaskLogModule pins the exact set of modules routed to a cluster's
// "task" HttpLog buffer (cluster.LogModuleWithFieldsPrintf,
// cluster/cluster_log.go, now calls IsTaskLogModule directly instead of
// duplicating this list as its own switch) — this function previously had no
// test coverage of its own at all.
func TestIsTaskLogModule(t *testing.T) {
	taskModules := map[int]bool{
		ConstLogModTask:         true,
		ConstLogModSST:          true,
		ConstLogModBackupStream: true,
		ConstLogModDbErrors:     true,
		ConstLogModDbSqlErrors:  true,
		ConstLogModDbSlowquery:  true,
		ConstLogModDbOptimize:   true,
		ConstLogModDbAudit:      true,
		ConstLogModRestic:       true,
	}
	for m := 0; m <= ConstLogModArbitration; m++ {
		want := taskModules[m]
		if got := IsTaskLogModule(m); got != want {
			t.Errorf("IsTaskLogModule(%d) = %v, want %v", m, got, want)
		}
	}
	if IsTaskLogModule(ConstLogModUncategorized) {
		t.Error("IsTaskLogModule(ConstLogModUncategorized) = true, want false")
	}
}

// TestLogModuleTagTable_CoversAllConstants guards against exactly the class
// of drift that let ConstLogModDbSqlErrors ship for a while with no
// GetTagsForLog/ModuleFromTag case: a new ConstLogMod* constant added to the
// block in config.go without a matching row in logModuleTagTable. Module ids
// are a plain, non-iota const block (0..highestKnownLogModule) that Go can't
// enumerate at runtime, so this range is a hardcoded mirror of that block —
// bump highestKnownLogModule here whenever a new ConstLogMod* constant is
// added there.
func TestLogModuleTagTable_CoversAllConstants(t *testing.T) {
	const highestKnownLogModule = ConstLogModArbitration // 32; bump alongside new ConstLogMod* constants

	seen := make(map[int]bool, len(logModuleTagTable))
	for _, e := range logModuleTagTable {
		if e.tag == "" {
			t.Errorf("module id %d has an empty tag in logModuleTagTable", e.module)
		}
		seen[e.module] = true
	}
	for m := 0; m <= highestKnownLogModule; m++ {
		if !seen[m] {
			t.Errorf("module id %d has no entry in logModuleTagTable (GetTagsForLog/ModuleFromTag/IsTaskLogModule will silently treat it as unrecognized)", m)
		}
	}
	if !seen[ConstLogModUncategorized] {
		t.Error("ConstLogModUncategorized has no entry in logModuleTagTable")
	}
}
