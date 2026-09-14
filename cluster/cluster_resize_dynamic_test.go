package cluster

import "testing"

// TestOpenSVCCPUQuotaKeyword pins the om3 pg_cpu_quota syntax that means "N cores" on
// any node: pct = cores × 100 with the "@all" suffix (om3 divides by maxCpus, which
// "@all" cancels). A bare "300%" would be 3/maxCpus of one core -- the dev3 surprise.
func TestOpenSVCCPUQuotaKeyword(t *testing.T) {
	cases := []struct {
		cores float64
		want  string
	}{
		{0, ""},
		{-1, ""},
		{1, "100%@all"},
		{3, "300%@all"},
		{0.5, "50%@all"},
		{2.25, "225%@all"},
		{24, "2400%@all"},
	}
	for _, c := range cases {
		if got := OpenSVCCPUQuotaKeyword(c.cores); got != c.want {
			t.Errorf("cores=%v: got %q want %q", c.cores, got, c.want)
		}
	}
}
