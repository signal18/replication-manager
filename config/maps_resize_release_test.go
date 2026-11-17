// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// A value preserved at another container size (here an 8 GB buffer pool kept in an 8 GB
// container, configurator value 3934M) is released to the configurator value by
// auto-agree: it moves to agreed (Preserved == Config, no source) and the DB takes it at
// its next restart. A variable the resize does not manage, a cluster-level preserve, and
// a preserve already equal to the configurator value are left as they are.
func TestReleaseDynamicResizeMemoryPreserved(t *testing.T) {
	m := NewVariablesMap()
	set := func(name, config, preserved, source string) *VariableState {
		s := NewVariableState(name)
		if config != "" {
			s.SetConfigValue(config)
		}
		if preserved != "" {
			s.SetPreservedValue(preserved)
		}
		s.PreservedSource = source
		m.Store(name, s)
		return s
	}
	bp := set("innodb_buffer_pool_size", "3934M", "8192M", "server-specific")
	key := set("key_buffer_size", "256M", "2048M", "server-specific")
	tmp := set("tmp_table_size", "256M", "2048M", "cluster-level")
	join := set("join_buffer_size", "128M", "128M", "server-specific")
	aria := set("aria_pagecache_buffer_size", "256M", "2048M", "server-specific")
	agreedOld := set("mrr_buffer_size", "256M", "1024M", "")

	released := m.ReleaseDynamicResizeMemoryPreserved()
	if len(released) != 3 {
		t.Fatalf("released %v, want innodb_buffer_pool_size, key_buffer_size, mrr_buffer_size", released)
	}
	for _, c := range []struct {
		s        *VariableState
		value    string
		source   string
		preserve bool
	}{
		{bp, "3934M", "", false},
		{key, "256M", "", false},
		{agreedOld, "256M", "", false},
		{tmp, "2048M", "cluster-level", true},
		{join, "128M", "server-specific", false},
		{aria, "2048M", "server-specific", true},
	} {
		if got := c.s.Preserved.String(); got != c.value || c.s.PreservedSource != c.source || c.s.IsPreserved() != c.preserve {
			t.Errorf("%s: preserved %s source %q isPreserved %v, want %s %q %v", c.s.VariableName, got, c.s.PreservedSource, c.s.IsPreserved(), c.value, c.source, c.preserve)
		}
	}
	if again := m.ReleaseDynamicResizeMemoryPreserved(); len(again) != 0 {
		t.Errorf("second run released %v, want nothing", again)
	}
}
