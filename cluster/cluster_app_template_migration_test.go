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

// TestLoadAppConfig_PersistsStorageMigrationOnFirstLoad ensures that auto-migrating
// a legacy storage model to the canonical v2 model is written back to disk during
// load, so the migration runs at most once rather than on every restart.
func TestLoadAppConfig_PersistsStorageMigrationOnFirstLoad(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "legacy-storage.toml")
	content := "app-host = \"legacy-storage\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyAppTemplateTOML
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
	_ = first.LoadAppConfig(appsDir, "legacy-storage")
	loaded := first.GetAppConfig("legacy-storage", "8080")
	if loaded == nil || loaded.Deployment == nil || !loaded.Deployment.IsCanonical() {
		t.Fatalf("expected in-memory deployment to be migrated to canonical storage")
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	if !strings.Contains(string(rewritten), `storage-layout-version = 2`) {
		t.Fatalf("expected migration to be persisted to disk, got:\n%s", string(rewritten))
	}

	// A second load from the now-canonical file must not need to re-migrate or
	// rewrite the file again (no further save boundary required).
	info, err := os.Stat(appFile)
	if err != nil {
		t.Fatalf("stat app file failed: %v", err)
	}
	mtimeAfterFirstLoad := info.ModTime()

	second := newCluster()
	_ = second.LoadAppConfig(appsDir, "legacy-storage")
	reloaded := second.GetAppConfig("legacy-storage", "8080")
	if reloaded == nil || reloaded.Deployment == nil || !reloaded.Deployment.IsCanonical() {
		t.Fatalf("expected reloaded deployment to remain canonical")
	}

	info, err = os.Stat(appFile)
	if err != nil {
		t.Fatalf("stat app file after second load failed: %v", err)
	}
	if !info.ModTime().Equal(mtimeAfterFirstLoad) {
		t.Fatalf("expected no rewrite on second load of an already-canonical config, mtime changed from %v to %v", mtimeAfterFirstLoad, info.ModTime())
	}
}

// legacyAppTemplateDuplicatePathsTOML declares two legacy [[deployment.paths]]
// entries that resolve to the same source/subpath and normalize to the same
// docker mount target ("/app/data" vs "/app//data/"). MigrateStorageToCanonical
// deduplicates these into a single canonical AppMount, while preserving both
// legacy Paths entries as-is — so the migrated deployment legitimately ends up
// with MORE legacy Paths shadows than canonical AppMounts
// (TestMigrateStorageToCanonical_NormalizesAndDeduplicatesMountTargets documents
// the same dedup behavior at the migration-function level).
const legacyAppTemplateDuplicatePathsTOML = `
[deployment.storages]
[[deployment.storages.volumes]]
name = "web-vol"
poolname = "tank"
volumedir = "/web"

[[deployment.paths]]
name = "canonical-a"
dockerpath = "/app/data"
srctype = "volume"
srcname = "web-vol"
srcpath = "/conf"
volumename = "web-vol"

[[deployment.paths]]
name = "canonical-b"
dockerpath = "/app//data/"
srctype = "volume"
srcname = "web-vol"
srcpath = "/conf"
volumename = "web-vol"
`

