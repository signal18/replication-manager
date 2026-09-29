package cluster

import (
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// A stateful app (app-stateful) is planned and measured as DBU on the Database ratio,
// never as APU; the switch moves it between the two tracks without leaving a residue.
func TestRefreshComputePlanAPU_StatefulAppIsDBU(t *testing.T) {
	cl := &Cluster{Name: "crm", Conf: &config.Config{}, IsProvision: true, resources: NewResourceManager()}
	minio := &App{Name: "minio", Mutex: &sync.Mutex{}, Agent: "s18-fr-5", AppConfig: &config.AppConfig{
		ProvAppCpuCores: "2", ProvAppMem: "8192", ProvAppDisk: "20",
		ProvAppHATopology: "failover", ProvAppAgents: "s18-fr-5", AppStateful: true,
		Deployment: config.NewDeploymentConfig(),
	}}
	pma := &App{Name: "phpmyadmin", Mutex: &sync.Mutex{}, Agent: "s18-fr-4", AppConfig: &config.AppConfig{
		ProvAppCpuCores: "1", ProvAppMem: "1024", ProvAppDisk: "4",
		ProvAppHATopology: "flex", ProvAppAgents: "s18-fr-4,s18-fr-5,s18-fr-6",
		Deployment: config.NewDeploymentConfig(),
	}}
	cl.Apps = []*App{minio, pma}
	cl.RefreshComputePlanAPU()

	k := AppKey{Cluster: "crm", App: "minio", Kind: KindApp}
	st := cl.resources.GetStatefulPlan(k)
	if st == nil || st.Dbu != 2 || st.Binding != "cpu" {
		t.Fatalf("minio stateful plan = %+v, want 2 DBU bound on cpu (2 cores; 8 GB = 2; 20 GB = 1)", st)
	}
	if cl.resources.GetAppPlan(k) != nil {
		t.Fatalf("a stateful app must hold no APU plan")
	}
	if got := cl.resources.AppPlanByCluster("crm").Apu; got != 3 {
		t.Fatalf("APU plan = %v, want 3 (phpmyadmin flex x3 only)", got)
	}
	if got := cl.resources.StatefulPlanByCluster("crm").Dbu; got != 2 {
		t.Fatalf("stateful plan = %v, want 2", got)
	}
	if cl.Conf.ProvServicePlanApu != 3 {
		t.Fatalf("prov-service-plan-apu = %d, want 3", cl.Conf.ProvServicePlanApu)
	}
	if cl.StatefulUnits == nil || cl.StatefulUnits.Plan != 2 || cl.StatefulUnits.FloorUnits != 1 || cl.StatefulUnits.BillableUnits != 1 {
		t.Fatalf("stateful billing = %+v, want plan 2, floor 1, billable 1", cl.StatefulUnits)
	}
	if cl.ComputeUnits == nil || cl.ComputeUnits.Units != 1 {
		t.Fatalf("compute billing units = %+v, want the 1 compute app only", cl.ComputeUnits)
	}

	// A sensor push for the stateful app lands on the DBU track with the Database ratio.
	now := time.Now()
	cl.IngestAppConsumedAPU(KindApp, "minio", now.Add(-time.Minute), now, 6*1024*1024*1024, 1.2, 0)
	c := cl.resources.GetStatefulConsumed(k)
	if c == nil || c.Binding != "mem" || c.DbuMem < 1.49 || c.DbuMem > 1.51 {
		t.Fatalf("stateful consumed = %+v, want 6 GB = 1.5 DBU bound on mem", c)
	}
	if cl.resources.GetAppConsumed(k) != nil {
		t.Fatalf("a stateful app must hold no APU consumption")
	}

	// Switch it off: the plan moves back to APU (2 cores, 8 GB = 4 APU, 20 GB = 2 -> 4 APU).
	minio.AppConfig.AppStateful = false
	cl.RefreshComputePlanAPU()
	if cl.resources.GetStatefulPlan(k) != nil {
		t.Fatalf("stateful plan must be cleared once the app is not stateful")
	}
	// The cluster aggregate pivots on the SUMMED axes (sumAPUReadings), not on the per-app
	// pivots: mem 4 (minio 8 GB) + 1.5 (phpmyadmin 3 x 1 GB) = 5.5 binds over cpu 2 + 3 = 5.
	if got := cl.resources.AppPlanByCluster("crm").Apu; got < 5.49 || got > 5.51 {
		t.Fatalf("APU plan = %v, want 5.5 (minio back on the Compute track, mem-bound aggregate)", got)
	}
}

func TestComputeStatefulBilling_FloorAndPlan(t *testing.T) {
	units := []apuBillingUnit{{Name: "minio", Floor: 1, Measured: 1.5}}
	b := computeStatefulBilling(units, 2, 40, 150, 80, time.Now())
	if b.RunningUnits != 1 || b.FloorUnits != 1 || b.BillableUnits != 2 || b.UnderPlanUnits != 0 || b.OverPlanUnits != 0 {
		t.Fatalf("billing = %+v, want billable 2 (ceil 1.5) on plan 2", b)
	}
	if b.MonthlyCost != 80 {
		t.Fatalf("cost = %v, want 80 (2 x 40)", b.MonthlyCost)
	}
	if b.MeasuredDbu != 1.5 {
		t.Fatalf("measured = %v, want 1.5", b.MeasuredDbu)
	}
}
