package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// A layout of five apps with 3/3/3/1/3 running instances and two proxies, nothing measured
// -> the floor bills one APU per instance, 15 against a plan of 16.
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
	// 15 x 36 + 1 unused plan unit x 36 x 0.2 (80% reduction) = 540 + 7.2
	if want := 547.2; b.MonthlyCost < want-1e-9 || b.MonthlyCost > want+1e-9 {
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
	// 6 x 10 + 2 x 10 x 2.5 (150% surcharge) = 110
	if b.MonthlyCost != 110 {
		t.Fatalf("cost=%v, want 110", b.MonthlyCost)
	}
	if b = computeAPUBilling(nil, 4, 10, 150, 80, time.Now()); b.BillableUnits != 0 || b.UnderPlanUnits != 4 || b.MonthlyCost != 8 {
		t.Fatalf("no unit: billable=%d under=%d cost=%v, want 0 4 8", b.BillableUnits, b.UnderPlanUnits, b.MonthlyCost)
	}
}

// Copies = the agents holding the app's volume (own agents, else cluster app agents, else
// cluster agents, never under 1). Instances = what runs: every agent when flex, ONE when
// failover (the standby agents only hold a volume copy).
func TestAppInstanceAndCopyCount(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{ProvAgents: "n1,n2,n3", ProvAppHATopology: "failover"}}
	failover := &App{Name: "a", AppConfig: &config.AppConfig{ProvAppHATopology: "failover"}}
	flex := &App{Name: "b", AppConfig: &config.AppConfig{ProvAppHATopology: "flex"}}
	if c.appCopyCount(failover) != 3 || c.appInstanceCount(failover) != 1 {
		t.Fatalf("failover on 3 agents: copies=%d instances=%d, want 3 1", c.appCopyCount(failover), c.appInstanceCount(failover))
	}
	if c.appCopyCount(flex) != 3 || c.appInstanceCount(flex) != 3 {
		t.Fatalf("flex on 3 agents: copies=%d instances=%d, want 3 3", c.appCopyCount(flex), c.appInstanceCount(flex))
	}
	one := &App{Name: "c", AppConfig: &config.AppConfig{ProvAppAgents: "n2", ProvAppHATopology: "flex"}}
	if c.appCopyCount(one) != 1 || c.appInstanceCount(one) != 1 {
		t.Fatalf("own single agent: copies=%d instances=%d, want 1 1", c.appCopyCount(one), c.appInstanceCount(one))
	}
	c.Conf.ProvAgents = ""
	if n := c.appCopyCount(&App{Name: "d", AppConfig: &config.AppConfig{}}); n != 1 {
		t.Fatalf("no agents anywhere: %d, want 1", n)
	}
}

// The crm layout on preprod: app disk = declared prov-app-disk-size x copies, rounded up per
// app; compute apps to BKU, the S3 provider (minio) to BAU as producer.
func TestAppDiskAccounting_Crm(t *testing.T) {
	unit := int64(20 * 1024 * 1024 * 1024)
	c := &Cluster{Name: "crm", Conf: &config.Config{ProvAppHATopology: "failover"}}
	mk := func(name, agents, ha, disk string, s3 bool) *App {
		return &App{Name: name, ClusterGroup: c, AppConfig: &config.AppConfig{ProvAppAgents: agents, ProvAppHATopology: ha, ProvAppDisk: disk, AppS3Provider: s3}}
	}
	c.Apps = []*App{
		mk("api", "n4,n5,n6", "failover", "4", false),
		mk("arbitrator", "n6,n4,n5", "flex", "4", false),
		mk("dolibarr", "n4,n5,n6", "failover", "4", false),
		mk("minio", "n5", "failover", "2048", true),
		mk("phpmyadmin", "n4,n5,n6", "flex", "4", false),
	}
	d := c.appDiskAccounting(unit)
	if d.computeUnits != 4 || d.computeBytes != 4*12*1024*1024*1024 {
		t.Fatalf("compute: units=%d bytes=%d, want 4 (12 GB x 4 apps, 1 BKU each) %d", d.computeUnits, d.computeBytes, 4*12*1024*1024*1024)
	}
	if d.producerUnits != 103 || d.producerBytes != 2048*1024*1024*1024 {
		t.Fatalf("producer: units=%d bytes=%d, want 103 (2048 GB / 20) %d", d.producerUnits, d.producerBytes, 2048*1024*1024*1024)
	}
	if d = c.appDiskAccounting(0); d.computeUnits != 0 || d.producerUnits != 0 {
		t.Fatalf("a zero unit counts nothing")
	}
}