func TestLoadAppConfig_PreservesDuplicateLegacyPathsAfterMigrationDedup(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "dup-paths.toml")
	content := "app-host = \"dup-paths\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + legacyAppTemplateDuplicatePathsTOML
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
	_ = first.LoadAppConfig(appsDir, "dup-paths")
	loaded := first.GetAppConfig("dup-paths", "8080")
	if loaded == nil || loaded.Deployment == nil || !loaded.Deployment.IsCanonical() {
		t.Fatalf("expected in-memory deployment to be migrated to canonical storage")
	}

	// Migration must collapse the duplicate-normalized mount targets into a single
	// canonical AppMount while preserving both original legacy Paths shadows.
	if len(loaded.Deployment.AppMounts) != 1 {
		t.Fatalf("expected duplicate mount targets to collapse into 1 canonical AppMount, got %d: %+v",
			len(loaded.Deployment.AppMounts), loaded.Deployment.AppMounts)
	}
	if len(loaded.Deployment.Paths) != 2 {
		t.Fatalf("expected both original legacy Paths entries preserved (overcount vs canonical mounts), got %d: %+v",
			len(loaded.Deployment.Paths), loaded.Deployment.Paths)
	}

	info, err := os.Stat(appFile)
	if err != nil {
		t.Fatalf("stat app file failed: %v", err)
	}
	mtimeAfterFirstLoad := info.ModTime()

	// A second load must recognize the preserved-overcount Paths as complete (not
	// "missing/incomplete" relative to the smaller canonical mount count) and must
	// not flatten them via a repair-triggered rewrite.
	second := newCluster()
	_ = second.LoadAppConfig(appsDir, "dup-paths")
	reloaded := second.GetAppConfig("dup-paths", "8080")
	if reloaded == nil || reloaded.Deployment == nil || !reloaded.Deployment.IsCanonical() {
		t.Fatalf("expected reloaded deployment to remain canonical")
	}
	if len(reloaded.Deployment.Paths) != 2 {
		t.Fatalf("expected both preserved legacy Paths entries to survive a second load, got %d: %+v",
			len(reloaded.Deployment.Paths), reloaded.Deployment.Paths)
	}

	info, err = os.Stat(appFile)
	if err != nil {
		t.Fatalf("stat app file after second load failed: %v", err)
	}
	if !info.ModTime().Equal(mtimeAfterFirstLoad) {
		t.Fatalf("expected no rewrite on second load preserving a legitimate Paths overcount, mtime changed from %v to %v", mtimeAfterFirstLoad, info.ModTime())
	}
}

// canonicalAppMountTraversalTOML declares a canonical (v2) deployment whose mount
// targetPath contains a ".." traversal that filepath.Clean would lexically resolve
// away (e.g. "/srv/../../etc" -> "/etc"). Validation must reject the raw value
// before any normalization runs, otherwise the traversal would be silently
// rewritten into something that looks valid and persisted to disk.
const canonicalAppMountTraversalTOML = `
[deployment]
storage-layout-version = 2

[[deployment.app-volumes]]
name = "v1"
pool = "tank"
size = "1g"

[[deployment.app-sources]]
name = "s1"
type = "directory"
volumeName = "v1"
basePath = "/data"

[[deployment.app-mounts]]
sourceName = "s1"
targetPath = "/srv/../../etc"
`

func TestLoadAppConfig_RejectsTraversalMountTargetPathBeforeNormalization(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "traversal.toml")
	content := "app-host = \"traversal\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + canonicalAppMountTraversalTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	if err := cl.LoadAppConfig(appsDir, "traversal"); err == nil {
		t.Fatal("expected LoadAppConfig to reject a mount targetPath containing '..'")
	}

	if len(cl.Conf.Apps) != 0 {
		t.Fatalf("expected rejected app not to be registered, got %d apps", len(cl.Conf.Apps))
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app file failed: %v", err)
	}
	got := string(rewritten)
	if !strings.Contains(got, `targetPath = "/srv/../../etc"`) {
		t.Fatalf("expected raw traversal targetPath to remain on disk (rejected, not normalized away), got:\n%s", got)
	}
	if strings.Contains(got, `targetPath = "/etc"`) {
		t.Fatalf("normalization must not have rewritten the rejected traversal path to /etc, got:\n%s", got)
	}
}

// canonicalAppMountUnnormalizedTOML declares a canonical (v2) deployment whose mount
// targetPath is not in cleaned form and carries no legacy Storages/Paths shadow data
// (as if it had never been synced). After a load that normalizes the mount, the
// legacy `paths` shadow must be (re)generated from the *normalized* canonical mount
// — proving SyncLegacyShadows ran on the already-normalized in-memory state before
// the migrated/normalized result was persisted.
const canonicalAppMountUnnormalizedTOML = `
[deployment]
storage-layout-version = 2

[[deployment.app-volumes]]
name = "v1"
pool = "tank"
size = "1g"

[[deployment.app-sources]]
name = "s1"
type = "directory"
volumeName = "v1"
basePath = "/data"

[[deployment.app-mounts]]
sourceName = "s1"
targetPath = "/app//data/"
`

