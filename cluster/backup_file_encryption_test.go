package cluster

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ---------------------------------------------------------------------------
// isStreamContainerFile — legacy vs stream format detection
// ---------------------------------------------------------------------------

// TestLegacyAndStreamFormatCoexist verifies that legacy AES-CBC backup files
// are NOT detected as stream containers, while RMSC files are — confirming that
// both formats can coexist in the same backup directory.
func TestLegacyAndStreamFormatCoexist(t *testing.T) {
	dir := t.TempDir()

	// Write a plaintext / legacy-style file (not an RMSC container)
	legacyPath := filepath.Join(dir, "legacy.sql.gz")
	if err := os.WriteFile(legacyPath, []byte("legacy-encrypted-data"), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	ok, err := isStreamContainerFile(legacyPath)
	if err != nil {
		t.Fatalf("isStreamContainerFile on legacy file: %v", err)
	}
	if ok {
		t.Error("expected legacy file NOT to be detected as stream container")
	}

	// Build a minimal RMSC stream container
	rootSecret := []byte("test-root-secret-32byteslong!!!!!")
	keyID := "cloud18-sponsor-user-credentials:v1"
	streamPath := filepath.Join(dir, "stream.sql.gz")
	if err := os.WriteFile(streamPath, []byte("plaintext content"), 0o644); err != nil {
		t.Fatalf("write stream fixture: %v", err)
	}
	if err := backupmgr.EncryptFileAsStreamContainer(streamPath, rootSecret, "test-cluster", "stream.sql.gz", keyID); err != nil {
		t.Fatalf("EncryptFileAsStreamContainer: %v", err)
	}

	ok, err = isStreamContainerFile(streamPath)
	if err != nil {
		t.Fatalf("isStreamContainerFile on stream file: %v", err)
	}
	if !ok {
		t.Error("expected stream container file to be detected")
	}
}

// ---------------------------------------------------------------------------
// openStreamContainerEntry — observability helpers
// ---------------------------------------------------------------------------

// TestOpenStreamContainerEntryMalformedHeaderReturnsPreflightError verifies that
// a file with a wrong magic prefix returns a malformed-header error, not a panic
// or a key derivation error.
func TestOpenStreamContainerEntryMalformedHeaderReturnsPreflightError(t *testing.T) {
	r := bytes.NewReader([]byte("NOT_RMSC_MAGIC_BYTES"))
	_, _, err := openStreamContainerEntry(context.Background(), r, "sponsor:s3cr3t", "")
	if err == nil {
		t.Fatal("expected error from malformed header")
	}
	if !isPreflightError(err) {
		t.Errorf("expected a preflight error, got: %v", err)
	}
}

// TestOpenStreamContainerEntryWrongKeySourceReturnsError verifies that when the
// resolved secret source does not match the key reference embedded in the
// preflight header, an error is returned.
func TestOpenStreamContainerEntryWrongKeySourceReturnsError(t *testing.T) {
	// Build a container with sponsor-source key reference
	plaintext := []byte("payload")
	rootSecret := []byte("test-root-secret-32byteslong!!!!!")
	keyID := "cloud18-sponsor-user-credentials:v1"

	tmp := filepath.Join(t.TempDir(), "backup.sql")
	os.WriteFile(tmp, plaintext, 0o644)
	if err := backupmgr.EncryptFileAsStreamContainer(tmp, rootSecret, "test-cluster", "backup.sql", keyID); err != nil {
		t.Fatalf("create container: %v", err)
	}
	data, _ := os.ReadFile(tmp)

	// Attempt to open using only API credentials (no sponsor) — source mismatch
	_, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), "", "dba:pass,admin:adminpass")
	if err == nil {
		t.Fatal("expected error from key source mismatch")
	}
}

// isPreflightError returns true when err wraps one of the backupmgr preflight
// sentinel errors, indicating the container was rejected at the header stage.
func isPreflightError(err error) bool {
	if err == nil {
		return false
	}
	for _, sentinel := range []error{
		backupmgr.ErrMalformedHeader,
		backupmgr.ErrUnsupportedVersion,
		backupmgr.ErrInvalidAlgorithm,
		backupmgr.ErrTruncatedHeader,
		backupmgr.ErrMissingKeyReference,
	} {
		if isErr(err, sentinel) {
			return true
		}
	}
	return false
}

