package config

import (
	"testing"
)

func TestMigrateStorageToCanonical_EmptyDeployment(t *testing.T) {
	d := &Deployment{}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.StorageLayoutVersion != StorageLayoutV2 {
		t.Errorf("expected StorageLayoutV2, got %d", d.StorageLayoutVersion)
	}
	if d.PhysicalVolumeStrategy != PhysicalVolumeStrategyPerVolume {
		t.Errorf("expected per-volume, got %q", d.PhysicalVolumeStrategy)
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}
}

func TestMigrateStorageToCanonical_AlreadyMigrated(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
	}
	if err := MigrateStorageToCanonical(d, "2g"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must remain per-volume (no overwrite)
	if d.PhysicalVolumeStrategy != PhysicalVolumeStrategyPerVolume {
		t.Errorf("expected per-volume to be preserved, got %q", d.PhysicalVolumeStrategy)
	}
	if len(d.AppVolumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(d.AppVolumes))
	}
}

func TestMigrateStorageToCanonical_LegacyVolume(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "config-vol", PoolName: "tank", VolumeDir: "/etc"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "5g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if len(d.AppVolumes) != 1 {
		t.Fatalf("expected 1 canonical volume, got %d", len(d.AppVolumes))
	}
	vol := d.AppVolumes[0]
	// The canonical volume represents the effective old pool-merged volume,
	// so it is named after the pool (not the legacy row), and carries a
	// RuntimeName override preserving the old "{appname}-{pool}" identity.
	if vol.Name != "tank" {
		t.Errorf("expected name=tank (pool-derived), got %q", vol.Name)
	}
	if vol.Pool != "tank" {
		t.Errorf("expected pool=tank, got %q", vol.Pool)
	}
	if vol.Size != "5g" {
		t.Errorf("expected size=5g, got %q", vol.Size)
	}
	if vol.RuntimeName != "{name}-tank" {
		t.Errorf("expected runtimeName={name}-tank, got %q", vol.RuntimeName)
	}
	if vol.CanonicalOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected volume canonical origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, vol.CanonicalOrigin)
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}

	// Synthesized directory source for VolumeDir
	if len(d.AppSources) < 1 {
		t.Fatalf("expected at least 1 canonical source, got %d", len(d.AppSources))
	}
	src := d.AppSources[0]
	if src.Type != AppSourceDirectory {
		t.Errorf("expected directory source, got %q", src.Type)
	}
	if src.VolumeName != "tank" {
		t.Errorf("expected volumeName=tank, got %q", src.VolumeName)
	}
	if src.BasePath != "/etc" {
		t.Errorf("expected basePath=/etc, got %q", src.BasePath)
	}
	if src.Name != "config-vol-root" {
		t.Errorf("expected name=config-vol-root, got %q", src.Name)
	}
}

// TestMigrateStorageToCanonical_MergedPoolGroup verifies the behavior-based
// migration contract: legacy volumes that the old runtime would have merged
// into a single physical OpenSVC volume (because they share a pool — see
// Volumes.GroupByPool / OpenSVCGetAppVolumeSections pre-canonical) must be
// migrated into exactly ONE canonical AppVolume, with one AppSource per
// legacy volume and a RuntimeName preserving the old "{appname}-{pool}"
// physical identity — not one AppVolume per legacy row.
func TestMigrateStorageToCanonical_MergedPoolGroup(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "etc-vol", PoolName: "tank", VolumeDir: "/etc"},
				{Name: "var-vol", PoolName: "tank", VolumeDir: "/var"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	if len(d.AppVolumes) != 1 {
		t.Fatalf("expected exactly 1 merged canonical volume for shared pool %q, got %d: %v", "tank", len(d.AppVolumes), d.AppVolumes)
	}
	vol := d.AppVolumes[0]
	if vol.Pool != "tank" {
		t.Errorf("expected merged volume pool=tank, got %q", vol.Pool)
	}
	if vol.RuntimeName != "{name}-tank" {
		t.Errorf("expected merged volume runtimeName={name}-tank (preserves old pooled identity), got %q", vol.RuntimeName)
	}
	if vol.CanonicalOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected merged volume canonical origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, vol.CanonicalOrigin)
	}

	if len(d.AppSources) != 2 {
		t.Fatalf("expected 2 directory sources (one per legacy volume), got %d: %v", len(d.AppSources), d.AppSources)
	}
	seenBasePaths := map[string]bool{}
	for _, s := range d.AppSources {
		if s.Type != AppSourceDirectory {
			t.Errorf("expected directory source, got %q", s.Type)
		}
		if s.VolumeName != vol.Name {
			t.Errorf("expected source %q to reference merged volume %q, got %q", s.Name, vol.Name, s.VolumeName)
		}
		seenBasePaths[s.BasePath] = true
	}
	if !seenBasePaths["/etc"] || !seenBasePaths["/var"] {
		t.Errorf("expected sources for both /etc and /var, got base paths: %v", seenBasePaths)
	}

	if d.PhysicalVolumeStrategy != PhysicalVolumeStrategyPerVolume {
		t.Errorf("expected per-volume strategy, got %q", d.PhysicalVolumeStrategy)
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}
}

// TestMigrateStorageToCanonical_SeparatePoolGroups verifies that legacy
// volumes on distinct pools — which the old runtime never merged — remain
// distinct canonical volumes after migration (one per pool, each with its
// own RuntimeName preserving its old "{appname}-{pool}" identity).
func TestMigrateStorageToCanonical_SeparatePoolGroups(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "etc-vol", PoolName: "tank", VolumeDir: "/etc"},
				{Name: "data-vol", PoolName: "ssd", VolumeDir: "/data"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	if len(d.AppVolumes) != 2 {
		t.Fatalf("expected 2 separate canonical volumes (distinct pools), got %d: %v", len(d.AppVolumes), d.AppVolumes)
	}
	pools := map[string]string{} // pool -> runtimeName
	for _, v := range d.AppVolumes {
		pools[v.Pool] = v.RuntimeName
		if v.CanonicalOrigin != CanonicalStorageOriginLegacyPooledV1 {
			t.Errorf("expected pool %q canonical origin %q, got %q", v.Pool, CanonicalStorageOriginLegacyPooledV1, v.CanonicalOrigin)
		}
	}
	if pools["tank"] != "{name}-tank" {
		t.Errorf("expected tank volume runtimeName={name}-tank, got %q", pools["tank"])
	}
	if pools["ssd"] != "{name}-ssd" {
		t.Errorf("expected ssd volume runtimeName={name}-ssd, got %q", pools["ssd"])
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}
	if len(d.AppSources) != 2 {
		t.Fatalf("expected 2 directory sources, got %d: %v", len(d.AppSources), d.AppSources)
	}
}

func TestMigrateStorageToCanonical_GitClone(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "data-vol", PoolName: "ssd", VolumeDir: "/data"},
			},
			GitClones: GitClones{
				{
					Name:       "my-git",
					VolumeName: "data-vol",
					VolumeDir:  "/data/repo",
					GitRepo:    "github.com/example/repo",
					GitBranch:  "main",
					GitUser:    "user",
					GitPass:    "pass",
				},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	var gitSrc *AppSource
	for _, s := range d.AppSources {
		if s.Name == "my-git" {
			gitSrc = s
			break
		}
	}
	if gitSrc == nil {
		t.Fatal("git source not found after migration")
	}
	if gitSrc.Type != AppSourceGit {
		t.Errorf("expected git source type, got %q", gitSrc.Type)
	}
	if gitSrc.Repo != "github.com/example/repo" {
		t.Errorf("unexpected repo %q", gitSrc.Repo)
	}
	if gitSrc.BasePath != "/data/repo" {
		t.Errorf("expected basePath=/data/repo, got %q", gitSrc.BasePath)
	}
}

