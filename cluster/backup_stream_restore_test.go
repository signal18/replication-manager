package cluster

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildStreamContainer creates a valid single-entry stream container for tests.
// Returns the encoded bytes (preflight + frames).
func buildStreamContainer(t *testing.T, plaintext []byte, sponsorCreds, apiCreds string) []byte {
	t.Helper()

	// Resolve root secret and derive keys
	rootSecret, _, err := backupmgr.ResolveStreamRootSecretForReference(sponsorCreds, apiCreds, "cloud18-sponsor-user-credentials:v1")
	if err != nil {
		t.Fatalf("buildStreamContainer: resolve root secret: %v", err)
	}
	containerKey, err := backupmgr.DeriveStreamContainerKey(rootSecret, "test-cluster")
	if err != nil {
		t.Fatalf("buildStreamContainer: derive container key: %v", err)
	}
	entryKey, err := backupmgr.DeriveStreamEntryKey(containerKey, "backup.sql")
	if err != nil {
		t.Fatalf("buildStreamContainer: derive entry key: %v", err)
	}

	preflight := &backupmgr.StreamPreflight{
		Magic:       backupmgr.StreamContainerMagic,
		Version:     backupmgr.StreamContainerVersionV1,
		Mode:        backupmgr.StreamModeSingleFile,
		CipherSuite: backupmgr.StreamCipherSuiteAES256GCMHKDFSHA256,
		FrameSize:   64 * 1024,
		KeyRef: backupmgr.StreamKeyReference{
			KeyID:          "cloud18-sponsor-user-credentials:v1",
			KeyCluster:     "test-cluster",
			VersionContext: backupmgr.BackupKeyContextStreamContainerV1,
		},
		Entries: []backupmgr.StreamEntryIndex{
			{
				Path:      "backup.sql",
				Class:     backupmgr.StreamEntryClassData,
				SizeBytes: uint64(len(plaintext)),
				OrderHint: 1,
				GroupHint: "full",
			},
		},
	}

	header, err := backupmgr.EncodePreflight(preflight)
	if err != nil {
		t.Fatalf("buildStreamContainer: encode preflight: %v", err)
	}

	var frameBuf bytes.Buffer
	w, err := backupmgr.NewFrameWriter(&frameBuf, entryKey, backupmgr.StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("buildStreamContainer: new frame writer: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("buildStreamContainer: write plaintext: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("buildStreamContainer: close frame writer: %v", err)
	}

	return append(header, frameBuf.Bytes()...)
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stream-container-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return f.Name()
}

// ---------------------------------------------------------------------------
// isStreamContainerFile
// ---------------------------------------------------------------------------

func TestIsStreamContainerFile_DetectsStreamContainer(t *testing.T) {
	t.Parallel()

	plaintext := []byte("SELECT 1;")
	data := buildStreamContainer(t, plaintext, "sponsor:s3cr3t", "")
	path := writeTempFile(t, data)

	ok, err := isStreamContainerFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected stream container to be detected")
	}
}

func TestIsStreamContainerFile_RejectsNonStreamFile(t *testing.T) {
	t.Parallel()

	path := writeTempFile(t, []byte("this is a plain mysqldump file"))

	ok, err := isStreamContainerFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected non-stream file to not be detected as stream container")
	}
}

func TestIsStreamContainerFile_RejectsEmptyFile(t *testing.T) {
	t.Parallel()

	path := writeTempFile(t, []byte{})

	ok, err := isStreamContainerFile(path)
	if err != nil {
		t.Fatalf("unexpected error for empty file: %v", err)
	}
	if ok {
		t.Fatalf("expected empty file to not be detected as stream container")
	}
}

func TestIsStreamContainerFile_ErrorsOnMissingFile(t *testing.T) {
	t.Parallel()

	_, err := isStreamContainerFile(filepath.Join(t.TempDir(), "no-such-file"))
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// openStreamContainerEntry (core testable logic)
// ---------------------------------------------------------------------------

func TestOpenStreamContainerEntry_SuccessPath(t *testing.T) {
	t.Parallel()

	plaintext := []byte("CREATE TABLE t (id INT);\nINSERT INTO t VALUES (1);\n")
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	r, preflight, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), sponsorCreds, "")
	if err != nil {
		t.Fatalf("openStreamContainerEntry: %v", err)
	}
	if preflight == nil {
		t.Fatalf("expected preflight to be returned")
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read decrypted stream: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q, want %q", got, plaintext)
	}
}

func TestOpenStreamContainerEntry_FailsOnInvalidPreflight(t *testing.T) {
	t.Parallel()

	// Not a stream container — bad magic
	data := []byte("PLAINTEXT SQL FILE NO MAGIC")

	_, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), "sponsor:s3cr3t", "")
	if err == nil {
		t.Fatalf("expected error for non-stream-container input")
	}
}

func TestOpenStreamContainerEntry_FailsOnTamperedCiphertext(t *testing.T) {
	t.Parallel()

	plaintext := []byte("DROP TABLE IF EXISTS sensitive;")
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	// Tamper the payload after the preflight header
	// Corrupt bytes in the second half to hit the frame ciphertext
	mid := len(data) / 2
	data[mid] ^= 0xFF

	r, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), sponsorCreds, "")
	if err != nil {
		// openStreamContainerEntry may succeed here (reads preflight, tamper is in frames)
		t.Fatalf("openStreamContainerEntry: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, backupmgr.ErrFrameAuthFailed) {
		t.Fatalf("expected ErrFrameAuthFailed for tampered stream, got: %v", err)
	}
}

