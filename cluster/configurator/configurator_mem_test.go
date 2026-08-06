package configurator

import (
	"testing"
)

func newConfiguratorWithMem(mem string) *Configurator {
	c := &Configurator{}
	c.ClusterConfig.ProvMem = mem
	c.ClusterConfig.ProvMemSharedPct = "threads:16,innodb:55,myisam:10,aria:10,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,tidesdb:1"
	c.ClusterConfig.ProvMemThreadedPct = "tmp:70,join:20,sort:10"
	return c
}

func TestGetUsableMemoryMB(t *testing.T) {
	tests := []struct {
		name    string
		mem     string
		want    int64
		wantErr bool
	}{
		{"4G default", "4G", 4096 - 2048, false},
		{"4g lowercase", "4g", 4096 - 2048, false},
		{"4096M same as 4G", "4096M", 4096 - 2048, false},
		{"4096m lowercase", "4096m", 4096 - 2048, false},
		{"4096 bare number", "4096", 4096 - 2048, false},
		{"8G", "8G", 8192 - 2048, false},
		{"256M below reserve", "256M", 0, false},
		{"256 bare below reserve", "256", 0, false},
		{"empty required", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfiguratorWithMem(tt.mem)
			got, err := c.getUsableMemoryMB()
			if (err != nil) != tt.wantErr {
				t.Fatalf("getUsableMemoryMB(%q) error = %v, wantErr %v", tt.mem, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("getUsableMemoryMB(%q) = %d, want %d", tt.mem, got, tt.want)
			}
		})
	}
}

func TestGetConfigInnoDBBPSize(t *testing.T) {
	// innodb share is 55% of usable memory
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G default", "4G", "1126"}, // (4096-2048)*55/100 = 1126
		{"4g lowercase", "4g", "1126"},
		{"4096M", "4096M", "1126"},
		{"4096m lowercase", "4096m", "1126"},
		{"4096 bare", "4096", "1126"},
		{"8G", "8G", "3379"},                          // (8192-2048)*55/100 = 3379
		{"256M below reserve floored", "256M", "128"}, // usable=0 → 0*55/100=0, floored to minEngineMemMB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfiguratorWithMem(tt.mem)
			got := c.GetConfigInnoDBBPSize()
			if got != tt.want {
				t.Errorf("GetConfigInnoDBBPSize(%q) = %q, want %q", tt.mem, got, tt.want)
			}
		})
	}
}

// TestGetConfigQueryCacheSizeDisabled guards the deliberate exception: an engine
// with a 0% share (query cache is off by default) must stay 0 and NOT be floored
// to 128M — flooring it would silently enable a feature that is meant to be off.
func TestGetConfigQueryCacheSizeDisabled(t *testing.T) {
	c := newConfiguratorWithMem("8G") // plenty of usable memory
	if got := c.GetConfigQueryCacheSize(); got != "0" {
		t.Errorf("GetConfigQueryCacheSize(disabled 0%%) = %q, want %q", got, "0")
	}
}

func TestGetConfigMyISAMKeyBufferSize(t *testing.T) {
	// myisam share is 10% of usable memory
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G", "4G", "204"}, // (4096-2048)*10/100 = 204
		{"4096m", "4096m", "204"},
		{"8G", "8G", "614"}, // (8192-2048)*10/100 = 614
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfiguratorWithMem(tt.mem)
			got := c.GetConfigMyISAMKeyBufferSize()
			if got != tt.want {
				t.Errorf("GetConfigMyISAMKeyBufferSize(%q) = %q, want %q", tt.mem, got, tt.want)
			}
		})
	}
}

func TestGetConfigAriaCacheSize(t *testing.T) {
	// aria share is 10% of usable memory
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G", "4G", "204"},
		{"1G below reserve floored", "1G", "128"}, // usable=0 → floored to minEngineMemMB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConfiguratorWithMem(tt.mem)
			got := c.GetConfigAriaCacheSize()
			if got != tt.want {
				t.Errorf("GetConfigAriaCacheSize(%q) = %q, want %q", tt.mem, got, tt.want)
			}
		})
	}
}
