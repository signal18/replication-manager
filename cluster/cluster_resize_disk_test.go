package cluster

import "testing"

// The configured disk follows the measured datadir in whole gigabytes, ceiling, no grid.
func TestDiskFollowTargetGB(t *testing.T) {
	const GiB = int64(1024 * 1024 * 1024)
	cases := []struct {
		used int64
		want int
	}{
		{19*GiB + 40*1024*1024, 20}, // dev3 db3: 19.04 GB -> 20
		{19 * GiB, 19},
		{2 * GiB, 2},
		{300 * 1024 * 1024, 1}, // a 300 MB database -> 1 GB
		{0, 0},
	}
	for _, c := range cases {
		if got := diskFollowTargetGB(c.used); got != c.want {
			t.Fatalf("diskFollowTargetGB(%d) = %d, want %d", c.used, got, c.want)
		}
	}
}
