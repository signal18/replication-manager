package cluster

import "testing"

func TestBenchmarkMethodsReturnErrorWithoutProxy(t *testing.T) {
	cluster := &Cluster{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "prepare", call: cluster.PrepareBench},
		{name: "cleanup", call: cluster.CleanupBench},
		{name: "run", call: func() error {
			_, _, _, err := cluster.RunSysBench("oltp", "1", "1", "1", "complex")
			return err
		}},
		{name: "run sysbench", call: cluster.RunSysbench},
		{name: "scale threads", call: cluster.RunSysbenchScaleThreads},
		{name: "tpc threads", call: cluster.RunSysbenchTPCPerMinuteIncreaseThreads},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil || err.Error() != "No proxy" {
				t.Fatalf("got error %v, want No proxy", err)
			}
		})
	}
}
