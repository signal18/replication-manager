package main

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		uri, expiry, now, want string
	}{
		// Legacy <company>.<YYYYMM>.<seat> — expiry is the URI's middle label (expiry arg empty).
		{"company.202612.1", "", "2026-11-01", ""},          // valid
		{"company.202612.1", "", "2026-12-15", "WARN0315"},  // within 30 days of 2026-12-31
		{"company.202612.1", "", "2027-01-01", "WARN0316"},  // just expired — within the 2-month grace
		{"company.202612.1", "", "2027-02-28", "WARN0316"},  // still within grace (ends 2027-03-01)
		{"company.202612.1", "", "2027-03-15", "ERR00122"},  // grace elapsed — enforcement
		{"", "", "2026-11-01", "ERR00122"},                  // built without a URI
		{"company.2026-12.1", "", "2026-11-01", "ERR00122"}, // expiry not YYYYMM
		// Online full URI (domain.subdomain.zone) — expiry passed separately as the contract end.
		{"acme.east.z1", "2026-12-31", "2026-11-01", ""},         // valid through 2026-12-31
		{"acme.east.z1", "2026-12-31", "2026-12-20", "WARN0315"}, // within 30 days
		{"acme.east.z1", "2026-12-31", "2027-01-02", "WARN0316"}, // expired, in grace
		{"acme.east.z1", "2026-12-31", "2027-03-15", "ERR00122"}, // grace elapsed
		{"acme.east.z1", "202612", "2026-11-01", ""},             // YYYYMM expiry also accepted
		{"acme.east.z1", "", "2026-11-01", "ERR00122"},           // a full URI needs an expiry
		{"company.202612.1", "notadate", "2026-11-01", "ERR00122"},             // bad expiry rejected
	}
	for _, c := range cases {
		got := evaluate(c.uri, c.expiry, at(c.now), 30)
		key := ""
		if len(got) > 0 {
			key = got[0].ErrKey
		}
		if key != c.want {
			t.Errorf("evaluate(%q, %q, %s) = %q (%v), want %q", c.uri, c.expiry, c.now, key, got, c.want)
		}
	}
}
