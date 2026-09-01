package configurator

import (
	"testing"
)

// Guards the prov-db-memory-derived PFS budget (#1749): the digest/text
// lengths must scale with the declared memory — the fixed 16384 preallocated
// ~727MB at init and OOMed small cgroups at boot.
func TestGetConfigPFSDigestLength(t *testing.T) {
	tests := []struct {
		name string
		mem  string
		pcts string
		want string
	}{
		// 768M: usable 0 -> budget 0 -> MariaDB defaults.
		{"768M defaults", "768M", "", "1024"},
		// 8G: usable 6144 x 5% = 307 -> intermediate tier.
		{"8G intermediate", "8G", "", "4096"},
		// 64G: usable 63488 x 5% = 3174 -> full capture sizing.
		{"64G capture sizing", "64G", "", "16384"},
		// Explicit pfs:0 disables the budget whatever the memory.
		{"pfs:0 off-switch", "64G", "threads:16,innodb:55,myisam:10,aria:10,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,pfs:0", "1024"},
		// Explicit share overrides the default 5.
		{"pfs:20 boosted", "8G", "threads:16,innodb:55,myisam:10,aria:10,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,pfs:20", "16384"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfiguratorWithMem(tt.mem)
			if tt.pcts != "" {
				c.ClusterConfig.ProvMemSharedPct = tt.pcts
			}
			if got := c.GetConfigPFSDigestLength(); got != tt.want {
				t.Errorf("GetConfigPFSDigestLength(mem=%s pcts=%q) = %s, want %s", tt.mem, tt.pcts, got, tt.want)
			}
		})
	}
}
