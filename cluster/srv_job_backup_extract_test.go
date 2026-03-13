package cluster

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testTarEntry struct {
	Name     string
	Type     byte
	Content  string
	Linkname string
	Mode     int64
}

func writeTestTar(t *testing.T, archivePath string, entries []testTarEntry) {
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("failed to create archive: %v", err)
	}
	defer file.Close()

	tw := tar.NewWriter(file)
	defer tw.Close()

	for _, e := range entries {
		var mode int64 = 0o644
		if e.Type == tar.TypeDir {
			mode = 0o755
		}
		if e.Mode != 0 {
			mode = e.Mode
		}

		var linkname string
		var content io.Reader
		if e.Type == tar.TypeSymlink {
			linkname = e.Linkname
		} else if e.Content != "" {
			content = strings.NewReader(e.Content)
		}

		header := &tar.Header{
			Name:     e.Name,
			Mode:     mode,
			Size:     int64(len(e.Content)),
			Typeflag: e.Type,
			Linkname: linkname,
		}

		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("failed to write header for %s: %v", e.Name, err)
		}

		if content != nil {
			if _, err := io.Copy(tw, content); err != nil {
				t.Fatalf("failed to write content for %s: %v", e.Name, err)
			}
		}
	}
}

func TestExtractArchiveToDirRejectsAbsoluteSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "malicious.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/bad", Type: tar.TypeSymlink, Linkname: "/etc/passwd"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for absolute symlink target, got nil")
	}
	if !strings.Contains(err.Error(), "symlink target must be relative") {
		t.Fatalf("error message should mention 'symlink target must be relative', got: %v", err)
	}
}

func TestExtractArchiveToDirRejectsEscapingRelativeSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "escaping.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/bad", Type: tar.TypeSymlink, Linkname: "../../outside"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for escaping symlink target, got nil")
	}
	if !strings.Contains(err.Error(), "symlink target escapes target dir") {
		t.Fatalf("error message should mention 'symlink target escapes target dir', got: %v", err)
	}
}

func TestExtractArchiveToDirBlocksSymlinkTraversalWriteChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	rootTmp := t.TempDir()
	extractDir := filepath.Join(rootTmp, "extract")
	outsideDir := filepath.Join(rootTmp, "outside")

	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	outsidePwnedPath := filepath.Join(outsideDir, "pwned.txt")

	archivePath := filepath.Join(t.TempDir(), "traversal.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/linkout", Type: tar.TypeSymlink, Linkname: "../../outside"},
		{Name: "root/linkout/pwned.txt", Type: tar.TypeReg, Content: "compromised"},
	})

	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for symlink traversal write chain, got nil")
	}

	if _, err := os.Stat(outsidePwnedPath); err == nil {
		t.Fatal("outside file should not exist - symlink traversal was not blocked")
	}
}

func TestExtractArchiveToDirAllowsRelativeInTreeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "valid.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/inner/", Type: tar.TypeDir},
		{Name: "root/inner/file.txt", Type: tar.TypeReg, Content: "hello world"},
		{Name: "root/link", Type: tar.TypeSymlink, Linkname: "inner"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for valid relative symlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	if root != expectedRoot {
		t.Fatalf("extracted root = %q, want %q", root, expectedRoot)
	}

	innerFile := filepath.Join(expectedRoot, "inner", "file.txt")
	content, err := os.ReadFile(innerFile)
	if err != nil {
		t.Fatalf("failed to read inner file: %v", err)
	}
	if string(content) != "hello world" {
		t.Fatalf("inner file content = %q, want %q", string(content), "hello world")
	}

	linkPath := filepath.Join(expectedRoot, "link")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("failed to lstat symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s, got mode %v", linkPath, info.Mode())
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("failed to read symlink target: %v", err)
	}
	if target != "inner" {
		t.Fatalf("symlink target = %q, want %q", target, "inner")
	}
}

func TestExtractArchiveToDirRejectsEmptySymlinkTarget(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "empty.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/emptylink", Type: tar.TypeSymlink, Linkname: ""},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for empty symlink target, got nil")
	}
	if !strings.Contains(err.Error(), "invalid empty symlink target") {
		t.Fatalf("error message should mention 'invalid empty symlink target', got: %v", err)
	}
}
