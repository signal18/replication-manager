package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/router/haproxy"
)

func TestGwuClusterOf(t *testing.T) {
	cases := map[string]string{
		"forgejo1.curepipe.svc.cloud18_3000": "curepipe", "pma.curepipe.svc.cloud18_80": "curepipe",
		"repman.s18.svc.cloud18_10001": "s18", "be_osvcapi_cloud18": "", "acme_challenge_backend": "", "": "",
	}
	for in, want := range cases {
		if got := gwuClusterOf(in); got != want {
			t.Errorf("gwuClusterOf(%q)=%q want %q", in, got, want)
		}
	}
}

func TestGwuDeltaAndReset(t *testing.T) {
	if gwuDelta(0, 500, false) != 500 || gwuDelta(500, 800, true) != 300 || gwuDelta(800, 100, true) != 100 {
		t.Fatalf("delta/reset rule broken")
	}
}

func TestGatewayTrafficIngestAttributesAndRollsOver(t *testing.T) {
	g := newGatewayTraffic(t.TempDir())
	rows := []haproxy.Stats{
		{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bin: "200", Bout: "800"}, // both directions count
		{Pxname: "a.c1.svc.x_80", Svname: "srv1", Bout: "999999"},             // server rows are ignored
		{Pxname: "b.c2.svc.x_80", Svname: "BACKEND", Bout: "10"},
		{Pxname: "be_other", Svname: "BACKEND", Bout: "5"},
	}
	jan := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	g.ingest("gw1", rows, jan)
	g.ingest("gw2", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bout: "20"}}, jan)
	if g.bytesOf("c1") != 1020 || g.bytesOf("c2") != 10 || g.bytesOf("other") != 0 {
		t.Fatalf("attribution: c1=%d c2=%d", g.bytesOf("c1"), g.bytesOf("c2"))
	}
	// second poll: deltas on gw1 (out 800->1500, in 200->250), reset on gw2 (20 -> 5)
	g.ingest("gw1", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bin: "250", Bout: "1500"}}, jan.Add(time.Minute))
	g.ingest("gw2", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bout: "5"}}, jan.Add(time.Minute))
	if g.bytesOf("c1") != 1020+700+50+5 {
		t.Fatalf("delta+reset: c1=%d", g.bytesOf("c1"))
	}
	// month rollover: totals restart, counters keep their last value
	g.ingest("gw1", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bout: "1600"}}, time.Date(2026, 2, 1, 0, 0, 1, 0, time.UTC))
	if g.bytesOf("c1") != 100 {
		t.Fatalf("rollover: c1=%d", g.bytesOf("c1"))
	}
	if err := g.save(); err != nil {
		t.Fatal(err)
	}
	g2 := newGatewayTraffic(filepath.Dir(g.path))
	if g2.bytesOf("c1") != 100 || g2.state.Month != "2026-02" {
		t.Fatalf("reload from disk: c1=%d month=%s", g2.bytesOf("c1"), g2.state.Month)
	}
}

func TestParseGatewayStats(t *testing.T) {
	body := "# pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,\nx.c.svc.o_80,BACKEND,0,0,0,1,,12,345,678,\n"
	rows, err := parseGatewayStats(body)
	if err != nil || len(rows) != 1 || rows[0].Bout != "678" || rows[0].Svname != "BACKEND" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestGatewayTrafficRates(t *testing.T) {
	g := newGatewayTraffic(t.TempDir())
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	g.ingest("gw", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bin: "0", Bout: "0"}, {Pxname: "b.c2.svc.x_80", Svname: "BACKEND", Bin: "0", Bout: "0"}}, t0)
	if g.gatewayMbps("gw") != 0 || g.clusterMbps("c1") != 0 {
		t.Fatalf("no rate on the first poll")
	}
	// 10 s later: c1 moved 12.5 MB (100 Mb), c2 2.5 MB in + 2.5 MB out (40 Mb)
	g.ingest("gw", []haproxy.Stats{{Pxname: "a.c1.svc.x_80", Svname: "BACKEND", Bin: "0", Bout: "12500000"}, {Pxname: "b.c2.svc.x_80", Svname: "BACKEND", Bin: "2500000", Bout: "2500000"}}, t0.Add(10*time.Second))
	near := func(a, b float64) bool { return a > b-0.01 && a < b+0.01 }
	if !near(g.clusterMbps("c1"), 10) || !near(g.clusterMbps("c2"), 4) || !near(g.gatewayMbps("gw"), 14) {
		t.Fatalf("rates: c1=%v c2=%v gw=%v", g.clusterMbps("c1"), g.clusterMbps("c2"), g.gatewayMbps("gw"))
	}
}