// isErr is a simple errors.Is wrapper used to avoid importing "errors" just for
// a helper that also lives in test files already importing it via backupmgr.
func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// BackupEncryptionStreamTransport flag routing
// ---------------------------------------------------------------------------

// TestFinalizeMysqldumpSingleFileEncryption_EncryptionOff_IsNoOp verifies that
// when BackupEncryption is false, finalizeMysqldumpSingleFileEncryption returns
// nil immediately without modifying the file.
func TestFinalizeMysqldumpSingleFileEncryption_EncryptionOff_IsNoOp(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "dump.sql.gz")
	original := []byte("plain-mysqldump-payload")
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	server := &ServerMonitor{
		ClusterGroup: &Cluster{
			Name: "noop-cluster",
			Conf: &config.Config{
				WorkingDir:       dir,
				BackupEncryption: false,
			},
		},
	}

	if err := server.finalizeMysqldumpSingleFileEncryption(backupPath); err != nil {
		t.Fatalf("expected no error when encryption is disabled, got: %v", err)
	}

	got, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(original, got) {
		t.Errorf("expected file unchanged when encryption is disabled")
	}
}

// TestFinalizeMysqldumpSingleFileEncryption_FlagOff_ProducesLegacyFormat verifies
// that BackupEncryptionStreamTransport=false routes to the legacy AES-CBC path:
// the output file must NOT begin with the RMSC stream container magic prefix.
func TestFinalizeMysqldumpSingleFileEncryption_FlagOff_ProducesLegacyFormat(t *testing.T) {
	root := t.TempDir()
	clusterName := "flag-off-cluster"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir:       root,
			BackupEncryption: true,
			// BackupEncryptionStreamTransport defaults to false
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
				"api-credentials":                  {Value: "dba:dba-pass,admin:admin-pass"},
			},
		},
	}
	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 1, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	backupPath := filepath.Join(t.TempDir(), "dump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("plaintext-payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := server.finalizeMysqldumpSingleFileEncryption(backupPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ok, err := isStreamContainerFile(backupPath)
	if err != nil {
		t.Fatalf("isStreamContainerFile: %v", err)
	}
	if ok {
		t.Errorf("BackupEncryptionStreamTransport=false: expected legacy (non-stream-container) output, got stream container magic")
	}
}

// TestFinalizeMysqldumpSingleFileEncryption_FlagOn_ProducesStreamContainerFormat
// verifies that BackupEncryptionStreamTransport=true routes to the RMSC stream
// container path: the output file must begin with the stream container magic prefix.
func TestFinalizeMysqldumpSingleFileEncryption_FlagOn_ProducesStreamContainerFormat(t *testing.T) {
	root := t.TempDir()
	clusterName := "flag-on-cluster"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir:                      root,
			BackupEncryption:                true,
			BackupEncryptionStreamTransport: true,
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
				"api-credentials":                  {Value: "dba:dba-pass,admin:admin-pass"},
			},
		},
	}
	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 1, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	backupPath := filepath.Join(t.TempDir(), "dump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("plaintext-payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := server.finalizeMysqldumpSingleFileEncryption(backupPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ok, err := isStreamContainerFile(backupPath)
	if err != nil {
		t.Fatalf("isStreamContainerFile: %v", err)
	}
	if !ok {
		t.Errorf("BackupEncryptionStreamTransport=true: expected stream container output, file did not begin with RMSC magic")
	}

	// Also verify metadata is tagged with stream format
	meta := server.LastBackupMeta.Logical
	if !meta.EncryptionStreamFormat {
		t.Errorf("expected EncryptionStreamFormat=true in metadata when stream transport is enabled")
	}
	if meta.EncryptionAlgo != backupmgr.StreamCipherSuiteAES256GCMHKDFSHA256 {
		t.Errorf("expected EncryptionAlgo=%s, got %s", backupmgr.StreamCipherSuiteAES256GCMHKDFSHA256, meta.EncryptionAlgo)
	}
}
