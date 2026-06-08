package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestGetRuntimeVolumeName_RuntimeNameOverride verifies that an AppVolume
// carrying a RuntimeName override (set by storage migration to preserve a
// pre-existing pooled physical-volume identity) always resolves to that
// override — regardless of PhysicalVolumeStrategy — so already-provisioned
// OpenSVC volumes are never orphaned by the canonical per-volume model.
func TestGetRuntimeVolumeName_RuntimeNameOverride(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				PhysicalVolumeStrategy: config.PhysicalVolumeStrategyPerVolume,
			},
		},
	}

	vol := &config.AppVolume{Name: "tank", Pool: "tank", RuntimeName: "{name}-tank"}

	if got := app.GetRuntimeVolumeName(vol, false); got != "{name}-tank" {
		t.Errorf("expected unresolved override {name}-tank, got %q", got)
	}
	if got := app.GetRuntimeVolumeName(vol, true); got != "myapp-tank" {
		t.Errorf("expected resolved override myapp-tank (matches old {appname}-{pool} identity), got %q", got)
	}
}

// TestGetRuntimeVolumeName_NoOverrideUsesStrategy verifies that AppVolumes
// without a RuntimeName override (i.e. user-authored, non-migrated volumes)
// fall back to standard strategy-based naming.
func TestGetRuntimeVolumeName_NoOverrideUsesStrategy(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				PhysicalVolumeStrategy: config.PhysicalVolumeStrategyPerVolume,
			},
		},
	}

	vol := &config.AppVolume{Name: "newvol", Pool: "ssd"}

	if got := app.GetRuntimeVolumeName(vol, true); got != "myapp-vol-newvol" {
		t.Errorf("expected per-volume name myapp-vol-newvol, got %q", got)
	}

	app.AppConfig.Deployment.PhysicalVolumeStrategy = config.PhysicalVolumeStrategyLegacyPooled
	if got := app.GetRuntimeVolumeName(vol, true); got != "myapp-ssd" {
		t.Errorf("expected legacy-pooled name myapp-ssd, got %q", got)
	}
}

// TestRuntimeVolumeNameConsistency_DeclaredMatchesMountReference guards the
// invariant that the OpenSVC volume# section's declared name
// (OpenSVCGetAppCanonicalVolumeSections, svcvol["name"]) and the volume name
// embedded in generated mount mappings (GetOpenSVCCanonicalDeploymentPathMapping)
// always agree for the same AppVolume — both must resolve through
// app.GetRuntimeVolumeName(vol, false). If they diverge, the generated service
// template declares one volume but mounts a differently-named one, which is
// fatal at provisioning time. This is exactly the bug a migrated AppVolume
// carrying a RuntimeName override would hit if either call site bypassed the
// override (e.g. by calling GetAppVolumeNamePerVolume directly).
func TestRuntimeVolumeNameConsistency_DeclaredMatchesMountReference(t *testing.T) {
	vol := &config.AppVolume{Name: "tank", Pool: "tank", Size: "1g", RuntimeName: "{name}-tank"}
	src := &config.AppSource{Type: config.AppSourceDirectory, Name: "etc-vol-root", VolumeName: "tank", BasePath: "/etc"}
	mount := &config.AppMount{SourceName: "etc-vol-root", TargetPath: "/etc/myapp"}

	deployment := &config.Deployment{
		PhysicalVolumeStrategy: config.PhysicalVolumeStrategyPerVolume,
		StorageLayoutVersion:   config.StorageLayoutV2,
		AppVolumes:             config.AppVolumes{vol},
		AppSources:             config.AppSources{src},
		AppMounts:              config.AppMounts{mount},
	}
	app := &App{
		Name:      "myapp",
		AppConfig: &config.AppConfig{Deployment: deployment},
	}

	cluster := &Cluster{}

	// What OpenSVCGetAppCanonicalVolumeSections now declares as svcvol["name"].
	declaredName := app.GetRuntimeVolumeName(vol, false)
	if declaredName != "{name}-tank" {
		t.Fatalf("expected declared name {name}-tank, got %q", declaredName)
	}

	// What GetOpenSVCCanonicalDeploymentPathMapping embeds as the mount's volume reference.
	mapping := cluster.GetOpenSVCCanonicalDeploymentPathMapping(app)
	expectedMapping := "{name}-tank/etc:/etc/myapp"
	if mapping != expectedMapping {
		t.Fatalf("expected mount mapping %q, got %q", expectedMapping, mapping)
	}

	if !strings.HasPrefix(mapping, declaredName+"/") {
		t.Errorf("mount mapping %q does not reference the declared volume name %q — provisioning would mount a non-existent volume", mapping, declaredName)
	}
}
