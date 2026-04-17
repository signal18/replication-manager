package cluster

import (
	"hash/crc64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

const legacyAppTemplateTOML = `
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
srctype = "volume"
srcname = "data-volume"
srcpath = "/"

[[deployment.paths]]
name = "assets"
parentname = "/var/www/html"
dockerpath = "/var/www/html/assets"
`

func TestLoadAppConfig_CanonicalizesLegacyAndRewritesFile(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "legacy.toml")
	content := "app-host = \"legacy\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyAppTemplateTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy app file failed: %v", err)
	}

	cluster := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	_ = cluster.LoadAppConfig(appsDir, "legacy")

	updated, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}

	got := string(updated)
	if !strings.Contains(got, `parentname = "web-root"`) {
		t.Fatalf("expected parentname migration in file, got:\n%s", got)
	}
	if !strings.Contains(got, `srcpath = "."`) {
		t.Fatalf("expected srcpath migration in file, got:\n%s", got)
	}

}

func TestGetTemplateContent_LocalCacheCanonicalizesAndRewrites(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte(legacyAppTemplateTOML), 0o644); err != nil {
		t.Fatalf("write local legacy template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	content, err := cluster.GetTemplateContent("legacy")
	if err != nil {
		t.Fatalf("GetTemplateContent failed: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, `parentname = "web-root"`) {
		t.Fatalf("expected canonical content from local cache, got:\n%s", got)
	}
	if !strings.Contains(got, `srcpath = "."`) {
		t.Fatalf("expected canonical srcpath from local cache, got:\n%s", got)
	}

	rewritten, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read rewritten local template failed: %v", err)
	}
	if !strings.Contains(string(rewritten), `parentname = "web-root"`) {
		t.Fatalf("expected local cache rewrite to canonical content, got:\n%s", string(rewritten))
	}
}

func TestGetTemplateContent_SharedTemplateCanonicalizedBeforeCacheWrite(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := filepath.Join(t.TempDir(), "share")
	sharedPath := filepath.Join(shareDir, "app", "deployments", "legacy.toml")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o755); err != nil {
		t.Fatalf("mkdir shared template dir failed: %v", err)
	}
	if err := os.WriteFile(sharedPath, []byte(legacyAppTemplateTOML), 0o644); err != nil {
		t.Fatalf("write shared legacy template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{
		WorkingDir:          workingDir,
		ShareDir:            shareDir,
		ProvAppTemplateRepo: "%%%",
	}}

	content, err := cluster.GetTemplateContent("shared/legacy")
	if err != nil {
		t.Fatalf("GetTemplateContent from shared failed: %v", err)
	}

	if !strings.Contains(string(content), `parentname = "web-root"`) {
		t.Fatalf("expected canonicalized fetched content, got:\n%s", string(content))
	}

	localCache := filepath.Join(workingDir, ".templates", "apps", "shared", "legacy.toml")
	localContent, err := os.ReadFile(localCache)
	if err != nil {
		t.Fatalf("read local cached template failed: %v", err)
	}
	if !strings.Contains(string(localContent), `parentname = "web-root"`) {
		t.Fatalf("expected canonical content in local cache, got:\n%s", string(localContent))
	}
}

func TestAddSeededApp_CanonicalizesLegacyResolvedTemplateBeforeUnmarshal(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-seed.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	template := "app-port = \"8080\"\nprov-app-docker-img = \"nginx:latest\"\n" + legacyAppTemplateTOML
	if err := os.WriteFile(localPath, []byte(template), 0o644); err != nil {
		t.Fatalf("write local seed template failed: %v", err)
	}

	cluster := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir: workingDir,
			Apps:       make([]*config.AppConfig, 0),
		},
	}

	if err := cluster.AddSeededApp("seed-host", "8080", "nginx:latest", "legacy-seed"); err != nil {
		t.Fatalf("AddSeededApp failed: %v", err)
	}

	seeded := cluster.GetAppConfig("seed-host", "8080")
	if seeded == nil || seeded.Deployment == nil {
		t.Fatalf("expected seeded app deployment to be loaded")
	}

	var child *config.PathMapping
	for _, p := range seeded.Deployment.Paths {
		if p != nil && p.Name == "assets" {
			child = p
			break
		}
	}
	if child == nil {
		t.Fatalf("expected canonicalized child path to be present")
	}
	if child.ParentName != "web-root" {
		t.Fatalf("expected parentname to be canonicalized, got %q", child.ParentName)
	}
	if seeded.Deployment.Paths[0].SourcePath != "." {
		t.Fatalf("expected srcpath to be canonicalized to '.', got %q", seeded.Deployment.Paths[0].SourcePath)
	}
}
