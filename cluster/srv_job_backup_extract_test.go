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
		if e.Type == tar.TypeSymlink || e.Type == tar.TypeLink {
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

func TestExtractArchiveToDirAllowsHardlinkToRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "CREATE TABLE test;"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "original.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for hardlink archive, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	if root != expectedRoot {
		t.Fatalf("extracted root = %q, want %q", root, expectedRoot)
	}

	originalContent, err := os.ReadFile(filepath.Join(expectedRoot, "original.sql"))
	if err != nil {
		t.Fatalf("failed to read original file: %v", err)
	}
	if string(originalContent) != "CREATE TABLE test;" {
		t.Fatalf("original content = %q, want %q", string(originalContent), "CREATE TABLE test;")
	}

	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "CREATE TABLE test;" {
		t.Fatalf("alias content = %q, want %q", string(aliasContent), "CREATE TABLE test;")
	}
}

func TestExtractArchiveToDirAllowsDeferredHardlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "deferred_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "original.sql"},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "CREATE TABLE deferred;"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for deferred hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "CREATE TABLE deferred;" {
		t.Fatalf("alias content = %q, want %q", string(aliasContent), "CREATE TABLE deferred;")
	}
	_ = root
}

func TestExtractArchiveToDirRejectsAbsoluteHardlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "absolute_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/bad", Type: tar.TypeLink, Linkname: "/etc/passwd"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for absolute hardlink target, got nil")
	}
	if !strings.Contains(err.Error(), "hardlink target must be relative") {
		t.Fatalf("error message should mention 'hardlink target must be relative', got: %v", err)
	}
}

func TestExtractArchiveToDirRejectsEscapingHardlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "escaping_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/bad", Type: tar.TypeLink, Linkname: "../../outside"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for escaping hardlink target, got nil")
	}
	if !strings.Contains(err.Error(), "hardlink target escapes target dir") {
		t.Fatalf("error message should mention 'hardlink target escapes target dir', got: %v", err)
	}
}

func TestExtractArchiveToDirFailsUnresolvedHardlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "unresolved_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "nonexistent.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for unresolved hardlink target, got nil")
	}
	if !strings.Contains(err.Error(), "unresolved hardlink target") {
		t.Fatalf("error message should mention 'unresolved hardlink target', got: %v", err)
	}
}

func TestExtractArchiveToDirAllowsHardlinkRootRelativeTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "root_rel_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "CREATE TABLE rootrel;"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "root/original.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for root-relative hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "CREATE TABLE rootrel;" {
		t.Fatalf("alias content = %q, want %q", string(aliasContent), "CREATE TABLE rootrel;")
	}
	_ = root
}

func TestExtractArchiveToDirAllowsDeferredHardlinkRootRelativeTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "deferred_root_rel_hardlink.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "root/original.sql"},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "CREATE TABLE deferredroot;"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for deferred root-relative hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "CREATE TABLE deferredroot;" {
		t.Fatalf("alias content = %q, want %q", string(aliasContent), "CREATE TABLE deferredroot;")
	}
	_ = root
}

func TestExtractArchiveToDirHardlinkBareNamePrefersDirRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "bare_name_ambig.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "DIR RELATIVE CONTENT"},
		{Name: "original.sql", Type: tar.TypeReg, Content: "ROOT LEVEL CONTENT"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "original.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for bare name hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "DIR RELATIVE CONTENT" {
		t.Fatalf("alias should use dir-relative source, got %q", string(aliasContent))
	}
	_ = root
}

func TestExtractArchiveToDirHardlinkPathPrefersRootRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "path_ambig.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "ROOT RELATIVE CONTENT"},
		{Name: "root/root/original.sql", Type: tar.TypeReg, Content: "NESTED CONTENT"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "root/original.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for path hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "ROOT RELATIVE CONTENT" {
		t.Fatalf("alias should use root-relative source, got %q", string(aliasContent))
	}
	_ = root
}

func TestExtractArchiveToDirHardlinkTargetPreservesTrailingSpace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "trailing_space.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql ", Type: tar.TypeReg, Content: "SPACED CONTENT"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "original.sql "},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for trailing space hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "SPACED CONTENT" {
		t.Fatalf("alias content = %q, want %q", string(aliasContent), "SPACED CONTENT")
	}
}

func TestExtractArchiveToDirHardlinkBackslashTargetUsesTarSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "backslash_path.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/original.sql", Type: tar.TypeReg, Content: "ROOT RELATIVE CONTENT"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "root\\original.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	root, err := extractArchiveToDir(archivePath, extractDir)
	if err != nil {
		t.Fatalf("expected success for backslash path hardlink, got error: %v", err)
	}

	expectedRoot := filepath.Join(extractDir, "root")
	aliasContent, err := os.ReadFile(filepath.Join(expectedRoot, "alias.sql"))
	if err != nil {
		t.Fatalf("failed to read hardlink file: %v", err)
	}
	if string(aliasContent) != "ROOT RELATIVE CONTENT" {
		t.Fatalf("alias should use root-relative source for path-shaped target, got %q", string(aliasContent))
	}
	_ = root
}

