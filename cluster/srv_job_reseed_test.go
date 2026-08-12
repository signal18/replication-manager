// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/version"
)

func newArtifactTestServer(t *testing.T, workingDir string) *ServerMonitor {
	t.Helper()
	return &ServerMonitor{
		ClusterGroup: &Cluster{
			Name: "testcluster",
			Conf: &config.Config{WorkingDir: workingDir},
		},
		Host:      "10.0.0.1",
		Port:      "3306",
		URL:       "10.0.0.1:3306",
		DBVersion: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11},
	}
}

func TestDirectReseedSystemArtifactDirLayout(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	got := server.directReseedSystemArtifactDir("job42", start)
	want := filepath.Join(server.GetMyBackupDirectoryPath(), "direct-reseed-system", "20260102T030405Z_job42")
	if got != want {
		t.Fatalf("directReseedSystemArtifactDir() = %q, want %q", got, want)
	}
}

func writeAndPublishTestArtifact(t *testing.T, server *ServerMonitor, jobID string, systemSQL string, meta splitdump.Metadata) string {
	t.Helper()
	w, err := server.newDirectReseedSystemArtifactWriter(jobID, time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}
	if _, err := io.WriteString(w, systemSQL); err != nil {
		t.Fatalf("write system SQL: %v", err)
	}
	finalDir, err := w.publish(meta, directReseedArtifactExtra{
		SourceServer:          "src:3306",
		DestinationServer:     server.Host + ":" + server.Port,
		SourceServerVersion:   "10.11.6-MariaDB",
		DestinationFamily:     server.DBVersion.Flavor,
		DestinationMajorMinor: directReseedServerMajorMinor(server.DBVersion),
		BoundaryFormat:        "v1-eof-bounded",
		ArtifactState:         directReseedArtifactStatePublished,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return finalDir
}

func TestDirectReseedArtifactAtomicPublicationContents(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "INSTALL PLUGIN disk SONAME 'disk.so';\n", splitdump.Metadata{
		File: "mysql-bin.000042", Position: 1234, GTID: "0-1-100",
	})

	if strings.Contains(filepath.Base(finalDir), ".tmp-") {
		t.Fatalf("published dir must not carry the temp suffix: %s", finalDir)
	}
	if _, err := os.Stat(finalDir); err != nil {
		t.Fatalf("published dir does not exist: %v", err)
	}

	// mysql.system-all.sql.gz is valid gzip with the expected content.
	f, err := os.Open(filepath.Join(finalDir, directReseedSystemArtifactName))
	if err != nil {
		t.Fatalf("open artifact file: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("artifact is not valid gzip: %v", err)
	}
	defer gzr.Close()
	content, err := io.ReadAll(gzr)
	if err != nil {
		t.Fatalf("read artifact content: %v", err)
	}
	if !strings.Contains(string(content), "INSTALL PLUGIN disk") {
		t.Fatalf("unexpected artifact content: %q", content)
	}

	// metadata is readable both by splitdump.ReadMetadata (the 4 known keys)
	// and by readDirectReseedArtifactExtra (the additive keys).
	meta, err := splitdump.ReadMetadata(finalDir)
	if err != nil {
		t.Fatalf("splitdump.ReadMetadata: %v", err)
	}
	if meta.File != "mysql-bin.000042" || meta.Position != 1234 || meta.GTID != "0-1-100" {
		t.Fatalf("unexpected standard metadata: %+v", meta)
	}
	extra, err := readDirectReseedArtifactExtra(finalDir)
	if err != nil {
		t.Fatalf("readDirectReseedArtifactExtra: %v", err)
	}
	if extra.ArtifactState != directReseedArtifactStatePublished {
		t.Fatalf("expected published state, got %q", extra.ArtifactState)
	}
	if extra.SourceServer != "src:3306" || extra.BoundaryFormat != "v1-eof-bounded" {
		t.Fatalf("unexpected extra metadata: %+v", extra)
	}

	// manifest assigns the artifact to the schema phase.
	m, err := splitdump.ReadManifest(finalDir)
	if err != nil {
		t.Fatalf("splitdump.ReadManifest: %v", err)
	}
	if len(m.Schema) != 1 || m.Schema[0] != directReseedSystemArtifactName {
		t.Fatalf("unexpected manifest schema phase: %+v", m.Schema)
	}
}

func TestDirectReseedArtifactSourceDataZeroWhenNoPositionCaptured(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job2", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	meta, err := splitdump.ReadMetadata(finalDir)
	if err != nil {
		t.Fatalf("splitdump.ReadMetadata must not fail on a no-position artifact: %v", err)
	}
	if meta.SourceData != 0 {
		t.Fatalf("expected Source_Data=0 fallback, got %+v", meta)
	}
}

func TestDirectReseedArtifactDiscardRemovesTempDirOnly(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	w, err := server.newDirectReseedSystemArtifactWriter("job3", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}
	tmpDir := w.tmpDir
	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatalf("temp dir should exist before discard: %v", err)
	}
	w.discard()
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("expected temp dir removed after discard, stat err: %v", err)
	}
	if _, err := os.Stat(w.finalDir); !os.IsNotExist(err) {
		t.Fatalf("discard must never have created the published dir")
	}
}