func TestOpenStreamContainerEntry_FailsOnWrongCredentials(t *testing.T) {
	t.Parallel()

	plaintext := []byte("SELECT 1;")
	sponsorCreds := "sponsor:correct-password"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	// Try to open with wrong credentials — key derivation produces wrong key
	r, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), "sponsor:wrong-password", "")
	if err != nil {
		// May fail at key reference source mismatch
		return
	}

	// If it doesn't fail at key resolution, it must fail during frame decryption
	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatalf("expected decryption failure with wrong credentials")
	}
}

func TestOpenStreamContainerEntry_FailsWithMismatchedKeySource(t *testing.T) {
	t.Parallel()

	plaintext := []byte("SELECT 1;")
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	// Pass admin creds but stream was created with sponsor source — source mismatch
	_, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), "", "dba:pass,admin:admin-pass")
	if err == nil {
		t.Fatalf("expected error when resolved source does not match key reference source")
	}
}

func TestOpenStreamContainerEntry_FailsOnTruncatedStream(t *testing.T) {
	t.Parallel()

	plaintext := bytes.Repeat([]byte("important data "), 200)
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	// Truncate mid-frame
	truncated := data[:len(data)-10]

	r, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(truncated), sponsorCreds, "")
	if err != nil {
		t.Fatalf("openStreamContainerEntry: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, backupmgr.ErrFrameTruncated) {
		t.Fatalf("expected ErrFrameTruncated for truncated stream, got: %v", err)
	}
}

func TestOpenStreamContainerEntry_ContextCancellation(t *testing.T) {
	t.Parallel()

	plaintext := bytes.Repeat([]byte("cancel me "), 5000)
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before reading

	r, _, err := openStreamContainerEntry(ctx, bytes.NewReader(data), sponsorCreds, "")
	if err != nil {
		t.Fatalf("openStreamContainerEntry: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Story 3.9: Negative test matrix additions
// ---------------------------------------------------------------------------

// TestOpenStreamContainerEntry_KeyResolutionSentinelInErrorChain verifies that
// when the resolved secret source does not match the key reference source,
// the returned error wraps ErrKeyResolutionFailed. This validates the sentinel
// wrapping added for structured log categorisation in logStreamLifecycleFailure.
func TestOpenStreamContainerEntry_KeyResolutionSentinelInErrorChain(t *testing.T) {
	t.Parallel()

	// Build a container with sponsor-source key reference.
	plaintext := []byte("payload")
	sponsorCreds := "sponsor:s3cr3t"
	data := buildStreamContainer(t, plaintext, sponsorCreds, "")

	// Provide only admin credentials — source mismatch triggers ErrKeyResolutionFailed.
	_, _, err := openStreamContainerEntry(context.Background(), bytes.NewReader(data), "", "dba:pass,admin:adminpass")
	if err == nil {
		t.Fatal("expected error when resolved source does not match key reference source")
	}
	if !errors.Is(err, backupmgr.ErrKeyResolutionFailed) {
		t.Errorf("expected ErrKeyResolutionFailed in error chain, got: %v", err)
	}
}

// TestOpenStreamContainerEntry_EmptySingleFileContainerRejected verifies that
// a single-file stream container whose preflight declares zero entries is
// rejected before any frame decryption begins, satisfying the "missing entry
// metadata" failure-closed requirement.
func TestOpenStreamContainerEntry_EmptySingleFileContainerRejected(t *testing.T) {
	t.Parallel()

	// Build a preflight with 0 entries in single-file mode. EncodePreflight
	// permits this (it only rejects >1 entries for single-file); the rejection
	// must therefore happen in openStreamContainerEntry.
	preflight := &backupmgr.StreamPreflight{
		Magic:       backupmgr.StreamContainerMagic,
		Version:     backupmgr.StreamContainerVersionV1,
		Mode:        backupmgr.StreamModeSingleFile,
		CipherSuite: backupmgr.StreamCipherSuiteAES256GCMHKDFSHA256,
		FrameSize:   64 * 1024,
		KeyRef: backupmgr.StreamKeyReference{
			KeyID:          "cloud18-sponsor-user-credentials:v1",
			KeyCluster:     "test-cluster",
			VersionContext: backupmgr.BackupKeyContextStreamContainerV1,
		},
		Entries: nil, // zero entries — missing entry metadata
	}

	header, err := backupmgr.EncodePreflight(preflight)
	if err != nil {
		t.Fatalf("EncodePreflight: %v", err)
	}

	// Supply only the preflight with no frame data; a frame would never be
	// reached because the entry check fires first.
	_, _, err = openStreamContainerEntry(context.Background(), bytes.NewReader(header), "sponsor:s3cr3t", "")
	if err == nil {
		t.Fatal("expected error for single-file container with no entries")
	}
	// The error must not be a preflight sentinel — it is caught after ReadPreflight succeeds.
	if isPreflightError(err) {
		t.Errorf("expected a non-preflight error (caught in openStreamContainerEntry), got preflight error: %v", err)
	}
}
