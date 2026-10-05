package cluster

import (
	"testing"
	"time"
)

func TestInjectTrafficIsNotReentrant(t *testing.T) {
	c := &Cluster{Name: "c"}
	t0 := time.Date(2026, 10, 5, 9, 4, 19, 0, time.UTC)
	if !c.tryStartInjectTraffic(t0) {
		t.Fatal("first injection must start")
	}
	if c.tryStartInjectTraffic(t0.Add(2 * time.Second)) {
		t.Fatal("a second injection must be refused while the first is in flight (the belair dump case)")
	}
	if got := time.Unix(c.injectTrafficSince.Load(), 0); !got.Equal(t0) {
		t.Fatalf("since must stay the FIRST start, got %v", got)
	}
	c.endInjectTraffic()
	if !c.tryStartInjectTraffic(t0.Add(time.Minute)) {
		t.Fatal("after the first returns the next tick starts one again")
	}
}
