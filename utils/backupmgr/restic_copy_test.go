package backupmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// --- validateCopySourceMode ---

func TestValidateCopySourceMode(t *testing.T) {
	valid := []string{
		config.ConstBackupArchiveModeResticLocal,
		config.ConstBackupArchiveModeResticAws,
		config.ConstBackupArchiveModeResticSftp,
	}
	for _, m := range valid {
		if err := validateCopySourceMode(m); err != nil {
			t.Errorf("expected mode %q to be valid, got: %v", m, err)
		}
	}

	if err := validateCopySourceMode(""); err == nil {
		t.Error("expected error for empty mode, got nil")
	}
	if err := validateCopySourceMode("restic-unknown"); err == nil {
		t.Error("expected error for unknown mode, got nil")
	}
}

// --- buildCopySourceRepoString ---

func TestBuildCopySourceRepoStringLocal(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticLocal, Repository: "/srv/backup"}
	got, err := buildCopySourceRepoString(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/srv/backup" {
		t.Errorf("expected /srv/backup, got %q", got)
	}
}

func TestBuildCopySourceRepoStringLocalEmpty(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticLocal, Repository: ""}
	_, err := buildCopySourceRepoString(src)
	if err == nil {
		t.Error("expected error for empty repository, got nil")
	}
}

func TestBuildCopySourceRepoStringSFTP(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticSftp, Repository: "sftp:backup@10.0.0.1:/srv/repo"}
	got, err := buildCopySourceRepoString(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sftp:backup@10.0.0.1:/srv/repo" {
		t.Errorf("unexpected value: %q", got)
	}
}

func TestBuildCopySourceRepoStringSFTPNoUser(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticSftp, Repository: "sftp:10.0.0.1:/srv/repo"}
	got, err := buildCopySourceRepoString(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sftp:10.0.0.1:/srv/repo" {
		t.Errorf("unexpected value: %q", got)
	}
}

func TestBuildCopySourceRepoStringSFTPMalformed(t *testing.T) {
	cases := []string{
		"backup@10.0.0.1:/srv/repo", // missing sftp: prefix
		"sftp:/srv/repo",            // missing host
		"sftp:10.0.0.1",             // missing path
		"sftp:",                     // empty
	}
	for _, repo := range cases {
		src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticSftp, Repository: repo}
		_, err := buildCopySourceRepoString(src)
		if err == nil {
			t.Errorf("expected error for malformed SFTP repo %q, got nil", repo)
		}
	}
}

func TestBuildCopySourceRepoStringS3WithEndpoint(t *testing.T) {
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS: &ResticCopySourceAWSOption{
			Endpoint: "https://minio.example.com",
			Bucket:   "backups",
			Prefix:   "cluster-a",
		},
	}
	got, err := buildCopySourceRepoString(src)
	if err != nil {
		t.Fatal(err)
	}
	want := "s3:https://minio.example.com/backups/cluster-a"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestBuildCopySourceRepoStringS3NoEndpoint(t *testing.T) {
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS: &ResticCopySourceAWSOption{
			Bucket: "my-bucket",
			Prefix: "prefix",
		},
	}
	got, err := buildCopySourceRepoString(src)
	if err != nil {
		t.Fatal(err)
	}
	want := "s3:my-bucket/prefix"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestBuildCopySourceRepoStringS3NilAWS(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticAws}
	_, err := buildCopySourceRepoString(src)
	if err == nil {
		t.Error("expected error for nil AWS config, got nil")
	}
}

func TestBuildCopySourceRepoStringS3EmptyBucket(t *testing.T) {
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS:  &ResticCopySourceAWSOption{Bucket: ""},
	}
	_, err := buildCopySourceRepoString(src)
	if err == nil {
		t.Error("expected error for empty bucket, got nil")
	}
}

// --- buildCopySourceEnvOverlay ---