func TestLoadAppConfig_SyncsLegacyShadowsAfterNormalization(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "unnormalized.toml")
	content := "app-host = \"unnormalized\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + canonicalAppMountUnnormalizedTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	_ = cl.LoadAppConfig(appsDir, "unnormalized")

	loaded := cl.GetAppConfig("unnormalized", "8080")
	if loaded == nil || loaded.Deployment == nil {
		t.Fatalf("expected app config to be loaded")
	}
	if got := loaded.Deployment.AppMounts[0].TargetPath; got != "/app/data" {
		t.Fatalf("expected canonical mount to be normalized to /app/data, got %q", got)
	}

	if len(loaded.Deployment.Paths) == 0 {
		t.Fatalf("expected SyncLegacyShadows to (re)generate the legacy paths shadow, got none")
	}
	var shadow *config.PathMapping
	for _, p := range loaded.Deployment.Paths {
		if p != nil && p.DockerPath == "/app/data" {
			shadow = p
			break
		}
	}
	if shadow == nil {
		t.Fatalf("expected legacy shadow path regenerated from the normalized mount (/app/data), got: %+v", loaded.Deployment.Paths)
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	got := string(rewritten)
	if !strings.Contains(got, `targetPath = "/app/data"`) {
		t.Fatalf("expected persisted mount to be normalized, got:\n%s", got)
	}
	if !strings.Contains(got, `dockerpath = "/app/data"`) {
		t.Fatalf("expected persisted legacy shadow path to be regenerated from the normalized canonical mount, got:\n%s", got)
	}
}

// canonicalAppMountNormalizedNoShadowsTOML represents a hand-authored or previously
// trimmed v2 config: the canonical model (app-volumes/app-sources/app-mounts) is
// fully populated and already normalized (cleaned targetPath, no rewrite needed),
// but it carries no legacy [storages]/[[deployment.paths]] shadow data. Older
// readers (UI display endpoints, GetGitClone, GetS3Mount) depend on those shadows,
// so the load path must regenerate them even though no migration/normalization occurred.
const canonicalAppMountNormalizedNoShadowsTOML = `
[deployment]
storage-layout-version = 2

[[deployment.app-volumes]]
name = "v1"
pool = "tank"
size = "1g"

[[deployment.app-sources]]
name = "s1"
type = "directory"
volumeName = "v1"
basePath = "/data"

[[deployment.app-mounts]]
sourceName = "s1"
targetPath = "/app/data"
`

func TestLoadAppConfig_RepairsMissingLegacyShadowsOnNormalizedCanonicalConfig(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "noshadows.toml")
	content := "app-host = \"noshadows\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + canonicalAppMountNormalizedNoShadowsTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	_ = cl.LoadAppConfig(appsDir, "noshadows")

	loaded := cl.GetAppConfig("noshadows", "8080")
	if loaded == nil || loaded.Deployment == nil {
		t.Fatalf("expected app config to be loaded")
	}
	// Mount target was already canonical/normalized — no normalization rewrite expected.
	if got := loaded.Deployment.AppMounts[0].TargetPath; got != "/app/data" {
		t.Fatalf("expected mount target to remain /app/data, got %q", got)
	}

	if len(loaded.Deployment.Paths) == 0 {
		t.Fatalf("expected missing legacy shadows to be repaired on load, got none")
	}
	var shadow *config.PathMapping
	for _, p := range loaded.Deployment.Paths {
		if p != nil && p.DockerPath == "/app/data" {
			shadow = p
			break
		}
	}
	if shadow == nil {
		t.Fatalf("expected legacy shadow path regenerated from the canonical mount (/app/data), got: %+v", loaded.Deployment.Paths)
	}
	if len(loaded.Deployment.Storages.Volumes) == 0 || loaded.Deployment.Storages.Volumes[0].Name != "v1" {
		t.Fatalf("expected legacy volume shadow regenerated for v1, got: %+v", loaded.Deployment.Storages.Volumes)
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	got := string(rewritten)
	if !strings.Contains(got, `dockerpath = "/app/data"`) {
		t.Fatalf("expected persisted legacy shadow path to be regenerated and written to disk, got:\n%s", got)
	}
}