func TestMigrateStorageToCanonical_S3Mount(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "media-vol", PoolName: "hdd", VolumeDir: "/media"},
			},
			S3Mounts: S3Mounts{
				{
					Name:       "my-s3",
					VolumeName: "media-vol",
					VolumeDir:  "/media/s3",
					Bucket:     "my-bucket",
					Region:     "us-east-1",
					Endpoint:   "http://minio:9000",
				},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "10g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	var s3Src *AppSource
	for _, s := range d.AppSources {
		if s.Name == "my-s3" {
			s3Src = s
			break
		}
	}
	if s3Src == nil {
		t.Fatal("s3 source not found after migration")
	}
	if s3Src.Type != AppSourceS3 {
		t.Errorf("expected s3 source type, got %q", s3Src.Type)
	}
	if s3Src.Bucket != "my-bucket" {
		t.Errorf("expected bucket=my-bucket, got %q", s3Src.Bucket)
	}
}

func TestMigrateStorageToCanonical_PathMapping(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "web-vol", PoolName: "tank", VolumeDir: "/web"},
			},
			GitClones: GitClones{
				{Name: "site-git", VolumeName: "web-vol", VolumeDir: "/web/site", GitRepo: "github.com/example/site", GitBranch: "main"},
			},
		},
		Paths: PathMaps{
			{
				Name:       "nginx-conf",
				SourceType: SourceGit,
				SourceName: "site-git",
				SourcePath: "/nginx/nginx.conf",
				DockerPath: "/etc/nginx/conf.d/default.conf",
				VolumeName: "web-vol",
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	if len(d.AppMounts) == 0 {
		t.Fatal("expected at least 1 canonical mount after migration")
	}
	m := d.AppMounts[0]
	if m.TargetPath != "/etc/nginx/conf.d/default.conf" {
		t.Errorf("unexpected targetPath %q", m.TargetPath)
	}
	if m.SourceName != "site-git" {
		t.Errorf("unexpected sourceName %q", m.SourceName)
	}
}

func TestMigrateStorageToCanonical_Idempotent(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "v1", PoolName: "pool", VolumeDir: "/v1"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("first migration error: %v", err)
	}
	vols1 := len(d.AppVolumes)
	srcs1 := len(d.AppSources)

	// Second call — should be no-op.
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("second migration error: %v", err)
	}
	if len(d.AppVolumes) != vols1 {
		t.Errorf("volumes count changed on second migration: %d -> %d", vols1, len(d.AppVolumes))
	}
	if len(d.AppSources) != srcs1 {
		t.Errorf("sources count changed on second migration: %d -> %d", srcs1, len(d.AppSources))
	}
}

func TestMigrateStorageToCanonical_NormalizesAndDeduplicatesMountTargets(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{{Name: "web-vol", PoolName: "tank", VolumeDir: "/web"}},
			GitClones: GitClones{{Name: "site-git", VolumeName: "web-vol", VolumeDir: "/web/site", GitRepo: "github.com/example/site", GitBranch: "main"}},
		},
		Paths: PathMaps{
			{
				Name:       "canonical-a",
				SourceType: SourceGit,
				SourceName: "site-git",
				SourcePath: "/conf",
				DockerPath: "/app/data",
				VolumeName: "web-vol",
			},
			{
				Name:       "canonical-b",
				SourceType: SourceGit,
				SourceName: "site-git",
				SourcePath: "/conf",
				DockerPath: "/app//data/",
				VolumeName: "web-vol",
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if len(d.AppMounts) != 1 {
		t.Fatalf("expected 1 canonical mount after normalization/dedup, got %d", len(d.AppMounts))
	}
	if got := d.AppMounts[0].TargetPath; got != "/app/data" {
		t.Fatalf("expected normalized targetPath /app/data, got %q", got)
	}
}

func TestValidateCanonicalStorage_DuplicateVolume(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
			{Name: "v1", Pool: "ssd", Size: "2g"},
		},
	}
	if err := d.ValidateCanonicalStorage(); err == nil {
		t.Error("expected duplicate volume error, got nil")
	}
}

func TestValidateCanonicalStorage_SourceBadPath(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/../../etc"},
		},
	}
	if err := d.ValidateCanonicalStorage(); err == nil {
		t.Error("expected path traversal error, got nil")
	}
}

func TestValidateCanonicalStorage_MountRelativeTarget(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{SourceName: "s1", TargetPath: "relative/path"},
		},
	}
	if err := d.ValidateCanonicalStorage(); err == nil {
		t.Error("expected relative targetPath error, got nil")
	}
}

func TestValidateCanonicalStorage_AcceptsUnnormalizedMountTargetPath(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"}},
		AppMounts:  AppMounts{{SourceName: "s1", TargetPath: "/app//data/"}},
	}
	if err := d.ValidateCanonicalStorage(); err != nil {
		t.Fatalf("expected mount with unnormalized targetPath to validate, got %v", err)
	}
	// ValidateCanonicalStorage must be read-only: it must not rewrite the
	// stored targetPath in place.
	if got := d.AppMounts[0].TargetPath; got != "/app//data/" {
		t.Fatalf("ValidateCanonicalStorage must not mutate targetPath, got %q", got)
	}
}

func TestNormalizeCanonicalStorage_RewritesMountPaths(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion: StorageLayoutV2,
		AppVolumes:           AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"}},
		AppMounts:  AppMounts{{SourceName: "s1", TargetPath: "/app//data/", SourceSubPath: "sub//dir/"}},
	}
	if changed := d.NormalizeCanonicalStorage(); !changed {
		t.Fatal("expected NormalizeCanonicalStorage to report a change")
	}
	if got := d.AppMounts[0].TargetPath; got != "/app/data" {
		t.Fatalf("expected normalized targetPath /app/data, got %q", got)
	}
	if got := d.AppMounts[0].SourceSubPath; got != "sub/dir" {
		t.Fatalf("expected normalized sourceSubPath sub/dir, got %q", got)
	}
	if changed := d.NormalizeCanonicalStorage(); changed {
		t.Fatal("expected NormalizeCanonicalStorage to be idempotent and report no further change")
	}
}

func TestNormalizeCanonicalStorage_NoOpOnLegacyDeployment(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion: StorageLayoutLegacy,
		AppMounts:            AppMounts{{SourceName: "s1", TargetPath: "/app//data/", SourceSubPath: "sub//dir/"}},
	}
	if changed := d.NormalizeCanonicalStorage(); changed {
		t.Fatal("expected NormalizeCanonicalStorage to be a no-op on a non-canonical deployment")
	}
	if got := d.AppMounts[0].TargetPath; got != "/app//data/" {
		t.Fatalf("expected legacy deployment's AppMounts to be left untouched, got %q", got)
	}
}

func TestValidateCanonicalLegacyOrigin_RejectsNativeRuntimeName(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		AppVolumes:             AppVolumes{{Name: "tank", Pool: "tank", Size: "1g", RuntimeName: "{name}-tank"}},
	}
	if err := d.ValidateCanonicalLegacyOrigin(); err == nil {
		t.Fatal("expected native V2 config with runtime-name to be rejected")
	}
}

func TestValidateCanonicalLegacyOrigin_RejectsNativeLegacyPooled(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyLegacyPooled,
		AppVolumes:             AppVolumes{{Name: "tank", Pool: "tank", Size: "1g"}},
	}
	if err := d.ValidateCanonicalLegacyOrigin(); err == nil {
		t.Fatal("expected native V2 config with legacy-pooled strategy to be rejected")
	}
}