func TestBuildCopySourceEnvOverlayLocal(t *testing.T) {
	src := ResticCopySourceOption{
		Mode:       config.ConstBackupArchiveModeResticLocal,
		Repository: "/srv/backup",
		Password:   "secret",
	}
	fromRepo := "/srv/backup"
	env, err := buildCopySourceEnvOverlay(src, fromRepo)
	if err != nil {
		t.Fatal(err)
	}
	checkEnvContains(t, env, "RESTIC_FROM_REPOSITORY=/srv/backup")
	checkEnvContains(t, env, "RESTIC_FROM_PASSWORD=secret")
	checkEnvAbsent(t, env, "RESTIC_FROM_KEY_HINT")
	checkEnvAbsent(t, env, "AWS_ACCESS_KEY_ID")
}

func TestBuildCopySourceEnvOverlayWithKeyHint(t *testing.T) {
	src := ResticCopySourceOption{
		Mode:       config.ConstBackupArchiveModeResticLocal,
		Repository: "/srv/backup",
		Password:   "secret",
		KeyHint:    "mykeyid",
	}
	env, err := buildCopySourceEnvOverlay(src, "/srv/backup")
	if err != nil {
		t.Fatal(err)
	}
	checkEnvContains(t, env, "RESTIC_FROM_KEY_HINT=mykeyid")
}

func TestBuildCopySourceEnvOverlayS3(t *testing.T) {
	src := ResticCopySourceOption{
		Mode:     config.ConstBackupArchiveModeResticAws,
		Password: "s3pass",
		AWS: &ResticCopySourceAWSOption{
			AccessKeyID:  "AKID",
			AccessSecret: "SECRET",
			Region:       "us-east-1",
		},
	}
	env, err := buildCopySourceEnvOverlay(src, "s3:bucket/prefix")
	if err != nil {
		t.Fatal(err)
	}
	checkEnvContains(t, env, "RESTIC_FROM_REPOSITORY=s3:bucket/prefix")
	checkEnvContains(t, env, "RESTIC_FROM_PASSWORD=s3pass")
	checkEnvContains(t, env, "AWS_ACCESS_KEY_ID=AKID")
	checkEnvContains(t, env, "AWS_SECRET_ACCESS_KEY=SECRET")
	checkEnvContains(t, env, "AWS_DEFAULT_REGION=us-east-1")
}

func TestBuildCopySourceEnvOverlayMissingPassword(t *testing.T) {
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticLocal, Repository: "/tmp/r"}
	_, err := buildCopySourceEnvOverlay(src, "/tmp/r")
	if err == nil {
		t.Error("expected error for missing password, got nil")
	}
}

// --- mergeEnvByKey ---

func TestMergeEnvByKeyDeduplicate(t *testing.T) {
	base := []string{"FOO=base", "BAR=1"}
	override := []string{"FOO=override", "BAZ=new"}
	result := mergeEnvByKey(base, override)

	m := envSliceToMap(result)
	if m["FOO"] != "override" {
		t.Errorf("FOO should be 'override', got %q", m["FOO"])
	}
	if m["BAR"] != "1" {
		t.Errorf("BAR should be '1', got %q", m["BAR"])
	}
	if m["BAZ"] != "new" {
		t.Errorf("BAZ should be 'new', got %q", m["BAZ"])
	}

	// Each key should appear exactly once.
	counts := make(map[string]int)
	for _, kv := range result {
		counts[envKey(kv)]++
	}
	for k, count := range counts {
		if count != 1 {
			t.Errorf("key %q appears %d times, expected 1", k, count)
		}
	}
}

func TestMergeEnvByKeyOrderPreserved(t *testing.T) {
	base := []string{"A=1", "B=2", "C=3"}
	override := []string{"B=override"}
	result := mergeEnvByKey(base, override)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	// B should be updated in-place (position 1).
	if result[1] != "B=override" {
		t.Errorf("expected B at index 1 = 'B=override', got %q", result[1])
	}
}

// --- validateS3SourceVsDest (G1 guardrail) ---

