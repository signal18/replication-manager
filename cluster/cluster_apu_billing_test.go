package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The crm layout on preprod (2026-09-28): five apps on 3/3/3/1/3 agents and two proxies,
// nothing measured -> the floor bills one APU per instance, 15 against a plan of 16.
func TestComputeAPUBilling_CrmFloor(t *testing.T) {
	units := []apuBillingUnit{
		{Name: "api", Floor: 3}, {Name: "arbitrator", Floor: 3}, {Name: "dolibarr", Floor: 3},
		{Name: "minio", Floor: 1}, {Name: "phpmyadmin", Floor: 3},
		{Name: "prx1", Floor: 1}, {Name: "prx2", Floor: 1},
	}
	b := computeAPUBilling(units, 16, 36, 150, 80, time.Now())
	if b.Units != 7 || b.RunningUnits != 7 || b.FloorUnits != 15 || b.MeasuredApu != 0 || b.BillableUnits != 15 {
		t.Fatalf("units=%d running=%d floor=%d measured=%v billable=%d, want 7 7 15 0 15", b.Units, b.RunningUnits, b.FloorUnits, b.MeasuredApu, b.BillableUnits)
	}
	if b.OverPlanUnits != 0 || b.UnderPlanUnits != 1 {
		t.Fatalf("over=%d under=%d, want 0 1", b.OverPlanUnits, b.UnderPlanUnits)
	}
	// 15 x 36 + 1 unused plan unit x 36 x 0.8 = 540 + 28.8
	if want := 568.8; b.MonthlyCost < want-1e-9 || b.MonthlyCost > want+1e-9 {
		t.Fatalf("cost=%v, want %v", b.MonthlyCost, want)
	}
}

// Measured consumption only counts above the floor; a down unit counts nothing; over the
// plan the excess is priced at the over-commit ratio.
func TestComputeAPUBilling_MeasuredAndDown(t *testing.T) {
	units := []apuBillingUnit{
		{Name: "big", Floor: 3, Measured: 4.2},   // above its floor: ceil -> 5
		{Name: "small", Floor: 2, Measured: 0.3}, // under its floor: 2
		{Name: "off", Floor: 3, Down: true},      // nothing
		{Name: "prx", Floor: 1, Measured: 0.05},  // 1
	}
	b := computeAPUBilling(units, 6, 10, 150, 80, time.Now())
	if b.RunningUnits != 3 || b.FloorUnits != 6 || b.BillableUnits != 8 || b.OverPlanUnits != 2 || b.UnderPlanUnits != 0 {
		t.Fatalf("running=%d floor=%d billable=%d over=%d under=%d, want 3 6 8 2 0", b.RunningUnits, b.FloorUnits, b.BillableUnits, b.OverPlanUnits, b.UnderPlanUnits)
	}
	// 6 x 10 + 2 x 10 x 1.5 = 90
	if b.MonthlyCost != 90 {
		t.Fatalf("cost=%v, want 90", b.MonthlyCost)
	}
	if b = computeAPUBilling(nil, 4, 10, 150, 80, time.Now()); b.BillableUnits != 0 || b.UnderPlanUnits != 4 || b.MonthlyCost != 32 {
		t.Fatalf("no unit: billable=%d under=%d cost=%v, want 0 4 32", b.BillableUnits, b.UnderPlanUnits, b.MonthlyCost)
	}
}

// An app occupies every agent it is placed on: its own agents, else the cluster app
// agents, else the cluster agents, never under 1.
func TestAppInstanceCount(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{ProvAgents: "n1,n2,n3"}}
	if n := c.appInstanceCount(&App{Name: "a", AppConfig: &config.AppConfig{}}); n != 3 {
		t.Fatalf("cluster agents: %d, want 3", n)
	}
	if n := c.appInstanceCount(&App{Name: "b", AppConfig: &config.AppConfig{ProvAppAgents: "n2"}}); n != 1 {
		t.Fatalf("own agent: %d, want 1", n)
	}
	c.Conf.ProvAgents = ""
	if n := c.appInstanceCount(&App{Name: "c", AppConfig: &config.AppConfig{}}); n != 1 {
		t.Fatalf("no agents anywhere: %d, want 1", n)
	}
}
