package cluster

import (
	"reflect"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestGetAppVolumeName_UsesSavedRowName(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "data"},
					},
				},
			},
		},
	}

	// The saved row's Name is the actual identity regardless of the
	// historical {name}-<pool> / <app>-<pool> convention encoded by resolved.
	if got := app.GetAppVolumeName("data", true); got != "myapp-data" {
		t.Fatalf("expected saved row name myapp-data, got %q", got)
	}
	if got := app.GetAppVolumeName("data", false); got != "myapp-data" {
		t.Fatalf("expected saved row name myapp-data regardless of resolved, got %q", got)
	}
}

func TestGetAppVolumeName_FallsBackWhenNoSavedRow(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: config.NewDeploymentConfig(),
		},
	}

	if got := app.GetAppVolumeName("missing", true); got != "myapp-missing" {
		t.Fatalf("expected resolved fallback myapp-missing, got %q", got)
	}
	if got := app.GetAppVolumeName("missing", false); got != "{name}-missing" {
		t.Fatalf("expected template fallback {name}-missing, got %q", got)
	}
}

func TestGetAppVolumeName_NilDeploymentFallsBack(t *testing.T) {
	app := &App{
		Name:      "myapp",
		AppConfig: &config.AppConfig{},
	}

	if got := app.GetAppVolumeName("data", true); got != "myapp-data" {
		t.Fatalf("expected resolved fallback myapp-data, got %q", got)
	}
	if got := app.GetAppVolumeName("data", false); got != "{name}-data" {
		t.Fatalf("expected template fallback {name}-data, got %q", got)
	}
}

func TestGetVolumes_ReturnsDistinctSavedRowNames(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "data"},
						{Name: "myapp-logs", PoolName: "logs", VolumeDir: "logs"},
					},
				},
			},
		},
	}

	got := app.GetVolumes(true)
	want := []string{"myapp-data", "myapp-logs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

// TestGetVolumes_ReturnsDistinctNamesForMultiRowSamePool covers Phase 11 task
// 5: two intentional V2 rows sharing a poolname must both be returned, each
// under its own Name. Before the fix, GetVolumes deduped via
// GetAppVolumeName(v.PoolName, ...), which is keyed by pool and returns only
// the first row's name for both entries, silently dropping the second
// volume.
func TestGetVolumes_ReturnsDistinctNamesForMultiRowSamePool(t *testing.T) {
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			AppConfigVersion: config.AppConfigVersionV2,
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "etc"},
						{Name: "myapp-data-logs", PoolName: "data", VolumeDir: "log"},
					},
				},
			},
		},
	}

	got := app.GetVolumes(true)
	want := []string{"myapp-data", "myapp-data-logs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