func TestDirectReseedArtifactPublishRenamesAtomically(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	w, err := server.newDirectReseedSystemArtifactWriter("job4", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}
	tmpDir := w.tmpDir
	finalDirWant := w.finalDir

	if _, err := io.WriteString(w, "INSTALL PLUGIN disk SONAME 'disk.so';\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	finalDir, err := w.publish(splitdump.Metadata{}, directReseedArtifactExtra{ArtifactState: directReseedArtifactStatePublished})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if finalDir != finalDirWant {
		t.Fatalf("publish returned %q, want %q", finalDir, finalDirWant)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir must not exist after publish, stat err: %v", err)
	}
}

func TestSetDirectReseedArtifactStateUpdatesInPlace(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job5", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	if err := setDirectReseedArtifactState(finalDir, directReseedArtifactStateReplayInProgress); err != nil {
		t.Fatalf("setDirectReseedArtifactState: %v", err)
	}
	extra, err := readDirectReseedArtifactExtra(finalDir)
	if err != nil {
		t.Fatalf("readDirectReseedArtifactExtra: %v", err)
	}
	if extra.ArtifactState != directReseedArtifactStateReplayInProgress {
		t.Fatalf("expected replay-in-progress, got %q", extra.ArtifactState)
	}
	// Other fields survive the in-place rewrite.
	if extra.SourceServer != "src:3306" {
		t.Fatalf("expected other metadata fields preserved, got %+v", extra)
	}

	// splitdump.ReadMetadata must still succeed after the state rewrite.
	if _, err := splitdump.ReadMetadata(finalDir); err != nil {
		t.Fatalf("splitdump.ReadMetadata after state update: %v", err)
	}
}

// TestValidateDirectReseedArtifactGzipRejectsTruncatedBody: gzip.NewReader
// alone only validates the 10-byte header, so a file truncated mid-body (a
// stall, short write, or disk error during publish) would carry a valid
// header but corrupt/incomplete compressed data -- undetectable until actual
// replay, too late to fail closed at publish or retry-validation time.
func TestValidateDirectReseedArtifactGzipRejectsTruncatedBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.sql.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write([]byte(strings.Repeat("CREATE USER 'x'@'y';\n", 1000))); err != nil {
		t.Fatalf("failed to write gzip content: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close file: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat test file: %v", err)
	}
	// Truncate well past the header (10 bytes) but before the compressed
	// body/CRC trailer are complete -- a header-only check would still accept
	// this file.
	if err := os.Truncate(path, info.Size()/2); err != nil {
		t.Fatalf("failed to truncate test file: %v", err)
	}

	if err := validateDirectReseedArtifactGzip(path); err == nil {
		t.Fatal("expected validateDirectReseedArtifactGzip to reject a truncated gzip body, got nil")
	}
}