func TestValidateS3SourceVsDestLocalSource(t *testing.T) {
	repo := newRepoWithAwsConfig("AKID", "SECRET", "us-east-1", "", "bucket", "prefix")
	src := ResticCopySourceOption{Mode: config.ConstBackupArchiveModeResticLocal}
	if err := repo.validateS3SourceVsDest(src); err != nil {
		t.Errorf("local source against S3 dest should be accepted, got: %v", err)
	}
}

func TestValidateS3SourceVsDestS3NonS3Dest(t *testing.T) {
	// Source is S3, destination is local (no AwsBucket, non-S3 repo path).
	repo := newRepoWithAwsConfig("", "", "", "", "", "")
	repo.Env = []string{"RESTIC_REPOSITORY=/local/path"}
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS:  &ResticCopySourceAWSOption{AccessKeyID: "SRC_AKID", AccessSecret: "SRC_SECRET"},
	}
	if err := repo.validateS3SourceVsDest(src); err != nil {
		t.Errorf("S3 source to local dest should be accepted, got: %v", err)
	}
}

func TestValidateS3SourceVsDestS3MatchingCreds(t *testing.T) {
	repo := newRepoWithAwsConfig("AKID", "SECRET", "us-east-1", "https://minio.example.com", "bucket", "prefix")
	repo.Env = []string{"RESTIC_REPOSITORY=s3:https://minio.example.com/bucket/prefix"}
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS: &ResticCopySourceAWSOption{
			AccessKeyID:  "AKID",
			AccessSecret: "SECRET",
			Region:       "us-east-1",
			Endpoint:     "https://minio.example.com",
			Bucket:       "src-bucket",
		},
	}
	if err := repo.validateS3SourceVsDest(src); err != nil {
		t.Errorf("matching S3 creds should be accepted, got: %v", err)
	}
}

func TestValidateS3SourceVsDestS3MismatchAccessKey(t *testing.T) {
	repo := newRepoWithAwsConfig("DEST_AKID", "SECRET", "us-east-1", "", "bucket", "")
	repo.Env = []string{"RESTIC_REPOSITORY=s3:bucket"}
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS:  &ResticCopySourceAWSOption{AccessKeyID: "SRC_AKID", AccessSecret: "SECRET", Region: "us-east-1"},
	}
	if err := repo.validateS3SourceVsDest(src); err == nil {
		t.Error("expected error for mismatched access key, got nil")
	}
}

func TestValidateS3SourceVsDestS3MismatchRegion(t *testing.T) {
	repo := newRepoWithAwsConfig("AKID", "SECRET", "us-east-1", "", "bucket", "")
	repo.Env = []string{"RESTIC_REPOSITORY=s3:bucket"}
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS:  &ResticCopySourceAWSOption{AccessKeyID: "AKID", AccessSecret: "SECRET", Region: "eu-west-1"},
	}
	if err := repo.validateS3SourceVsDest(src); err == nil {
		t.Error("expected error for mismatched region, got nil")
	}
}

func TestValidateS3SourceVsDestS3MismatchEndpoint(t *testing.T) {
	repo := newRepoWithAwsConfig("AKID", "SECRET", "us-east-1", "https://minio1.example.com", "bucket", "")
	repo.Env = []string{"RESTIC_REPOSITORY=s3:https://minio1.example.com/bucket"}
	src := ResticCopySourceOption{
		Mode: config.ConstBackupArchiveModeResticAws,
		AWS:  &ResticCopySourceAWSOption{AccessKeyID: "AKID", AccessSecret: "SECRET", Region: "us-east-1", Endpoint: "https://minio2.example.com"},
	}
	if err := repo.validateS3SourceVsDest(src); err == nil {
		t.Error("expected error for mismatched endpoint, got nil")
	}
}

// --- ValidateCopyOption ---

func TestValidateCopyOptionValid(t *testing.T) {
	repo := newRepoWithAwsConfig("", "", "", "", "", "")
	repo.Env = []string{"RESTIC_REPOSITORY=/local/dest"}
	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/srv/src",
			Password:   "pass",
		},
	}
	if err := repo.ValidateCopyOption(opt); err != nil {
		t.Errorf("expected valid option to pass, got: %v", err)
	}
}

