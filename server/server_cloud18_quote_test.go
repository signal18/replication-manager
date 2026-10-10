package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/peer"
)

// selfService answers shaped like GET /api/cloud18/self-service once JSON-decoded.
func ssFixture(enabled bool, reason string, freeDbu, freeApu, dbuPrice, apuPrice float64) map[string]any {
	ss := map[string]any{
		"enabled": enabled, "defaultDbu": 2.0, "defaultApu": 4.0, "defaultBku": 1.0, "remaining": 3.0,
		"pool":   map[string]any{"known": true, "freeDbu": freeDbu, "freeApu": freeApu},
		"prices": map[string]any{"currency": "EUR", "dbu": dbuPrice, "apu": apuPrice, "bku": 0.0},
	}
	if reason != "" {
		ss["reason"] = reason
		if !enabled && strings.HasPrefix(reason, "no") {
			ss["poolNote"] = reason
		}
	}
	return ss
}

func quoteSpec() Cloud18ClusterSpec {
	s, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBCount: 2, DBU: 2, APU: 5})
	return s
}

func TestQuoteMonthlyPriceAtFullCapacity(t *testing.T) {
	// RapidSpace prices: 12 / 6.67 -> 2 nodes x 2 DBU x 12 + 5 APU x 6.67 = 48 + 33.35
	q := cloud18QuoteOf(quoteSpec(), ssFixture(true, "", 34.8, 69.6, 12, 6.67))
	if q["monthlyAtFullCapacity"] != 81.35 || q["canCreate"] != true || q["fits"] != true {
		t.Fatalf("quote = %v", q)
	}
	if u := q["units"].(map[string]float64); u["dbu"] != 4 || u["apu"] != 5 {
		t.Fatalf("units = %v", u)
	}
}

func TestQuotePoolVerdictRetakenForTheRequest(t *testing.T) {
	// the infrastructure refused its DEFAULT cluster for the pool, the request is smaller and fits
	q := cloud18QuoteOf(quoteSpec(), ssFixture(false, "no free DBU on this infrastructure", 4.5, 10, 15, 8.33))
	if q["canCreate"] != true {
		t.Fatalf("a request that fits must be creatable: %v", q)
	}
	// it does not fit: refused with the pool reason
	q = cloud18QuoteOf(quoteSpec(), ssFixture(false, "no free DBU on this infrastructure", -2.4, -2.4, 15, 8.33))
	if q["canCreate"] != false || q["fits"] != false || q["monthlyAtFullCapacity"] != 101.65 {
		t.Fatalf("full pool: %v", q)
	}
	// the switch is off: refused with the infrastructure's own reason, still priced
	q = cloud18QuoteOf(quoteSpec(), ssFixture(false, "self-service cluster creation is disabled", 50, 50, 30, 16.67))
	if q["canCreate"] != false || q["reason"] != "self-service cluster creation is disabled" || q["monthlyAtFullCapacity"] != 203.35 {
		t.Fatalf("switch off: %v", q)
	}
}

func TestQuoteUnknownPrice(t *testing.T) {
	ss := ssFixture(true, "", 50, 50, 0, 0)
	delete(ss, "prices")
	if q := cloud18QuoteOf(quoteSpec(), ss); q["monthlyAtFullCapacity"] != nil || q["price"] == nil {
		t.Fatalf("an infrastructure without prices must say unknown, never 0: %v", q)
	}
}

func TestQuoteSpecProxyCount(t *testing.T) {
	s, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", ProxyCount: 2})
	if err != nil || s.ProxyCount != 2 || len(plannedHosts(s)) != 2+2+1 {
		t.Fatalf("2 proxies: %v %v %v", s, err, plannedHosts(s))
	}
	if s, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", Proxy: "none", ProxyCount: 2}); s.ProxyCount != 0 {
		t.Fatalf("proxy none keeps no proxy: %v", s)
	}
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", ProxyCount: 4}); err == nil {
		t.Fatal("4 proxies must be refused")
	}
}

func TestDefaultRequestResolved(t *testing.T) {
	d := defaultRequestOf(ssFixture(true, "", 10, 10, 12, 6.67))
	want := map[string]any{"db_image": "mariadb:lts", "db_count": 2, "dbu": 2.0, "proxy": "haproxy", "proxy_count": 1, "apu": 4.0, "bku": 1.0}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %v, want %v", k, d[k], v)
		}
	}
	if apps, _ := d["apps"].([]string); len(apps) != 1 || apps[0] != "phpmyadmin" {
		t.Errorf("apps = %v", d["apps"])
	}
}