func TestValidateCanonicalLegacyOrigin_AllowsMigratedRuntimeName(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		CanonicalStorageOrigin: CanonicalStorageOriginLegacyPooledV1,
		AppVolumes:             AppVolumes{{Name: "tank", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1, RuntimeName: "{name}-tank"}},
	}
	if err := d.ValidateCanonicalLegacyOrigin(); err != nil {
		t.Fatalf("expected migrated legacy metadata to validate, got %v", err)
	}
}

func TestValidateCanonicalLegacyOrigin_AllowsMigratedAndNewVolumes(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		CanonicalStorageOrigin: CanonicalStorageOriginLegacyPooledV1,
		AppVolumes: AppVolumes{
			{Name: "tank", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1, RuntimeName: "{name}-tank"},
			{Name: "logs", Pool: "tank", Size: "1g"},
		},
	}
	if err := d.ValidateCanonicalLegacyOrigin(); err != nil {
		t.Fatalf("expected mixed migrated/native V2 metadata to validate, got %v", err)
	}
}

func TestValidateCanonicalLegacyOrigin_RejectsNativeVolumeWithCanonicalOrigin(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		AppVolumes:             AppVolumes{{Name: "tank", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1}},
	}
	if err := d.ValidateCanonicalLegacyOrigin(); err == nil {
		t.Fatal("expected native V2 config with canonical-origin metadata to be rejected")
	}
}

func TestValidateCanonicalStorage_DuplicateMountTargetAfterNormalization(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"}},
		AppMounts: AppMounts{
			{SourceName: "s1", TargetPath: "/app/data"},
			{SourceName: "s1", TargetPath: "/app//data/"},
		},
	}
	if err := d.ValidateCanonicalStorage(); err == nil {
		t.Fatal("expected duplicate mount targetPath error after normalization")
	}
}

func TestInsertAppVolume_RequiresPool(t *testing.T) {
	d := &Deployment{}
	err := d.InsertAppVolume(&AppVolume{Name: "v", Size: "1g"})
	if err == nil {
		t.Error("expected error for missing pool")
	}
}

func TestDropAppVolume_BlockedBySource(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
	}
	if err := d.DropAppVolume("v1"); err == nil {
		t.Error("expected error when source still references the volume")
	}
}

func TestInsertAppMount_DuplicateTarget(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{SourceName: "s1", TargetPath: "/app/data"},
		},
	}
	err := d.InsertAppMount(&AppMount{SourceName: "s1", TargetPath: "/app/data"})
	if err == nil {
		t.Error("expected error for duplicate targetPath")
	}
}

func TestSynthesizeSourceName_NoCollision(t *testing.T) {
	used := map[string]bool{"vol1-root": true, "vol1-root-2": true}
	name := synthesizeSourceName("vol1", used)
	if name != "vol1-root-3" {
		t.Errorf("expected vol1-root-3, got %q", name)
	}
}

func TestMigrateStorageToCanonical_NameCollisionPreSeeded(t *testing.T) {
	// git source is already named "vol1-root"; synthesized dir source must use "vol1-root-2"
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "vol1", PoolName: "tank", VolumeDir: "/data"},
			},
			GitClones: GitClones{
				{Name: "vol1-root", VolumeName: "vol1", VolumeDir: "/data/git", GitRepo: "github.com/x/y", GitBranch: "main"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	// Git source must keep its name.
	var gitFound, dirFound bool
	for _, s := range d.AppSources {
		if s.Name == "vol1-root" && s.Type == AppSourceGit {
			gitFound = true
		}
		if s.Name == "vol1-root-2" && s.Type == AppSourceDirectory {
			dirFound = true
		}
	}
	if !gitFound {
		t.Error("git source vol1-root not found")
	}
	if !dirFound {
		t.Errorf("expected dir source vol1-root-2, got: %v", d.AppSources)
	}
}

func TestMigrateStorageToCanonical_SizeNormalization(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "v1", PoolName: "pool", VolumeDir: "/data"},
			},
		},
	}
	// bare integer without unit suffix
	if err := MigrateStorageToCanonical(d, "10"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if d.AppVolumes[0].Size != "10g" {
		t.Errorf("expected size=10g, got %q", d.AppVolumes[0].Size)
	}
}

func TestMigrateStorageToCanonical_SizeWithUnit(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "v1", PoolName: "pool", VolumeDir: "/data"},
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "5G"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if d.AppVolumes[0].Size != "5g" {
		t.Errorf("expected size=5g, got %q", d.AppVolumes[0].Size)
	}
}

func TestNormalizePath_Root(t *testing.T) {
	if got := normalizePath("/"); got != "/" {
		t.Errorf("expected '/', got %q", got)
	}
}

func TestNormalizePath_TrailingSlash(t *testing.T) {
	if got := normalizePath("/data/"); got != "/data" {
		t.Errorf("expected '/data', got %q", got)
	}
}

func TestNormalizePath_NoPrefixSlash(t *testing.T) {
	if got := normalizePath("etc/nginx"); got != "/etc/nginx" {
		t.Errorf("expected '/etc/nginx', got %q", got)
	}
}

func TestNormalizePath_DoubleSlash(t *testing.T) {
	if got := normalizePath("//foo//bar"); got != "/foo/bar" {
		t.Errorf("expected '/foo/bar', got %q", got)
	}
}

func TestNormalizePath_DotComponent(t *testing.T) {
	if got := normalizePath("/foo/./bar"); got != "/foo/bar" {
		t.Errorf("expected '/foo/bar', got %q", got)
	}
}

func TestInsertAppSource_DotDotRejected(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
	}
	// ".." in original input must be rejected before normalization
	err := d.InsertAppSource(&AppSource{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/foo/../etc/passwd"})
	if err == nil {
		t.Error("expected error for '..' in basePath, got nil")
	}
}

func TestMigrateStorageToCanonical_NestedPathFlattening(t *testing.T) {
	// parent path has srcpath=/nginx, child path has srcpath=/conf; the canonical
	// mount should have subpath=/nginx/conf.
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "web-vol", PoolName: "tank", VolumeDir: "/web"},
			},
			GitClones: GitClones{
				{Name: "site-git", VolumeName: "web-vol", VolumeDir: "/web/site", GitRepo: "github.com/x/y", GitBranch: "main"},
			},
		},
		Paths: PathMaps{
			{
				Name:       "nginx-parent",
				SourceType: SourceGit,
				SourceName: "site-git",
				SourcePath: "/nginx",
				DockerPath: "/parent",
				VolumeName: "web-vol",
			},
			{
				Name:       "conf-child",
				ParentName: "nginx-parent",
				SourcePath: "/conf",
				DockerPath: "/etc/nginx/nginx.conf",
				VolumeName: "web-vol",
			},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}

	var childMount *AppMount
	for _, m := range d.AppMounts {
		if m.TargetPath == "/etc/nginx/nginx.conf" {
			childMount = m
			break
		}
	}
	if childMount == nil {
		t.Fatal("child mount not found")
	}
	if childMount.SourceSubPath != "/nginx/conf" {
		t.Errorf("expected subpath=/nginx/conf, got %q", childMount.SourceSubPath)
	}
	if childMount.SourceName != "site-git" {
		t.Errorf("expected sourceName=site-git, got %q", childMount.SourceName)
	}
}

func TestUpdateAppVolume_CascadeRename(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "old-vol", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "old-vol", BasePath: "/data"},
		},
	}
	if err := d.UpdateAppVolume("old-vol", &AppVolume{Name: "new-vol", Pool: "tank", Size: "1g"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AppSources[0].VolumeName != "new-vol" {
		t.Errorf("expected cascade to new-vol, got %q", d.AppSources[0].VolumeName)
	}
}

func TestUpdateAppSource_CascadeRename(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "old-src", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{SourceName: "old-src", TargetPath: "/app/data"},
		},
	}
	if err := d.UpdateAppSource("old-src", &AppSource{Name: "new-src", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AppMounts[0].SourceName != "new-src" {
		t.Errorf("expected cascade to new-src, got %q", d.AppMounts[0].SourceName)
	}
}

func TestUpdateAppMount_DuplicateTargetRejected(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{SourceName: "s1", TargetPath: "/app/a"},
			{SourceName: "s1", TargetPath: "/app/b"},
		},
	}
	// Try to rename /app/a to /app/b — should fail
	err := d.UpdateAppMount("/app/a", &AppMount{SourceName: "s1", TargetPath: "/app/b"})
	if err == nil {
		t.Error("expected error for duplicate targetPath")
	}
}

