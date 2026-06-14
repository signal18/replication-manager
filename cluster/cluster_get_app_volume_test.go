package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestSetAppLocalVolume_TokenAwareMatch covers Phase 6 task 1:
// SetAppLocalVolume must match dir against VolumeDir's whitespace-separated
// tokens, not as a string prefix, so a merged row (e.g. "data mnt") matches
// on "mnt" even though "mnt" is not a prefix of "data mnt".
func TestSetAppLocalVolume_TokenAwareMatch(t *testing.T) {
	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
					},
				},
			},
		},
	}

	got, err := cluster.SetAppLocalVolume(app, "mnt")
	if err != nil {
		t.Fatalf("expected token match for \"mnt\", got error: %v", err)
	}
	if got.Name != "myapp-data" {
		t.Fatalf("expected myapp-data, got %q", got.Name)
	}
}

// TestSetAppLocalVolume_NoPrefixFalsePositive ensures a volume whose only
// directory token merely starts with dir (e.g. "mntlogs") is not matched.
func TestSetAppLocalVolume_NoPrefixFalsePositive(t *testing.T) {
	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "mntlogs"},
					},
				},
			},
		},
	}

	if _, err := cluster.SetAppLocalVolume(app, "mnt"); err == nil {
		t.Fatalf("expected no match for \"mnt\" against directory token \"mntlogs\"")
	}
}

func TestSetAppLocalMountVolume_UsesMntToken(t *testing.T) {
	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{
		Name: "myapp",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					Volumes: config.Volumes{
						{Name: "myapp-shared", PoolName: "shared", VolumeDir: "etc mnt"},
					},
				},
			},
		},
	}

	got, err := cluster.SetAppLocalMountVolume(app)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "myapp-shared" {
		t.Fatalf("expected myapp-shared, got %q", got.Name)
	}
}
