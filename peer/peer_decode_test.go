package peer

import "testing"

func TestPeerClusterSizesWithUnits(t *testing.T) {
	list, skipped, err := DecodePeerList([]byte(`[
		{"cluster-name":"tamarin","prov-db-memory":"4G","prov-db-disk-size":"20G","prov-db-cpu-cores":"2","prov-db-disk-iops":"300","cloud18-shared":"true"},
		{"cluster-name":"figari","prov-db-memory":"131072","prov-db-disk-size":"600","prov-db-cpu-cores":16,"prov-db-disk-iops":50000},
		{"cluster-name":"x","prov-db-memory":"16384M","prov-db-disk-size":"1T"},
		{"cluster-name":"empty","prov-db-memory":"","prov-db-disk-size":null}
	]`))
	if err != nil || len(skipped) != 0 {
		t.Fatalf("err=%v skipped=%v", err, skipped)
	}
	want := []struct {
		mem, cores int
		disk, iops int64
		shared     bool
	}{{4096, 2, 20, 300, true}, {131072, 16, 600, 50000, false}, {16384, 0, 1024, 0, false}, {0, 0, 0, 0, false}}
	for i, w := range want {
		p := list[i]
		if p.ProvDbMemory != w.mem || p.ProvDbCpuCores != w.cores || p.ProvDbDiskSize != w.disk || p.ProvDbDiskIops != w.iops || p.Cloud18Shared != w.shared {
			t.Errorf("%s: mem=%d cores=%d disk=%d iops=%d shared=%v, want %+v", p.ClusterName, p.ProvDbMemory, p.ProvDbCpuCores, p.ProvDbDiskSize, p.ProvDbDiskIops, p.Cloud18Shared, w)
		}
	}
}

func TestDecodePeerListSkipsABadEntry(t *testing.T) {
	list, skipped, err := DecodePeerList([]byte(`[
		{"cluster-name":"good","cloud18-domain":"signal18","prov-db-memory":"8192"},
		{"cluster-name":"bad","cloud18-domain":"bso","prov-db-memory":"lots"},
		{"cluster-name":"good2","prov-db-memory":"4G"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ClusterName != "good" || list[1].ClusterName != "good2" {
		t.Fatalf("kept %d entries, want good and good2", len(list))
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped=%v, want the bad entry only", skipped)
	}
	if _, _, err := DecodePeerList([]byte(`{"not":"a list"}`)); err == nil {
		t.Fatal("a non-list peer.json must still be an error")
	}
}

func TestPeerClusterLowercaseUnitAndNegativeRefused(t *testing.T) {
	list, skipped, err := DecodePeerList([]byte(`[
		{"cluster-name":"lower","prov-db-memory":"4g","prov-db-disk-size":"20g"},
		{"cluster-name":"negcores","prov-db-cpu-cores":"-1"},
		{"cluster-name":"negmem","prov-db-memory":"-4G"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ProvDbMemory != 4096 || list[0].ProvDbDiskSize != 20 {
		t.Fatalf("lowercase units: %+v", list)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped=%v, want both negative entries", skipped)
	}
}