// TestValidateDirectReseedArtifactGzipAcceptsWellFormedFile is the control
// case: a complete, well-formed artifact must still pass.
func TestValidateDirectReseedArtifactGzipAcceptsWellFormedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.sql.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write([]byte("CREATE USER 'x'@'y';\n")); err != nil {
		t.Fatalf("failed to write gzip content: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close file: %v", err)
	}

	if err := validateDirectReseedArtifactGzip(path); err != nil {
		t.Fatalf("expected a well-formed artifact to validate cleanly, got: %v", err)
	}
}

func TestRetryDirectReseedSystemCatalogRejectsNilDest(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(nil, "/nonexistent")
	if err == nil {
		t.Fatal("expected an error for a nil destination")
	}
}

func TestRetryDirectReseedSystemCatalogRejectsUnpublishedTempDir(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, filepath.Join(t.TempDir(), "20260101T000000Z_job1.tmp-abcdef"))
	if err == nil {
		t.Fatal("expected an error for a temp (unpublished) artifact directory")
	}
	if !strings.Contains(err.Error(), string(reseedStageSystemCatalogReplay)) {
		t.Fatalf("expected the %q stage in the error, got: %v", reseedStageSystemCatalogReplay, err)
	}
}

func TestRetryDirectReseedSystemCatalogRejectsDestinationMismatch(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	other := newArtifactTestServer(t, t.TempDir())
	other.Host, other.Port = "10.0.0.99", "3307"

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(other, finalDir)
	if err == nil {
		t.Fatal("expected an error for a destination-identity mismatch")
	}
}

// TestRetryDirectReseedSystemCatalogIgnoresMajorMinorDriftWhenNotStrict
// confirms a same-family major.minor bump between publish and retry doesn't
// block (or even get evaluated) when BackupRestoreVersionStrict is unset.
func TestRetryDirectReseedSystemCatalogIgnoresMajorMinorDriftWhenNotStrict(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	// Simulate the destination having been upgraded since publish, same family.
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err != nil && strings.Contains(err.Error(), "family/version") {
		t.Fatalf("a same-family major.minor bump must not block retry when BackupRestoreVersionStrict is false: %v", err)
	}
	// (The call still fails overall in this harness -- no real DB -- but not
	// for a family/version reason; that's covered by the "no real DB" tests.)
}

// TestRetryDirectReseedSystemCatalogRejectsMajorMinorDriftWhenStrict confirms
// that setting BackupRestoreVersionStrict turns a same-family major.minor
// bump into a hard block too, uniformly with the flavor-mismatch case.
func TestRetryDirectReseedSystemCatalogRejectsMajorMinorDriftWhenStrict(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	server.ClusterGroup.Conf.BackupRestoreVersionStrict = true

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err == nil {
		t.Fatal("expected an error for a major.minor drift when BackupRestoreVersionStrict is set")
	}
	if !strings.Contains(err.Error(), "family/version") && !strings.Contains(err.Error(), "backup-restore-version-strict") {
		t.Fatalf("expected a family/version compatibility error, got: %v", err)
	}
}

// TestRetryDirectReseedSystemCatalogIgnoresFlavorMismatchWhenNotStrict covers
// the other half of "family": same major.minor, different flavor entirely
// (e.g. a destination reprovisioned as MySQL instead of MariaDB).
func TestRetryDirectReseedSystemCatalogIgnoresFlavorMismatchWhenNotStrict(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 10, Minor: 11}

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err != nil && strings.Contains(err.Error(), "family/version") {
		t.Fatalf("a flavor mismatch must not block retry when BackupRestoreVersionStrict is false: %v", err)
	}
	// (The call still fails overall in this harness -- no real DB -- but not
	// for a family/version reason; that's covered by the "no real DB" tests.)
}

