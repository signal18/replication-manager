package backupmgr

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// opensslCLI returns the openssl binary or skips the test.
func opensslCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl CLI not installed")
	}
	return bin
}

func opensslArgs(extra ...string) []string {
	return append([]string{"enc", "-aes-256-cbc", "-pbkdf2", "-iter", strconv.Itoa(OpenSSLIterations()), "-md", "sha256"}, extra...)
}

// Repman-encrypted artifacts decrypt with the stock openssl CLI, for every
// padding boundary.
func TestOpenSSLCLIDecryptsRepmanArtifact(t *testing.T) {
	bin := opensslCLI(t)
	for _, size := range []int{0, 1, 15, 16, 17, 65535, 65536, 65537, 300000} {
		dir := t.TempDir()
		content := bytes.Repeat([]byte("x"), size)
		for i := range content {
			content[i] = byte(i * 7)
		}
		src := filepath.Join(dir, "dump.sql.gz")
		os.WriteFile(src, content, 0600)
		if err := EncryptFile(src, src+EncryptedFileSuffix, testPassword); err != nil {
			t.Fatalf("size %d: EncryptFile: %v", size, err)
		}
		cmd := exec.Command(bin, opensslArgs("-d", "-pass", "env:REPMAN_TEST_PASS", "-in", src+EncryptedFileSuffix)...)
		cmd.Env = append(os.Environ(), "REPMAN_TEST_PASS="+testPassword)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("size %d: openssl -d: %v", size, err)
		}
		if !bytes.Equal(out, content) {
			t.Fatalf("size %d: openssl output differs from the original", size)
		}
	}
}

// Stock OpenSSL files remain stream-decryptable for the existing Restic
// streaming path, while local artifact restores require Repman's HMAC sidecar.
func TestRepmanDecryptsOpenSSLCLIFile(t *testing.T) {
	bin := opensslCLI(t)
	dir := t.TempDir()
	content := bytes.Repeat([]byte("mariabackup stream "), 5000)
	src := filepath.Join(dir, "xb")
	os.WriteFile(src, content, 0600)
	cmd := exec.Command(bin, opensslArgs("-e", "-salt", "-pass", "env:REPMAN_TEST_PASS", "-in", src, "-out", src+".enc")...)
	cmd.Env = append(os.Environ(), "REPMAN_TEST_PASS="+testPassword)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl -e: %v: %s", err, out)
	}
	if _, err := OpenDecryptedFile(src+".enc", testPassword); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatalf("local OpenSSL artifact without HMAC sidecar error = %v, want integrity failure", err)
	}
	f, _ := os.Open(src + ".enc")
	defer f.Close()
	stream, err := DecryptStream(f, testPassword)
	if err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("stream round trip from openssl failed: %v", err)
	}
}