func TestNormalizeSizeWithUnit(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "1g"},
		{"4", "4g"},
		{"4g", "4g"},
		{"4G", "4g"},
		{"512m", "512m"},
		{"1024k", "1024k"},
	}
	for _, tc := range tests {
		if got := NormalizeSizeWithUnit(tc.input); got != tc.want {
			t.Errorf("NormalizeSizeWithUnit(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAppSourceGitVariablePrefix(t *testing.T) {
	src := &AppSource{Name: "my-git-src"}
	got := src.GetGitVariablePrefix()
	if got != "GIT_MY_GIT_SRC_" {
		t.Errorf("unexpected prefix %q", got)
	}
}

func TestAppSourceS3VariablePrefix(t *testing.T) {
	src := &AppSource{Name: "my-s3"}
	got := src.GetS3VariablePrefix()
	if got != "MY_S3_" {
		t.Errorf("unexpected prefix %q", got)
	}
}

func TestAppSourceGitEnvVariables_DefaultBranch(t *testing.T) {
	src := &AppSource{Name: "g", Repo: "github.com/x/y", Branch: ""}
	envs := src.GetGitEnvVariables()
	if envs[GitVarSuffixBranch] != "master" {
		t.Errorf("expected default branch=master, got %q", envs[GitVarSuffixBranch])
	}
}

func TestAppSourceGitEnvVariables_RepoStripsScheme(t *testing.T) {
	src := &AppSource{Name: "g", Repo: "https://github.com/x/y", Branch: "main"}
	envs := src.GetGitEnvVariables()
	if envs[GitVarSuffixRepo] != "github.com/x/y" {
		t.Errorf("expected stripped repo, got %q", envs[GitVarSuffixRepo])
	}
}

func TestMigrateStorageToCanonical_PreservesLegacyFields(t *testing.T) {
	// Legacy fields must remain intact after migration so existing readers continue to work.
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "v1", PoolName: "tank", VolumeDir: "/data"},
			},
			GitClones: GitClones{
				{Name: "my-git", VolumeName: "v1", VolumeDir: "/data/git", GitRepo: "github.com/x/y", GitBranch: "main"},
			},
			S3Mounts: S3Mounts{
				{Name: "my-s3", VolumeName: "v1", VolumeDir: "/data/s3", Bucket: "b", Endpoint: "http://minio:9000"},
			},
		},
		Paths: PathMaps{
			{Name: "p1", SourceType: SourceGit, SourceName: "my-git", DockerPath: "/app", VolumeName: "v1"},
		},
	}
	if err := MigrateStorageToCanonical(d, "1g"); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if len(d.Storages.Volumes) != 1 {
		t.Errorf("expected legacy Volumes preserved (1), got %d", len(d.Storages.Volumes))
	}
	if len(d.Storages.GitClones) != 1 {
		t.Errorf("expected legacy GitClones preserved (1), got %d", len(d.Storages.GitClones))
	}
	if len(d.Storages.S3Mounts) != 1 {
		t.Errorf("expected legacy S3Mounts preserved (1), got %d", len(d.Storages.S3Mounts))
	}
	if len(d.Paths) != 1 {
		t.Errorf("expected legacy Paths preserved (1), got %d", len(d.Paths))
	}
	// Canonical fields must also be populated.
	if d.StorageLayoutVersion != StorageLayoutV2 {
		t.Errorf("expected StorageLayoutV2 after migration, got %d", d.StorageLayoutVersion)
	}
	if len(d.AppVolumes) == 0 {
		t.Error("expected canonical AppVolumes populated after migration")
	}
}

// buildRowBasedMigratedDeployment constructs a deployment in the shape that
// the OLD, row-based migration would have produced for two legacy volumes
// sharing one pool: one AppVolume per legacy row (named after the row, not
// the pool), no RuntimeName, strategy stamped legacy-pooled. This is the
// "wrong" shape RepairLegacyMigrationShape must detect and correct.
func buildRowBasedMigratedDeployment() *Deployment {
	return &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "etc-vol", PoolName: "tank", VolumeDir: "/etc"},
				{Name: "var-vol", PoolName: "tank", VolumeDir: "/var"},
			},
		},
		AppVolumes: AppVolumes{
			{Name: "etc-vol", Pool: "tank", Size: "1g"},
			{Name: "var-vol", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Type: AppSourceDirectory, Name: "etc-vol-root", VolumeName: "etc-vol", BasePath: "/etc"},
			{Type: AppSourceDirectory, Name: "var-vol-root", VolumeName: "var-vol", BasePath: "/var"},
		},
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyLegacyPooled,
	}
}

func TestHasLegacyMigrationShape_DetectsOldShape(t *testing.T) {
	d := buildRowBasedMigratedDeployment()
	if !d.HasLegacyMigrationShape() {
		t.Error("expected row-based migration shape to be detected")
	}
}

func TestHasLegacyMigrationShape_IgnoresCorrectedShape(t *testing.T) {
	// What the corrected migration produces for the same legacy input: one
	// merged AppVolume, RuntimeName set, per-volume strategy.
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "etc-vol", PoolName: "tank", VolumeDir: "/etc"},
				{Name: "var-vol", PoolName: "tank", VolumeDir: "/var"},
			},
		},
		AppVolumes: AppVolumes{
			{Name: "tank", Pool: "tank", Size: "1g", RuntimeName: "{name}-tank"},
		},
		AppSources: AppSources{
			{Type: AppSourceDirectory, Name: "etc-vol-root", VolumeName: "tank", BasePath: "/etc"},
			{Type: AppSourceDirectory, Name: "var-vol-root", VolumeName: "tank", BasePath: "/var"},
		},
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
	}
	if d.HasLegacyMigrationShape() {
		t.Error("corrected canonical shape must not be flagged as row-based migration")
	}
}

func TestHasLegacyMigrationShape_IgnoresHandAuthoredV2(t *testing.T) {
	// A genuine hand-authored v2 config with multiple same-pool volumes:
	// no legacy shadow row-shape match, no legacy-pooled stamp by default.
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "logs", Pool: "tank", Size: "1g"},
			{Name: "data", Pool: "tank", Size: "1g"},
		},
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
	}
	if d.HasLegacyMigrationShape() {
		t.Error("hand-authored per-volume config must not be flagged as row-based migration")
	}

	// Even if a hand-authored config explicitly opts into legacy-pooled, it
	// won't match the legacy-shadow row-shape (no Storages.Volumes preserved),
	// so it must not be flagged.
	d.PhysicalVolumeStrategy = PhysicalVolumeStrategyLegacyPooled
	if d.HasLegacyMigrationShape() {
		t.Error("hand-authored legacy-pooled config without matching legacy shadow must not be flagged")
	}
}

