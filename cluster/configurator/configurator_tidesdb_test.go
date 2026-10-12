// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import "testing"

// #2001: the TidesDB placeholders of the enginetidesdb ruleset.
func TestTidesDBSizing(t *testing.T) {
	for _, tc := range []struct {
		name, mem, shares, cores     string
		cache, wb, flush, compaction string
	}{
		// the default 1% share on a small node: the engine floor (minEngineMemMB), the write buffer floor
		{"small node default share", "4G", "innodb:45,tidesdb:1", "1", "128", "64", "1", "2"},
		// a real share on a large node: the write buffer capped at 256 MB
		{"large node 25% share", "64G", "innodb:30,tidesdb:25", "16", "16384", "256", "4", "8"},
		// in between: a quarter of the cache
		{"8G 10% share", "8G", "innodb:40,tidesdb:10", "4", "512", "128", "1", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Configurator{}
			c.ClusterConfig.ProvMem = tc.mem
			c.ClusterConfig.ProvMemSharedPct = tc.shares
			c.ClusterConfig.ProvCores = tc.cores
			if got := c.GetConfigTidesDBCacheSize(); got != tc.cache {
				t.Errorf("cache %s, want %s", got, tc.cache)
			}
			if got := c.GetConfigTidesDBWriteBufferSize(); got != tc.wb {
				t.Errorf("write buffer %s, want %s", got, tc.wb)
			}
			if got := c.GetConfigTidesDBFlushThreads(); got != tc.flush {
				t.Errorf("flush threads %s, want %s", got, tc.flush)
			}
			if got := c.GetConfigTidesDBCompactionThreads(); got != tc.compaction {
				t.Errorf("compaction threads %s, want %s", got, tc.compaction)
			}
		})
	}
}
