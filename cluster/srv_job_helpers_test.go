package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestJobTaskFreshnessTimestamp(t *testing.T) {
	tests := []struct {
		name string
		task *config.Task
		want int64
	}{
		{name: "nil task", task: nil, want: 0},
		{name: "use start when end not set", task: &config.Task{Start: 10, End: 0}, want: 10},
		{name: "prefer end when present", task: &config.Task{Start: 10, End: 42}, want: 42},
		{name: "zero timestamps", task: &config.Task{}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobTaskFreshnessTimestamp(tt.task); got != tt.want {
				t.Fatalf("jobTaskFreshnessTimestamp() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShouldUpdateCachedJobTask(t *testing.T) {
	base := &config.Task{Task: "backup", Start: 100, End: 0, State: 1, Done: 0}

	tests := []struct {
		name   string
		cached *config.Task
		dbTask *config.Task
		want   bool
	}{
		{name: "nil db task is ignored", cached: base, dbTask: nil, want: false},
		{name: "missing cache should store db row", cached: nil, dbTask: &config.Task{Task: "backup", Start: 100}, want: true},
		{name: "newer db timestamp updates cache", cached: &config.Task{Task: "backup", Start: 100, End: 0}, dbTask: &config.Task{Task: "backup", Start: 100, End: 101}, want: true},
		{name: "older db timestamp does not overwrite", cached: &config.Task{Task: "backup", Start: 100, End: 150}, dbTask: &config.Task{Task: "backup", Start: 100, End: 120}, want: false},
		{name: "equal timestamp unchanged payload", cached: &config.Task{Task: "backup", Start: 100, State: 1, Done: 0}, dbTask: &config.Task{Task: "backup", Start: 100, State: 1, Done: 0}, want: false},
		{name: "equal timestamp with field change updates", cached: &config.Task{Task: "backup", Start: 100, State: 1, Done: 0}, dbTask: &config.Task{Task: "backup", Start: 100, State: 3, Done: 1}, want: true},
		{name: "end beats start for freshness", cached: &config.Task{Task: "backup", Start: 100, End: 0}, dbTask: &config.Task{Task: "backup", Start: 50, End: 120}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUpdateCachedJobTask(tt.cached, tt.dbTask); got != tt.want {
				t.Fatalf("shouldUpdateCachedJobTask() = %v, want %v", got, tt.want)
			}
		})
	}
}
