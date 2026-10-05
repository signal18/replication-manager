package cluster

import (
	"testing"
	"time"
)

// The memory scale-DOWN state: configured memory over the plan AND no buffer-pool pressure.
func TestMemoryOverPlanNoPressure(t *testing.T) {
	s := &ServerMonitor{}
	cfg, plan := DBUReading{DbuMem: 4}, DBUReading{DbuMem: 1}
	if !s.memoryOverPlanNoPressure(cfg, plan) {
		t.Fatalf("4 DBU configured over a plan of 1 with no pressure must be shrinkable")
	}
	if s.memoryOverPlanNoPressure(DBUReading{DbuMem: 1}, plan) {
		t.Fatalf("memory at the plan is not over it")
	}
	s.BufferPoolMemGrowDue = true
	if s.memoryOverPlanNoPressure(cfg, plan) {
		t.Fatalf("a due memory grow (pressure) forbids the shrink state")
	}
	s.BufferPoolMemGrowDue = false
	s.bufferPoolPressureSince = time.Now()
	if s.memoryOverPlanNoPressure(cfg, plan) {
		t.Fatalf("sustained pressure forbids the shrink state")
	}
	if s.memoryOverPlanNoPressure(cfg, DBUReading{}) {
		t.Fatalf("no plan, no state")
	}
	if got := appendAxis([]string{"cpu", "disk"}, "mem"); len(got) != 3 || got[1] != "mem" {
		t.Fatalf("appendAxis order = %v", got)
	}
	if got := appendAxis([]string{"mem"}, "mem"); len(got) != 1 {
		t.Fatalf("appendAxis must not duplicate: %v", got)
	}
}
