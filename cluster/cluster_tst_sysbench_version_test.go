package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func TestShouldUseSysbenchV1Syntax(t *testing.T) {
	tests := []struct {
		name              string
		detected          string
		flagSysbenchV1    bool
		injectZeroVersion bool
		want              bool
	}{
		{"detected <1.0 uses legacy syntax", "0.5.0", true, false, false},
		{"detected >=1.0 uses v1 syntax", "1.0.20", false, false, true},
		{"detected 1.1.0 uses v1 syntax", "1.1.0", true, false, true},
		{"detected 1.0.0 uses v1 syntax", "1.0.0", false, false, true},
		{"detected 0.14.0 uses legacy syntax", "0.14.0", true, false, false},
		{"no detected version falls back to flag false", "", false, false, false},
		{"no detected version falls back to flag true", "", true, false, true},
		{"zero detected version falls back to flag false", "", false, true, false},
		{"zero detected version falls back to flag true", "", true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cluster{
				Conf:        &config.Config{SysbenchV1: tt.flagSysbenchV1},
				VersionsMap: config.NewVersionsMap(),
			}

			if tt.detected != "" {
				v, _ := version.NewVersionFromString("sysbench", tt.detected)
				c.VersionsMap.Set("sysbench", v)
			}
			if tt.injectZeroVersion {
				c.VersionsMap.Set("sysbench", &version.Version{Flavor: "sysbench"})
			}

			got := c.shouldUseSysbenchV1Syntax()
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
