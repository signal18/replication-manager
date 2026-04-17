package config

import (
	"strings"
	"testing"
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
