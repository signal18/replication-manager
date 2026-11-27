// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package alertmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The alerts go as a JSON array to /api/v2/alerts of every URL, with the bearer token.
func TestPostEveryURL(t *testing.T) {
	hits := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer MARKERTOKEN" {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
		var alerts []Alert
		if err := json.NewDecoder(r.Body).Decode(&alerts); err != nil || len(alerts) != 1 || alerts[0].Labels["alertname"] != "ERR00042" {
			t.Errorf("body %+v %v", alerts, err)
		}
		hits++
	})
	a, b := httptest.NewServer(h), httptest.NewServer(h)
	defer a.Close()
	defer b.Close()
	c := New(a.URL+", "+b.URL+"/api/v2/alerts", "", "", "MARKERTOKEN", false, time.Second)
	if err := c.Post(context.Background(), []Alert{{Labels: map[string]string{"alertname": "ERR00042"}, StartsAt: Timestamp(time.Now())}}); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("%d posts, want one per URL", hits)
	}
}

// One peer down is not a failure; every peer down is, and the error holds no credential.
func TestPostFailureRedacted(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "am" || p != "MARKERPASS" {
			t.Errorf("basic auth %q %q", u, p)
		}
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request: MARKERPASS"))
	}))
	defer bad.Close()
	alerts := []Alert{{Labels: map[string]string{"alertname": "x"}}}

	if err := New(ok.URL+","+bad.URL, "am", "MARKERPASS", "", false, time.Second).Post(context.Background(), alerts); err != nil {
		t.Fatalf("one peer accepted: %v", err)
	}
	userinfo := strings.Replace(bad.URL, "http://", "http://am:MARKERPASS@", 1)
	err := New(bad.URL+","+userinfo, "am", "MARKERPASS", "", false, time.Second).Post(context.Background(), alerts)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("every peer refused: %v", err)
	}
	if strings.Contains(err.Error(), "MARKER") {
		t.Fatalf("a credential in the error: %v", err)
	}
}
