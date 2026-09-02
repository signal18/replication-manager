package configurator

import (
	"testing"
)

func newConfiguratorWithMem(mem string) *Configurator {
	c := &Configurator{}
	c.ClusterConfig.ProvMem = mem
	c.ClusterConfig.ProvMemSharedPct = "threads:10,innodb:45,myisam:4,aria:8,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,tidesdb:1,pfs:4,fscache:20"
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
		// usable == total memory now; the FS-cache reserve is not subtracted here
		// (it is held out by the shares summing below 100).
		{"4G", "4G", 4096, false},
		{"4g lowercase", "4g", 4096, false},
		{"4096M same as 4G", "4096M", 4096, false},
		{"4096m lowercase", "4096m", 4096, false},
		{"4096 bare number", "4096", 4096, false},
		{"8G", "8G", 8192, false},
		{"256M", "256M", 256, false},
		{"256 bare", "256", 256, false},
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
	// innodb share is 45% of total memory (usable = total), rounded down to a power of two
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G default", "4G", "1024"}, // pow2(4096*45/100=1843)=1024
		{"4g lowercase", "4g", "1024"},
		{"4096M", "4096M", "1024"},
		{"4096m lowercase", "4096m", "1024"},
		{"4096 bare", "4096", "1024"},
		{"8G", "8G", "2048"}, // pow2(8192*45/100=3686)=2048
		{"256M floored", "256M", "128"}, // pow2(256*45/100=115)=64 -> floor 128
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
	// myisam share is 4% of total memory, pow2
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G", "4G", "128"}, // pow2(4096*4/100=163)=128
		{"4096m", "4096m", "128"},
		{"8G", "8G", "256"}, // pow2(8192*4/100=327)=256
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
	// aria share is 8% of total memory, pow2
	tests := []struct {
		name string
		mem  string
		want string
	}{
		{"4G", "4G", "256"}, // pow2(4096*8/100=327)=256
		{"1G floored", "1G", "128"}, // pow2(1024*8/100=81)=64 -> floor 128
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
