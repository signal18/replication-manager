package configurator

import "testing"

// thread_pool_size: one group per core, four under semi-sync, whichever of the three
// sources says semi-sync (force-slave-semisync, the "semisync" DB tag, the observed state).
func TestGetConfigThreadPoolSize(t *testing.T) {
	tests := []struct {
		name     string
		cores    string
		flag     bool
		tag      bool
		observed bool
		want     string
	}{
		{"async 1 core", "1", false, false, false, "1"},
		{"async 2 cores", "2", false, false, false, "2"},
		{"flag", "2", true, false, false, "8"},
		{"configurator tag", "2", false, true, false, "8"},
		{"observed only (belair)", "2", false, false, true, "8"},
		{"observed 1 core", "1", false, false, true, "4"},
		{"bad cores", "", false, false, false, "1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Configurator{}
			c.ClusterConfig.ProvCores = tc.cores
			c.ClusterConfig.ForceSlaveSemisync = tc.flag
			if tc.tag {
				c.DBTags = []string{"semisync"}
			}
			if got := c.GetConfigThreadPoolSize(tc.observed); got != tc.want {
				t.Fatalf("%s: got %s want %s", tc.name, got, tc.want)
			}
		})
	}
}