func TestHasLegacyMigrationShape_IgnoresEditedV2AdditionOnWrongBase(t *testing.T) {
	// If a previously wrong row-based migration somehow accumulates a new V2
	// volume before repair, that new volume must not be mistaken for legacy
	// input and re-merged on the next load. The old row-based migration emitted
	// synthesized directory sources named <volume>-root; this user-added V2
	// volume uses an arbitrary source name, so the detector must refuse to treat
	// the whole config as pure legacy-derived row shape.
	d := buildRowBasedMigratedDeployment()
	d.Storages.Volumes = append(d.Storages.Volumes, &Volume{Name: "logs", PoolName: "tank", VolumeDir: "/logs"})
	d.AppVolumes = append(d.AppVolumes, &AppVolume{Name: "logs", Pool: "tank", Size: "1g"})
	d.AppSources = append(d.AppSources, &AppSource{Type: AppSourceDirectory, Name: "logs-src", VolumeName: "logs", BasePath: "/logs"})

	if d.HasLegacyMigrationShape() {
		t.Error("mixed config containing a user-added V2 volume must not be flagged as pure row-based migration")
	}
}

func buildSeparatePoolsRowBasedMigratedDeployment() *Deployment {
	return &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{
				{Name: "etc-vol", PoolName: "tank", VolumeDir: "/etc"},
				{Name: "data-vol", PoolName: "ssd", VolumeDir: "/data"},
			},
		},
		AppVolumes: AppVolumes{
			{Name: "etc-vol", Pool: "tank", Size: "1g"},
			{Name: "data-vol", Pool: "ssd", Size: "1g"},
		},
		AppSources: AppSources{
			{Type: AppSourceDirectory, Name: "etc-vol-root", VolumeName: "etc-vol", BasePath: "/etc"},
			{Type: AppSourceDirectory, Name: "data-vol-root", VolumeName: "data-vol", BasePath: "/data"},
		},
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyLegacyPooled,
	}
}

func TestHasLegacyMigrationShape_DetectsSeparatePools(t *testing.T) {
	// Row-shaped, legacy-pooled, no RuntimeName, mirrors the legacy shadow —
	// the exact shape the old migration always produced, even though every
	// legacy volume happens to sit on its own pool today. It must still be
	// flagged: left stamped legacy-pooled, it stays trapped in pool-grouping
	// runtime semantics forever, so any same-pool AppVolume added later would
	// silently merge into the old {appname}-{pool} physical volume — exactly
	// the forever-re-merge bug this migration rewrite exists to eliminate.
	d := buildSeparatePoolsRowBasedMigratedDeployment()
	if !d.HasLegacyMigrationShape() {
		t.Error("row-shaped legacy-pooled config without RuntimeName must be flagged regardless of current pool sharing")
	}
}

func TestRepairLegacyMigrationShape_CorrectsMergedGroup(t *testing.T) {
	d := buildRowBasedMigratedDeployment()

	repaired, err := RepairLegacyMigrationShape(d, "1g")
	if err != nil {
		t.Fatalf("repair error: %v", err)
	}
	if !repaired {
		t.Fatal("expected repair to report a change")
	}

	if len(d.AppVolumes) != 1 {
		t.Fatalf("expected 1 merged canonical volume after repair, got %d: %v", len(d.AppVolumes), d.AppVolumes)
	}
	vol := d.AppVolumes[0]
	if vol.Pool != "tank" || vol.RuntimeName != "{name}-tank" {
		t.Errorf("expected merged volume pool=tank runtimeName={name}-tank, got pool=%q runtimeName=%q", vol.Pool, vol.RuntimeName)
	}
	if vol.CanonicalOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected merged volume canonical origin %q, got %q", CanonicalStorageOriginLegacyPooledV1, vol.CanonicalOrigin)
	}
	if d.PhysicalVolumeStrategy != PhysicalVolumeStrategyPerVolume {
		t.Errorf("expected per-volume strategy after repair, got %q", d.PhysicalVolumeStrategy)
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q after repair, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}
	if len(d.AppSources) != 2 {
		t.Errorf("expected 2 sources preserved after repair, got %d", len(d.AppSources))
	}
	for _, s := range d.AppSources {
		if s.VolumeName != "tank" {
			t.Errorf("expected source %q to reference merged volume tank, got %q", s.Name, s.VolumeName)
		}
	}
	// Legacy shadow must remain untouched.
	if len(d.Storages.Volumes) != 2 {
		t.Errorf("expected legacy Volumes shadow preserved (2), got %d", len(d.Storages.Volumes))
	}
}

func TestRepairLegacyMigrationShape_CorrectsSeparatePoolGroups(t *testing.T) {
	d := buildSeparatePoolsRowBasedMigratedDeployment()

	repaired, err := RepairLegacyMigrationShape(d, "1g")
	if err != nil {
		t.Fatalf("repair error: %v", err)
	}
	if !repaired {
		t.Fatal("expected repair to report a change")
	}

	if len(d.AppVolumes) != 2 {
		t.Fatalf("expected 2 canonical volumes after repair (one per pool), got %d: %v", len(d.AppVolumes), d.AppVolumes)
	}

	byPool := make(map[string]*AppVolume, len(d.AppVolumes))
	for _, v := range d.AppVolumes {
		byPool[v.Pool] = v
	}

	for _, pool := range []string{"tank", "ssd"} {
		v, ok := byPool[pool]
		if !ok {
			t.Fatalf("expected a canonical volume for pool %q after repair", pool)
		}
		if v.Name != pool {
			t.Errorf("expected volume for pool %q to be renamed to the pool identity, got name %q", pool, v.Name)
		}
		wantRuntimeName := "{name}-" + pool
		if v.RuntimeName != wantRuntimeName {
			t.Errorf("expected RuntimeName %q for pool %q, got %q", wantRuntimeName, pool, v.RuntimeName)
		}
		if v.CanonicalOrigin != CanonicalStorageOriginLegacyPooledV1 {
			t.Errorf("expected CanonicalOrigin %q for pool %q, got %q", CanonicalStorageOriginLegacyPooledV1, pool, v.CanonicalOrigin)
		}
	}

	if d.PhysicalVolumeStrategy != PhysicalVolumeStrategyPerVolume {
		t.Errorf("expected per-volume strategy after repair, got %q", d.PhysicalVolumeStrategy)
	}
	if d.CanonicalStorageOrigin != CanonicalStorageOriginLegacyPooledV1 {
		t.Errorf("expected canonical storage origin %q after repair, got %q", CanonicalStorageOriginLegacyPooledV1, d.CanonicalStorageOrigin)
	}

	if len(d.AppSources) != 2 {
		t.Fatalf("expected 2 sources preserved after repair, got %d", len(d.AppSources))
	}
	for _, s := range d.AppSources {
		if _, ok := byPool[s.VolumeName]; !ok {
			t.Errorf("expected source %q to reference a renamed pool-identity volume, got VolumeName %q", s.Name, s.VolumeName)
		}
	}
}

func TestRepairLegacyMigrationShape_NoOpWhenNotNeeded(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "tank", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1, RuntimeName: "{name}-tank"},
		},
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
	}
	repaired, err := RepairLegacyMigrationShape(d, "1g")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repaired {
		t.Error("expected no-op for an already-correct canonical config")
	}
}

func TestInsertAppVolume_StripsRuntimeName(t *testing.T) {
	d := &Deployment{}
	vol := &AppVolume{Name: "newvol", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1, RuntimeName: "{name}-tank"}
	if err := d.InsertAppVolume(vol); err != nil {
		t.Fatalf("InsertAppVolume error: %v", err)
	}
	if got := d.AppVolumes[0].CanonicalOrigin; got != "" {
		t.Fatalf("expected user-authored insert to strip CanonicalOrigin, got %q", got)
	}
	if got := d.AppVolumes[0].RuntimeName; got != "" {
		t.Fatalf("expected user-authored insert to strip RuntimeName, got %q", got)
	}
	if got := vol.CanonicalOrigin; got != "" {
		t.Fatalf("expected inserted payload to be scrubbed of CanonicalOrigin in place too, got %q", got)
	}
	if got := vol.RuntimeName; got != "" {
		t.Fatalf("expected inserted payload to be scrubbed in place too, got %q", got)
	}
}

