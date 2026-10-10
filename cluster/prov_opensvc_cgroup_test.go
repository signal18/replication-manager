package cluster

import "testing"

func TestOpenSVCServiceCgroupMount(t *testing.T) {
	if got := systemdEscape("pg-logical"); got != `pg\x2dlogical` {
		t.Fatalf("dash: %q", got)
	}
	if got := openSVCServiceCgroupMount("belair", "db1"); got != "/sys/fs/cgroup/opensvc.slice/opensvc-ns.belair.slice/opensvc-ns.belair-svc.db1.slice:/svc-cgroup:ro" {
		t.Fatalf("plain names keep the exact slice: %s", got)
	}
	if got := openSVCServiceCgroupMount("pg-logical", "pg1"); got != "/sys/fs/cgroup/opensvc.slice:/svc-cgroup-root:ro" {
		t.Fatalf("a dashed name gets the tree: %s", got)
	}
}
