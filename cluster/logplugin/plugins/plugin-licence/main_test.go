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
		uri, now, want string
	}{
		{"company.202612.1", "2026-11-01", ""},          // valid
		{"company.202612.1", "2026-12-15", "WARN0315"},  // within 30 days of 2026-12-31
		{"company.202612.1", "2027-01-01", "ERR00122"},  // expired
		{"", "2026-11-01", "ERR00122"},                  // built without a URI
		{"company.2026-12.1", "2026-11-01", "ERR00122"}, // expiry not YYYYMM
	}
	for _, c := range cases {
		got := evaluate(c.uri, at(c.now), 30)
		key := ""
		if len(got) > 0 {
			key = got[0].ErrKey
		}
		if key != c.want {
			t.Errorf("evaluate(%q, %s) = %q (%v), want %q", c.uri, c.now, key, got, c.want)
		}
	}
}
