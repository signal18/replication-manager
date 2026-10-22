// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2091: the retention policy is the cluster's; the plugin is asked to apply it, and its
// manifest says how it purges (its purge-* settings, grouped)
func TestArchivePurgePolicy(t *testing.T) {
	conf := &Config{BackupKeepLast: 3, BackupKeepWithin: " 30d "}
	p := conf.ArchivePurgePolicy()
	if p.KeepLast != 3 || p.KeepWithin != "30d" || p.IsEmpty() || p.Validate() != nil {
		t.Fatalf("policy %+v", p)
	}
	if !(ArchivePurgePolicy{}).IsEmpty() {
		t.Fatal("an empty policy must never be applied")
	}
	if (ArchivePurgePolicy{KeepWithinDaily: "two days"}).Validate() == nil {
		t.Fatal("a bad duration must be refused")
	}

	if ValidateArchivePurgeTrigger(ConstArchivePurgeAfterBackup) != nil || ValidateArchivePurgeTrigger(ConstArchivePurgeSchedule) != nil || ValidateArchivePurgeTrigger("hourly") == nil {
		t.Fatal("trigger validation")
	}
	if !(&Config{}).ArchivePurgeAfterBackup() || !(&Config{BackupArchivePurgeTrigger: ConstArchivePurgeSchedule}).ArchivePurgeOnSchedule() {
		t.Fatal("after-backup is the default, schedule on demand")
	}

	for _, plugin := range []string{ConstArchivePluginResticLocal, ConstArchivePluginResticSftp, ConstArchivePluginResticS3} {
		m, _ := ArchivePlugin(plugin)
		if !m.Can(ConstArchiveCapPurge) {
			t.Errorf("%s cannot purge", plugin)
		}
		if m.HasSetting("keep-last") {
			t.Errorf("%s holds the retention policy, the user's", plugin)
		}
		for name, group := range map[string]string{"purge-prune": ConstArchiveSettingGroupPurge, "purge-keep-tag": ConstArchiveSettingGroupPurge, "mount-host": ConstArchiveSettingGroupAdvanced, "tags": ConstArchiveSettingGroupAdvanced} {
			if st, ok := m.Setting(name); !ok || st.Group != group {
				t.Errorf("%s %s: %+v, want group %s", plugin, name, st, group)
			}
		}
		if st, ok := m.Setting("password"); !ok || st.Group != "" {
			t.Errorf("%s password: %+v, want a main setting", plugin, st)
		}
	}
}