// TestExtractArchiveToDirHardlinkLiteralBackslashPolicyNote documents the policy tradeoff:
// normalizeArchivePathRef treats backslashes as path separators (tar/POSIX semantics).
// This is a documentation-only note; runtime verification is covered by
// TestExtractArchiveToDirHardlinkBackslashTargetUsesTarSemantics.
// Archives that intentionally use literal backslashes (not as separators) will be normalized.
func TestExtractArchiveToDirHardlinkLiteralBackslashPolicyNote(t *testing.T) {
	t.Skip("Policy is documented in normalizeArchivePathRef; backslash-as-separator is intentional for cross-platform compatibility")
}

func TestExtractArchiveToDirRejectsRelativePathTraversalEntryName(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "relative_traversal.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "../outside.txt", Type: tar.TypeReg, Content: "owned"},
	})

	rootTmp := t.TempDir()
	extractDir := filepath.Join(rootTmp, "extract")
	outsidePath := filepath.Join(rootTmp, "outside.txt")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for path traversal archive entry, got nil")
	}
	if !strings.Contains(err.Error(), "archive entry escapes target dir") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(outsidePath); err == nil {
		t.Fatal("outside file should not be created")
	}
}

func TestExtractArchiveToDirRejectsAbsoluteEntryName(t *testing.T) {
	tests := []struct {
		name          string
		entries       []testTarEntry
		skipOnWindows bool
	}{
		{
			name: "absolute_directory",
			entries: []testTarEntry{
				{Name: "root/", Type: tar.TypeDir},
				{Name: "/absdir/", Type: tar.TypeDir},
			},
		},
		{
			name: "absolute_regular_file",
			entries: []testTarEntry{
				{Name: "root/", Type: tar.TypeDir},
				{Name: "/abs/evil.txt", Type: tar.TypeReg, Content: "owned"},
			},
		},
		{
			name: "absolute_symlink",
			entries: []testTarEntry{
				{Name: "root/", Type: tar.TypeDir},
				{Name: "/abslink", Type: tar.TypeSymlink, Linkname: "root"},
			},
			skipOnWindows: true,
		},
		{
			name: "absolute_hardlink",
			entries: []testTarEntry{
				{Name: "root/", Type: tar.TypeDir},
				{Name: "root/original.sql", Type: tar.TypeReg, Content: "SELECT 1;"},
				{Name: "/absalias.sql", Type: tar.TypeLink, Linkname: "root/original.sql"},
			},
			skipOnWindows: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipOnWindows && runtime.GOOS == "windows" {
				t.Skip("symlink/hardlink test not supported on windows")
			}

			archivePath := filepath.Join(t.TempDir(), "absolute_"+tc.name+".tar")
			writeTestTar(t, archivePath, tc.entries)

			extractDir := filepath.Join(t.TempDir(), "extract")
			if err := os.MkdirAll(extractDir, 0o755); err != nil {
				t.Fatalf("failed to create extract dir: %v", err)
			}

			_, err := extractArchiveToDir(archivePath, extractDir)
			if err == nil {
				t.Fatal("expected error for absolute archive entry, got nil")
			}
			if !strings.Contains(err.Error(), "archive entry must be relative") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestExtractArchiveToDirRejectsHardlinkToSymlinkSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink/symlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "hardlink_symlink_source.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/real.sql", Type: tar.TypeReg, Content: "SELECT 1;"},
		{Name: "root/link.sql", Type: tar.TypeSymlink, Linkname: "real.sql"},
		{Name: "root/alias.sql", Type: tar.TypeLink, Linkname: "link.sql"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for hardlink-to-symlink source, got nil")
	}
	if !strings.Contains(err.Error(), "hardlink source is a symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractArchiveToDirRejectsHardlinkToDirectorySource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test not supported on windows")
	}

	archivePath := filepath.Join(t.TempDir(), "hardlink_dir_source.tar")
	writeTestTar(t, archivePath, []testTarEntry{
		{Name: "root/", Type: tar.TypeDir},
		{Name: "root/dir/", Type: tar.TypeDir},
		{Name: "root/alias", Type: tar.TypeLink, Linkname: "dir"},
	})

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}

	_, err := extractArchiveToDir(archivePath, extractDir)
	if err == nil {
		t.Fatal("expected error for hardlink-to-directory source, got nil")
	}
	if !strings.Contains(err.Error(), "hardlink source is not a regular file") {
		t.Fatalf("unexpected error: %v", err)
	}
}
