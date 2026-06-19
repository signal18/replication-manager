// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package backupmgr

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Issue 1: Boot-fetch callback fires in FetchRepo(), not via task tagging
// ---------------------------------------------------------------------------

// TestBootFetchCallbackFiredByFetchRepo verifies that OnBootFetchSuccess is
// called on the very first successful FetchRepo() regardless of how FetchRepo()
// was invoked (directly, via FetchTask, or via UnlockTask→FetchRepo).
func TestBootFetchCallbackFiredByFetchRepo(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, false)

	// Reset bootInitDone so this is treated as the first boot fetch.
	repo.errorMutex.Lock()
	repo.bootInitDone = false
	repo.errorMutex.Unlock()

	var fired atomic.Int32
	repo.OnBootFetchSuccess = func() { fired.Add(1) }

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("FetchRepo() error: %v", err)
	}

	// Give the goroutine a moment to execute.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fired.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatalf("OnBootFetchSuccess fired %d times, want 1", fired.Load())
	}
}

// TestBootFetchCallbackNotFiredOnSubsequentFetch verifies that the callback
// does NOT fire on any fetch after the first one.
func TestBootFetchCallbackNotFiredOnSubsequentFetch(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, false)

	var fired atomic.Int32

	// First fetch: callback should fire.
	repo.errorMutex.Lock()
	repo.bootInitDone = false
	repo.errorMutex.Unlock()
	repo.OnBootFetchSuccess = func() { fired.Add(1) }
	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("first FetchRepo(): %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Second fetch: callback must not fire again.
	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("second FetchRepo(): %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if fired.Load() != 1 {
		t.Fatalf("OnBootFetchSuccess fired %d times, want exactly 1", fired.Load())
	}
}

// TestBootFetchCallbackNotFiredOnFailedBoot verifies no callback when the boot
// fetch itself returns an error (e.g. lock error or missing repo).
func TestBootFetchCallbackNotFiredOnFailedBoot(t *testing.T) {
	repo := newPausedRepo(t)
	repo.SetEnv([]string{
		"RESTIC_PASSWORD=testpassword",
		"RESTIC_REPOSITORY=/nonexistent/path/repo",
	})

	repo.errorMutex.Lock()
	repo.bootInitDone = false
	repo.errorMutex.Unlock()

	var fired atomic.Int32
	repo.OnBootFetchSuccess = func() { fired.Add(1) }

	// FetchRepo will fail: repo does not exist.
	_ = repo.FetchRepo()
	time.Sleep(100 * time.Millisecond)

	if fired.Load() != 0 {
		t.Fatalf("OnBootFetchSuccess must not fire on failed boot fetch; fired %d times", fired.Load())
	}
}

// TestUnlockTaskTriggersBoot ensures that FetchRepo called from inside the
// worker's UnlockTask path (simulated by calling FetchRepo directly while
// bootInitDone=false) still fires the callback.
func TestUnlockTaskTriggersBoot(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, false)

	repo.errorMutex.Lock()
	repo.bootInitDone = false
	repo.errorMutex.Unlock()

	var fired atomic.Int32
	repo.OnBootFetchSuccess = func() { fired.Add(1) }

	// Simulate what UnlockTask does: it calls FetchRepo() as part of unlock.
	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("FetchRepo(): %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fired.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatalf("callback not fired after unlock-path boot fetch (fired=%d)", fired.Load())
	}
}

// ---------------------------------------------------------------------------
// Issue 2: ProbeS3Candidate — probe-based auto selection via s3existenceProber
// ---------------------------------------------------------------------------

// buildS3Repo returns a paused ResticManager configured with an S3 repo URL
// and an s3existenceProber seam.  The seam maps bucket names to probe results.
type s3BucketResult struct {
	configExists bool
	configErr    error
	dataExists   bool
	dataErr      error
}

func buildS3RepoWithSeam(t *testing.T, results map[string]s3BucketResult) *ResticManager {
	t.Helper()
	repo := newPausedRepo(t)
	repo.SetEnv([]string{
		"RESTIC_PASSWORD=testpassword",
		"RESTIC_REPOSITORY=s3:https://minio.example.com/primary-bucket/prefix",
	})
	repo.s3existenceProber = func(bucket, configKey, dataPrefix string) (bool, error, bool, error) {
		if r, ok := results[bucket]; ok {
			return r.configExists, r.configErr, r.dataExists, r.dataErr
		}
		return false, fmt.Errorf("unexpected bucket %q in probe seam", bucket), false, nil
	}
	return repo
}

