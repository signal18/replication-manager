package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestValidateTemplateNameForLocalWrite(t *testing.T) {
	workDir := t.TempDir()

	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", ""); err == nil {
		t.Fatalf("expected error for empty template name")
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "shared/nginx"); err == nil {
		t.Fatalf("expected error for shared template")
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "../escape"); err == nil {
		t.Fatalf("expected error for path traversal")
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "/tmp/escape"); err == nil {
		t.Fatalf("expected error for absolute path")
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "local/../../escape"); err == nil {
		t.Fatalf("expected error for cleaned traversal path")
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "./local/custom"); err != nil {
		t.Fatalf("expected normalized local template name, got %v", err)
	}
	if err := validateTemplateNameForLocalWrite(workDir, "testcluster", "local/custom"); err != nil {
		t.Fatalf("expected valid local template name, got %v", err)
	}
}

func TestResolveTemplateCachePath(t *testing.T) {
	workDir := t.TempDir()

	t.Run("shared template path is normalized and accepted", func(t *testing.T) {
		normalized, localPath, err := resolveTemplateCachePath(workDir, "testcluster", "./shared/nginx")
		if err != nil {
			t.Fatalf("expected shared template to be valid, got %v", err)
		}
		if normalized != "shared/nginx" {
			t.Fatalf("expected normalized template shared/nginx, got %q", normalized)
		}
		if localPath == "" {
			t.Fatal("expected non-empty local cache path")
		}
	})

	t.Run("shared traversal is rejected", func(t *testing.T) {
		if _, _, err := resolveTemplateCachePath(workDir, "testcluster", "shared/../../escape"); err == nil {
			t.Fatal("expected shared traversal to be rejected")
		}
	})
}

func TestValidateCanonicalTemplateContentForSave(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}

	validTemplate := []byte(`
[[deployment.storages.volumes]]
name = "v1"
poolname = "data"
volumedir = "d"

[[deployment.paths]]
name = "root"
dockerpath = "/srv/app"
srctype = "volume"
srcname = "v1"
srcpath = "."
`)
	if err := validateCanonicalTemplateContentForSave(cl, "local/valid", validTemplate); err != nil {
		t.Fatalf("expected valid template content, got %v", err)
	}

	invalidTemplate := []byte(`
[[deployment.paths]]
name = "child"
parentname = "missing"
dockerpath = "/srv/app/child"
`)
	if err := validateCanonicalTemplateContentForSave(cl, "local/invalid", invalidTemplate); err == nil {
		t.Fatalf("expected invalid template content to fail validation")
	}
}

// TestAppTemplateContentSave_MergesMultiRowVolumePool covers Phase 7: saving
// edited template content must pass through the same canonical merge as
// GetTemplateContent (CanonicalizeAppContent with appName=""), not just the
// path/level migration. Otherwise a hand-edited template with two rows for
// the same pool would be persisted un-merged, only to be silently rewritten
// (and reported as a separate "Changed" canonicalization) the next time
// GetTemplateContent loads it.
func TestAppTemplateContentSave_MergesMultiRowVolumePool(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}

	submitted := []byte(`
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

	canonicalContent, _, err := cluster.CanonicalizeAppContent(submitted, "")
	if err != nil {
		t.Fatalf("CanonicalizeAppContent failed: %v", err)
	}
	if err := validateCanonicalTemplateContentForSave(cl, "local/merge-on-save", canonicalContent); err != nil {
		t.Fatalf("validateCanonicalTemplateContentForSave failed: %v", err)
	}

	got := string(canonicalContent)
	if !strings.Contains(got, `name = "{name}-data"`) {
		t.Fatalf("expected merged volume row named {name}-data, got:\n%s", got)
	}
	if !strings.Contains(got, `volumedir = "data logs"`) {
		t.Fatalf("expected merged volumedir 'data logs', got:\n%s", got)
	}
	if strings.Contains(got, `"data-volume"`) || strings.Contains(got, `"logs-volume"`) {
		t.Fatalf("expected legacy volume names rewritten away, got:\n%s", got)
	}
	if !strings.Contains(got, "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 to be stamped on save, got:\n%s", got)
	}
}

// TestAppTemplateContentSave_AlreadyV2NoOp covers Phase 10 task 4 for the
// template save flow: re-saving already-V2 canonical content does not
// produce a second rewrite of the version marker (CanonicalizeAppContent is
// idempotent on already-flagged content).
func TestAppTemplateContentSave_AlreadyV2NoOp(t *testing.T) {
	versioned := []byte(`
app-config-version = 2

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
srcpath = "."
level = 0
`)

	canonicalContent, res, err := cluster.CanonicalizeAppContent(versioned, "")
	if err != nil {
		t.Fatalf("CanonicalizeAppContent failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected no changes for already-V2 canonical content, got %+v", res)
	}
	if string(canonicalContent) != string(versioned) {
		t.Fatalf("expected output to equal input unchanged:\nin:\n%s\nout:\n%s", versioned, canonicalContent)
	}
}

func TestWriteTemplateContentAtomically(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, ".templates", "apps", "local", "custom.toml")

	if err := writeTemplateContentAtomically(path, []byte("prov-app-docker-img = \"nginx:latest\"\n")); err != nil {
		t.Fatalf("writeTemplateContentAtomically failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written template failed: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("expected non-empty template file content")
	}
}

func TestCreateLocalTemplateCopyFromTemplate(t *testing.T) {
	workDir := t.TempDir()
	shareDir := t.TempDir()

	sharedPath := filepath.Join(shareDir, "app", "templates", "dummy.toml")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o755); err != nil {
		t.Fatalf("mkdir shared template dir failed: %v", err)
	}
	if err := os.WriteFile(sharedPath, []byte(`
[[deployment.storages.volumes]]
name = "v1"
poolname = "data"
volumedir = "d"

[[deployment.paths]]
name = "root"
dockerpath = "/srv/app"
srctype = "volume"
srcname = "v1"
srcpath = "."
`), 0o644); err != nil {
		t.Fatalf("write shared template failed: %v", err)
	}

	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: workDir, ShareDir: shareDir, ProvAppTemplateRepo: "%%%"}}

	if err := createLocalTemplateCopyFromTemplate(cl, "shared/dummy", "local/copyme"); err != nil {
		t.Fatalf("createLocalTemplateCopyFromTemplate failed: %v", err)
	}

	localPath := filepath.Join(workDir, ".templates", "apps", "local", "copyme.toml")
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("expected local template copy at %s, err=%v", localPath, err)
	}

	// Phase 10 task 5: the local copy is created via CanonicalizeAppContent,
	// so it must carry the app-config-version = 2 marker.
	copied, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local template copy failed: %v", err)
	}
	if !strings.Contains(string(copied), "app-config-version = 2") {
		t.Fatalf("expected app-config-version = 2 in local template copy, got:\n%s", copied)
	}
}

func TestValidateDummyTemplateRenamePolicy(t *testing.T) {
	if err := validateDummyTemplateRenamePolicy("shared/dummy", "local/dummy"); err == nil {
		t.Fatalf("expected rename policy error when keeping dummy basename")
	}
	if err := validateDummyTemplateRenamePolicy("shared/dummy", "local/my-template"); err != nil {
		t.Fatalf("expected renamed dummy template to pass, got %v", err)
	}
	if err := validateDummyTemplateRenamePolicy("repo/template-a", "local/dummy"); err != nil {
		t.Fatalf("expected non-dummy source to bypass rename policy, got %v", err)
	}
}
