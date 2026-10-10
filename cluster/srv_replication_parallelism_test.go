package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func parallelismServer(status, prev, vars map[string]string) *ServerMonitor {
	s := &ServerMonitor{ReplicationGroupCommitSize: 42} // stale value: must never survive
	if status != nil {
		s.Status = config.NewStringsMap()
		for k, v := range status {
			s.Status.Set(k, v)
		}
	}
	if prev != nil {
		s.PrevStatus = config.NewStringsMap()
		for k, v := range prev {
			s.PrevStatus.Set(k, v)
		}
	}
	s.Variables = config.NewStringsMap()
	for k, v := range vars {
		s.Variables.Set(k, v)
	}
	return s
}

func TestRefreshReplicationParallelism(t *testing.T) {
	cases := []struct {
		name          string
		status, prev  map[string]string
		vars          map[string]string
		wantGroupSize float64
		wantThreads   int64
	}{
		{
			name:          "normal delta: 30 commits in 10 groups = 3 per group",
			status:        map[string]string{"BINLOG_COMMITS": "130", "BINLOG_GROUP_COMMITS": "60"},
			prev:          map[string]string{"BINLOG_COMMITS": "100", "BINLOG_GROUP_COMMITS": "50"},
			vars:          map[string]string{"SLAVE_PARALLEL_THREADS": "32"},
			wantGroupSize: 3,
			wantThreads:   32,
		},
		{
			name:          "no group commit in the tick: 0, not the previous value",
			status:        map[string]string{"BINLOG_COMMITS": "100", "BINLOG_GROUP_COMMITS": "50"},
			prev:          map[string]string{"BINLOG_COMMITS": "100", "BINLOG_GROUP_COMMITS": "50"},
			wantGroupSize: 0,
		},
		{
			name:          "counter reset (server restart): commits went backwards -> 0",
			status:        map[string]string{"BINLOG_COMMITS": "5", "BINLOG_GROUP_COMMITS": "3"},
			prev:          map[string]string{"BINLOG_COMMITS": "100", "BINLOG_GROUP_COMMITS": "50"},
			wantGroupSize: 0,
		},
		{
			name:          "missed tick: previous snapshot has no counter -> 0, stale value dropped",
			status:        map[string]string{"BINLOG_COMMITS": "130", "BINLOG_GROUP_COMMITS": "60"},
			prev:          map[string]string{},
			wantGroupSize: 0,
		},
		{
			name:          "no previous snapshot at all (first tick) -> 0",
			status:        map[string]string{"BINLOG_COMMITS": "130", "BINLOG_GROUP_COMMITS": "60"},
			prev:          nil,
			wantGroupSize: 0,
		},
		{
			name:          "MySQL: no Binlog_* counters, workers from slave_parallel_workers",
			status:        map[string]string{"QUERIES": "10"},
			prev:          map[string]string{"QUERIES": "5"},
			vars:          map[string]string{"SLAVE_PARALLEL_WORKERS": "8"},
			wantGroupSize: 0,
			wantThreads:   8,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := parallelismServer(c.status, c.prev, c.vars)
			s.refreshReplicationParallelism()
			if s.ReplicationGroupCommitSize != c.wantGroupSize {
				t.Fatalf("group commit size = %v, want %v", s.ReplicationGroupCommitSize, c.wantGroupSize)
			}
			if s.ReplicationParallelThreads != c.wantThreads {
				t.Fatalf("parallel threads = %v, want %v", s.ReplicationParallelThreads, c.wantThreads)
			}
		})
	}
}
