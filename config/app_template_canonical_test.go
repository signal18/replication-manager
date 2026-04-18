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
