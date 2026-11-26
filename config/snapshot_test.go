// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
	"testing"
)

func TestSnapshotPluginAllowed(t *testing.T) {
	conf := &Config{BackupSnapshotPlugin: ConstSnapshotPluginZFS}
	if m, err := conf.SnapshotPluginAllowed(); err != nil || m.Name != ConstSnapshotPluginZFS || m.Run != ConstSnapshotRunDatabaseHost {
		t.Fatalf("zfs: %+v %v", m, err)
	}
	conf.BackupSnapshotPlugin = ConstSnapshotPluginLVM
	if m, err := conf.SnapshotPluginAllowed(); err != nil || m.Name != ConstSnapshotPluginLVM {
		t.Fatalf("lvm: %+v %v", m, err)
	}
	conf.BackupSnapshotPlugin = ConstSnapshotPluginScript
	if _, err := conf.SnapshotPluginAllowed(); err == nil {
		t.Fatal("the script plugin without a script must be refused")
	}
	conf.BackupSnapshotScript = "/bin/snap"
	conf.BackupSnapshotPlugin = ConstSnapshotPluginLVM
	if m, err := conf.SnapshotPluginAllowed(); err != nil || m.Name != ConstSnapshotPluginScript {
		t.Fatalf("the script overrides the plugin: %+v %v", m, err)
	}
	conf.BackupSnapshotScript = ""
	conf.BackupSnapshotPlugin = "unknown"
	if _, err := conf.SnapshotPluginAllowed(); err == nil {
		t.Fatal("an unknown plugin must be refused")
	}
}

func TestSnapshotPluginEnterpriseTier(t *testing.T) {
	if err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: "array-test", Tier: ConstArchiveTierEnterprise}, "/opt/snapshotplugin-array-test"); err != nil {
		t.Fatal(err)
	}
	conf := &Config{BackupSnapshotPlugin: "array-test"}
	if _, err := conf.SnapshotPluginAllowed(); !errors.Is(err, ErrSnapshotEnterprisePlan) {
		t.Fatalf("enterprise without the plan: %v", err)
	}
	conf.Cloud18GitUser, conf.Cloud18SubscriptionPlan = "someone", "developer"
	m, err := conf.SnapshotPluginAllowed()
	if err != nil || !m.External || m.Run != ConstSnapshotRunMonitorHost || !m.Can(ConstSnapshotActionSnapback) {
		t.Fatalf("enterprise with the developer plan: %+v %v", m, err)
	}
	if err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: ConstSnapshotPluginZFS}, "/tmp/x"); err == nil {
		t.Fatal("a built-in plugin can not be replaced")
	}
	if err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: "Bad Name"}, "/tmp/x"); err == nil {
		t.Fatal("an invalid name must be refused")
	}
	if err := RegisterSnapshotPlugin(SnapshotPluginManifest{Name: "x", Tier: "gold"}, "/tmp/x"); err == nil {
		t.Fatal("an unknown tier must be refused")
	}
}

func TestSnapshotKeepBounded(t *testing.T) {
	for in, want := range map[int]int{0: ConstSnapshotKeepDefault, -4: ConstSnapshotKeepDefault, 5: 5, 1000: ConstSnapshotKeepMax} {
		if got := (&Config{BackupSnapshotKeep: in}).SnapshotKeep(); got != want {
			t.Errorf("SnapshotKeep(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSnapshotValidators(t *testing.T) {
	for _, ok := range []string{"repman-20261126T101500Z", "daily.1", "a_b"} {
		if err := ValidSnapshotName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a;rm", "a/b", "a@b", "$(id)"} {
		if ValidSnapshotName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"", "tank/db", "data/db3.dev.vol.c", "vg0/lv_data"} {
		if err := ValidSnapshotTarget(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"tank db", "tank/../x", "x;y", "-o"} {
		if ValidSnapshotTarget(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"20%ORIGIN", "10G", "512m", "1.5T"} {
		if err := ValidSnapshotLVMSize(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "lots", "10%", "1G;x"} {
		if ValidSnapshotLVMSize(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
