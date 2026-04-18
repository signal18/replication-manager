package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestInferTemplateMetadata_SharedTemplate(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	meta := inferTemplateMetadata(cl, "shared/nginx")

	if meta.Origin != "shared" {
		t.Fatalf("expected shared origin, got %q", meta.Origin)
	}
	if meta.Editable {
		t.Fatalf("expected shared template non-editable")
	}
	if !meta.Refreshable {
		t.Fatalf("expected shared template refreshable")
	}
}

func TestInferTemplateMetadata_LocalTemplate(t *testing.T) {
	workDir := t.TempDir()
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: workDir}}

	localPath := filepath.Join(workDir, ".templates", "apps", "my-template.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		t.Fatalf("mkdir local template dir failed: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("prov-app-docker-img='nginx:latest'"), 0o600); err != nil {
		t.Fatalf("write local template failed: %v", err)
	}

	meta := inferTemplateMetadata(cl, "my-template")
	if meta.Origin != "local" {
		t.Fatalf("expected local origin, got %q", meta.Origin)
	}
	if !meta.Editable {
		t.Fatalf("expected local template editable")
	}
	if !meta.HasLocalCopy {
		t.Fatalf("expected local template to have local copy")
	}
	if meta.Refreshable {
		t.Fatalf("expected local template not refreshable by source")
	}
}

func TestInferTemplateMetadata_RepoTemplate(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	meta := inferTemplateMetadata(cl, "repo/template-a")

	if meta.Origin != "repo" {
		t.Fatalf("expected repo origin, got %q", meta.Origin)
	}
	if meta.Editable {
		t.Fatalf("expected repo template non-editable")
	}
	if !meta.Refreshable {
		t.Fatalf("expected repo template refreshable")
	}
}
