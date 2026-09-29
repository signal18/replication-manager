package cluster

import "testing"

func TestParseUnitRatios(t *testing.T) {
	r, err := ParseUnitRatios("cores=1,mem=4g,disk=20g,iops=1000")
	if err != nil || r.CoresPerUnit != 1 || r.MemMBPerUnit != 4096 || r.DiskGBPerUnit != 20 || r.IopsPerUnit != 1000 {
		t.Fatalf("dbu = %+v %v", r, err)
	}
	if r, err = ParseUnitRatios("mem=2048m, cores=1 ,disk=10g"); err != nil || r.MemMBPerUnit != 2048 || r.IopsPerUnit != 0 {
		t.Fatalf("apu = %+v %v", r, err)
	}
	if r, err = ParseUnitRatios("disk=20g"); err != nil || r.DiskGBPerUnit != 20 || r.CoresPerUnit != 0 {
		t.Fatalf("bku = %+v %v", r, err)
	}
	if r, err = ParseUnitRatios("disk=1t"); err != nil || r.DiskGBPerUnit != 1024 {
		t.Fatalf("tera = %+v %v", r, err)
	}
	for _, bad := range []string{"", "cores=x", "foo=1", "cores", "cores=0,mem=0"} {
		if _, err := ParseUnitRatios(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if mustRatio(DefaultRatioDBU).String() != "cores=1,mem=4096m,disk=20g,iops=1000" {
		t.Fatalf("String = %q", mustRatio(DefaultRatioDBU).String())
	}
	m := NewResourceManager()
	if err := m.ApplyRatioSettings("cores=2,mem=8g,disk=40g,iops=2000", "cores=1,mem=2g,disk=10g", "disk=50g"); err != nil {
		t.Fatal(err)
	}
	if m.Ratios(ProfileDatabase).MemMBPerUnit != 8192 || m.Ratios(ProfileStorage).DiskGBPerUnit != 50 {
		t.Fatalf("ratios not applied: %+v", m.AllRatios())
	}
	// A bad setting keeps the previous ratio for that profile and reports it.
	if err := m.ApplyRatioSettings("cores=bad", "cores=1,mem=2g,disk=10g", "disk=50g"); err == nil || m.Ratios(ProfileDatabase).MemMBPerUnit != 8192 {
		t.Fatalf("bad dbu ratio must be refused and the previous kept: %v %+v", err, m.Ratios(ProfileDatabase))
	}
}