// With the right password among the candidates, a typed artifact always
// decrypts with it, even when wrong passwords are tried first (secret_store
// history): the content check rejects wrong passwords whose padding happens
// to be valid (about 1 in 256).
func TestOpenDecryptedFileSkipsWrongCandidates(t *testing.T) {
	dir := t.TempDir()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), 500))
	zw.Close()
	src := filepath.Join(dir, "mysqldump.sql.gz")
	os.WriteFile(src, gz.Bytes(), 0600)
	if err := EncryptFile(src, src+EncryptedFileSuffix, testPassword); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	for i := 0; i < 200; i++ {
		r, err := OpenDecryptedFile(src+EncryptedFileSuffix, "wrong-"+strconv.Itoa(i), testPassword)
		if err != nil {
			t.Fatalf("candidate %d: %v", i, err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(got, gz.Bytes()) {
			t.Fatalf("candidate %d: a wrong password was accepted before the right one", i)
		}
	}
}

// Content that does not match its name (e.g. a custom save script writing
// plain SQL to a .gz name) still restores with the right password.
func TestOpenDecryptedFileMislabeledContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mysqldump.sql.gz")
	os.WriteFile(src, []byte("-- plain SQL, not gzip\n"), 0600)
	EncryptFile(src, src+EncryptedFileSuffix, testPassword)
	r, err := OpenDecryptedFile(src+EncryptedFileSuffix, testPassword)
	if err != nil {
		t.Fatalf("mislabeled content must still restore: %v", err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "-- plain SQL, not gzip\n" {
		t.Fatalf("unexpected content %q", got)
	}
}

// Unknown artifact types only get the padding check: a wrong password is
// still refused up front most of the time.
func TestOpenDecryptedFileRefusesWrongPasswordUpFront(t *testing.T) {
	refused := 0
	for i := 0; i < 20; i++ {
		dir := t.TempDir()
		src := filepath.Join(dir, "d")
		os.WriteFile(src, bytes.Repeat([]byte{byte(i)}, 1000+i), 0600)
		EncryptFile(src, src+".enc", testPassword)
		r, err := OpenDecryptedFile(src+".enc", "wrong-"+strconv.Itoa(i))
		if err != nil {
			if !errors.Is(err, ErrBackupIntegrity) {
				t.Fatalf("unexpected error type: %v", err)
			}
			refused++
			continue
		}
		r.Close()
	}
	if refused < 18 {
		t.Fatalf("only %d/20 wrong passwords refused up front", refused)
	}
}

func TestPlausiblePlaintextStart(t *testing.T) {
	tarHead := make([]byte, 512)
	copy(tarHead[257:], "ustar")
	cases := []struct {
		name string
		head []byte
		want bool
	}{
		{"mysqldump.sql.gz.enc", []byte{0x1f, 0x8b, 8}, true},
		{"mysqldump.sql.gz.enc", []byte{0x00, 0x8b, 8}, false},
		{"splitdump.tar.enc", tarHead, true},
		{"splitdump.tar.enc", make([]byte, 512), false},
		{"mariabackup.xbtream.enc", []byte("XBSTCK01rest"), true},
		{"mariabackup.xbtream.enc", []byte("XBSTCK0Xrest"), false},
		{"mysql-bin.000042.enc", []byte{0xfe, 'b', 'i', 'n', 0}, true},
		{"mysql-bin.000042.enc", []byte{0xfe, 'x', 'i', 'n', 0}, false},
		{"dump.sql.enc", []byte("-- MariaDB dump\n"), true},
		{"dump.sql.enc", []byte{'-', 0x01, 'x'}, false},
		{"custom.bin.enc", []byte{0x00, 0x01}, true},
	}
	for _, tc := range cases {
		if got := plausiblePlaintextStart("/backups/"+tc.name, tc.head); got != tc.want {
			t.Fatalf("%s %x: got %v, want %v", tc.name, tc.head, got, tc.want)
		}
	}
}

func TestDecryptTruncatedArtifact(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "d")
	os.WriteFile(src, bytes.Repeat([]byte("a"), 100000), 0600)
	EncryptFile(src, src+".enc", testPassword)
	b, _ := os.ReadFile(src + ".enc")
	os.WriteFile(src+".cut", b[:len(b)-7], 0600)
	if _, err := OpenDecryptedFile(src+".cut", testPassword); !errors.Is(err, ErrBackupDecrypt) {
		t.Fatalf("truncated file must be refused, got %v", err)
	}
	stream, err := DecryptStream(bytes.NewReader(b[:len(b)-7]), testPassword)
	if err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	if _, err := io.ReadAll(stream); !errors.Is(err, ErrBackupDecrypt) {
		t.Fatalf("truncated stream must fail at the end, got %v", err)
	}
}

func TestEncryptRefusesEmptyPassword(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "d")
	os.WriteFile(src, []byte("x"), 0600)
	if err := EncryptFile(src, src+".enc", ""); err == nil {
		t.Fatalf("empty password must be refused")
	}
	if _, err := os.Stat(src + ".enc"); !os.IsNotExist(err) {
		t.Fatalf("no artifact may be created with an empty password")
	}
}
