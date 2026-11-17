// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// Under scale-speed, a live buffer pool that differs from the provisioned target is
// reconciled (the 2026-11-17 master: 8192 MB live in an 8 GB container), on the active
// monitor only, never under daily-time (its own driver), never with live resize off, never
// while a dbjob runs, and at most once per window.
func TestDynamicResizeReconcileDue(t *testing.T) {
	cl, s, done := k8sResizeTestServer(t, "reconcile", "db1")
	defer done()
	cl.Conf.ProvMem = "8192"
	cl.Conf.ProvMemSharedPct = "threads:16,innodb:48,myisam:10,aria:10,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,tidesdb:1"
	cl.Configurator.ClusterConfig = *cl.Conf
	target, err := strconv.ParseInt(cl.Configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil || target <= 0 {
		t.Fatalf("fixture: target %v %v", target, err)
	}
	s.State = stateMaster
	s.Variables = config.NewStringsMap()
	setLive := func(mb int64) { s.Variables.Set("INNODB_BUFFER_POOL_SIZE", strconv.FormatInt(mb*1024*1024, 10)) }
	reset := func() {
		cl.Status = ConstMonitorActif
		cl.Conf.ProvDBDynamicResource = true
		cl.Conf.ProvDBDynamicResizePolicy = "scale-speed"
		cl.LastDynamicResizeReconcile = time.Time{}
		s.IsRunningJobs = false
		setLive(8192)
	}
	now := time.Now()

	reset()
	if grow, due := cl.dynamicResizeReconcileDue(now); !due || grow {
		t.Fatalf("8192 MB live, target %d MB: want a shrink due, got grow=%v due=%v", target, grow, due)
	}
	reset()
	setLive(target / 2)
	if grow, due := cl.dynamicResizeReconcileDue(now); !due || !grow {
		t.Errorf("live below target: want a grow due, got grow=%v due=%v", grow, due)
	}
	for name, change := range map[string]func(){
		"live at target":  func() { setLive(target) },
		"standby":         func() { cl.Status = ConstMonitorStandby },
		"live resize off": func() { cl.Conf.ProvDBDynamicResource = false },
		"daily-time":      func() { cl.Conf.ProvDBDynamicResizePolicy = config.ConstResizePolicyDailyTime },
		"dbjob running":   func() { s.IsRunningJobs = true },
		"inside window":   func() { cl.LastDynamicResizeReconcile = now.Add(-10 * time.Minute) },
	} {
		reset()
		change()
		if _, due := cl.dynamicResizeReconcileDue(now); due {
			t.Errorf("%s: no reconcile expected", name)
		}
	}
	reset()
	cl.LastDynamicResizeReconcile = now.Add(-(dynamicResizeWindowMinutes + 1) * time.Minute)
	if _, due := cl.dynamicResizeReconcileDue(now); !due {
		t.Error("after the window: the reconcile must be due again")
	}
}
