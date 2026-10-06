//go:build clients
// +build clients

package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// eventsServer answers like GET .../events in list mode: pages of pageSize
// events out of total, X-Total-Count set, and records the offsets asked for.
func eventsServer(t *testing.T, total, pageSize int, offsets *[]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		*offsets = append(*offsets, off)
		var page []map[string]string
		for i := off; i < total && i < off+pageSize; i++ {
			page = append(page, map[string]string{"db": "app", "name": fmt.Sprintf("ev%03d", i)})
		}
		if page == nil {
			page = []map[string]string{}
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "\t")
		enc.Encode(page)
	}))
}

func TestCliStreamEventsPages(t *testing.T) {
	for _, tc := range []struct{ total, pageSize int }{{5, 2}, {4, 2}, {0, 3}, {1, 100}, {250, 100}} {
		var offsets []int
		srv := eventsServer(t, tc.total, tc.pageSize, &offsets)
		var out bytes.Buffer
		if err := cliStreamEvents(srv.Client().Do, "tok", srv.URL+"/events", &out); err != nil {
			t.Fatalf("total %d page %d: %v", tc.total, tc.pageSize, err)
		}
		srv.Close()
		var events []map[string]string
		if err := json.Unmarshal(out.Bytes(), &events); err != nil {
			t.Fatalf("total %d page %d: the output is not one JSON array: %v\n%s", tc.total, tc.pageSize, err, out.String())
		}
		if len(events) != tc.total {
			t.Fatalf("total %d page %d: %d events written", tc.total, tc.pageSize, len(events))
		}
		for i, e := range events {
			if e["name"] != fmt.Sprintf("ev%03d", i) {
				t.Fatalf("total %d page %d: event %d is %v (order or duplicate)", tc.total, tc.pageSize, i, e)
			}
		}
		// one request per page, each at the next offset; no extra request after the total
		want := []int{0}
		for o := tc.pageSize; o < tc.total; o += tc.pageSize {
			want = append(want, o)
		}
		if fmt.Sprint(offsets) != fmt.Sprint(want) {
			t.Fatalf("total %d page %d: offsets %v, want %v", tc.total, tc.pageSize, offsets, want)
		}
	}
}

func TestCliStreamEventsError(t *testing.T) {
	var offsets []int
	srv := eventsServer(t, 3, 2, &offsets)
	defer srv.Close()
	var out bytes.Buffer
	err := cliStreamEvents(srv.Client().Do, "wrong", srv.URL+"/events", &out)
	if err == nil || !strings.Contains(err.Error(), "No valid ACL") {
		t.Fatalf("a refused request must return the server's message, got %v", err)
	}
	if strings.Contains(out.String(), "]") {
		t.Fatalf("a failed stream must not close the array as if complete: %q", out.String())
	}
}
