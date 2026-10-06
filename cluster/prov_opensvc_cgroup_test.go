package cluster

import "testing"

func TestOpenSVCServiceCgroupSlice(t *testing.T) {
	if got := systemdEscape("pg-logical"); got != `pg\x2dlogical` {
		t.Fatalf("dash: %q", got)
	}
	if got := systemdEscape("belair"); got != "belair" {
		t.Fatalf("plain: %q", got)
	}
	want := `/sys/fs/cgroup/opensvc.slice/opensvc-ns.pg\x2dlogical.slice/opensvc-ns.pg\x2dlogical-svc.pg1.slice`
	if got := openSVCServiceCgroupSlice("pg-logical", "pg1"); got != want {
		t.Fatalf("slice:\n got  %s\n want %s", got, want)
	}
}