// canonicalAppMountPartialShadowsTOML is a hand-edited v2 config where the
// canonical model has both a directory-backed mount and a git-backed mount, but
// the legacy shadow data only covers the directory side ([[deployment.paths]] for
// /app/data and a [[storages.volumes]] entry) — the git source has no surviving
// [[storages.gitclones]] shadow nor a [[deployment.paths]] entry for /app/src.
// This simulates a partially-trimmed/edited TOML, as opposed to one with no
// shadows at all.
const canonicalAppMountPartialShadowsTOML = `
[deployment]
storage-layout-version = 2

[[deployment.app-volumes]]
name = "v1"
pool = "tank"
size = "1g"

[[deployment.app-sources]]
name = "dir-src"
type = "directory"
volumeName = "v1"
basePath = "/data"

[[deployment.app-sources]]
name = "git-src"
type = "git"
volumeName = "v1"
basePath = "/src"
repo = "github.com/x/y"
branch = "main"

[[deployment.app-mounts]]
sourceName = "dir-src"
targetPath = "/app/data"

[[deployment.app-mounts]]
sourceName = "git-src"
targetPath = "/app/src"

[[deployment.storages.volumes]]
name = "v1"
poolname = "tank"

[[deployment.paths]]
name = "dir-mount"
dockerpath = "/app/data"
srctype = "volume"
srcname = "v1"
srcpath = "/data"
volumename = "v1"
`

func TestLoadAppConfig_RepairsPartiallyMissingLegacyShadows(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "partialshadows.toml")
	content := "app-host = \"partialshadows\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + canonicalAppMountPartialShadowsTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	_ = cl.LoadAppConfig(appsDir, "partialshadows")

	loaded := cl.GetAppConfig("partialshadows", "8080")
	if loaded == nil || loaded.Deployment == nil {
		t.Fatalf("expected app config to be loaded")
	}

	// The git-backed mount's shadow path must have been (re)generated even though
	// the directory-backed shadow was already present — proving the repair covers
	// the whole shadow set, not just the missing family in isolation.
	var gitShadow, dirShadow *config.PathMapping
	for _, p := range loaded.Deployment.Paths {
		switch {
		case p != nil && p.DockerPath == "/app/src":
			gitShadow = p
		case p != nil && p.DockerPath == "/app/data":
			dirShadow = p
		}
	}
	if gitShadow == nil {
		t.Fatalf("expected missing git path shadow (/app/src) to be repaired, got: %+v", loaded.Deployment.Paths)
	}
	if gitShadow.SourceType != config.SourceGit || gitShadow.SourceName != "git-src" {
		t.Fatalf("unexpected repaired git path shadow: %+v", gitShadow)
	}
	if dirShadow == nil {
		t.Fatalf("expected pre-existing directory path shadow (/app/data) to remain, got: %+v", loaded.Deployment.Paths)
	}

	if len(loaded.Deployment.Storages.GitClones) == 0 || loaded.Deployment.Storages.GitClones[0].Name != "git-src" {
		t.Fatalf("expected missing git clone shadow to be repaired for git-src, got: %+v", loaded.Deployment.Storages.GitClones)
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	got := string(rewritten)
	if !strings.Contains(got, `dockerpath = "/app/src"`) {
		t.Fatalf("expected persisted repaired git path shadow, got:\n%s", got)
	}
	if !strings.Contains(got, `repo = "github.com/x/y"`) {
		t.Fatalf("expected persisted repaired git clone shadow, got:\n%s", got)
	}
}