// TestRetryDirectReseedSystemCatalogRejectsFlavorMismatchWhenStrict confirms
// that setting BackupRestoreVersionStrict (the same flag
// CheckLogicalBackupToolVersion/CheckPhysicalBackupToolVersion already use)
// turns the flavor-mismatch warning into a hard block.
func TestRetryDirectReseedSystemCatalogRejectsFlavorMismatchWhenStrict(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 10, Minor: 11}
	server.ClusterGroup.Conf.BackupRestoreVersionStrict = true

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err == nil {
		t.Fatal("expected an error for a flavor mismatch when BackupRestoreVersionStrict is set")
	}
	if !strings.Contains(err.Error(), "family/version") && !strings.Contains(err.Error(), "backup-restore-version-strict") {
		t.Fatalf("expected a family/version compatibility error, got: %v", err)
	}
}

// TestRetryDirectReseedSystemCatalogAllowsPatchLevelDrift confirms the
// compatibility check is deliberately coarse (family + major.minor): a
// patch-level change between publish and retry must NOT block retry.
func TestRetryDirectReseedSystemCatalogAllowsPatchLevelDrift(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	// Same family/major.minor, different patch/release -- must not be treated
	// as an incompatible family/version change.
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11, Release: 9}

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err != nil && strings.Contains(err.Error(), "family/version") {
		t.Fatalf("a patch-level version difference must not be treated as a family/version incompatibility: %v", err)
	}
	// (The call still fails overall in this harness -- no real DB -- but not
	// for a family/version reason; that's covered by the "no real DB" tests.)
}

func TestRetryDirectReseedSystemCatalogRejectsNonPublishedState(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	for _, state := range []string{directReseedArtifactStateReplayInProgress, directReseedArtifactStateReplayFailed, directReseedArtifactStateReplaySucceeded} {
		if err := setDirectReseedArtifactState(finalDir, state); err != nil {
			t.Fatalf("setDirectReseedArtifactState(%s): %v", state, err)
		}
		err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
		if err == nil {
			t.Fatalf("expected retry to be refused for artifact state %q (narrow v1 scope)", state)
		}
	}
}

func TestRetryDirectReseedSystemCatalogRejectsMissingManifest(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	if err := os.Remove(filepath.Join(finalDir, "manifest")); err != nil {
		t.Fatalf("failed to remove manifest for test setup: %v", err)
	}

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err == nil {
		t.Fatal("expected an error for an artifact with a missing manifest")
	}
}

func TestRetryDirectReseedSystemCatalogAttemptsReplayAndMarksFailedWithoutRealDB(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	finalDir := writeAndPublishTestArtifact(t, server, "job1", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})

	err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir)
	if err == nil {
		t.Fatal("expected replay to fail in this harness (no real destination DB)")
	}
	if !strings.Contains(err.Error(), string(reseedStageSystemCatalogReplay)) {
		t.Fatalf("expected the %q stage in the error, got: %v", reseedStageSystemCatalogReplay, err)
	}

	// GetNewDBConn fails immediately in this harness (no real DSN) -- nothing
	// ever executed against the destination, so this is the "safe" failure
	// case: the artifact must land in replay-failed-safe, not replay-failed.
	extra, extraErr := readDirectReseedArtifactExtra(finalDir)
	if extraErr != nil {
		t.Fatalf("readDirectReseedArtifactExtra: %v", extraErr)
	}
	if extra.ArtifactState != directReseedArtifactStateReplayFailedSafe {
		t.Fatalf("expected state %q after a failure with no progress, got %q", directReseedArtifactStateReplayFailedSafe, extra.ArtifactState)
	}

	// The artifact itself must survive the failed retry -- never deleted.
	if _, statErr := os.Stat(finalDir); statErr != nil {
		t.Fatalf("expected artifact directory to survive a failed retry: %v", statErr)
	}

	// A second retry attempt must be PERMITTED: replay-failed-safe means
	// nothing committed, so a from-the-beginning retry is still within the
	// narrow v1 scope. It fails the same way (still no real DB), but it must
	// not be refused by the state gate itself.
	if err := server.ClusterGroup.RetryDirectReseedSystemCatalog(server, finalDir); err == nil {
		t.Fatal("expected the second attempt to also fail (no real DB), but it must not be refused by the retry-scope gate")
	} else if strings.Contains(err.Error(), "not safely retryable") {
		t.Fatalf("second retry was refused by the retry-scope gate, expected it to be attempted: %v", err)
	}
}