// TestProbeS3Candidate_Initialized verifies that ProbeS3Candidate returns nil
// when S3 reports the config key exists (repo is initialized and accessible).
func TestProbeS3Candidate_Initialized(t *testing.T) {
	results := map[string]s3BucketResult{
		"my-bucket": {configExists: true, dataExists: true},
	}
	repo := buildS3RepoWithSeam(t, results)

	err := repo.ProbeS3Candidate("my-bucket", "prefix", "https://minio.example.com", "key", "secret", "us-east-1")
	if err != nil {
		t.Fatalf("ProbeS3Candidate() unexpected error for initialized bucket: %v", err)
	}
}

// TestProbeS3Candidate_FreshBucket verifies that ProbeS3Candidate returns an
// "initialization required" error for a reachable but uninitialised bucket.
func TestProbeS3Candidate_FreshBucket(t *testing.T) {
	results := map[string]s3BucketResult{
		"fresh-bucket": {configExists: false, dataExists: false},
	}
	repo := buildS3RepoWithSeam(t, results)

	err := repo.ProbeS3Candidate("fresh-bucket", "prefix", "https://minio.example.com", "key", "secret", "us-east-1")
	if !S3ProbeUsable(err) {
		t.Fatalf("ProbeS3Candidate() should be usable for fresh (uninitialised) bucket; got: %v", err)
	}
}

// TestProbeS3Candidate_Inaccessible verifies that ProbeS3Candidate returns a
// non-usable error when the bucket cannot be reached.
func TestProbeS3Candidate_Inaccessible(t *testing.T) {
	results := map[string]s3BucketResult{
		"bad-bucket": {configErr: fmt.Errorf("connection refused")},
	}
	repo := buildS3RepoWithSeam(t, results)

	err := repo.ProbeS3Candidate("bad-bucket", "prefix", "https://minio.example.com", "key", "secret", "us-east-1")
	if S3ProbeUsable(err) {
		t.Fatalf("ProbeS3Candidate() should be non-usable for inaccessible bucket; got nil (or usable error)")
	}
}

// TestProbeS3Candidate_RestoresState verifies that manager state is not
// permanently altered after a probe: CanInitRepo, AwsBucket, AwsRegion, and
// error-backoff fields must all be restored.
func TestProbeS3Candidate_RestoresState(t *testing.T) {
	results := map[string]s3BucketResult{
		"probe-bucket": {configErr: fmt.Errorf("network error")}, // triggers error state inside probe
	}
	repo := buildS3RepoWithSeam(t, results)

	// Set known pre-probe state.
	repo.CanInitRepo = true
	repo.AwsBucket = "original-bucket"
	repo.AwsRegion = "eu-west-1"
	repo.errorMutex.Lock()
	repo.initErrorCount = 7
	repo.errorMutex.Unlock()

	_ = repo.ProbeS3Candidate("probe-bucket", "prefix", "https://minio.example.com", "k", "s", "us-east-1")

	if !repo.CanInitRepo {
		t.Errorf("CanInitRepo was not restored after probe")
	}
	if repo.AwsBucket != "original-bucket" {
		t.Errorf("AwsBucket not restored: got %q", repo.AwsBucket)
	}
	if repo.AwsRegion != "eu-west-1" {
		t.Errorf("AwsRegion not restored: got %q", repo.AwsRegion)
	}
	repo.errorMutex.Lock()
	count := repo.initErrorCount
	repo.errorMutex.Unlock()
	if count != 7 {
		t.Errorf("initErrorCount not restored: got %d", count)
	}
}

