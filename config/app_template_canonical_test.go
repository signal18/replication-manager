package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml"
)

func TestCanonicalizeAppTemplateTOML_LegacyPathsAreMigrated(t *testing.T) {
	legacy := []byte(`
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
dockerpath = "/var/www/html/assets"
parentname = "/var/www/html"
`)

	canonical, res, err := CanonicalizeAppTemplateTOML(legacy)
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.UpdatedParentNames == 0 {
		t.Fatalf("expected parentname migration to be reported")
	}
	if res.UpdatedRootSourcePaths == 0 {
		t.Fatalf("expected srcpath migration to be reported")
	}
	if res.UpdatedLevels == 0 {
		t.Fatalf("expected level migration to be reported")
	}

	got := string(canonical)
	if !strings.Contains(got, `parentname = "web-root"`) {
		t.Fatalf("expected canonical parentname in output, got:\n%s", got)
	}
	if !strings.Contains(got, `srcpath = "."`) {
		t.Fatalf("expected canonical srcpath in output, got:\n%s", got)
	}
	if !strings.Contains(got, "level = 0") || !strings.Contains(got, "level = 1") {
		t.Fatalf("expected levels to be materialized in output, got:\n%s", got)
	}
}

func TestCanonicalizeAppTemplateTOML_LevelsComputedFromHierarchyNotInputOrder(t *testing.T) {
	legacy := []byte(`
[deployment.storages]

[[deployment.paths]]
name = "assets"
parentname = "web-root"
dockerpath = "/var/www/html/assets"

[[deployment.paths]]
name = "images"
parentname = "assets"
dockerpath = "/var/www/html/assets/images"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
`)

	canonical, _, err := CanonicalizeAppTemplateTOML(legacy)
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}

	tree, err := toml.LoadBytes(canonical)
	if err != nil {
		t.Fatalf("load canonical toml failed: %v", err)
	}
	raw := tree.ToMap()
	dep, ok := raw["deployment"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing deployment map")
	}
	pathsAny, ok := dep["paths"].([]interface{})
	if !ok {
		t.Fatalf("missing deployment.paths array")
	}

	levels := make(map[string]int, len(pathsAny))
	for _, item := range pathsAny {
		p, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := p["name"].(string)
		level, _ := p["level"].(int64)
		levels[name] = int(level)
	}

	if levels["web-root"] != 0 {
		t.Fatalf("expected web-root level=0, got %d", levels["web-root"])
	}
	if levels["assets"] != 1 {
		t.Fatalf("expected assets level=1, got %d", levels["assets"])
	}
	if levels["images"] != 2 {
		t.Fatalf("expected images level=2, got %d", levels["images"])
	}
}

// loadVolumesAndPaths parses content and returns deployment.storages.volumes
// and deployment.paths as slices of maps for convenient assertions.
func loadVolumesAndPaths(t *testing.T, content []byte) ([]map[string]any, []map[string]any) {
	tree, err := toml.LoadBytes(content)
	if err != nil {
		t.Fatalf("load toml failed: %v", err)
	}
	raw := tree.ToMap()

	deployment, ok := asAnyMap(raw["deployment"])
	if !ok {
		t.Fatalf("missing deployment map")
	}
	storages, ok := asAnyMap(deployment["storages"])
	if !ok {
		t.Fatalf("missing deployment.storages map")
	}

	volumesAny, ok := asAnySlice(storages["volumes"])
	if !ok {
		t.Fatalf("missing deployment.storages.volumes array")
	}
	volumes := make([]map[string]any, 0, len(volumesAny))
	for _, v := range volumesAny {
		vm, ok := asAnyMap(v)
		if !ok {
			t.Fatalf("volume entry is not a table")
		}
		volumes = append(volumes, vm)
	}

	var paths []map[string]any
	if pathsRaw, ok := deployment["paths"]; ok {
		pathsAny, ok := asAnySlice(pathsRaw)
		if !ok {
			t.Fatalf("deployment.paths is not an array")
		}
		paths = make([]map[string]any, 0, len(pathsAny))
		for _, p := range pathsAny {
			pm, ok := asAnyMap(p)
			if !ok {
				t.Fatalf("path entry is not a table")
			}
			paths = append(paths, pm)
		}
	}

	return volumes, paths
}

