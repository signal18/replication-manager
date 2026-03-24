package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestShouldRefreshSysbenchVersionPeriodically(t *testing.T) {
	tests := []struct {
		name                     string
		sysbenchBinaryPath       string
		test                     bool
		testInjectTraffic        bool
		testInjectTrafficStaging bool
		want                     bool
	}{
		{
			name:                     "default path no test flags",
			sysbenchBinaryPath:       "/usr/bin/sysbench",
			test:                     false,
			testInjectTraffic:        false,
			testInjectTrafficStaging: false,
			want:                     false,
		},
		{
			name:                     "empty path no test flags",
			sysbenchBinaryPath:       "",
			test:                     false,
			testInjectTraffic:        false,
			testInjectTrafficStaging: false,
			want:                     false,
		},
		{
			name:                     "non-default path",
			sysbenchBinaryPath:       "/usr/local/bin/sysbench",
			test:                     false,
			testInjectTraffic:        false,
			testInjectTrafficStaging: false,
			want:                     true,
		},
		{
			name:                     "default path with Test enabled",
			sysbenchBinaryPath:       "/usr/bin/sysbench",
			test:                     true,
			testInjectTraffic:        false,
			testInjectTrafficStaging: false,
			want:                     true,
		},
		{
			name:                     "default path with TestInjectTraffic enabled",
			sysbenchBinaryPath:       "/usr/bin/sysbench",
			test:                     false,
			testInjectTraffic:        true,
			testInjectTrafficStaging: false,
			want:                     true,
		},
		{
			name:                     "default path with TestInjectTrafficStaging enabled",
			sysbenchBinaryPath:       "/usr/bin/sysbench",
			test:                     false,
			testInjectTraffic:        false,
			testInjectTrafficStaging: true,
			want:                     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cluster{
				Conf: &config.Config{
					SysbenchBinaryPath:       tt.sysbenchBinaryPath,
					Test:                     tt.test,
					TestInjectTraffic:        tt.testInjectTraffic,
					TestInjectTrafficStaging: tt.testInjectTrafficStaging,
				},
			}

			got := c.shouldRefreshSysbenchVersionPeriodically()
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