func TestUpdateAppVolume_PreservesStoredRuntimeName(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "tank", Pool: "tank", Size: "1g", CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1, RuntimeName: "{name}-tank"}},
	}
	updated := &AppVolume{Name: "tank-renamed", Pool: "tank", Size: "2g", RuntimeName: "{name}-evil"}
	if err := d.UpdateAppVolume("tank", updated); err != nil {
		t.Fatalf("UpdateAppVolume error: %v", err)
	}
	if got := d.AppVolumes[0].RuntimeName; got != "{name}-tank" {
		t.Fatalf("expected stored RuntimeName preserved, got %q", got)
	}
	if got := d.AppVolumes[0].Name; got != "tank-renamed" {
		t.Fatalf("expected rename to apply, got %q", got)
	}
	if got := d.AppVolumes[0].CanonicalOrigin; got != CanonicalStorageOriginLegacyPooledV1 {
		t.Fatalf("expected stored CanonicalOrigin preserved, got %q", got)
	}
	if got := updated.RuntimeName; got != "{name}-tank" {
		t.Fatalf("expected update payload RuntimeName to be overwritten with stored value, got %q", got)
	}
	if got := updated.CanonicalOrigin; got != CanonicalStorageOriginLegacyPooledV1 {
		t.Fatalf("expected update payload CanonicalOrigin to be overwritten with stored value, got %q", got)
	}
}

func TestUpdateAppVolume_RollbackOnCollision(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
			{Name: "v2", Pool: "ssd", Size: "2g"},
		},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
	}
	// Try to rename v1 → v2 (collision with existing volume v2).
	err := d.UpdateAppVolume("v1", &AppVolume{Name: "v2", Pool: "tank", Size: "1g"})
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	// State must be unchanged.
	if d.AppVolumes[0].Name != "v1" {
		t.Errorf("expected rollback: v1 still at index 0, got %q", d.AppVolumes[0].Name)
	}
	if d.AppSources[0].VolumeName != "v1" {
		t.Errorf("expected rollback: source still references v1, got %q", d.AppSources[0].VolumeName)
	}
}

