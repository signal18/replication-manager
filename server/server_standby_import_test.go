// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func newStandbyImportRepman(runs *atomic.Int32, block chan struct{}) *ReplicationManager {
	repman := &ReplicationManager{Conf: &config.Config{Cloud18: true, Arbitration: true, GitConfigSyncStandby: true, GitUrl: "https://gitlab.example/x/y.git"}}
	repman.Status = ConstMonitorStandby
	repman.standbyImport.run = func() (*DynamicClusterImportResult, error) {
		runs.Add(1)
		if block != nil {
			<-block
		}
		return &DynamicClusterImportResult{Imported: []string{"tamarin"}}, nil
	}
	return repman
}

func TestStandbyImportOnlyOnAStandbyPair(t *testing.T) {
	var runs atomic.Int32
	now := time.Now()
	for name, mutate := range map[string]func(r *ReplicationManager){
		"active":          func(r *ReplicationManager) { r.Status = ConstMonitorActif },
		"no arbitration":  func(r *ReplicationManager) { r.Conf.Arbitration = false },
		"no standby sync": func(r *ReplicationManager) { r.Conf.GitConfigSyncStandby = false },
		"not Cloud18":     func(r *ReplicationManager) { r.Conf.Cloud18 = false },
		"no config repo":  func(r *ReplicationManager) { r.Conf.GitUrl = "" },
	} {
		r := newStandbyImportRepman(&runs, nil)
		mutate(r)
		if r.standbyImportDue(now) {
			t.Errorf("%s: import due, want not", name)
		}
	}
	if !newStandbyImportRepman(&runs, nil).standbyImportDue(now) {
		t.Fatal("standby pair: import not due")
	}
}

func TestStandbyImportThrottledAndSingleFlight(t *testing.T) {
	var runs atomic.Int32
	block := make(chan struct{})
	r := newStandbyImportRepman(&runs, block)
	now := time.Now()

	r.maybeImportClustersOnStandby(now)
	r.maybeImportClustersOnStandby(now.Add(standbyImportInterval + time.Second)) // due, but one is running
	close(block)
	for i := 0; i < 100 && r.standbyImport.inFlight.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1 (single flight)", got)
	}

	r.maybeImportClustersOnStandby(now.Add(time.Minute)) // within the interval of the first
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1 (throttled)", got)
	}
	r.maybeImportClustersOnStandby(now.Add(standbyImportInterval))
	for i := 0; i < 100 && runs.Load() < 2; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs = %d, want 2 after the interval", got)
	}
}
