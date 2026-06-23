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

const legacyMultiRowPoolVolumesTOML = `
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.storages.volumes]]
name = "logs-volume"
poolname = "data"
volumedir = "logs"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
srctype = "volume"
srcname = "data-volume"
volumename = "data-volume"
srcpath = "."

[[deployment.paths]]
name = "log-dir"
dockerpath = "/var/log/app"
srctype = "volume"
srcname = "logs-volume"
volumename = "logs-volume"
srcpath = "."
`

const invalidLegacyTemplateTOML = `
[deployment.storages]

[[deployment.paths]]
name = "invalid-root"
dockerpath = "/var/www/html"
srctype = "volume"
srcname = "missing-volume"
srcpath = "/"
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

func TestGetTemplateContent_SharedDummyCanonicalizedWithoutLocalRewrite(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := filepath.Join(t.TempDir(), "share")
	sharedPath := filepath.Join(shareDir, "app", "templates", "dummy.toml")
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

	content, err := cluster.GetTemplateContent("shared/dummy")
	if err != nil {
		t.Fatalf("GetTemplateContent from shared failed: %v", err)
	}

	if !strings.Contains(string(content), `parentname = "web-root"`) {
		t.Fatalf("expected canonicalized fetched content, got:\n%s", string(content))
	}

	localCache := filepath.Join(workingDir, ".templates", "apps", "shared", "dummy.toml")
	if _, err := os.Stat(localCache); !os.IsNotExist(err) {
		t.Fatalf("expected no local cache rewrite for shared template, err=%v", err)
	}
}

func TestGetTemplateContent_RejectsPathTraversalInIdentifier(t *testing.T) {
	workingDir := t.TempDir()
	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	if _, err := cluster.GetTemplateContent("../escape"); err == nil {
		t.Fatal("expected traversal template identifier to be rejected")
	}
	if _, err := cluster.GetTemplateContent("shared/../../escape"); err == nil {
		t.Fatal("expected shared traversal template identifier to be rejected")
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

func TestAddSeededApp_InvalidTemplateDoesNotRegisterApp(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "invalid-seed.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	template := "app-port = \"8080\"\nprov-app-docker-img = \"nginx:latest\"\n" + invalidLegacyTemplateTOML
	if err := os.WriteFile(localPath, []byte(template), 0o644); err != nil {
		t.Fatalf("write invalid seed template failed: %v", err)
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

	if err := cluster.AddSeededApp("seed-host", "8080", "nginx:latest", "invalid-seed"); err == nil {
		t.Fatalf("expected AddSeededApp to fail for invalid template")
	}

	if len(cluster.Conf.Apps) != 0 {
		t.Fatalf("expected no app config to be registered after failure, got %d", len(cluster.Conf.Apps))
	}

	if app, _ := cluster.GetAppByHostPort("seed-host", "8080"); app != nil {
		t.Fatalf("expected no app object to remain after failure")
	}
}

func TestLoadAppConfig_InvalidCanonicalizedTemplateDoesNotRewriteFile(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "invalid.toml")
	content := "app-host = \"invalid\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + invalidLegacyTemplateTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write invalid app file failed: %v", err)
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

	if err := cluster.LoadAppConfig(appsDir, "invalid"); err == nil {
		t.Fatalf("expected LoadAppConfig to fail for invalid canonicalized template")
	}

	updated, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app file failed: %v", err)
	}
	got := string(updated)
	if !strings.Contains(got, `srcpath = "/"`) {
		t.Fatalf("expected invalid file to remain unchanged, got:\n%s", got)
	}
	if strings.Contains(got, `srcpath = "."`) {
		t.Fatalf("expected no canonical rewrite on invalid template, got:\n%s", got)
	}
}

func TestGetTemplateContent_InvalidTemplateDoesNotRewriteLocalCache(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "invalid.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte(invalidLegacyTemplateTOML), 0o644); err != nil {
		t.Fatalf("write local invalid template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	if _, err := cluster.GetTemplateContent("invalid"); err == nil {
		t.Fatalf("expected GetTemplateContent to fail for invalid local template")
	}

	rewritten, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local template failed: %v", err)
	}
	if !strings.Contains(string(rewritten), `srcpath = "/"`) {
		t.Fatalf("expected local invalid template not to be rewritten, got:\n%s", string(rewritten))
	}
}

func TestGetTemplateContent_InvalidSharedTemplateDoesNotWriteCache(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := filepath.Join(t.TempDir(), "share")
	sharedPath := filepath.Join(shareDir, "app", "templates", "invalid.toml")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o755); err != nil {
		t.Fatalf("mkdir shared template dir failed: %v", err)
	}
	if err := os.WriteFile(sharedPath, []byte(invalidLegacyTemplateTOML), 0o644); err != nil {
		t.Fatalf("write shared invalid template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{
		WorkingDir:          workingDir,
		ShareDir:            shareDir,
		ProvAppTemplateRepo: "%%%",
	}}

	if _, err := cluster.GetTemplateContent("shared/invalid"); err == nil {
		t.Fatalf("expected GetTemplateContent to fail for invalid shared template")
	}

	localCache := filepath.Join(workingDir, ".templates", "apps", "shared", "invalid.toml")
	if _, err := os.Stat(localCache); !os.IsNotExist(err) {
		t.Fatalf("expected no local cache file for invalid shared template, got err=%v", err)
	}
}

func TestLoadAppConfigs_ReturnsAggregateErrorButLoadsValidApps(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	goodFile := filepath.Join(appsDir, "good.toml")
	goodContent := "app-host = \"good\"\napp-port = \"8081\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyAppTemplateTOML
	if err := os.WriteFile(goodFile, []byte(goodContent), 0o644); err != nil {
		t.Fatalf("write good app file failed: %v", err)
	}

	badFile := filepath.Join(appsDir, "bad.toml")
	badContent := "app-host = \"bad\"\napp-port = \"8082\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + invalidLegacyTemplateTOML
	if err := os.WriteFile(badFile, []byte(badContent), 0o644); err != nil {
		t.Fatalf("write bad app file failed: %v", err)
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

	err := cluster.LoadAppConfigs()
	if err == nil {
		t.Fatalf("expected aggregate error when one app config is invalid")
	}

	if len(cluster.Conf.Apps) != 1 {
		t.Fatalf("expected one valid app config to load, got %d", len(cluster.Conf.Apps))
	}
	if cluster.Conf.Apps[0].AppHost != "good" || cluster.Conf.Apps[0].AppPort != "8081" {
		t.Fatalf("unexpected loaded app config: host=%q port=%q", cluster.Conf.Apps[0].AppHost, cluster.Conf.Apps[0].AppPort)
	}
}

func TestGetTemplateContent_SharedPrefixOnlyDummyIsAllowed(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := filepath.Join(t.TempDir(), "share")

	sharedPath := filepath.Join(shareDir, "app", "templates", "some-template.toml")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o755); err != nil {
		t.Fatalf("mkdir shared template dir failed: %v", err)
	}
	if err := os.WriteFile(sharedPath, []byte(legacyAppTemplateTOML), 0o644); err != nil {
		t.Fatalf("write shared template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{
		WorkingDir:          workingDir,
		ShareDir:            shareDir,
		ProvAppTemplateRepo: "%%%",
	}}

	if _, err := cluster.GetTemplateContent("shared/some-template"); err == nil {
		t.Fatalf("expected non-dummy shared template to be rejected")
	}

	localCache := filepath.Join(workingDir, ".templates", "apps", "shared", "some-template.toml")
	if _, err := os.Stat(localCache); !os.IsNotExist(err) {
		t.Fatalf("expected no local cache rewrite for shared/some-template, err=%v", err)
	}
}

func TestRefreshTemplateContent_SharedNonDummyRejected(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := filepath.Join(t.TempDir(), "share")

	sharedPath := filepath.Join(shareDir, "app", "templates", "refreshable.toml")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o755); err != nil {
		t.Fatalf("mkdir shared template dir failed: %v", err)
	}
	sharedTemplate := `
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.paths]]
name = "web-root"
level = 0
dockerpath = "/var/www/new"
srctype = "volume"
srcname = "data-volume"
srcpath = "."
`
	if err := os.WriteFile(sharedPath, []byte(sharedTemplate), 0o644); err != nil {
		t.Fatalf("write shared template failed: %v", err)
	}

	localCache := filepath.Join(workingDir, ".templates", "apps", "shared", "refreshable.toml")
	if err := os.MkdirAll(filepath.Dir(localCache), 0o755); err != nil {
		t.Fatalf("mkdir local cache dir failed: %v", err)
	}
	localTemplate := `
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.paths]]
name = "web-root"
level = 0
dockerpath = "/var/www/old"
srctype = "volume"
srcname = "data-volume"
srcpath = "."
`
	if err := os.WriteFile(localCache, []byte(localTemplate), 0o644); err != nil {
		t.Fatalf("write local cache template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{
		WorkingDir:          workingDir,
		ShareDir:            shareDir,
		ProvAppTemplateRepo: "%%%",
	}}

	if _, err := cluster.RefreshTemplateContent("shared/refreshable"); err == nil {
		t.Fatalf("expected non-dummy shared refresh to be rejected")
	}

	rewritten, err := os.ReadFile(localCache)
	if err != nil {
		t.Fatalf("read local cache failed: %v", err)
	}
	if !strings.Contains(string(rewritten), `dockerpath = "/var/www/old"`) {
		t.Fatalf("expected local cache to remain unchanged, got:\n%s", string(rewritten))
	}
}

func TestRefreshTemplateContent_RepoSyncFailureFallsBackToStaleCache(t *testing.T) {
	workingDir := t.TempDir()

	cl := &Cluster{Conf: &config.Config{
		WorkingDir:              workingDir,
		ProvAppTemplateRepo:     "https://127.0.0.1.invalid/nonexistent/repo.git",
		ProvAppTemplateRepoUser: "git",
	}}

	repoDir, err := cl.Conf.ResolveAppTemplateRepoCacheDir()
	if err != nil {
		t.Fatalf("ResolveAppTemplateRepoCacheDir failed: %v", err)
	}
	stalePath := filepath.Join(repoDir, "repo-only.toml")
	if err := os.MkdirAll(filepath.Dir(stalePath), 0o755); err != nil {
		t.Fatalf("mkdir stale cache dir failed: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte(legacyAppTemplateTOML), 0o644); err != nil {
		t.Fatalf("write stale template failed: %v", err)
	}

	content, err := cl.RefreshTemplateContent("repo-only")
	if err != nil {
		t.Fatalf("RefreshTemplateContent should fallback to stale cache, got err: %v", err)
	}
	if !strings.Contains(string(content), `parentname = "web-root"`) {
		t.Fatalf("expected stale cached template content, got:\n%s", string(content))
	}

	localPath := filepath.Join(workingDir, ".templates", "apps", "repo-only.toml")
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("expected no local template write from repo cache read, err=%v", err)
	}
}

func TestLoadAppConfig_MergesMultiRowVolumePoolWithResolvedName(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "legacy-vol.toml")
	content := "app-host = \"legacy-vol\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyMultiRowPoolVolumesTOML
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

	if err := cluster.LoadAppConfig(appsDir, "legacy-vol"); err != nil && err.Error() != "" {
		t.Fatalf("LoadAppConfig failed: %v", err)
	}

	if len(cluster.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app config to load, got %d", len(cluster.Conf.Apps))
	}
	appcnf := cluster.Conf.Apps[0]
	if len(appcnf.Deployment.Storages.Volumes) != 1 {
		t.Fatalf("expected volumes merged into 1 row, got %d", len(appcnf.Deployment.Storages.Volumes))
	}
	vol := appcnf.Deployment.Storages.Volumes[0]
	if vol.Name != "legacy-vol-data" {
		t.Fatalf("expected resolved volume name legacy-vol-data, got %q", vol.Name)
	}
	if vol.VolumeDir != "data logs" {
		t.Fatalf("expected merged volumedir 'data logs', got %q", vol.VolumeDir)
	}

	for _, p := range appcnf.Deployment.Paths {
		if p.SourceName != "legacy-vol-data" {
			t.Fatalf("expected srcname rewritten to legacy-vol-data, got %q", p.SourceName)
		}
		if p.VolumeName != "legacy-vol-data" {
			t.Fatalf("expected volumename rewritten to legacy-vol-data, got %q", p.VolumeName)
		}
	}

	updated, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	got := string(updated)
	if !strings.Contains(got, `name = "legacy-vol-data"`) {
		t.Fatalf("expected canonical volume name in rewritten file, got:\n%s", got)
	}
	if !strings.Contains(got, `volumedir = "data logs"`) {
		t.Fatalf("expected merged volumedir in rewritten file, got:\n%s", got)
	}
}

// TestLoadAppConfig_RewritesLegacyConfigOnlyOnce covers Phase 9 task 1/2: a
// legacy multi-row-pool app config is canonicalized and rewritten to disk on
// the first load, but a second load of the now-canonical file must not
// trigger another rewrite (CanonicalizeAppContent reports Changed=false on
// already-canonical content).
func TestLoadAppConfig_RewritesLegacyConfigOnlyOnce(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "legacy-vol.toml")
	content := "app-host = \"legacy-vol\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyMultiRowPoolVolumesTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy app file failed: %v", err)
	}

	newCluster := func() *Cluster {
		return &Cluster{
			Name:       "test-cluster",
			WorkingDir: workingDir,
			crcTable:   crc64.MakeTable(crc64.ECMA),
			Conf: &config.Config{
				WorkingDir:     workingDir,
				Apps:           make([]*config.AppConfig, 0),
				DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
			},
		}
	}

	if err := newCluster().LoadAppConfig(appsDir, "legacy-vol"); err != nil && err.Error() != "" {
		t.Fatalf("first LoadAppConfig failed: %v", err)
	}
	firstPass, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app file after first load failed: %v", err)
	}

	if err := newCluster().LoadAppConfig(appsDir, "legacy-vol"); err != nil && err.Error() != "" {
		t.Fatalf("second LoadAppConfig failed: %v", err)
	}
	secondPass, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app file after second load failed: %v", err)
	}

	if string(firstPass) != string(secondPass) {
		t.Fatalf("expected second load not to rewrite already-canonical config\nfirst:\n%s\nsecond:\n%s", firstPass, secondPass)
	}
}

// TestGetTemplateContent_RewritesLegacyTemplateOnlyOnce covers Phase 9 task
// 1/2: a legacy multi-row-pool template is canonicalized and rewritten to the
// local cache on the first load, but a second load of the now-canonical cache
// must not trigger another rewrite.
func TestGetTemplateContent_RewritesLegacyTemplateOnlyOnce(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-vol.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte(legacyMultiRowPoolVolumesTOML), 0o644); err != nil {
		t.Fatalf("write local legacy template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	if _, err := cluster.GetTemplateContent("legacy-vol"); err != nil {
		t.Fatalf("first GetTemplateContent failed: %v", err)
	}
	firstPass, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local template after first load failed: %v", err)
	}

	if _, err := cluster.GetTemplateContent("legacy-vol"); err != nil {
		t.Fatalf("second GetTemplateContent failed: %v", err)
	}
	secondPass, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local template after second load failed: %v", err)
	}

	if string(firstPass) != string(secondPass) {
		t.Fatalf("expected second load not to rewrite already-canonical template\nfirst:\n%s\nsecond:\n%s", firstPass, secondPass)
	}
}

func TestGetTemplateContent_MergesMultiRowVolumePoolWithTemplateName(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-vol.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte(legacyMultiRowPoolVolumesTOML), 0o644); err != nil {
		t.Fatalf("write local legacy template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	content, err := cluster.GetTemplateContent("legacy-vol")
	if err != nil {
		t.Fatalf("GetTemplateContent failed: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, `name = "{name}-data"`) {
		t.Fatalf("expected canonical template volume name {name}-data, got:\n%s", got)
	}
	if !strings.Contains(got, `volumedir = "data logs"`) {
		t.Fatalf("expected merged volumedir, got:\n%s", got)
	}

	rewritten, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read rewritten local template failed: %v", err)
	}
	if !strings.Contains(string(rewritten), `name = "{name}-data"`) {
		t.Fatalf("expected local cache rewrite to canonical volume name, got:\n%s", string(rewritten))
	}
}

func TestAddSeededApp_MergesMultiRowVolumePoolWithResolvedName(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-vol-seed.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	template := "app-port = \"8080\"\nprov-app-docker-img = \"nginx:latest\"\n" + legacyMultiRowPoolVolumesTOML
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

	if err := cluster.AddSeededApp("seed-vol-host", "8080", "nginx:latest", "legacy-vol-seed"); err != nil {
		t.Fatalf("AddSeededApp failed: %v", err)
	}

	seeded := cluster.GetAppConfig("seed-vol-host", "8080")
	if seeded == nil || seeded.Deployment == nil {
		t.Fatalf("expected seeded app deployment to be loaded")
	}

	if len(seeded.Deployment.Storages.Volumes) != 1 {
		t.Fatalf("expected volumes merged into 1 row, got %d", len(seeded.Deployment.Storages.Volumes))
	}
	vol := seeded.Deployment.Storages.Volumes[0]
	if vol.Name != "seed-vol-host-data" {
		t.Fatalf("expected resolved volume name seed-vol-host-data, got %q", vol.Name)
	}
	if vol.VolumeDir != "data logs" {
		t.Fatalf("expected merged volumedir 'data logs', got %q", vol.VolumeDir)
	}

	for _, p := range seeded.Deployment.Paths {
		if p.SourceName != "seed-vol-host-data" {
			t.Fatalf("expected srcname rewritten to seed-vol-host-data, got %q", p.SourceName)
		}
		if p.VolumeName != "seed-vol-host-data" {
			t.Fatalf("expected volumename rewritten to seed-vol-host-data, got %q", p.VolumeName)
		}
	}
}

// TestLoadAppConfig_StampsAppConfigVersionOnLegacyContent covers Phase 10
// tasks 1, 3, 4 and 5 for the app config load flow: loading unversioned
// legacy app config content stamps app-config-version = 2 into both the
// rewritten file and the unmarshalled AppConfig, and a second load of the
// now-V2 content does not rewrite the file again.
func TestLoadAppConfig_StampsAppConfigVersionOnLegacyContent(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "legacy-version.toml")
	content := "app-host = \"legacy-version\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyAppTemplateTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy app file failed: %v", err)
	}

	newCluster := func() *Cluster {
		return &Cluster{
			Name:       "test-cluster",
			WorkingDir: workingDir,
			crcTable:   crc64.MakeTable(crc64.ECMA),
			Conf: &config.Config{
				WorkingDir:     workingDir,
				Apps:           make([]*config.AppConfig, 0),
				DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
			},
		}
	}

	first := newCluster()
	if err := first.LoadAppConfig(appsDir, "legacy-version"); err != nil && err.Error() != "" {
		t.Fatalf("first LoadAppConfig failed: %v", err)
	}
	if len(first.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app config to load, got %d", len(first.Conf.Apps))
	}
	if got := first.Conf.Apps[0].AppConfigVersion; got != config.AppConfigVersionV2 {
		t.Fatalf("expected AppConfigVersion %d, got %d", config.AppConfigVersionV2, got)
	}

	firstPass, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	if !strings.Contains(string(firstPass), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in rewritten file, got:\n%s", firstPass)
	}

	second := newCluster()
	if err := second.LoadAppConfig(appsDir, "legacy-version"); err != nil && err.Error() != "" {
		t.Fatalf("second LoadAppConfig failed: %v", err)
	}
	secondPass, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app file after second load failed: %v", err)
	}
	if string(firstPass) != string(secondPass) {
		t.Fatalf("expected second load not to rewrite already-V2 config\nfirst:\n%s\nsecond:\n%s", firstPass, secondPass)
	}
	if got := second.Conf.Apps[0].AppConfigVersion; got != config.AppConfigVersionV2 {
		t.Fatalf("expected AppConfigVersion %d on second load, got %d", config.AppConfigVersionV2, got)
	}
}

// TestGetTemplateContent_StampsAppConfigVersionOnLocalCache covers Phase 10
// tasks 1, 3 and 4 for the template load flow: an unversioned local template
// is rewritten with app-config-version = 2, and a second load of the now-V2
// cache does not rewrite the file again.
func TestGetTemplateContent_StampsAppConfigVersionOnLocalCache(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-version.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte(legacyAppTemplateTOML), 0o644); err != nil {
		t.Fatalf("write local legacy template failed: %v", err)
	}

	cluster := &Cluster{Conf: &config.Config{WorkingDir: workingDir}}

	content, err := cluster.GetTemplateContent("legacy-version")
	if err != nil {
		t.Fatalf("GetTemplateContent failed: %v", err)
	}
	if !strings.Contains(string(content), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in returned content, got:\n%s", content)
	}

	firstPass, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read rewritten local template failed: %v", err)
	}
	if !strings.Contains(string(firstPass), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in rewritten local cache, got:\n%s", firstPass)
	}

	if _, err := cluster.GetTemplateContent("legacy-version"); err != nil {
		t.Fatalf("second GetTemplateContent failed: %v", err)
	}
	secondPass, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local template after second load failed: %v", err)
	}
	if string(firstPass) != string(secondPass) {
		t.Fatalf("expected second load not to rewrite already-V2 template\nfirst:\n%s\nsecond:\n%s", firstPass, secondPass)
	}
}

// TestAddSeededApp_StampsAppConfigVersion covers Phase 10 task 5 for the
// seeded-app creation flow: a seeded app resolved from an unversioned
// template ends up flagged with AppConfigVersion = config.AppConfigVersionV2.
func TestAddSeededApp_StampsAppConfigVersion(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "legacy-version-seed.toml")
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

	if err := cluster.AddSeededApp("seed-version-host", "8080", "nginx:latest", "legacy-version-seed"); err != nil {
		t.Fatalf("AddSeededApp failed: %v", err)
	}

	seeded := cluster.GetAppConfig("seed-version-host", "8080")
	if seeded == nil {
		t.Fatalf("expected seeded app config to be loaded")
	}
	if got := seeded.AppConfigVersion; got != config.AppConfigVersionV2 {
		t.Fatalf("expected AppConfigVersion %d, got %d", config.AppConfigVersionV2, got)
	}
}

func TestRefreshTemplateContent_RepoSyncFailureWithoutCacheReturnsError(t *testing.T) {
	workingDir := t.TempDir()

	cl := &Cluster{Conf: &config.Config{
		WorkingDir:              workingDir,
		ProvAppTemplateRepo:     "https://127.0.0.1.invalid/nonexistent/repo.git",
		ProvAppTemplateRepoUser: "git",
	}}

	_, err := cl.RefreshTemplateContent("repo-only")
	if err == nil {
		t.Fatal("expected error when repo sync fails and no cache exists")
	}
	if !strings.Contains(err.Error(), "repository") && !strings.Contains(err.Error(), "repo") {
		t.Fatalf("expected repo-related error, got: %v", err)
	}
}

// TestLoadAppConfig_InvalidVolumeSizeIsWarningNotFatal verifies that a persisted
// invalid volume size does not prevent the app config from loading. The raw
// invalid value must be preserved so the operator can identify and fix it.
func TestLoadAppConfig_InvalidVolumeSizeIsWarningNotFatal(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	const invalidSizeContent = `
app-host = "badsize-app"
app-port = "8080"
prov-app-memory = "128M"
prov-app-disk-size = "1G"