func TestValidateCopyOptionChunkerWithoutInit(t *testing.T) {
	repo := newRepoWithAwsConfig("", "", "", "", "", "")
	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/srv/src",
			Password:   "pass",
		},
		CopyChunkerParams: true,
		InitDestination:   false,
	}
	if err := repo.ValidateCopyOption(opt); err == nil {
		t.Error("expected error for chunker_params without init_destination, got nil")
	}
}

func TestValidateCopyOptionMissingPassword(t *testing.T) {
	repo := newRepoWithAwsConfig("", "", "", "", "", "")
	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/srv/src",
		},
	}
	if err := repo.ValidateCopyOption(opt); err == nil {
		t.Error("expected error for missing password, got nil")
	}
}

// --- AddCopyTask queue behavior ---

func TestAddCopyTaskQueued(t *testing.T) {
	repo := newPausedRepo(t)
	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/srv/src",
			Password:   "pass",
		},
	}
	repo.AddCopyTask(opt)

	repo.Mutex.Lock()
	qlen := len(repo.TaskQueue)
	var taskType TaskType
	if qlen > 0 {
		taskType = repo.TaskQueue[0].Type
	}
	repo.Mutex.Unlock()

	if qlen != 1 {
		t.Fatalf("expected 1 task in queue, got %d", qlen)
	}
	if taskType != CopyTask {
		t.Errorf("expected CopyTask (%d), got %d", CopyTask, taskType)
	}
}

// --- Copy task JSON secrecy ---

func TestCopyTaskNotExposedInJSON(t *testing.T) {
	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/srv/src",
			Password:   "topsecret",
			KeyHint:    "hint",
			AWS: &ResticCopySourceAWSOption{
				AccessSecret: "aws-secret",
			},
		},
	}
	task := ResticTask{
		ID:      1,
		Type:    CopyTask,
		CopyOpt: &opt,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}

	s := string(data)
	if strings.Contains(s, "topsecret") {
		t.Error("JSON must not contain source password")
	}
	if strings.Contains(s, "aws-secret") {
		t.Error("JSON must not contain source AWS secret")
	}
	if strings.Contains(s, "copy_opt") || strings.Contains(s, "CopyOpt") {
		t.Error("JSON must not contain copy_opt field")
	}
	// Task ID and type should still be visible.
	if !strings.Contains(s, `"task_id":1`) {
		t.Errorf("expected task_id in JSON, got: %s", s)
	}
}

// --- GetTaskName ---

func TestGetTaskNameCopy(t *testing.T) {
	if name := GetTaskName(CopyTask); name != "copy" {
		t.Errorf("expected 'copy', got %q", name)
	}
}

// --- Integration: copy between two local repos (requires restic binary) ---

