package config

import "testing"

func TestVolume_GetVolumeDirs(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want []string
	}{
		{"empty", "", nil},
		{"single", "data", []string{"data"}},
		{"merged", "etc log var data", []string{"etc", "log", "var", "data"}},
		{"extra whitespace", "  etc   log  ", []string{"etc", "log"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Volume{VolumeDir: tc.dir}
			got := v.GetVolumeDirs()
			if len(got) != len(tc.want) {
				t.Fatalf("GetVolumeDirs() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("GetVolumeDirs() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestNormalizeVolumeDirs(t *testing.T) {
	cases := []struct {
		name string
		dirs []string
		want string
	}{
		{"single", []string{"data"}, "data"},
		{"dedup within one input", []string{"data data"}, "data"},
		{"dedup across inputs preserves first-seen order", []string{"etc log", "log var", "data"}, "etc log var data"},
		{"empty inputs", []string{"", "  "}, ""},
		{"trims surrounding whitespace", []string{"  data  "}, "data"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeVolumeDirs(tc.dirs...); got != tc.want {
				t.Fatalf("NormalizeVolumeDirs(%v) = %q, want %q", tc.dirs, got, tc.want)
			}
		})
	}
}

func TestVolume_Validate(t *testing.T) {
	cases := []struct {
		name    string
		vol     Volume
		wantErr bool
	}{
		{"valid single dir", Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data"}, false},
		{"valid merged dirs", Volume{Name: "app-data", PoolName: "data", VolumeDir: "etc log var data"}, false},
		{"missing name", Volume{PoolName: "data", VolumeDir: "data"}, true},
		{"missing pool", Volume{Name: "data-volume", VolumeDir: "data"}, true},
		{"missing volumedir", Volume{Name: "data-volume", PoolName: "data"}, true},
		{"blank volumedir", Volume{Name: "data-volume", PoolName: "data", VolumeDir: "   "}, true},
		{"traversal in one token", Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data ../etc"}, true},
		{"absolute token", Volume{Name: "data-volume", PoolName: "data", VolumeDir: "/data"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.vol.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestDeployment_InsertVolume_NormalizesVolumeDir(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data data  log"})
	if err != nil {
		t.Fatalf("InsertVolume() error = %v", err)
	}

	got, err := d.GetVolumeByName("data-volume")
	if err != nil {
		t.Fatalf("GetVolumeByName() error = %v", err)
	}
	if got.VolumeDir != "data log" {
		t.Fatalf("VolumeDir = %q, want %q", got.VolumeDir, "data log")
	}
}

func TestDeployment_InsertVolume_RejectsInvalidRow(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	if err := d.InsertVolume(&Volume{Name: "", PoolName: "data", VolumeDir: "data"}); err == nil {
		t.Fatalf("expected error for missing name")
	}
	if err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "", VolumeDir: "data"}); err == nil {
		t.Fatalf("expected error for missing pool name")
	}
	if err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "data", VolumeDir: ""}); err == nil {
		t.Fatalf("expected error for missing volume directory")
	}
	if err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "data", VolumeDir: "../etc"}); err == nil {
		t.Fatalf("expected error for traversal volume directory")
	}
}

func TestDeployment_InsertVolume_RejectsDuplicateName(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	if err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data"}); err != nil {
		t.Fatalf("first InsertVolume() error = %v", err)
	}

	err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "other", VolumeDir: "log"})
	if err == nil {
		t.Fatalf("expected error for duplicate volume name")
	}
}

// TestDeployment_InsertVolume_RejectsDuplicatePool covers Phase 2 task 4:
// new writes for a pool that already has a saved row are rejected, even
// when the name and directory differ from the existing row.
func TestDeployment_InsertVolume_RejectsDuplicatePool(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	if err := d.InsertVolume(&Volume{Name: "app-data", PoolName: "data", VolumeDir: "etc"}); err != nil {
		t.Fatalf("first InsertVolume() error = %v", err)
	}

	err := d.InsertVolume(&Volume{Name: "app-data-2", PoolName: "data", VolumeDir: "log"})
	if err == nil {
		t.Fatalf("expected error inserting a second row for an existing pool")
	}
}

// TestDeployment_GetVolumeByPool covers the shared lookup used by both
// InsertVolume and the volume "poolname" edit path in server/api_app.go to
// enforce the one-row-per-pool invariant.
func TestDeployment_GetVolumeByPool(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	if err := d.InsertVolume(&Volume{Name: "app-data", PoolName: "data", VolumeDir: "etc"}); err != nil {
		t.Fatalf("InsertVolume() error = %v", err)
	}

	if got := d.GetVolumeByPool("data"); got == nil || got.Name != "app-data" {
		t.Fatalf("GetVolumeByPool(data) = %v, want app-data", got)
	}
	if got := d.GetVolumeByPool("docs"); got != nil {
		t.Fatalf("GetVolumeByPool(docs) = %v, want nil", got)
	}
}

func TestDeployment_InsertVolume_AllowsDistinctPools(t *testing.T) {
	d := NewDeploymentConfig()
	d.Storages = *NewStorageMapping()

	if err := d.InsertVolume(&Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data"}); err != nil {
		t.Fatalf("InsertVolume(data) error = %v", err)
	}
	if err := d.InsertVolume(&Volume{Name: "docs-volume", PoolName: "docs", VolumeDir: "docs"}); err != nil {
		t.Fatalf("InsertVolume(docs) error = %v", err)
	}

	if len(d.Storages.Volumes) != 2 {
		t.Fatalf("len(Storages.Volumes) = %d, want 2", len(d.Storages.Volumes))
	}
}

// TestVolumes_GroupByPool_SupportsLegacyDuplicateRows covers Phase 2 task 5:
// GroupByPool is a read path and must keep grouping multiple legacy rows
// that share a poolname, even though InsertVolume now rejects creating new
// duplicates. Legacy duplicates are consolidated by the canonicalization
// migration on load, not by removing read-side support.
func TestVolumes_GroupByPool_SupportsLegacyDuplicateRows(t *testing.T) {
	legacy := Volumes{
		{Name: "app-etc", PoolName: "data", VolumeDir: "etc"},
		{Name: "app-log", PoolName: "data", VolumeDir: "log"},
		{Name: "docs-volume", PoolName: "docs", VolumeDir: "docs"},
	}

	grouped := legacy.GroupByPool()

	if len(grouped["data"]) != 2 {
		t.Fatalf("grouped[data] = %v, want 2 entries", grouped["data"])
	}
	if grouped["data"]["app-etc"] != "etc" || grouped["data"]["app-log"] != "log" {
		t.Fatalf("grouped[data] = %v, want app-etc=etc and app-log=log", grouped["data"])
	}
	if len(grouped["docs"]) != 1 || grouped["docs"]["docs-volume"] != "docs" {
		t.Fatalf("grouped[docs] = %v, want docs-volume=docs", grouped["docs"])
	}
}
