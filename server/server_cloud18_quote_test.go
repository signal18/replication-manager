package server

import "testing"

// selfService answers shaped like GET /api/cloud18/self-service once JSON-decoded.
func ssFixture(enabled bool, reason string, freeDbu, freeApu, dbuPrice, apuPrice float64) map[string]any {
	ss := map[string]any{
		"enabled": enabled, "defaultDbu": 2.0, "defaultApu": 4.0, "defaultBku": 1.0, "remaining": 3.0,
		"pool":   map[string]any{"known": true, "freeDbu": freeDbu, "freeApu": freeApu},
		"prices": map[string]any{"currency": "EUR", "dbu": dbuPrice, "apu": apuPrice, "bku": 0.0},
	}
	if reason != "" {
		ss["reason"] = reason
		if !enabled && reason[:2] == "no" {
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