// TestCopyRepoLocalToLocal tests copy with init_destination=true against an
// uninitialized destination (plain init, no chunker params — works on all restic
// versions). Re-running copy is idempotent (restic skips already-present snapshots).
func TestCopyRepoLocalToLocal(t *testing.T) {
	resticPath, err := findResticBinary()
	if err != nil {
		t.Skipf("restic binary not found, skipping integration test: %v", err)
	}

	base := t.TempDir()
	srcDir := filepath.Join(base, "src-repo")
	dstDir := filepath.Join(base, "dst-repo")
	dataDir := filepath.Join(base, "data")
	cacheDir := filepath.Join(base, "cache")
	for _, d := range []string{srcDir, dstDir, dataDir, cacheDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	dummyFile := filepath.Join(dataDir, "hello.txt")
	if err := os.WriteFile(dummyFile, []byte("hello restic copy"), 0644); err != nil {
		t.Fatal(err)
	}

	srcPass := "srcpass"
	dstPass := "dstpass"

	// Init and populate source repo.
	srcRepo := newLocalResticRepo(t, resticPath, srcDir, srcPass, cacheDir)
	defer srcRepo.ShutdownWorker()
	if err := srcRepo.InitRepo(false); err != nil {
		t.Fatalf("init src repo: %v", err)
	}
	if _, err := srcRepo.BackupWithOptions(ResticBackupOption{DirPath: dataDir}); err != nil {
		t.Fatalf("backup to src repo: %v", err)
	}
	if err := srcRepo.FetchRepo(); err != nil {
		t.Fatalf("fetch src repo: %v", err)
	}
	if len(srcRepo.Backups) == 0 {
		t.Fatal("expected at least one snapshot in source repo")
	}

	// Destination is uninitialized; init_destination=true triggers plain init
	// (no --from-repo since CopyChunkerParams=false) — compatible with all restic versions.
	dstRepo := newLocalResticRepo(t, resticPath, dstDir, dstPass, cacheDir)
	defer dstRepo.ShutdownWorker()

	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: srcDir,
			Password:   srcPass,
		},
		InitDestination: true,
	}

	if err := dstRepo.CopyRepoWithOptions(opt); err != nil {
		t.Fatalf("CopyRepoWithOptions: %v", err)
	}
	if len(dstRepo.Backups) == 0 {
		t.Fatal("expected snapshots in destination repo after copy")
	}

	// Re-running copy should be idempotent.
	snapshotsBefore := len(dstRepo.Backups)
	if err := dstRepo.CopyRepoWithOptions(opt); err != nil {
		t.Fatalf("second CopyRepoWithOptions: %v", err)
	}
	if len(dstRepo.Backups) != snapshotsBefore {
		t.Errorf("expected %d snapshots after re-copy, got %d", snapshotsBefore, len(dstRepo.Backups))
	}
}

// TestCopyRepoInitDestinationWithChunkerParams tests init_destination=true with
// copy_chunker_params=true. This uses restic init --from-repo --copy-chunker-params
// which requires restic >= 0.14 and is skipped on older versions.
func TestCopyRepoInitDestinationWithChunkerParams(t *testing.T) {
	resticPath, err := findResticBinary()
	if err != nil {
		t.Skipf("restic binary not found: %v", err)
	}
	if !resticSupportsFromRepo(resticPath) {
		t.Skip("restic init --from-repo requires restic >= 0.14, skipping")
	}

	base := t.TempDir()
	srcDir := filepath.Join(base, "src")
	dstDir := filepath.Join(base, "dst")
	dataDir := filepath.Join(base, "data")
	cacheDir := filepath.Join(base, "cache")
	for _, d := range []string{srcDir, dstDir, dataDir, cacheDir} {
		os.MkdirAll(d, 0755)
	}
	os.WriteFile(filepath.Join(dataDir, "f.txt"), []byte("data"), 0644)

	srcRepo := newLocalResticRepo(t, resticPath, srcDir, "srcpass", cacheDir)
	defer srcRepo.ShutdownWorker()
	if err := srcRepo.InitRepo(false); err != nil {
		t.Fatalf("init src: %v", err)
	}
	if _, err := srcRepo.BackupWithOptions(ResticBackupOption{DirPath: dataDir}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := srcRepo.FetchRepo(); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	dstRepo := newLocalResticRepo(t, resticPath, dstDir, "dstpass", cacheDir)
	defer dstRepo.ShutdownWorker()

	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: srcDir,
			Password:   "srcpass",
		},
		InitDestination:   true,
		CopyChunkerParams: true,
	}
	if err := dstRepo.CopyRepoWithOptions(opt); err != nil {
		t.Fatalf("CopyRepoWithOptions with chunker params: %v", err)
	}
	if len(dstRepo.Backups) == 0 {
		t.Fatal("expected snapshots after copy with chunker-params init")
	}
}