func TestCanonicalizeAppVolumesTOML_SingleRowRenamedTemplateMode(t *testing.T) {
	legacy := []byte(`
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
volumename = "data-volume"
srcpath = "."
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.MergedVolumePools != 1 || res.MergedVolumeRows != 1 {
		t.Fatalf("expected 1 merged pool/row, got pools=%d rows=%d", res.MergedVolumePools, res.MergedVolumeRows)
	}
	if res.RewrittenVolumeReferences != 1 {
		t.Fatalf("expected 1 rewritten reference, got %d", res.RewrittenVolumeReferences)
	}

	volumes, paths := loadVolumesAndPaths(t, canonical)
	if len(volumes) != 1 {
		t.Fatalf("expected 1 volume row, got %d", len(volumes))
	}
	if name := asTrimmedString(volumes[0]["name"]); name != "{name}-data" {
		t.Fatalf("expected canonical volume name {name}-data, got %q", name)
	}
	if dir := asTrimmedString(volumes[0]["volumedir"]); dir != "data" {
		t.Fatalf("expected volumedir 'data', got %q", dir)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if srcname := asTrimmedString(paths[0]["srcname"]); srcname != "{name}-data" {
		t.Fatalf("expected srcname {name}-data, got %q", srcname)
	}
	if volname := asTrimmedString(paths[0]["volumename"]); volname != "{name}-data" {
		t.Fatalf("expected volumename {name}-data, got %q", volname)
	}
	if srcpath := asTrimmedString(paths[0]["srcpath"]); srcpath != "." {
		t.Fatalf("expected srcpath '.' (unchanged, relative to pool volume mount), got %q", srcpath)
	}
}

func TestCanonicalizeAppVolumesTOML_SingleRowRenamedResolvedMode(t *testing.T) {
	legacy := []byte(`
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
volumename = "data-volume"
srcpath = "."
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "myapp")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}

	volumes, paths := loadVolumesAndPaths(t, canonical)
	if name := asTrimmedString(volumes[0]["name"]); name != "myapp-data" {
		t.Fatalf("expected canonical volume name myapp-data, got %q", name)
	}
	if srcname := asTrimmedString(paths[0]["srcname"]); srcname != "myapp-data" {
		t.Fatalf("expected srcname myapp-data, got %q", srcname)
	}
	if volname := asTrimmedString(paths[0]["volumename"]); volname != "myapp-data" {
		t.Fatalf("expected volumename myapp-data, got %q", volname)
	}
	if srcpath := asTrimmedString(paths[0]["srcpath"]); srcpath != "." {
		t.Fatalf("expected srcpath '.' (unchanged, relative to pool volume mount), got %q", srcpath)
	}
}

