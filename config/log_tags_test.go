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
