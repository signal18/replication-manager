package backupmgr

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testPassword  = "correct horse battery staple"
	otherPassword = "wrong-password"
)

func init() {
	// Keep PBKDF2 cheap in tests; the openssl interop tests pass the same
	// count to the CLI.
	SetOpenSSLIterationsForTesting(1000)
}

func TestEncryptDecryptFileRoundTrip(t *testing.T) {
	cases := map[string][]byte{
		"empty":      {},
		"small":      []byte("hello world"),
		"large":      bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 200000), // ~9.4MB
		"compressed": mustGzipLike(),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "artifact.bin")
			if err := os.WriteFile(source, content, 0600); err != nil {
				t.Fatalf("write source: %v", err)
			}

			dest := filepath.Join(dir, "artifact.bin.enc")
			if err := EncryptFile(source, dest, testPassword); err != nil {
				t.Fatalf("EncryptFile: %v", err)
			}

			// Source must be untouched by EncryptFile itself.
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source removed unexpectedly: %v", err)
			}

			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("stat dest: %v", err)
			}
			if perm := info.Mode().Perm(); perm != 0600 {
				t.Fatalf("expected 0600 artifact, got %o", perm)
			}
			sidecarInfo, err := os.Stat(IntegritySidecarPath(dest))
			if err != nil {
				t.Fatalf("stat integrity sidecar: %v", err)
			}
			if perm := sidecarInfo.Mode().Perm(); perm != 0600 {
				t.Fatalf("expected 0600 integrity sidecar, got %o", perm)
			}

			r, err := OpenDecryptedFile(dest, testPassword)
			if err != nil {
				t.Fatalf("OpenDecryptedFile: %v", err)
			}
			defer r.Close()

			got := make([]byte, 0, len(content))
			buf := make([]byte, 4096)
			for {
				n, rerr := r.Read(buf)
				got = append(got, buf[:n]...)
				if rerr != nil {
					break
				}
			}
			if !bytes.Equal(got, content) {
				t.Fatalf("round trip mismatch: got %d bytes, want %d bytes", len(got), len(content))
			}
		})
	}
}

func mustGzipLike() []byte {
	// Not actually gzip-compressed, but high-entropy-looking binary content
	// stands in for "compressed input" without pulling in compress/gzip.
	b := make([]byte, 65536)
	for i := range b {
		b[i] = byte((i*2654435761 + 17) % 256)
	}
	return b
}

func TestEncryptFileWrongPassword(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "artifact.bin")
	os.WriteFile(source, []byte("secret data"), 0600)
	dest := filepath.Join(dir, "artifact.bin.enc")

	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	r, err := OpenDecryptedFile(dest, otherPassword)
	if err != nil {
		// age may fail fast on header verification.
		return
	}
	defer r.Close()
	buf := make([]byte, 1024)
	if _, err := r.Read(buf); err == nil {
		t.Fatalf("expected decrypt error with wrong password")
	}
}

func TestOpenDecryptedFileDamagedHeader(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "artifact.bin")
	os.WriteFile(source, []byte("secret data"), 0600)
	dest := filepath.Join(dir, "artifact.bin.enc")
	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	raw, _ := os.ReadFile(dest)
	if len(raw) < 10 {
		t.Fatalf("encrypted artifact unexpectedly small")
	}
	raw[0] ^= 0xFF
	os.WriteFile(dest, raw, 0600)

	if _, err := OpenDecryptedFile(dest, testPassword); err == nil {
		t.Fatalf("expected error opening artifact with damaged header")
	}
}

func TestOpenDecryptedFileDamagedCiphertext(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "artifact.bin")
	os.WriteFile(source, bytes.Repeat([]byte("x"), 8192), 0600)
	dest := filepath.Join(dir, "artifact.bin.enc")
	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	raw, _ := os.ReadFile(dest)
	// Flip a byte near the end, in the payload rather than the header.
	raw[len(raw)-5] ^= 0xFF
	os.WriteFile(dest, raw, 0600)

	r, err := OpenDecryptedFile(dest, testPassword)
	if err != nil {
		return
	}
	defer r.Close()
	buf := make([]byte, len(raw))
	_, readErr := io.ReadFull(r, buf)
	if readErr == nil {
		t.Fatalf("expected error reading damaged ciphertext")
	}
}

func TestEncryptFileIncompletePartialIsIgnored(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "artifact.bin")
	os.WriteFile(source, []byte("secret data"), 0600)
	dest := filepath.Join(dir, "artifact.bin.enc")

	// Simulate a crash mid-encryption: a stale .partial file with garbage.
	os.WriteFile(dest+".partial", []byte("garbage-from-a-previous-crashed-run"), 0600)

	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("EncryptFile should overwrite a stale partial: %v", err)
	}

	r, err := OpenDecryptedFile(dest, testPassword)
	if err != nil {
		t.Fatalf("OpenDecryptedFile: %v", err)
	}
	defer r.Close()
	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "secret data" {
		t.Fatalf("unexpected content after stale-partial overwrite: %q", buf[:n])
	}
}

func TestEncryptFileFailureLeavesSourceAndNoPartial(t *testing.T) {
	dir := t.TempDir()
	// Source does not exist: EncryptFile must fail before ever creating a
	// destination or leaving a .partial artifact behind.
	source := filepath.Join(dir, "missing.bin")
	dest := filepath.Join(dir, "artifact.bin.enc")

	if err := EncryptFile(source, dest, testPassword); err == nil {
		t.Fatalf("expected error for missing source")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist after failed encryption")
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("no .partial artifact should remain after failed encryption")
	}
	if _, err := os.Stat(IntegritySidecarPath(dest)); !os.IsNotExist(err) {
		t.Fatalf("no integrity sidecar should remain after failed encryption")
	}
	if _, err := os.Stat(IntegritySidecarPath(dest) + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("no partial integrity sidecar should remain after failed encryption")
	}
}

func TestEncryptDecryptDirectoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "src")
	mustMkdirAll(t, filepath.Join(sourceDir, "sub"))
	mustWriteFile(t, filepath.Join(sourceDir, "a.txt"), []byte("aaa"), 0644)
	mustWriteFile(t, filepath.Join(sourceDir, "sub", "b.txt"), []byte("bbb"), 0640)

	dest := filepath.Join(dir, "src.tar.enc")
	if err := EncryptDirectory(sourceDir, dest, testPassword); err != nil {
		t.Fatalf("EncryptDirectory: %v", err)
	}
	if _, err := os.Stat(IntegritySidecarPath(dest)); err != nil {
		t.Fatalf("directory encryption sidecar: %v", err)
	}

	tempBase := filepath.Join(dir, "restore-tmp")
	extractedDir, cleanup, err := MaterializeDecryptedDirectory(dest, tempBase, testPassword)
	if err != nil {
		t.Fatalf("MaterializeDecryptedDirectory: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(extractedDir)
	if err != nil {
		t.Fatalf("stat extracted dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("expected 0700 extracted dir, got %o", perm)
	}

	gotA, err := os.ReadFile(filepath.Join(extractedDir, "a.txt"))
	if err != nil || string(gotA) != "aaa" {
		t.Fatalf("a.txt mismatch: %v %q", err, gotA)
	}
	gotB, err := os.ReadFile(filepath.Join(extractedDir, "sub", "b.txt"))
	if err != nil || string(gotB) != "bbb" {
		t.Fatalf("sub/b.txt mismatch: %v %q", err, gotB)
	}

	cleanup()
	if _, err := os.Stat(extractedDir); !os.IsNotExist(err) {
		t.Fatalf("expected extracted dir to be removed after cleanup")
	}
}

func TestOpenDecryptedFileRequiresValidIntegritySidecar(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "artifact.bin")
	dest := source + EncryptedFileSuffix
	if err := os.WriteFile(source, []byte("authenticated backup"), 0600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	if _, err := OpenDecryptedFile(dest, otherPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("wrong password error = %v, want integrity failure", err)
	}

	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	raw[len(raw)/2] ^= 0x01
	if err := os.WriteFile(dest, raw, 0600); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	if _, err := OpenDecryptedFile(dest, testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("tampered artifact error = %v, want integrity failure", err)
	}

	if err := EncryptFile(source, dest, testPassword); err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}
	sidecar := IntegritySidecarPath(dest)
	sidecarRaw, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	index := len(IntegrityFormatHMACSHA256V1) + 1
	if sidecarRaw[index] == '0' {
		sidecarRaw[index] = '1'
	} else {
		sidecarRaw[index] = '0'
	}
	if err := os.WriteFile(sidecar, sidecarRaw, 0600); err != nil {
		t.Fatalf("tamper sidecar: %v", err)
	}
	if _, err := OpenDecryptedFile(dest, testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("tampered sidecar error = %v, want integrity failure", err)
	}

	if err := os.Remove(sidecar); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	if _, err := OpenDecryptedFile(dest, testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("missing sidecar error = %v, want integrity failure", err)
	}
	if err := os.WriteFile(sidecar, []byte(IntegrityFormatHMACSHA256V1+":00\n"), 0600); err != nil {
		t.Fatalf("write wrong-length sidecar: %v", err)
	}
	if _, err := OpenDecryptedFile(dest, testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("wrong-length sidecar error = %v, want integrity failure", err)
	}
	if err := os.WriteFile(sidecar, []byte("hmac-sha256-v2:"+strings.Repeat("0", 64)+"\n"), 0600); err != nil {
		t.Fatalf("write unsupported sidecar: %v", err)
	}
	if _, err := OpenDecryptedFile(dest, testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("unsupported sidecar error = %v, want integrity failure", err)
	}
}

func TestEncryptDirectoryRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "src")
	outsideDir := filepath.Join(dir, "outside")
	mustMkdirAll(t, sourceDir)
	mustMkdirAll(t, outsideDir)
	mustWriteFile(t, filepath.Join(outsideDir, "secret.txt"), []byte("outside"), 0600)

	if err := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(sourceDir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	dest := filepath.Join(dir, "src.tar.enc")
	if err := EncryptDirectory(sourceDir, dest, testPassword); err == nil {
		t.Fatalf("expected EncryptDirectory to reject escaping symlink")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist after rejected symlink")
	}
}

func TestEncryptDirectoryDereferencesInternalSymlink(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "src")
	mustMkdirAll(t, sourceDir)
	mustWriteFile(t, filepath.Join(sourceDir, "real.txt"), []byte("real content"), 0644)
	if err := os.Symlink(filepath.Join(sourceDir, "real.txt"), filepath.Join(sourceDir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	dest := filepath.Join(dir, "src.tar.enc")
	if err := EncryptDirectory(sourceDir, dest, testPassword); err != nil {
		t.Fatalf("EncryptDirectory with internal symlink should succeed: %v", err)
	}

	tempBase := filepath.Join(dir, "restore-tmp")
	extractedDir, cleanup, err := MaterializeDecryptedDirectory(dest, tempBase, testPassword)
	if err != nil {
		t.Fatalf("MaterializeDecryptedDirectory should restore an archive with a dereferenced internal symlink: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(extractedDir, "link.txt"))
	if err != nil || string(got) != "real content" {
		t.Fatalf("link.txt (dereferenced) mismatch: %v %q", err, got)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
