package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func TestShouldUseSysbenchV1Syntax(t *testing.T) {
	tests := []struct {
		name     string
		detected string
		want     bool
		wantErr  bool
	}{
		{"detected <1.0 uses legacy syntax", "0.5.0", false, false},
		{"detected >=1.0 uses v1 syntax", "1.0.20", true, false},
		{"detected 1.1.0 uses v1 syntax", "1.1.0", true, false},
		{"detected 1.0.0 uses v1 syntax", "1.0.0", true, false},
		{"detected 0.14.0 uses legacy syntax", "0.14.0", false, false},
		{"no detected version returns error", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cluster{
				Conf:        &config.Config{},
				VersionsMap: config.NewVersionsMap(),
			}

			if tt.detected != "" {
				v, tokens := version.NewVersionFromString("sysbench", tt.detected)
				if v == nil || tokens == 0 {
					t.Fatalf("failed to parse test version %q", tt.detected)
				}
				c.VersionsMap.Set("sysbench", v)
			}

			got, err := c.shouldUseSysbenchV1Syntax()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