// TestCopyRepoAlreadyInitializedWithChunkerParamsFails verifies G4: requesting
// copy_chunker_params=true against an already-initialized destination is rejected.
func TestCopyRepoAlreadyInitializedWithChunkerParamsFails(t *testing.T) {
	resticPath, err := findResticBinary()
	if err != nil {
		t.Skipf("restic binary not found: %v", err)
	}

	base := t.TempDir()
	srcDir := filepath.Join(base, "src")
	dstDir := filepath.Join(base, "dst")
	cacheDir := filepath.Join(base, "cache")
	for _, d := range []string{srcDir, dstDir, cacheDir} {
		os.MkdirAll(d, 0755)
	}

	srcRepo := newLocalResticRepo(t, resticPath, srcDir, "srcpass", cacheDir)
	defer srcRepo.ShutdownWorker()
	dstRepo := newLocalResticRepo(t, resticPath, dstDir, "dstpass", cacheDir)
	defer dstRepo.ShutdownWorker()

	if err := srcRepo.InitRepo(false); err != nil {
		t.Fatalf("init src: %v", err)
	}
	if err := dstRepo.InitRepo(false); err != nil {
		t.Fatalf("init dst: %v", err)
	}
	// Wait for worker to settle after init FetchTask.
	dstRepo.FetchRepo()

	opt := ResticCopyOption{
		Source: ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: srcDir,
			Password:   "srcpass",
		},
		InitDestination:   true,
		CopyChunkerParams: true,
	}

	copyErr := dstRepo.CopyRepoWithOptions(opt)
	if copyErr == nil {
		t.Error("expected error when applying copy_chunker_params to already-initialized repo, got nil")
	}
	if !strings.Contains(copyErr.Error(), "already initialized") {
		t.Errorf("expected 'already initialized' in error, got: %v", copyErr)
	}
}

// resticSupportsFromRepo probes whether the installed restic binary supports
// the --from-repo flag on init (requires restic >= 0.14).
func resticSupportsFromRepo(binaryPath string) bool {
	out, err := exec.Command(binaryPath, "init", "--help").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "--from-repo")
}

// --- helpers ---

func newRepoWithAwsConfig(accessKeyID, secretAccessKey, region, endpoint, bucket, prefix string) *ResticManager {
	mu := &sync.Mutex{}
	repo := &ResticManager{
		BinaryPath:         "restic",
		Env:                []string{},
		envMutex:           &sync.RWMutex{},
		Mutex:              mu,
		errorMutex:         &sync.Mutex{},
		currentTaskMutex:   &sync.Mutex{},
		mountRefMutex:      &sync.Mutex{},
		mountUsers:         make(map[string]struct{}),
		TaskErrors:         make(map[TaskType]error),
		TaskQueue:          make([]*ResticTask, 0),
		AwsAccessKeyID:     accessKeyID,
		AwsSecretAccessKey: secretAccessKey,
		AwsRegion:          region,
		AwsEndpoint:        endpoint,
		AwsBucket:          bucket,
		AwsPrefix:          prefix,
	}
	repo.cond = sync.NewCond(mu)
	return repo
}

func newLocalResticRepo(t *testing.T, binaryPath, repoPath, password, cacheDir string) *ResticManager {
	t.Helper()
	repo := NewResticRepo(binaryPath, testMsgChan, 0)
	repo.SetEnv([]string{
		"RESTIC_REPOSITORY=" + repoPath,
		"RESTIC_PASSWORD=" + password,
		"RESTIC_CACHE_DIR=" + cacheDir,
	})
	return repo
}

func checkEnvContains(t *testing.T, env []string, want string) {
	t.Helper()
	for _, e := range env {
		if e == want {
			return
		}
	}
	t.Errorf("env does not contain %q; got: %v", want, env)
}

func checkEnvAbsent(t *testing.T, env []string, prefix string) {
	t.Helper()
	for _, e := range env {
		if strings.HasPrefix(e, prefix+"=") {
			t.Errorf("env should not contain key %q, but found: %q", prefix, e)
		}
	}
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			m[kv] = ""
		} else {
			m[kv[:idx]] = kv[idx+1:]
		}
	}
	return m
}

func findResticBinary() (string, error) {
	for _, candidate := range []string{"restic", "/usr/local/bin/restic", "/usr/bin/restic"} {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("restic binary not found")
}
