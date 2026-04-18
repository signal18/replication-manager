package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestValidateTemplateNameForLocalWrite(t *testing.T) {
	if err := validateTemplateNameForLocalWrite(""); err == nil {
		t.Fatalf("expected error for empty template name")
	}
	if err := validateTemplateNameForLocalWrite("shared/nginx"); err == nil {
		t.Fatalf("expected error for shared template")
	}
	if err := validateTemplateNameForLocalWrite("../escape"); err == nil {
		t.Fatalf("expected error for path traversal")
	}
	if err := validateTemplateNameForLocalWrite("local/custom"); err != nil {
		t.Fatalf("expected valid local template name, got %v", err)
	}
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

	sharedPath := filepath.Join(shareDir, "app", "deployments", "copyme.toml")
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

	if err := createLocalTemplateCopyFromTemplate(cl, "shared/copyme", "local/copyme"); err != nil {
		t.Fatalf("createLocalTemplateCopyFromTemplate failed: %v", err)
	}

	localPath := filepath.Join(workDir, ".templates", "apps", "local", "copyme.toml")
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("expected local template copy at %s, err=%v", localPath, err)
	}
}
