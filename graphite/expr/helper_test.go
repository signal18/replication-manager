package expr

import (
	"strings"
	"testing"
)

func TestInterval(t *testing.T) {

	var tests = []struct {
		t       string
		seconds int32
		sign    int
	}{
		{"1s", 1, 1},
		{"2d", 2 * 60 * 60 * 24, 1},
		{"10hours", 60 * 60 * 10, 1},
		{"7d13h45min21s", 7*24*60*60 + 13*60*60 + 45*60 + 21, 1},
		{"01hours", 60 * 60 * 1, 1},
		{"2d2d", 4 * 60 * 60 * 24, 1},

		{"1s", -1, -1},
		{"+2d", 2 * 60 * 60 * 24, -1},
		{"-10hours", -60 * 60 * 10, -1},
		{"-360h2min", -360*60*60 - 2*60, -1},
	}

	for _, tt := range tests {
		if secs, _ := IntervalString(tt.t, tt.sign); secs != tt.seconds {
			t.Errorf("intervalString(%q)=%d, want %d\n", tt.t, secs, tt.seconds)
		}
	}

	var exceptTests = []struct {
		t       string
		seconds int32
		err     string
		sign    int
	}{
		{"10m10s", 0, "unknown time units", 1},
		{"10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000y", 0, "value out of range", 1},
	}
	for _, tt := range exceptTests {
		secs, err := IntervalString(tt.t, tt.sign)
		if secs != tt.seconds {
			t.Errorf("intervalString(%q)=%d, want %d\n", tt.t, secs, tt.seconds)
		}
		if !strings.Contains(err.Error(), tt.err) {
			t.Errorf("Error of intervalString(%q)=%v, expected to contain %v\n", tt.t, err.Error(), tt.err)
		}
	}
}