func TestUpdateAppSource_RollbackOnCollision(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "1g"},
		},
		AppSources: AppSources{
			{Name: "src-a", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/a"},
			{Name: "src-b", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/b"},
		},
		AppMounts: AppMounts{
			{SourceName: "src-a", TargetPath: "/app/a"},
		},
	}
	// Rename src-a → src-b (collision).
	err := d.UpdateAppSource("src-a", &AppSource{Name: "src-b", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/a"})
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	// State must be unchanged.
	if d.AppSources[0].Name != "src-a" {
		t.Errorf("expected rollback: src-a still at index 0, got %q", d.AppSources[0].Name)
	}
	if d.AppMounts[0].SourceName != "src-a" {
		t.Errorf("expected rollback: mount still references src-a, got %q", d.AppMounts[0].SourceName)
	}
}

func TestInsertAppSource_RelativeBasePathNormalized(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
	}
	src := &AppSource{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "etc/nginx"}
	if err := d.InsertAppSource(src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AppSources[0].BasePath != "/etc/nginx" {
		t.Errorf("expected normalized basePath=/etc/nginx, got %q", d.AppSources[0].BasePath)
	}
}

func TestInsertAppSource_TrailingSlashStripped(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
	}
	src := &AppSource{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data/"}
	if err := d.InsertAppSource(src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AppSources[0].BasePath != "/data" {
		t.Errorf("expected trailing slash stripped, got %q", d.AppSources[0].BasePath)
	}
}

func TestUpdateAppSource_RelativeBasePathNormalized(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/old"},
		},
	}
	if err := d.UpdateAppSource("s1", &AppSource{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "new/path"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AppSources[0].BasePath != "/new/path" {
		t.Errorf("expected normalized basePath=/new/path, got %q", d.AppSources[0].BasePath)
	}
}

func TestInsertAppMount_RootTargetRejected(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
	}
	err := d.InsertAppMount(&AppMount{SourceName: "s1", TargetPath: "/"})
	if err == nil {
		t.Error("expected error for root targetPath '/', got nil")
	}
}

func TestUpdateAppMount_RootTargetRejected(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{
			{Name: "s1", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{SourceName: "s1", TargetPath: "/app/data"},
		},
	}
	err := d.UpdateAppMount("/app/data", &AppMount{SourceName: "s1", TargetPath: "/"})
	if err == nil {
		t.Error("expected error for root targetPath '/', got nil")
	}
}

func TestDeployment_IsCanonical(t *testing.T) {
	legacy := &Deployment{}
	if legacy.IsCanonical() {
		t.Error("empty deployment should not be canonical")
	}
	canonical := &Deployment{StorageLayoutVersion: StorageLayoutV2}
	if !canonical.IsCanonical() {
		t.Error("v2 deployment should be canonical")
	}
}

func TestInsertAppSource_TypeValidation(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
	}

	// Unknown type rejected.
	err := d.InsertAppSource(&AppSource{Name: "s1", Type: "ftp", VolumeName: "v1", BasePath: "/foo"})
	if err == nil {
		t.Error("expected error for invalid source type 'ftp', got nil")
	}

	// Git without repo rejected.
	err = d.InsertAppSource(&AppSource{Name: "s2", Type: AppSourceGit, VolumeName: "v1", BasePath: "/foo"})
	if err == nil {
		t.Error("expected error for git source without repo, got nil")
	}

	// S3 without bucket rejected.
	err = d.InsertAppSource(&AppSource{Name: "s3", Type: AppSourceS3, VolumeName: "v1", BasePath: "/foo", Endpoint: "http://minio:9000"})
	if err == nil {
		t.Error("expected error for s3 source without bucket, got nil")
	}

	// S3 without endpoint or providerName rejected.
	err = d.InsertAppSource(&AppSource{Name: "s4", Type: AppSourceS3, VolumeName: "v1", BasePath: "/foo", Bucket: "mybucket"})
	if err == nil {
		t.Error("expected error for s3 source without endpoint or providerName, got nil")
	}

	// Valid git source accepted.
	err = d.InsertAppSource(&AppSource{Name: "git-ok", Type: AppSourceGit, VolumeName: "v1", BasePath: "/src", Repo: "github.com/x/y"})
	if err != nil {
		t.Fatalf("unexpected error for valid git source: %v", err)
	}

	// Valid s3 source with endpoint accepted.
	err = d.InsertAppSource(&AppSource{Name: "s3-ok", Type: AppSourceS3, VolumeName: "v1", BasePath: "/s3", Bucket: "mybucket", Endpoint: "http://minio:9000"})
	if err != nil {
		t.Fatalf("unexpected error for valid s3 source: %v", err)
	}

	// Valid s3 source with providerName (no endpoint required).
	err = d.InsertAppSource(&AppSource{Name: "s3-prov", Type: AppSourceS3, VolumeName: "v1", BasePath: "/s3b", Bucket: "b2", ProviderName: "my-provider"})
	if err != nil {
		t.Fatalf("unexpected error for valid s3 source with providerName: %v", err)
	}
}

func TestUpdateAppSource_TypeValidation(t *testing.T) {
	d := &Deployment{
		AppVolumes: AppVolumes{{Name: "v1", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{
			{Name: "existing", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
	}

	// Changing to git without repo rejected.
	err := d.UpdateAppSource("existing", &AppSource{Name: "existing", Type: AppSourceGit, VolumeName: "v1", BasePath: "/data"})
	if err == nil {
		t.Error("expected error for git update without repo, got nil")
	}

	// Valid update to directory (existing type) accepted.
	err = d.UpdateAppSource("existing", &AppSource{Name: "existing", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/new-data"})
	if err != nil {
		t.Fatalf("unexpected error for valid directory update: %v", err)
	}
}

func TestSyncLegacyShadows_NoopOnLegacy(t *testing.T) {
	d := &Deployment{
		Storages: StorageMapping{
			Volumes: Volumes{{Name: "v1", PoolName: "tank"}},
		},
	}
	d.SyncLegacyShadows()
	// Legacy deployment: shadows must not be cleared.
	if len(d.Storages.Volumes) != 1 {
		t.Errorf("expected legacy volumes unchanged, got %d", len(d.Storages.Volumes))
	}
}

func TestSyncLegacyShadows_Canonical(t *testing.T) {
	d := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		AppVolumes: AppVolumes{
			{Name: "v1", Pool: "tank", Size: "2g"},
		},
		AppSources: AppSources{
			{Name: "git-src", Type: AppSourceGit, VolumeName: "v1", BasePath: "/src", Repo: "github.com/x/y", Branch: "main"},
			{Name: "s3-src", Type: AppSourceS3, VolumeName: "v1", BasePath: "/s3", Bucket: "mybucket", Endpoint: "http://minio:9000"},
			{Name: "dir-src", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
		},
		AppMounts: AppMounts{
			{Name: "git-mount", SourceName: "git-src", TargetPath: "/app/src"},
			{Name: "s3-mount", SourceName: "s3-src", TargetPath: "/app/s3"},
			{Name: "dir-mount", SourceName: "dir-src", TargetPath: "/app/data"},
		},
	}

	d.SyncLegacyShadows()

	// Volumes synced — VolumeDir comes from the first directory AppSource's BasePath.
	if len(d.Storages.Volumes) != 1 || d.Storages.Volumes[0].Name != "v1" {
		t.Errorf("expected 1 volume shadow with name=v1, got %v", d.Storages.Volumes)
	}
	if d.Storages.Volumes[0].VolumeDir != "/data" {
		t.Errorf("expected VolumeDir=/data (from dir-src.BasePath), got %q", d.Storages.Volumes[0].VolumeDir)
	}

	// Git source mapped to GitClone.
	if len(d.Storages.GitClones) != 1 {
		t.Fatalf("expected 1 git clone shadow, got %d", len(d.Storages.GitClones))
	}
	gc := d.Storages.GitClones[0]
	if gc.Name != "git-src" || gc.GitRepo != "github.com/x/y" || gc.GitBranch != "main" {
		t.Errorf("unexpected git clone shadow: %+v", gc)
	}

	// S3 source mapped to S3Mount.
	if len(d.Storages.S3Mounts) != 1 {
		t.Fatalf("expected 1 s3 mount shadow, got %d", len(d.Storages.S3Mounts))
	}
	s3 := d.Storages.S3Mounts[0]
	if s3.Name != "s3-src" || s3.Bucket != "mybucket" || s3.Endpoint != "http://minio:9000" {
		t.Errorf("unexpected s3 mount shadow: %+v", s3)
	}

	// Mounts mapped to PathMappings (3 mounts → 3 paths).
	if len(d.Paths) != 3 {
		t.Fatalf("expected 3 path shadows, got %d", len(d.Paths))
	}

	pathByTarget := make(map[string]*PathMapping)
	for _, p := range d.Paths {
		pathByTarget[p.DockerPath] = p
	}

	gitPath, ok := pathByTarget["/app/src"]
	if !ok || gitPath.SourceType != SourceGit || gitPath.SourceName != "git-src" {
		t.Errorf("unexpected git path shadow: %+v", gitPath)
	}
	// Empty SourceSubPath → SourcePath = src.BasePath ("/src").
	if gitPath.SourcePath != "/src" {
		t.Errorf("git path SourcePath: want /src, got %q", gitPath.SourcePath)
	}

	s3Path, ok := pathByTarget["/app/s3"]
	if !ok || s3Path.SourceType != SourceS3 || s3Path.SourceName != "s3-src" {
		t.Errorf("unexpected s3 path shadow: %+v", s3Path)
	}
	if s3Path.SourcePath != "/s3" {
		t.Errorf("s3 path SourcePath: want /s3, got %q", s3Path.SourcePath)
	}

	// Directory source paths must use the volume name as SourceName (not source name)
	// so legacy ResolvePointers can find the backing Volume by name.
	dirPath, ok := pathByTarget["/app/data"]
	if !ok || dirPath.SourceType != SourceVolume || dirPath.SourceName != "v1" {
		t.Errorf("directory path shadow should have SourceType=volume SourceName=v1, got %+v", dirPath)
	}
	// Empty SourceSubPath → SourcePath = src.BasePath ("/data").
	if dirPath.SourcePath != "/data" {
		t.Errorf("directory path SourcePath: want /data, got %q", dirPath.SourcePath)
	}
}

func TestHasMissingLegacyShadows(t *testing.T) {
	// canonicalWithAllFamilies has one source of each shadow-able type (directory,
	// git, s3) so every legacy family (Volumes, GitClones, S3Mounts, Paths) can be
	// dropped or truncated independently to prove per-family count detection.
	canonicalWithAllFamilies := func() *Deployment {
		return &Deployment{
			StorageLayoutVersion:   StorageLayoutV2,
			PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
			AppVolumes:             AppVolumes{{Name: "v1", Pool: "tank", Size: "2g"}},
			AppSources: AppSources{
				{Name: "dir-src", Type: AppSourceDirectory, VolumeName: "v1", BasePath: "/data"},
				{Name: "git-src", Type: AppSourceGit, VolumeName: "v1", BasePath: "/src", Repo: "github.com/x/y", Branch: "main"},
				{Name: "s3-src", Type: AppSourceS3, VolumeName: "v1", BasePath: "/s3", Bucket: "mybucket", Endpoint: "http://minio:9000"},
			},
			AppMounts: AppMounts{
				{Name: "dir-mount", SourceName: "dir-src", TargetPath: "/app/data"},
				{Name: "git-mount", SourceName: "git-src", TargetPath: "/app/src"},
				{Name: "s3-mount", SourceName: "s3-src", TargetPath: "/app/s3"},
			},
		}
	}
	syncedWithAllFamilies := func() *Deployment {
		d := canonicalWithAllFamilies()
		d.SyncLegacyShadows()
		return d
	}

	t.Run("legacy deployment is never reported as missing shadows", func(t *testing.T) {
		d := &Deployment{
			Storages: StorageMapping{Volumes: Volumes{{Name: "v1", PoolName: "tank"}}},
		}
		if d.HasMissingLegacyShadows() {
			t.Error("legacy (v1) deployment must never report missing shadows")
		}
	})

	t.Run("empty canonical deployment has nothing to shadow", func(t *testing.T) {
		d := &Deployment{StorageLayoutVersion: StorageLayoutV2}
		if d.HasMissingLegacyShadows() {
			t.Error("canonical deployment with no AppVolumes/AppSources/AppMounts should not need shadows")
		}
	})

	t.Run("populated canonical model without shadows is detected", func(t *testing.T) {
		d := canonicalWithAllFamilies()
		if !d.HasMissingLegacyShadows() {
			t.Error("expected populated v2 deployment with empty shadows to report missing shadows")
		}
	})

	t.Run("complete shadow set produced by SyncLegacyShadows is not re-reported", func(t *testing.T) {
		d := syncedWithAllFamilies()
		if d.HasMissingLegacyShadows() {
			t.Error("expected no missing shadows immediately after SyncLegacyShadows populated them")
		}
	})

	t.Run("freshly-migrated config with preserved (richer) legacy data is not flagged", func(t *testing.T) {
		// MigrateStorageToCanonical preserves legacy Storages/Paths as-is — possibly
		// with different content than SyncLegacyShadows would regenerate (e.g. a
		// hand-set VolumeDir) — but always with one shadow entry per corresponding
		// legacy element, which 1:1-maps to the derived canonical element. The count
		// based check must treat this as "complete", not "stale", or every load of a
		// migrated config would be rewritten — see the comment on HasMissingLegacyShadows.
		d := &Deployment{
			Storages: StorageMapping{
				Volumes: Volumes{{Name: "data-volume", PoolName: "data", VolumeDir: "data"}},
			},
			Paths: PathMaps{
				{Name: "web-root", SourceType: SourceVolume, SourceName: "data-volume", DockerPath: "/var/www/html", SourcePath: "/"},
			},
		}
		if err := MigrateStorageToCanonical(d, "1g"); err != nil {
			t.Fatalf("migration error: %v", err)
		}
		if !d.IsCanonical() {
			t.Fatalf("expected migration to produce a canonical (v2) deployment")
		}
		if d.HasMissingLegacyShadows() {
			t.Error("expected freshly-migrated config with preserved legacy shadows to not be reported as missing")
		}
	})

	t.Run("freshly-migrated config with deduplicated Paths overcount is not flagged", func(t *testing.T) {
		// MigrateStorageToCanonical deduplicates/skips legacy Paths when deriving
		// AppMounts (e.g. two legacy paths normalizing to the same mount target
		// collapse into a single canonical mount — see
		// TestMigrateStorageToCanonical_NormalizesAndDeduplicatesMountTargets), so a
		// freshly migrated deployment can legitimately end up with MORE preserved
		// legacy Paths than the canonical model would derive. That overcount must
		// not be mistaken for "missing/incomplete" shadows and trigger a flattening
		// regeneration on every subsequent load.
		d := &Deployment{
			Storages: StorageMapping{
				Volumes:   Volumes{{Name: "web-vol", PoolName: "tank", VolumeDir: "/web"}},
				GitClones: GitClones{{Name: "site-git", VolumeName: "web-vol", VolumeDir: "/web/site", GitRepo: "github.com/example/site", GitBranch: "main"}},
			},
			Paths: PathMaps{
				{Name: "canonical-a", SourceType: SourceGit, SourceName: "site-git", SourcePath: "/conf", DockerPath: "/app/data", VolumeName: "web-vol"},
				{Name: "canonical-b", SourceType: SourceGit, SourceName: "site-git", SourcePath: "/conf", DockerPath: "/app//data/", VolumeName: "web-vol"},
			},
		}
		if err := MigrateStorageToCanonical(d, "1g"); err != nil {
			t.Fatalf("migration error: %v", err)
		}
		if !d.IsCanonical() {
			t.Fatalf("expected migration to produce a canonical (v2) deployment")
		}
		if len(d.AppMounts) >= len(d.Paths) {
			t.Fatalf("setup: expected dedup to leave fewer canonical mounts (%d) than preserved legacy paths (%d)",
				len(d.AppMounts), len(d.Paths))
		}
		if d.HasMissingLegacyShadows() {
			t.Error("expected preserved legacy Paths overcount (from migration dedup) to not be reported as missing")
		}
	})

	t.Run("partial loss: volumes shadow missing while others survive", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Storages.Volumes = nil
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when only Storages.Volumes is empty")
		}
	})

	t.Run("partial loss: gitclones shadow missing while others survive", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Storages.GitClones = nil
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when only Storages.GitClones is empty")
		}
	})

	t.Run("partial loss: s3mounts shadow missing while others survive", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Storages.S3Mounts = nil
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when only Storages.S3Mounts is empty")
		}
	})

	t.Run("partial loss: paths shadow missing while others survive", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Paths = nil
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when only Paths is empty")
		}
	})

	t.Run("incomplete: paths shadow present but short an entry", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Paths = d.Paths[:len(d.Paths)-1]
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when Paths has fewer entries than AppMounts resolve to")
		}
	})

	t.Run("incomplete: gitclones shadow present but short an entry", func(t *testing.T) {
		d := syncedWithAllFamilies()
		d.Storages.GitClones = d.Storages.GitClones[:0]
		if !d.HasMissingLegacyShadows() {
			t.Error("expected missing-shadow detection when GitClones has fewer entries than git AppSources")
		}
	})
}