// touchArtifactModTime backdates a published artifact DIRECTORY's own mtime
// so retention-sweep tests can control recency ordering deterministically
// instead of relying on real wall-clock gaps between fast test writes.
// PurgeExpiredDirectReseedSystemArtifacts sorts by the directory entry's
// ModTime (os.ReadDir's DirEntry.Info(), read directly off the artifact
// directory itself, not any file inside it) -- os.Chtimes on a file inside
// the directory does not change the directory's own mtime, so this must
// target artifactDir itself to actually control what production reads.
func touchArtifactModTime(t *testing.T, artifactDir string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(artifactDir, when, when); err != nil {
		t.Fatalf("os.Chtimes(%s): %v", artifactDir, err)
	}
}

func TestPurgeExpiredDirectReseedSystemArtifactsKeepsMostRecentSuccesses(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	server.ClusterGroup.Servers = serverList{server}
	server.ClusterGroup.Conf.BackupKeepLast = 2

	now := time.Now()
	var dirs []string
	for i := 0; i < 4; i++ {
		dir := writeAndPublishTestArtifact(t, server, "job"+string(rune('a'+i)), "CREATE USER 'x'@'y';\n", splitdump.Metadata{})
		if err := setDirectReseedArtifactState(dir, directReseedArtifactStateReplaySucceeded); err != nil {
			t.Fatalf("setDirectReseedArtifactState: %v", err)
		}
		touchArtifactModTime(t, dir, now.Add(time.Duration(i)*time.Minute)) // job0 oldest, job3 newest
		dirs = append(dirs, dir)
	}

	server.ClusterGroup.PurgeExpiredDirectReseedSystemArtifacts()

	for i, dir := range dirs {
		_, err := os.Stat(dir)
		wantExist := i >= 2 // keep the 2 most recent (indices 2, 3)
		exists := err == nil
		if exists != wantExist {
			t.Errorf("artifact %d (%s): exists=%v, want %v", i, dir, exists, wantExist)
		}
	}
}

func TestPurgeExpiredDirectReseedSystemArtifactsNeverTouchesNonSuccessStates(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	server.ClusterGroup.Servers = serverList{server}
	server.ClusterGroup.Conf.BackupKeepLast = 0 // "0 = omitted from policy" but must never mean "purge everything"

	published := writeAndPublishTestArtifact(t, server, "job-published", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})
	failed := writeAndPublishTestArtifact(t, server, "job-failed", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})
	if err := setDirectReseedArtifactState(failed, directReseedArtifactStateReplayFailed); err != nil {
		t.Fatalf("setDirectReseedArtifactState: %v", err)
	}
	inProgress := writeAndPublishTestArtifact(t, server, "job-inprogress", "CREATE USER 'x'@'y';\n", splitdump.Metadata{})
	if err := setDirectReseedArtifactState(inProgress, directReseedArtifactStateReplayInProgress); err != nil {
		t.Fatalf("setDirectReseedArtifactState: %v", err)
	}

	server.ClusterGroup.PurgeExpiredDirectReseedSystemArtifacts()

	for _, dir := range []string{published, failed, inProgress} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("expected %s to survive the sweep (BackupKeepLast=0 disables purge entirely), got: %v", dir, err)
		}
	}
}