[deployment.storages]
[[deployment.storages.volumes]]
name = "badsize-app-data"
poolname = "data"
volumedir = "data"
size = "badvalue"
`
	if err := os.WriteFile(filepath.Join(appsDir, "badsize-app.toml"), []byte(invalidSizeContent), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
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

	if err := cluster.LoadAppConfig(appsDir, "badsize-app"); err != nil {
		t.Fatalf("LoadAppConfig must succeed for invalid volume size, got error: %v", err)
	}

	if len(cluster.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app loaded, got %d", len(cluster.Conf.Apps))
	}
	app := cluster.Conf.Apps[0]
	if app.Deployment == nil || len(app.Deployment.Storages.Volumes) == 0 {
		t.Fatal("expected deployment volumes to be present")
	}
	if got := app.Deployment.Storages.Volumes[0].Size; got != "badvalue" {
		t.Fatalf("expected raw invalid size %q preserved, got %q", "badvalue", got)
	}
}

// TestLoadAppConfigs_InvalidVolumeSizeLoadsAppButAggregateIsClean verifies that
// LoadAppConfigs does not return an aggregate error for an app whose only problem
// is an invalid volume size — the app is loaded with a warning so it remains
// accessible while the operator fixes the TOML.
func TestLoadAppConfigs_InvalidVolumeSizeLoadsAppAndNoError(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	const content = `
app-host = "badsize2"
app-port = "9090"

[deployment.storages]
[[deployment.storages.volumes]]
name = "badsize2-data"
poolname = "data"
volumedir = "data"
size = "notanumber"
`
	if err := os.WriteFile(filepath.Join(appsDir, "badsize2.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cluster := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{},
		},
	}

	if err := cluster.LoadAppConfigs(); err != nil {
		t.Fatalf("LoadAppConfigs must not return error for invalid-size-only app, got: %v", err)
	}
	if len(cluster.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app loaded, got %d", len(cluster.Conf.Apps))
	}
}