func TestSyncLegacyShadows_SourcePathComposition(t *testing.T) {
	// Tests that SourcePath in the shadow is BasePath+SourceSubPath, not just SourceSubPath.
	base := &Deployment{
		StorageLayoutVersion:   StorageLayoutV2,
		PhysicalVolumeStrategy: PhysicalVolumeStrategyPerVolume,
		AppVolumes: AppVolumes{{Name: "vol", Pool: "tank", Size: "1g"}},
		AppSources: AppSources{
			{Name: "dir-src", Type: AppSourceDirectory, VolumeName: "vol", BasePath: "/src"},
			{Name: "git-src", Type: AppSourceGit, VolumeName: "vol", BasePath: "/repo", Repo: "https://github.com/x/y"},
		},
	}

	tests := []struct {
		name           string
		sourceSubPath  string
		sourceName     string
		targetPath     string
		wantSourcePath string
	}{
		{
			name:           "dir base+subpath",
			sourceName:     "dir-src",
			sourceSubPath:  "/conf",
			targetPath:     "/app/conf",
			wantSourcePath: "/src/conf",
		},
		{
			name:           "dir root subpath slash",
			sourceName:     "dir-src",
			sourceSubPath:  "/",
			targetPath:     "/app/root",
			wantSourcePath: "/src",
		},
		{
			name:           "dir empty subpath",
			sourceName:     "dir-src",
			sourceSubPath:  "",
			targetPath:     "/app/empty",
			wantSourcePath: "/src",
		},
		{
			name:           "git base+subpath",
			sourceName:     "git-src",
			sourceSubPath:  "/config",
			targetPath:     "/etc/cfg",
			wantSourcePath: "/repo/config",
		},
		{
			name:           "git root subpath",
			sourceName:     "git-src",
			sourceSubPath:  "/",
			targetPath:     "/etc/root",
			wantSourcePath: "/repo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srcs := make(AppSources, len(base.AppSources))
			copy(srcs, base.AppSources)
			d := &Deployment{
				StorageLayoutVersion:   base.StorageLayoutVersion,
				PhysicalVolumeStrategy: base.PhysicalVolumeStrategy,
				AppVolumes:             AppVolumes{{Name: "vol", Pool: "tank", Size: "1g"}},
				AppSources:             srcs,
				AppMounts: AppMounts{
					{Name: tc.name, SourceName: tc.sourceName, SourceSubPath: tc.sourceSubPath, TargetPath: tc.targetPath},
				},
			}
			d.SyncLegacyShadows()
			if len(d.Paths) != 1 {
				t.Fatalf("expected 1 path shadow, got %d", len(d.Paths))
			}
			if got := d.Paths[0].SourcePath; got != tc.wantSourcePath {
				t.Errorf("SourcePath: want %q, got %q", tc.wantSourcePath, got)
			}
		})
	}
}

func TestLegacyShadowSourcePath(t *testing.T) {
	tests := []struct {
		basePath      string
		sourceSubPath string
		want          string
	}{
		{"/src", "/conf", "/src/conf"},
		{"/src", "/", "/src"},
		{"/src", "", "/src"},
		{"", "/", "."},
		{"", "", "."},
		{"/", "/", "."},
		{"/data", "/sub/dir", "/data/sub/dir"},
	}
	for _, tc := range tests {
		got := legacyShadowSourcePath(tc.basePath, tc.sourceSubPath)
		if got != tc.want {
			t.Errorf("legacyShadowSourcePath(%q, %q) = %q, want %q", tc.basePath, tc.sourceSubPath, got, tc.want)
		}
	}
}