// canonicalAppMountIncompleteShadowsTOML is a hand-edited v2 config where every
// shadow family is *non-empty* but undercounts its canonical counterpart: two
// directory mounts exist but only one [[deployment.paths]] entry survived, and
// two git sources exist but only one [[storages.gitclones]] entry survived. This
// is distinct from a family being entirely absent — it exercises the count-based
// comparison in HasMissingLegacyShadows rather than the emptiness checks.
const canonicalAppMountIncompleteShadowsTOML = `
[deployment]
storage-layout-version = 2

[[deployment.app-volumes]]
name = "v1"
pool = "tank"
size = "1g"

[[deployment.app-sources]]
name = "dir-src-1"
type = "directory"
volumeName = "v1"
basePath = "/data1"

[[deployment.app-sources]]
name = "dir-src-2"
type = "directory"
volumeName = "v1"
basePath = "/data2"

[[deployment.app-sources]]
name = "git-src-1"
type = "git"
volumeName = "v1"
basePath = "/src1"
repo = "github.com/x/y"
branch = "main"

[[deployment.app-sources]]
name = "git-src-2"
type = "git"
volumeName = "v1"
basePath = "/src2"
repo = "github.com/a/b"
branch = "main"

[[deployment.app-mounts]]
sourceName = "dir-src-1"
targetPath = "/app/data1"

[[deployment.app-mounts]]
sourceName = "dir-src-2"
targetPath = "/app/data2"

[[deployment.app-mounts]]
sourceName = "git-src-1"
targetPath = "/app/src1"

[[deployment.app-mounts]]
sourceName = "git-src-2"
targetPath = "/app/src2"

[[deployment.storages.volumes]]
name = "v1"
poolname = "tank"

[[deployment.storages.gitclones]]
name = "git-src-1"
repo = "github.com/x/y"
branch = "main"
volumename = "v1"

[[deployment.paths]]
name = "dir-mount-1"
dockerpath = "/app/data1"
srctype = "volume"
srcname = "v1"
srcpath = "/data1"
volumename = "v1"
`

func TestLoadAppConfig_RepairsIncompleteLegacyShadows(t *testing.T) {
	workingDir := t.TempDir()
	appsDir := filepath.Join(workingDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatalf("mkdir apps dir failed: %v", err)
	}

	appFile := filepath.Join(appsDir, "incompleteshadows.toml")
	content := "app-host = \"incompleteshadows\"\napp-port = \"8080\"\nprov-app-memory = \"128M\"\nprov-app-disk-size = \"1G\"\n" + canonicalAppMountIncompleteShadowsTOML
	if err := os.WriteFile(appFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write app file failed: %v", err)
	}

	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir:     workingDir,
			Apps:           make([]*config.AppConfig, 0),
			DefaultFlagMap: map[string]interface{}{"prov-app-memory": "128M", "prov-app-disk-size": "1G"},
		},
	}

	_ = cl.LoadAppConfig(appsDir, "incompleteshadows")

	loaded := cl.GetAppConfig("incompleteshadows", "8080")
	if loaded == nil || loaded.Deployment == nil {
		t.Fatalf("expected app config to be loaded")
	}

	// Four AppMounts, each resolving to a known source type, must yield four path shadows.
	if len(loaded.Deployment.Paths) != 4 {
		t.Fatalf("expected the full set of 4 path shadows to be regenerated, got %d: %+v",
			len(loaded.Deployment.Paths), loaded.Deployment.Paths)
	}
	pathByTarget := make(map[string]bool, len(loaded.Deployment.Paths))
	for _, p := range loaded.Deployment.Paths {
		if p != nil {
			pathByTarget[p.DockerPath] = true
		}
	}
	for _, want := range []string{"/app/data1", "/app/data2", "/app/src1", "/app/src2"} {
		if !pathByTarget[want] {
			t.Errorf("expected repaired path shadow for %q, got: %+v", want, loaded.Deployment.Paths)
		}
	}

	// Two git AppSources must yield two git clone shadows (was undercounted at one).
	if len(loaded.Deployment.Storages.GitClones) != 2 {
		t.Fatalf("expected 2 git clone shadows after repair, got %d: %+v",
			len(loaded.Deployment.Storages.GitClones), loaded.Deployment.Storages.GitClones)
	}
	gitNames := map[string]bool{}
	for _, gc := range loaded.Deployment.Storages.GitClones {
		gitNames[gc.Name] = true
	}
	if !gitNames["git-src-1"] || !gitNames["git-src-2"] {
		t.Fatalf("expected both git clone shadows present, got: %+v", loaded.Deployment.Storages.GitClones)
	}

	rewritten, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read rewritten app file failed: %v", err)
	}
	got := string(rewritten)
	if !strings.Contains(got, `dockerpath = "/app/src2"`) {
		t.Fatalf("expected persisted repaired path shadow for /app/src2, got:\n%s", got)
	}
	if strings.Count(got, `name = "git-src-`) < 2 {
		t.Fatalf("expected persisted repaired set of both git clone shadows, got:\n%s", got)
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