// TestS3ProbeUsable covers the helper function directly.
func TestS3ProbeUsable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, true},
		{fmt.Errorf("repository needs initialization required"), true},
		{fmt.Errorf("initialization required before backup"), true},
		{fmt.Errorf("connection refused"), false},
		{fmt.Errorf("access denied"), false},
	}
	for _, tc := range cases {
		got := S3ProbeUsable(tc.err)
		if got != tc.want {
			t.Errorf("S3ProbeUsable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Auto mode: probe order — new wins, legacy fallback, both fail
// ---------------------------------------------------------------------------

// These tests simulate the cluster-layer auto-probe logic at the backupmgr
// level by calling ProbeS3Candidate for two candidate configs in order and
// checking which one is selected.

// autoProbeWinner is a local helper that mirrors what resolveResticS3AutoProbe
// does at the cluster layer: try new bucket first, fallback to legacy.
func autoProbeWinner(repo *ResticManager, newBucket, legacyBucket string) (string, error) {
	err := repo.ProbeS3Candidate(newBucket, "prefix", "endpoint", "key", "secret", "region")
	if S3ProbeUsable(err) {
		return newBucket, nil
	}
	err2 := repo.ProbeS3Candidate(legacyBucket, "prefix", "endpoint", "key", "secret", "region")
	if S3ProbeUsable(err2) {
		return legacyBucket, nil
	}
	return "", fmt.Errorf("both new (%v) and legacy (%v) probes failed", err, err2)
}

// TestAutoProbe_NewWins: new config is reachable, legacy is not → new wins.
func TestAutoProbe_NewWins(t *testing.T) {
	results := map[string]s3BucketResult{
		"new-bucket":    {configExists: true},
		"legacy-bucket": {configErr: fmt.Errorf("connection refused")},
	}
	repo := buildS3RepoWithSeam(t, results)

	winner, err := autoProbeWinner(repo, "new-bucket", "legacy-bucket")
	if err != nil {
		t.Fatalf("autoProbeWinner error: %v", err)
	}
	if winner != "new-bucket" {
		t.Errorf("expected new-bucket to win, got %q", winner)
	}
}

// TestAutoProbe_LegacyFallback: new config fails, legacy is reachable → legacy wins.
func TestAutoProbe_LegacyFallback(t *testing.T) {
	results := map[string]s3BucketResult{
		"new-bucket":    {configErr: fmt.Errorf("endpoint not reachable")},
		"legacy-bucket": {configExists: true},
	}
	repo := buildS3RepoWithSeam(t, results)

	winner, err := autoProbeWinner(repo, "new-bucket", "legacy-bucket")
	if err != nil {
		t.Fatalf("autoProbeWinner error: %v", err)
	}
	if winner != "legacy-bucket" {
		t.Errorf("expected legacy-bucket fallback, got %q", winner)
	}
}

// TestAutoProbe_BothFail: both configs are unreachable → error.
func TestAutoProbe_BothFail(t *testing.T) {
	results := map[string]s3BucketResult{
		"new-bucket":    {configErr: fmt.Errorf("network error")},
		"legacy-bucket": {configErr: fmt.Errorf("network error")},
	}
	repo := buildS3RepoWithSeam(t, results)

	_, err := autoProbeWinner(repo, "new-bucket", "legacy-bucket")
	if err == nil {
		t.Fatal("expected error when both probes fail, got nil")
	}
}

// TestAutoProbe_NewFreshBucketWins: new bucket is fresh (init required) — still usable.
func TestAutoProbe_NewFreshBucketWins(t *testing.T) {
	results := map[string]s3BucketResult{
		"new-bucket":    {configExists: false, dataExists: false}, // fresh → init required
		"legacy-bucket": {configErr: fmt.Errorf("not reachable")},
	}
	repo := buildS3RepoWithSeam(t, results)

	winner, err := autoProbeWinner(repo, "new-bucket", "legacy-bucket")
	if err != nil {
		t.Fatalf("autoProbeWinner error: %v", err)
	}
	if winner != "new-bucket" {
		t.Errorf("expected fresh new-bucket to win, got %q", winner)
	}
}

// TestAutoProbe_StateRestoredBetweenProbes: manager state must be clean after
// the first (failing) probe so the second probe sees a neutral starting state.
func TestAutoProbe_StateRestoredBetweenProbes(t *testing.T) {
	results := map[string]s3BucketResult{
		"new-bucket":    {configErr: fmt.Errorf("unreachable")},
		"legacy-bucket": {configExists: true},
	}
	repo := buildS3RepoWithSeam(t, results)
	repo.CanInitRepo = true

	winner, err := autoProbeWinner(repo, "new-bucket", "legacy-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if winner != "legacy-bucket" {
		t.Errorf("expected legacy-bucket, got %q", winner)
	}
	// CanInitRepo must still be true — not clobbered by the failing first probe.
	if !repo.CanInitRepo {
		t.Error("CanInitRepo was corrupted by the failing first probe")
	}
}