// TestQuoteReadsPricesThroughTheStatusFilter: the quote is computed from what
// selfServiceStatusOfTimeout keeps of the infrastructure's answer, so the prices
// must survive that filter (they did not: every quote said "unknown").
func TestQuoteReadsPricesThroughTheStatusFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ssFixture(true, "", 34.8, 69.6, 12, 6.67))
	}))
	defer srv.Close()
	ss, _, err := selfServiceStatusOfTimeout(&peerSession{base: srv.URL}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	q := cloud18QuoteOf(quoteSpec(), ss)
	if q["monthlyAtFullCapacity"] != 81.35 {
		t.Fatalf("quote through the status filter = %v", q)
	}
}

func TestInfraDefinitionOfPeer(t *testing.T) {
	d := cloud18InfraDefinitionOf(&peer.PeerCluster{Cloud18Domain: "bso", Cloud18SubDomain: "bso", Cloud18SubDomainZone: "fr-1",
		Cloud18InfraCPUModel: "EPYC", Cloud18InfraDataCenters: "Ajaccio", Cloud18SlaRepairTime: 4, Cloud18OpenDbops: true})
	sla, _ := d["sla"].(map[string]any)
	if d["partner"] != "bso" || d["zone"] != "bso-fr-1" || d["cpuModel"] != "EPYC" || d["dataCenters"] != "Ajaccio" || sla["repairTime"] != 4.0 || d["dbops"] != true {
		t.Fatalf("definition = %v", d)
	}
}

// The quoted APU is the APU the plan really reserves (review of #1964, item 1).
func TestQuoteAPUIsTheAppliedAPU(t *testing.T) {
	// 2 apps + 1 proxy need at least 3
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", APU: 2, Apps: []string{"a", "b"}}); err == nil {
		t.Fatal("apu below apps + proxies must be refused")
	}
	// 1 app + 2 proxies, apu 4: each proxy gets (4-1)/2 = 1, so 3 are reserved and quoted
	s, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", APU: 4, ProxyCount: 2})
	if err != nil || s.APU != 3 || proxyAPUOf(s) != 1 {
		t.Fatalf("apu 4 with 1 app and 2 proxies: %v %v (per proxy %d)", s.APU, err, proxyAPUOf(s))
	}
	// no proxy: only the apps hold APU
	if s, _ = normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", APU: 5, Proxy: "none"}); s.APU != 1 {
		t.Fatalf("apu with no proxy = apps only: %v", s.APU)
	}
	if s, _ = normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", APU: 7}); s.APU != 7 || proxyAPUOf(s) != 6 {
		t.Fatalf("apu 7, 1 app, 1 proxy: %v per proxy %d", s.APU, proxyAPUOf(s))
	}
}

// The pool-only refusal is read from the infrastructure's flag, the wording only for an
// older release that has no flag (review of #1964, item 3).
func TestQuotePoolBlockedFlag(t *testing.T) {
	ss := ssFixture(false, "some other wording", 50, 50, 12, 6)
	ss["poolBlocked"] = true
	if q := cloud18QuoteOf(quoteSpec(), ss); q["canCreate"] != true {
		t.Fatalf("flagged pool refusal with room for the request must be creatable: %v", q)
	}
	ss["poolBlocked"] = false
	ss["poolNote"] = "some other wording"
	if q := cloud18QuoteOf(quoteSpec(), ss); q["canCreate"] != false {
		t.Fatalf("a refusal the infrastructure says is NOT the pool must stay refused: %v", q)
	}
	delete(ss, "poolBlocked") // older release: the wording decides
	if q := cloud18QuoteOf(quoteSpec(), ss); q["canCreate"] != true {
		t.Fatalf("older release, reason == poolNote: %v", q)
	}
}

// applyRequestedPlan moves the plan through change-plan-units by the difference, per unit.
func TestApplyRequestedPlan(t *testing.T) {
	var calls []string
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/clusters/q":
			_, _ = w.Write([]byte(`{"config":{"provDbDbu":2,"provProxyApu":1}}`))
		case refuse:
			http.Error(w, "plan increase refused", http.StatusForbidden)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	sess := &peerSession{base: srv.URL}
	spec, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBU: 4, APU: 5})
	note, err := applyRequestedPlan(sess, "/api/clusters/q", spec)
	if err != nil || note != "plan DBU 2 -> 4, plan APU 1 -> 4" {
		t.Fatalf("note %q err %v calls %v", note, err, calls)
	}
	want := []string{"GET /api/clusters/q", "POST /api/clusters/q/settings/actions/change-plan-units/DBU/2", "POST /api/clusters/q/settings/actions/change-plan-units/APU/3"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	calls, refuse = nil, true
	if _, err := applyRequestedPlan(sess, "/api/clusters/q", spec); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused plan must fail: %v", err)
	}
}
