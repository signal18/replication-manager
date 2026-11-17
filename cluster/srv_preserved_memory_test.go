// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func TestMysqlSizeMB(t *testing.T) {
	for in, want := range map[string]int64{"8192M": 8192, "8g": 8192, "2048m": 2048, "8589934592": 8192, "1T": 1048576, "512K": 0} {
		if got, ok := mysqlSizeMB(in); !ok || got != want {
			t.Errorf("mysqlSizeMB(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	if _, ok := mysqlSizeMB("abc"); ok {
		t.Error("abc must not parse")
	}
}

// The 2026-11-17 master: 8 GB container, preserved buffer pool 8192M plus 2 GB key and
// aria buffers: WARN0322 opens on that server. The same server with no preserve (the
// configurator's own values fit) or a preserve that fits opens nothing.
func TestPreservedGlobalBuffersOverMemory(t *testing.T) {
	newServer := func(preserve map[string]string) (*Cluster, *ServerMonitor) {
		cl := &Cluster{Conf: &config.Config{ProvMem: "8192"}, ConfigStateMachine: &state.StateMachine{}}
		cl.ConfigStateMachine.Init()
		s := &ServerMonitor{URL: "db1:3306", ClusterGroup: cl, VariablesMap: config.NewVariablesMap()}
		for name, cfg := range map[string]string{"innodb_buffer_pool_size": "3934M", "key_buffer_size": "256M", "aria_pagecache_buffer_size": "256M", "query_cache_size": "0"} {
			v := config.NewVariableState(name)
			v.SetConfigValue(cfg)
			if p, ok := preserve[name]; ok {
				v.SetPreservedValue(p)
				v.PreservedSource = "server-specific"
			}
			s.VariablesMap.Store(name, v)
		}
		return cl, s
	}
	cl, s := newServer(map[string]string{"innodb_buffer_pool_size": "8192M", "key_buffer_size": "2048M", "aria_pagecache_buffer_size": "2048M"})
	if total, preserved, over := s.preservedGlobalBuffersOverMemory(); !over || total != 12288 || len(preserved) != 3 {
		t.Fatalf("db1 of 2026-11-17: total %d preserved %v over %v, want 12288, 3 preserved, over", total, preserved, over)
	}
	s.checkPreservedMemoryFitsContainer()
	if !cl.ConfigStateMachine.CurState.Search("WARN0322@db1:3306") {
		t.Fatal("WARN0322 must open")
	}
	for name, p := range map[string]map[string]string{"no preserve": {}, "preserve that fits": {"innodb_buffer_pool_size": "4096M"}} {
		cl, s := newServer(p)
		s.checkPreservedMemoryFitsContainer()
		if cl.ConfigStateMachine.CurState.Search("WARN0322@db1:3306") {
			t.Errorf("%s: WARN0322 must not open", name)
		}
	}
}