func TestPurgeExpiredDirectReseedSystemArtifactsIgnoresTempDirs(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	server.ClusterGroup.Servers = serverList{server}
	server.ClusterGroup.Conf.BackupKeepLast = 1

	w, err := server.newDirectReseedSystemArtifactWriter("job-unpublished", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}
	tmpDir := w.tmpDir
	defer w.discard()

	server.ClusterGroup.PurgeExpiredDirectReseedSystemArtifacts()

	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatalf("expected the unpublished temp dir to survive the sweep untouched: %v", err)
	}
}

// TestIsDirectReseedArtifactVersionCompatible is the direct, low-level test
// of the isolated policy function -- the single place meant to change once
// real --system=all cross-version compatibility data exists (see its doc
// comment). Current policy mirrors CheckLogicalBackupToolVersion/
// CheckPhysicalBackupToolVersion: family AND major.minor must both match;
// patch-level drift (not represented in the major.minor string at all) is
// the only thing tolerated.
func TestIsDirectReseedArtifactVersionCompatible(t *testing.T) {
	cases := []struct {
		name                                 string
		publishedFamily, publishedMajorMinor string
		currentFamily, currentMajorMinor     string
		want                                 bool
	}{
		{"identical family and version", "MariaDB", "10.11", "MariaDB", "10.11", true},
		{"same family, minor drift", "MariaDB", "10.6", "MariaDB", "10.11", false},
		{"same family, major drift", "MariaDB", "10.11", "MariaDB", "11.4", false},
		{"same family, patch-only drift recorded identically", "MariaDB", "10.11", "MariaDB", "10.11", true},
		{"family swap MariaDB to MySQL", "MariaDB", "10.11", "MySQL", "10.11", false},
		{"family swap MySQL to Percona", "MySQL", "8.4", "Percona", "8.4", false},
		{"empty recorded family (legacy/corrupt artifact) vs real family", "", "10.11", "MariaDB", "10.11", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isDirectReseedArtifactVersionCompatible(c.publishedFamily, c.publishedMajorMinor, c.currentFamily, c.currentMajorMinor)
			if got != c.want {
				t.Errorf("isDirectReseedArtifactVersionCompatible(%q, %q, %q, %q) = %v, want %v",
					c.publishedFamily, c.publishedMajorMinor, c.currentFamily, c.currentMajorMinor, got, c.want)
			}
		})
	}
}

// TestCheckDirectReseedSourceDestVersion is the direct, low-level test of
// the source-vs-destination preflight check (JobRejoinMysqldumpFromSource's
// gate, distinct from CheckDirectReseedArtifactVersion's dest-vs-itself
// retry check): nil inputs, matching versions, and a mismatch.
func TestCheckDirectReseedSourceDestVersion(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{}}

	if err := cluster.CheckDirectReseedSourceDestVersion(nil, &ServerMonitor{}); err == nil {
		t.Fatal("expected an error for a nil source")
	}
	if err := cluster.CheckDirectReseedSourceDestVersion(&ServerMonitor{}, nil); err == nil {
		t.Fatal("expected an error for a nil destination")
	}

	source := &ServerMonitor{URL: "source:3306", DBVersion: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}}
	dest := &ServerMonitor{URL: "dest:3306", DBVersion: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}}
	if err := cluster.CheckDirectReseedSourceDestVersion(source, dest); err != nil {
		t.Fatalf("expected no error for matching source/dest family+major.minor, got: %v", err)
	}

	dest.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}
	err := cluster.CheckDirectReseedSourceDestVersion(source, dest)
	if err == nil {
		t.Fatal("expected an error for a source/dest family mismatch")
	}
	if !strings.Contains(err.Error(), "family/version") {
		t.Fatalf("expected a family/version compatibility error, got: %v", err)
	}

	// nil DBVersion on either side must not panic.
	source.DBVersion = nil
	if err := cluster.CheckDirectReseedSourceDestVersion(source, dest); err == nil {
		t.Fatal("expected an error when source has no recorded version")
	}
}