func TestCanonicalizeAppVolumesTOML_MultiRowMergedIntoOnePool(t *testing.T) {
	legacy := []byte(`
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
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.MergedVolumePools != 1 {
		t.Fatalf("expected 1 merged pool, got %d", res.MergedVolumePools)
	}
	if res.MergedVolumeRows != 2 {
		t.Fatalf("expected 2 merged rows, got %d", res.MergedVolumeRows)
	}
	if res.RewrittenVolumeReferences != 2 {
		t.Fatalf("expected 2 rewritten references, got %d", res.RewrittenVolumeReferences)
	}

	volumes, paths := loadVolumesAndPaths(t, canonical)
	if len(volumes) != 1 {
		t.Fatalf("expected volumes to be merged into 1 row, got %d", len(volumes))
	}
	if name := asTrimmedString(volumes[0]["name"]); name != "{name}-data" {
		t.Fatalf("expected canonical volume name {name}-data, got %q", name)
	}
	if dir := asTrimmedString(volumes[0]["volumedir"]); dir != "data logs" {
		t.Fatalf("expected merged volumedir 'data logs', got %q", dir)
	}

	srcpaths := make(map[string]string, len(paths))
	for _, p := range paths {
		if srcname := asTrimmedString(p["srcname"]); srcname != "{name}-data" {
			t.Fatalf("expected srcname {name}-data, got %q", srcname)
		}
		if volname := asTrimmedString(p["volumename"]); volname != "{name}-data" {
			t.Fatalf("expected volumename {name}-data, got %q", volname)
		}
		srcpaths[asTrimmedString(p["name"])] = asTrimmedString(p["srcpath"])
	}
	// srcpath is relative to the pool's OpenSVC volume mount, which is keyed
	// by poolname and unchanged by merging same-pool rows, so both stay ".".
	if srcpaths["web-root"] != "." {
		t.Fatalf("expected web-root srcpath '.', got %q", srcpaths["web-root"])
	}
	if srcpaths["log-dir"] != "." {
		t.Fatalf("expected log-dir srcpath '.', got %q", srcpaths["log-dir"])
	}
}

func TestCanonicalizeAppVolumesTOML_Idempotent(t *testing.T) {
	legacy := []byte(`
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
`)

	first, res1, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("first canonicalize failed: %v", err)
	}
	if !res1.Changed {
		t.Fatalf("expected first pass to report changes")
	}

	second, res2, err := CanonicalizeAppVolumesTOML(first, "")
	if err != nil {
		t.Fatalf("second canonicalize failed: %v", err)
	}
	if res2.Changed {
		t.Fatalf("expected second pass to report no changes, got %+v", res2)
	}
	if string(second) != string(first) {
		t.Fatalf("expected second pass output to be unchanged:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestCanonicalizeAppVolumesTOML_GitCloneAndS3MountReferencesRewritten(t *testing.T) {
	legacy := []byte(`
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.storages.git-clones]]
name = "app-src"
volumename = "data-volume"
volumedir = "data/app-src"

[[deployment.storages.s3-mounts]]
name = "media"
volumename = "data-volume"
volumedir = "data/media"
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.RewrittenVolumeReferences != 2 {
		t.Fatalf("expected 2 rewritten references, got %d", res.RewrittenVolumeReferences)
	}

	tree, err := toml.LoadBytes(canonical)
	if err != nil {
		t.Fatalf("load canonical toml failed: %v", err)
	}
	raw := tree.ToMap()
	deployment, _ := asAnyMap(raw["deployment"])
	storages, _ := asAnyMap(deployment["storages"])

	gitClonesAny, ok := asAnySlice(storages["git-clones"])
	if !ok || len(gitClonesAny) != 1 {
		t.Fatalf("expected 1 git-clone entry")
	}
	gc, _ := asAnyMap(gitClonesAny[0])
	if v := asTrimmedString(gc["volumename"]); v != "{name}-data" {
		t.Fatalf("expected git-clone volumename {name}-data, got %q", v)
	}
	// volumedir is already relative to the pool volume mount (it was built as
	// filepath.Join(volume.VolumeDir, gc.Name)); it must not be re-prefixed.
	if v := asTrimmedString(gc["volumedir"]); v != "data/app-src" {
		t.Fatalf("expected git-clone volumedir to stay 'data/app-src', got %q", v)
	}

	s3MountsAny, ok := asAnySlice(storages["s3-mounts"])
	if !ok || len(s3MountsAny) != 1 {
		t.Fatalf("expected 1 s3-mount entry")
	}
	s3, _ := asAnyMap(s3MountsAny[0])
	if v := asTrimmedString(s3["volumename"]); v != "{name}-data" {
		t.Fatalf("expected s3-mount volumename {name}-data, got %q", v)
	}
	if v := asTrimmedString(s3["volumedir"]); v != "data/media" {
		t.Fatalf("expected s3-mount volumedir to stay 'data/media', got %q", v)
	}
}

