package server

import (
	"net/url"
	"testing"
)

func TestEventsPage(t *testing.T) {
	const max = 100
	ok := []struct {
		query         string
		limit, offset int
	}{
		{"", max, 0},          // no limit: the maximum, never unlimited
		{"limit=0", max, 0},   // 0: the maximum, never unlimited
		{"limit=1", 1, 0},     // smallest page
		{"limit=100", max, 0}, // the maximum itself
		{"limit=20&offset=40", 20, 40},
		{"offset=7", max, 7},
	}
	for _, tc := range ok {
		q, _ := url.ParseQuery(tc.query)
		limit, offset, err := eventsPage(q, max)
		if err != nil || limit != tc.limit || offset != tc.offset {
			t.Errorf("%q: limit %d offset %d err %v, want %d %d", tc.query, limit, offset, err, tc.limit, tc.offset)
		}
	}
	for _, bad := range []string{"limit=101", "limit=-1", "limit=x", "limit=1.5", "offset=-1", "offset=x"} {
		q, _ := url.ParseQuery(bad)
		if _, _, err := eventsPage(q, max); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
