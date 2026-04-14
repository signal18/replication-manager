package backupmgr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildValidManifest returns a structurally valid manifest for testing.
func buildValidManifest(keyRef, keyCluster string, entries []BackupEncryptionManifestEntry) *BackupEncryptionManifest {
	return &BackupEncryptionManifest{
		Version:    BackupEncryptionManifestVersion,
		KeyRef:     keyRef,
		KeyCluster: keyCluster,
		Entries:    entries,
	}
}

func validManifestEntry(path string) BackupEncryptionManifestEntry {
	return BackupEncryptionManifestEntry{
		Path: path,
		IV:   "hex:aabbccddeeff0011",
		MAC:  "deadbeefdeadbeefdeadbeefdeadbeef",
	}
}

// ---- ValidateBackupEncryptionManifest ----------------------------------------

func TestValidateBackupEncryptionManifestAcceptsValidManifest(t *testing.T) {
	m := buildValidManifest(
		"cloud18-sponsor-user-credentials:v3",
		"clusterA",
		[]BackupEncryptionManifestEntry{validManifestEntry("file1.sql")},
	)
	if err := ValidateBackupEncryptionManifest(m); err != nil {
		t.Fatalf("expected valid manifest to pass, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsNil(t *testing.T) {
	err := ValidateBackupEncryptionManifest(nil)
	if !errors.Is(err, ErrEncryptedManifestNil) {
		t.Fatalf("expected nil manifest error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsUnknownVersion(t *testing.T) {
	m := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", []BackupEncryptionManifestEntry{validManifestEntry("f")})
	m.Version = 99
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestVersionUnknown) {
		t.Fatalf("expected unknown version error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsZeroVersion(t *testing.T) {
	m := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", []BackupEncryptionManifestEntry{validManifestEntry("f")})
	m.Version = 0
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestVersionUnknown) {
		t.Fatalf("expected unknown version error for version 0, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsMissingKeyRef(t *testing.T) {
	m := buildValidManifest("", "cl", []BackupEncryptionManifestEntry{validManifestEntry("f")})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestKeyRefMissing) {
		t.Fatalf("expected key ref missing error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsInvalidKeyRef(t *testing.T) {
	m := buildValidManifest("not-a-valid-ref", "cl", []BackupEncryptionManifestEntry{validManifestEntry("f")})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestKeyRefMissing) {
		t.Fatalf("expected key ref missing error for invalid ref, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsMissingKeyCluster(t *testing.T) {
	m := buildValidManifest("api-credentials/admin:v1", "", []BackupEncryptionManifestEntry{validManifestEntry("f")})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestKeyClusterMissing) {
		t.Fatalf("expected key cluster missing error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsEmptyEntries(t *testing.T) {
	m := buildValidManifest("api-credentials/admin:v2", "cl", nil)
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestNoEntries) {
		t.Fatalf("expected no entries error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsEntryMissingPath(t *testing.T) {
	entry := validManifestEntry("")
	m := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", []BackupEncryptionManifestEntry{entry})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestEntryPathMissing) {
		t.Fatalf("expected entry path missing error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsEntryMissingIV(t *testing.T) {
	entry := validManifestEntry("file.sql")
	entry.IV = ""
	m := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", []BackupEncryptionManifestEntry{entry})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestEntryIVMissing) {
		t.Fatalf("expected entry IV missing error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestRejectsEntryMissingMAC(t *testing.T) {
	entry := validManifestEntry("file.sql")
	entry.MAC = ""
	m := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", []BackupEncryptionManifestEntry{entry})
	err := ValidateBackupEncryptionManifest(m)
	if !errors.Is(err, ErrEncryptedManifestEntryMACMissing) {
		t.Fatalf("expected entry MAC missing error, got: %v", err)
	}
}

// ---- BackupEncryptionManifestPath --------------------------------------------

func TestBackupEncryptionManifestPath(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/data/backups/mydumper", "/data/backups/mydumper" + BackupEncryptionManifestFileSuffix},
		{"/data/backups/splitdump/", "/data/backups/splitdump" + BackupEncryptionManifestFileSuffix},
		{"relative/dir", "relative/dir" + BackupEncryptionManifestFileSuffix},
	}
	for _, tc := range cases {
		got := BackupEncryptionManifestPath(tc.input)
		if got != tc.expected {
			t.Errorf("BackupEncryptionManifestPath(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// ---- WriteBackupEncryptionManifest / ReadBackupEncryptionManifest ------------

func TestWriteAndReadBackupEncryptionManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "mydumper")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	original := &BackupEncryptionManifest{
		KeyRef:     "cloud18-sponsor-user-credentials:v5",
		KeyCluster: "clusterA",
		Entries: []BackupEncryptionManifestEntry{
			{Path: "db1.sql", IV: "hex:aabb", MAC: "ccdd"},
			{Path: "db2.sql", IV: "hex:eeff", MAC: "0011"},
		},
	}

	if err := WriteBackupEncryptionManifest(backupDir, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify file was created at expected path
	manifestPath := BackupEncryptionManifestPath(backupDir)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected manifest file at %s: %v", manifestPath, err)
	}

	got, err := ReadBackupEncryptionManifest(backupDir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if got.Version != BackupEncryptionManifestVersion {
		t.Errorf("expected version %d, got %d", BackupEncryptionManifestVersion, got.Version)
	}
	if got.KeyRef != original.KeyRef {
		t.Errorf("expected KeyRef %q, got %q", original.KeyRef, got.KeyRef)
	}
	if got.KeyCluster != original.KeyCluster {
		t.Errorf("expected KeyCluster %q, got %q", original.KeyCluster, got.KeyCluster)
	}
	if len(got.Entries) != len(original.Entries) {
		t.Fatalf("expected %d entries, got %d", len(original.Entries), len(got.Entries))
	}
	for i, e := range original.Entries {
		if got.Entries[i].Path != e.Path || got.Entries[i].IV != e.IV || got.Entries[i].MAC != e.MAC {
			t.Errorf("entry[%d] mismatch: got %+v, want %+v", i, got.Entries[i], e)
		}
	}
}

func TestReadBackupEncryptionManifestFailsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadBackupEncryptionManifest(filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestWriteBackupEncryptionManifestRejectsNil(t *testing.T) {
	dir := t.TempDir()
	err := WriteBackupEncryptionManifest(dir, nil)
	if !errors.Is(err, ErrEncryptedManifestNil) {
		t.Fatalf("expected nil manifest error, got: %v", err)
	}
}

// ---- ValidateBackupEncryptionManifestEntries ---------------------------------

func TestValidateBackupEncryptionManifestEntriesSucceeds(t *testing.T) {
	baseDir := t.TempDir()
	secret := "test-secret"

	// Create two real encrypted files
	files := []string{"part1.sql", "part2.sql"}
	entries := make([]BackupEncryptionManifestEntry, 0, len(files))
	for _, name := range files {
		path := filepath.Join(baseDir, name)
		if err := os.WriteFile(path, []byte("payload-"+name), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		ivToken, err := EncryptBackupFileAES256CBC(path, secret)
		if err != nil {
			t.Fatalf("encrypt %s: %v", name, err)
		}
		mac, err := ComputeBackupFileHMACSHA256(path, secret)
		if err != nil {
			t.Fatalf("hmac %s: %v", name, err)
		}
		entries = append(entries, BackupEncryptionManifestEntry{Path: name, IV: ivToken, MAC: mac})
	}

	manifest := buildValidManifest("cloud18-sponsor-user-credentials:v1", "cl", entries)
	if err := ValidateBackupEncryptionManifestEntries(manifest, baseDir, secret); err != nil {
		t.Fatalf("expected valid entries to pass, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestEntriesFailsOnMissingFile(t *testing.T) {
	baseDir := t.TempDir()

	entry := BackupEncryptionManifestEntry{
		Path: "missing-file.sql",
		IV:   "hex:aabb",
		MAC:  "somemac",
	}
	manifest := buildValidManifest("api-credentials/admin:v1", "cl", []BackupEncryptionManifestEntry{entry})

	err := ValidateBackupEncryptionManifestEntries(manifest, baseDir, "secret")
	if err == nil {
		t.Fatal("expected missing file to fail closed")
	}
	if !strings.Contains(err.Error(), "missing from disk") {
		t.Fatalf("expected 'missing from disk' in error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestEntriesFailsOnTamperedMAC(t *testing.T) {
	baseDir := t.TempDir()
	secret := "correct-secret"

	path := filepath.Join(baseDir, "file.sql")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ivToken, err := EncryptBackupFileAES256CBC(path, secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Use a tampered MAC — restore must fail closed before decrypting.
	entry := BackupEncryptionManifestEntry{
		Path: "file.sql",
		IV:   ivToken,
		MAC:  "tampered-mac-value",
	}
	manifest := buildValidManifest("api-credentials/admin:v2", "cl", []BackupEncryptionManifestEntry{entry})

	err = ValidateBackupEncryptionManifestEntries(manifest, baseDir, secret)
	if err == nil {
		t.Fatal("expected tampered MAC to fail closed")
	}
	if !strings.Contains(err.Error(), "HMAC verification failed") {
		t.Fatalf("expected HMAC failure in error, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestEntriesFailsClosedOnFirstBadEntry(t *testing.T) {
	baseDir := t.TempDir()
	secret := "secret"

	// First entry: valid encrypted file
	path1 := filepath.Join(baseDir, "good.sql")
	if err := os.WriteFile(path1, []byte("good"), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}
	ivToken, _ := EncryptBackupFileAES256CBC(path1, secret)
	mac, _ := ComputeBackupFileHMACSHA256(path1, secret)

	// Second entry: file does not exist
	entries := []BackupEncryptionManifestEntry{
		{Path: "good.sql", IV: ivToken, MAC: mac},
		{Path: "absent.sql", IV: "hex:aa", MAC: "bb"},
	}
	manifest := buildValidManifest("api-credentials/admin:v3", "cl", entries)

	err := ValidateBackupEncryptionManifestEntries(manifest, baseDir, secret)
	if err == nil {
		t.Fatal("expected second missing entry to fail closed")
	}
	if !strings.Contains(err.Error(), "absent.sql") {
		t.Fatalf("expected error to reference missing file, got: %v", err)
	}
}

func TestValidateBackupEncryptionManifestEntriesRejectsInvalidStructure(t *testing.T) {
	err := ValidateBackupEncryptionManifestEntries(nil, t.TempDir(), "secret")
	if err == nil {
		t.Fatal("expected nil manifest to fail")
	}
}