// TestCanonicalizeAppVolumesTOML_MergeWithExistingCanonicalRowLeavesItsReferencesUnchanged
// covers a pool that already contains a row named like the canonical name
// alongside another legacy row. References to the already-canonical row must
// be left untouched (both name and srcpath), while references to the other
// row are retargeted to the (unchanged) canonical name.
func TestCanonicalizeAppVolumesTOML_MergeWithExistingCanonicalRowLeavesItsReferencesUnchanged(t *testing.T) {
	legacy := []byte(`
[deployment.storages]
[[deployment.storages.volumes]]
name = "{name}-data"
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
srcname = "{name}-data"
volumename = "{name}-data"
srcpath = "."

[[deployment.paths]]
name = "log-dir"
dockerpath = "/var/log/app"
srctype = "volume"
srcname = "logs-volume"
volumename = "logs-volume"
srcpath = "."
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.MergedVolumePools != 1 || res.MergedVolumeRows != 2 {
		t.Fatalf("expected 1 merged pool of 2 rows, got pools=%d rows=%d", res.MergedVolumePools, res.MergedVolumeRows)
	}
	if res.RewrittenVolumeReferences != 1 {
		t.Fatalf("expected only the logs-volume reference to be rewritten, got %d", res.RewrittenVolumeReferences)
	}

	volumes, paths := loadVolumesAndPaths(t, canonical)
	if len(volumes) != 1 {
		t.Fatalf("expected volumes to be merged into 1 row, got %d", len(volumes))
	}
	if name := asTrimmedString(volumes[0]["name"]); name != "{name}-data" {
		t.Fatalf("expected canonical volume name {name}-data, got %q", name)
	}
	if dir := asTrimmedString(volumes[0]["volumedir"]); dir != "data logs" {
		t.Fatalf("expected merged volumedir 'data logs', got %q", dir)
	}

	srcpaths := make(map[string]string, len(paths))
	for _, p := range paths {
		if srcname := asTrimmedString(p["srcname"]); srcname != "{name}-data" {
			t.Fatalf("expected srcname {name}-data, got %q", srcname)
		}
		srcpaths[asTrimmedString(p["name"])] = asTrimmedString(p["srcpath"])
	}
	if srcpaths["web-root"] != "." {
		t.Fatalf("expected web-root srcpath to stay '.', got %q", srcpaths["web-root"])
	}
	if srcpaths["log-dir"] != "." {
		t.Fatalf("expected log-dir srcpath to stay '.', got %q", srcpaths["log-dir"])
	}
}

// TestCanonicalizeAppVolumesTOML_NormalizesVolumeDirOnAlreadyCanonicalSingleRow
// covers a pool with a single row that already has the canonical name but an
// un-normalized volumedir (duplicate tokens / irregular whitespace). The
// merge fast-path for single canonical rows must still normalize volumedir.
func TestCanonicalizeAppVolumesTOML_NormalizesVolumeDirOnAlreadyCanonicalSingleRow(t *testing.T) {
	legacy := []byte(`
[deployment.storages]
[[deployment.storages.volumes]]
name = "{name}-data"
poolname = "data"
volumedir = "data   logs data"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
srctype = "volume"
srcname = "{name}-data"
volumename = "{name}-data"
srcpath = "."
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.MergedVolumePools != 0 || res.MergedVolumeRows != 0 {
		t.Fatalf("expected no row merges, got pools=%d rows=%d", res.MergedVolumePools, res.MergedVolumeRows)
	}

	volumes, _ := loadVolumesAndPaths(t, canonical)
	if len(volumes) != 1 {
		t.Fatalf("expected 1 volume row, got %d", len(volumes))
	}
	if name := asTrimmedString(volumes[0]["name"]); name != "{name}-data" {
		t.Fatalf("expected canonical volume name {name}-data, got %q", name)
	}
	if dir := asTrimmedString(volumes[0]["volumedir"]); dir != "data logs" {
		t.Fatalf("expected normalized volumedir 'data logs', got %q", dir)
	}

	second, res2, err := CanonicalizeAppVolumesTOML(canonical, "")
	if err != nil {
		t.Fatalf("second canonicalize failed: %v", err)
	}
	if res2.Changed {
		t.Fatalf("expected second pass to report no changes, got %+v", res2)
	}
	if string(second) != string(canonical) {
		t.Fatalf("expected second pass output to be unchanged:\nfirst:\n%s\nsecond:\n%s", canonical, second)
	}
}

func TestCanonicalizeAppVolumesTOML_AlreadyCanonicalNoOp(t *testing.T) {
	canonical := []byte(`
[deployment.storages]
[[deployment.storages.volumes]]
name = "{name}-data"
poolname = "data"
volumedir = "data"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
srctype = "volume"
srcname = "{name}-data"
volumename = "{name}-data"
srcpath = "data"
`)

	out, res, err := CanonicalizeAppVolumesTOML(canonical, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected no changes for already-canonical input, got %+v", res)
	}
	if string(out) != string(canonical) {
		t.Fatalf("expected output to equal input unchanged")
	}
}

// TestCanonicalizeAppVolumesTOML_GitSourcedPathVolumeNameRewritten covers a
// deployment.paths entry sourced from a git-clone (srctype = "git"). Its
// volumename is persisted independently of srcname at InsertPath time
// (config/deployment.go) and must be retargeted when the underlying volume
// row is renamed, even though srctype != "volume" and srcname never matches
// a volume row name.
func TestCanonicalizeAppVolumesTOML_GitSourcedPathVolumeNameRewritten(t *testing.T) {
	legacy := []byte(`
[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.storages.volumes]]
name = "logs-volume"
poolname = "data"
volumedir = "logs"

[[deployment.storages.git-clones]]
name = "app-src"
volumename = "data-volume"
volumedir = "data/app-src"

[[deployment.paths]]
name = "web-root"
dockerpath = "/var/www/html"
srctype = "git"
srcname = "app-src"
srcpath = "."
volumename = "data-volume"
`)

	canonical, res, err := CanonicalizeAppVolumesTOML(legacy, "")
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected canonicalization to report changes")
	}
	if res.RewrittenVolumeReferences != 2 {
		t.Fatalf("expected 2 rewritten references (git-clone + path), got %d", res.RewrittenVolumeReferences)
	}

	tree, err := toml.LoadBytes(canonical)
	if err != nil {
		t.Fatalf("load canonical toml failed: %v", err)
	}
	raw := tree.ToMap()
	deployment, _ := asAnyMap(raw["deployment"])
	storages, _ := asAnyMap(deployment["storages"])

	gitClonesAny, ok := asAnySlice(storages["git-clones"])
	if !ok || len(gitClonesAny) != 1 {
		t.Fatalf("expected 1 git-clone entry")
	}
	gc, _ := asAnyMap(gitClonesAny[0])
	if v := asTrimmedString(gc["volumename"]); v != "{name}-data" {
		t.Fatalf("expected git-clone volumename {name}-data, got %q", v)
	}

	pathsAny, ok := asAnySlice(deployment["paths"])
	if !ok || len(pathsAny) != 1 {
		t.Fatalf("expected 1 path entry")
	}
	pm, _ := asAnyMap(pathsAny[0])
	if v := asTrimmedString(pm["srcname"]); v != "app-src" {
		t.Fatalf("expected path srcname to remain 'app-src' (git source), got %q", v)
	}
	if v := asTrimmedString(pm["volumename"]); v != "{name}-data" {
		t.Fatalf("expected path volumename rewritten to {name}-data, got %q", v)
	}
}

func TestCanonicalizeAppTemplateRaw_UnresolvedParentReportedOnce(t *testing.T) {
	raw := map[string]any{
		"deployment": map[string]any{
			"paths": []any{
				map[string]any{
					"name":       "child",
					"dockerpath": "/var/www/html/child",
					"parentname": "missing-parent",
				},
			},
		},
	}

	res, err := CanonicalizeAppTemplateRaw(raw)
	if err != nil {
		t.Fatalf("canonicalize failed: %v", err)
	}
	if len(res.UnresolvedParentReferences) != 1 {
		t.Fatalf("expected unresolved parent to be reported once, got %v", res.UnresolvedParentReferences)
	}
	if res.UnresolvedParentReferences[0] != "missing-parent" {
		t.Fatalf("expected unresolved parent %q, got %q", "missing-parent", res.UnresolvedParentReferences[0])
	}
}

// TestStampAppConfigVersionTOML_UnversionedContentIsStamped covers Phase 10
// task 1/3: content with no app-config-version marker is rewritten with
// app-config-version = 2.
func TestStampAppConfigVersionTOML_UnversionedContentIsStamped(t *testing.T) {
	unversioned := []byte(`
app-host = "myapp"
app-port = "8080"
`)

	out, res, err := StampAppConfigVersionTOML(unversioned)
	if err != nil {
		t.Fatalf("stamp failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected stamping to report changes")
	}
	if !res.StampedAppConfigVersion {
		t.Fatalf("expected StampedAppConfigVersion to be true")
	}
	if !strings.Contains(string(out), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in output, got:\n%s", out)
	}
}

// TestStampAppConfigVersionTOML_AlreadyV2NoOp covers Phase 10 task 4:
// content already marked app-config-version = 2 must remain byte-identical.
func TestStampAppConfigVersionTOML_AlreadyV2NoOp(t *testing.T) {
	versioned := []byte(`
app-config-version = 2
app-host = "myapp"
app-port = "8080"
`)

	out, res, err := StampAppConfigVersionTOML(versioned)
	if err != nil {
		t.Fatalf("stamp failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected no changes for already-V2 content, got %+v", res)
	}
	if res.StampedAppConfigVersion {
		t.Fatalf("expected StampedAppConfigVersion to be false for already-V2 content")
	}
	if string(out) != string(versioned) {
		t.Fatalf("expected output to equal input unchanged:\nin:\n%s\nout:\n%s", versioned, out)
	}
}

// TestStampAppConfigVersionTOML_StaleVersionIsBumped covers a marker older
// than AppConfigVersionV2 being bumped up to the current version.
func TestStampAppConfigVersionTOML_StaleVersionIsBumped(t *testing.T) {
	stale := []byte(`
app-config-version = 1
app-host = "myapp"
`)

	out, res, err := StampAppConfigVersionTOML(stale)
	if err != nil {
		t.Fatalf("stamp failed: %v", err)
	}
	if !res.Changed || !res.StampedAppConfigVersion {
		t.Fatalf("expected stale version to be bumped, got %+v", res)
	}
	if !strings.Contains(string(out), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in output, got:\n%s", out)
	}
}

func TestAppConfigVersionFromRaw(t *testing.T) {
	if v := AppConfigVersionFromRaw(map[string]any{}); v != 0 {
		t.Fatalf("expected 0 for missing marker, got %d", v)
	}
	if v := AppConfigVersionFromRaw(map[string]any{"app-config-version": int64(2)}); v != 2 {
		t.Fatalf("expected 2 for int64 marker, got %d", v)
	}
	if v := AppConfigVersionFromRaw(map[string]any{"app-config-version": "not-a-number"}); v != 0 {
		t.Fatalf("expected 0 for non-numeric marker, got %d", v)
	}
}
