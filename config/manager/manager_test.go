package manager

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	git_config "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

type testClusterConfig struct {
	name   string
	saveFn func() error
}

func (tc *testClusterConfig) GetName() string {
	return tc.name
}

func (tc *testClusterConfig) Save() error {
	if tc.saveFn != nil {
		return tc.saveFn()
	}
	return nil
}

func testWorkDir(t *testing.T) string {
	t.Helper()

	if wd := os.Getenv("REPMAN_GIT_TEST_WORKDIR"); wd != "" {
		if err := os.RemoveAll(wd); err != nil {
			t.Fatalf("failed resetting static test workdir %s: %v", wd, err)
		}
		if err := os.MkdirAll(wd, 0o755); err != nil {
			t.Fatalf("failed creating static test workdir %s: %v", wd, err)
		}
		t.Logf("using static test workdir: %s", wd)
		return wd
	}

	return t.TempDir()
}

func snapshotLocalFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()

	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if d.IsDir() {
			if rel == ".git" || rel == ".tmp" {
				return filepath.SkipDir
			}
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = raw
		return nil
	})
	if err != nil {
		t.Fatalf("failed to snapshot local files: %v", err)
	}

	return files
}

func testGitCredentialsFromEnv(t *testing.T) (string, string, string) {
	t.Helper()

	gitURL := os.Getenv("REPMAN_GIT_TEST_URL")
	gitToken := os.Getenv("REPMAN_GIT_TEST_TOKEN")
	gitUser := os.Getenv("REPMAN_GIT_TEST_USERNAME")

	if gitURL == "" || gitToken == "" || gitUser == "" {
		t.Skip("set REPMAN_GIT_TEST_URL, REPMAN_GIT_TEST_TOKEN, and REPMAN_GIT_TEST_USERNAME to run this integration test")
	}

	return gitURL, gitToken, gitUser
}

func TestPushConfigToGit_WithEnvCredentials(t *testing.T) {
	gitURL, gitToken, gitUser := testGitCredentialsFromEnv(t)

	workDir := testWorkDir(t)
	clusterName := "cluster1"

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      gitURL,
		GitUsername: gitUser,
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: gitToken},
		},
	}

	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	// First call: clone/open repository into empty temp working dir.
	if err := cm.PushConfigToGit(conf, nil); err != nil {
		t.Fatalf("initial PushConfigToGit (clone/open) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		t.Fatalf("expected .git directory after initial push call: %v", err)
	}

	// Second phase: create files so we produce an actual commit and push.
	if err := os.MkdirAll(filepath.Join(workDir, clusterName), 0o755); err != nil {
		t.Fatalf("failed creating cluster directory: %v", err)
	}

	defaultToml := []byte("# repman integration test\nupdated-at = \"" + time.Now().UTC().Format(time.RFC3339Nano) + "\"\n")
	if err := os.WriteFile(filepath.Join(workDir, "default.toml"), defaultToml, 0o644); err != nil {
		t.Fatalf("failed writing default.toml: %v", err)
	}

	clusterState := []byte("{\"servers\":\"\",\"crashes\":[],\"sla\":{},\"slaHistory\":[],\"provisioned\":false,\"repmgrVersion\":\"test\"}\n")
	if err := os.WriteFile(filepath.Join(workDir, clusterName, "clusterstate.json"), clusterState, 0o644); err != nil {
		t.Fatalf("failed writing clusterstate.json: %v", err)
	}

	if err := cm.PushConfigToGit(conf, []string{clusterName}); err != nil {
		t.Fatalf("second PushConfigToGit (commit+push) failed: %v", err)
	}
}

func TestPushConfigToGit_ShallowClonePreservesLocalFilesThenPushMaster(t *testing.T) {
	gitURL, gitToken, gitUser := testGitCredentialsFromEnv(t)

	workDir := testWorkDir(t)
	clusterName := "cluster1"

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      gitURL,
		GitUsername: gitUser,
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: gitToken},
		},
	}

	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	preservedFile := filepath.Join(workDir, "local-preserved.txt")
	preservedContent := "preserve-me"
	if err := os.WriteFile(preservedFile, []byte(preservedContent), 0o644); err != nil {
		t.Fatalf("failed writing local preserved file: %v", err)
	}

	preservedNestedDir := filepath.Join(workDir, "local-dir")
	if err := os.MkdirAll(preservedNestedDir, 0o755); err != nil {
		t.Fatalf("failed creating nested preserved dir: %v", err)
	}
	preservedNestedFile := filepath.Join(preservedNestedDir, "keep.txt")
	preservedNestedContent := "keep-nested"
	if err := os.WriteFile(preservedNestedFile, []byte(preservedNestedContent), 0o644); err != nil {
		t.Fatalf("failed writing nested preserved file: %v", err)
	}

	t.Logf("snapshotting pre-clone local files")
	beforeClone := snapshotLocalFiles(t, workDir)

	// 1) Shallow clone into non-empty directory.
	t.Logf("step 1: shallow clone (non-empty workdir)")
	if err := cm.PushConfigToGit(conf, nil); err != nil {
		t.Fatalf("initial PushConfigToGit (shallow clone) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workDir, ".git", "shallow")); err != nil {
		t.Fatalf("expected shallow clone metadata in .git/shallow: %v", err)
	}

	// 2) Pre-existing local files are preserved.
	t.Logf("step 2: verify pre-existing files remain intact")
	afterClone := snapshotLocalFiles(t, workDir)
	if len(beforeClone) != len(afterClone) {
		t.Fatalf("file count changed after clone, before=%d after=%d", len(beforeClone), len(afterClone))
	}
	for name, beforeRaw := range beforeClone {
		afterRaw, ok := afterClone[name]
		if !ok {
			t.Fatalf("file missing after clone: %s", name)
		}
		if !bytes.Equal(beforeRaw, afterRaw) {
			t.Fatalf("file content changed after clone: %s", name)
		}
	}

	raw, err := os.ReadFile(preservedFile)
	if err != nil {
		t.Fatalf("failed reading preserved file after clone: %v", err)
	}
	if string(raw) != preservedContent {
		t.Fatalf("preserved file content changed after clone, got=%q want=%q", string(raw), preservedContent)
	}
	rawNested, err := os.ReadFile(preservedNestedFile)
	if err != nil {
		t.Fatalf("failed reading nested preserved file after clone: %v", err)
	}
	if string(rawNested) != preservedNestedContent {
		t.Fatalf("nested preserved file content changed after clone, got=%q want=%q", string(rawNested), preservedNestedContent)
	}

	// 3) Commit local files.
	t.Logf("step 3: create tracked files and commit+push")
	if err := os.MkdirAll(filepath.Join(workDir, clusterName), 0o755); err != nil {
		t.Fatalf("failed creating cluster directory: %v", err)
	}

	marker := "integration-marker=" + time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(workDir, "default.toml"), []byte("# repman integration test\n"+marker+"\n"), 0o644); err != nil {
		t.Fatalf("failed writing default.toml: %v", err)
	}

	clusterState := "{\"integrationMarker\":\"" + marker + "\"}\n"
	if err := os.WriteFile(filepath.Join(workDir, clusterName, "clusterstate.json"), []byte(clusterState), 0o644); err != nil {
		t.Fatalf("failed writing clusterstate.json: %v", err)
	}

	if err := cm.PushConfigToGit(conf, []string{clusterName}); err != nil {
		t.Fatalf("second PushConfigToGit (commit+push) failed: %v", err)
	}

	// 4) Push succeeded: verify remote master contains marker.
	t.Logf("step 4: verify marker exists on remote master")
	verifyDir := t.TempDir()
	_, err = git.PlainClone(verifyDir, false, &git.CloneOptions{
		URL:           gitURL,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName("master"),
		Auth: &git_https.BasicAuth{
			Username: gitUser,
			Password: gitToken,
		},
	})
	if err != nil {
		t.Fatalf("failed to clone remote master for verification: %v", err)
	}

	remoteState, err := os.ReadFile(filepath.Join(verifyDir, clusterName, "clusterstate.json"))
	if err != nil {
		t.Fatalf("failed reading clusterstate.json from remote clone: %v", err)
	}
	if !strings.Contains(string(remoteState), marker) {
		t.Fatalf("remote clusterstate.json does not contain marker %q", marker)
	}
}

func TestCommitManagerStop_DrainsPendingTasksAndRejectsNewOnes(t *testing.T) {
	workDir := t.TempDir()

	r, err := git.PlainInit(workDir, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	t.Cleanup(cm.Stop)

	cmm := cm.gitManager.CommitManager

	const taskCount = 200
	var waitQueued sync.WaitGroup
	waitQueued.Add(taskCount)

	for i := 0; i < taskCount; i++ {
		fileName := filepath.Join(workDir, fmt.Sprintf("file-%03d.txt", i))
		if err := os.WriteFile(fileName, []byte("payload"), 0o644); err != nil {
			t.Fatalf("failed to create file %s: %v", fileName, err)
		}

		relPath, err := filepath.Rel(workDir, fileName)
		if err != nil {
			t.Fatalf("failed to create relative path: %v", err)
		}

		cmm.AddFileToCommit(GitAddTask{Filename: relPath, W: w, WaitGroup: &waitQueued})
	}

	stopDone := make(chan struct{})
	go func() {
		cmm.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("CommitManager.Stop() timed out")
	}

	queuedDone := make(chan struct{})
	go func() {
		waitQueued.Wait()
		close(queuedDone)
	}()

	select {
	case <-queuedDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("queued commit waiters were not released")
	}

	postStopFile := filepath.Join(workDir, "post-stop.txt")
	if err := os.WriteFile(postStopFile, []byte("after-stop"), 0o644); err != nil {
		t.Fatalf("failed to create post-stop file: %v", err)
	}
	postStopRel, err := filepath.Rel(workDir, postStopFile)
	if err != nil {
		t.Fatalf("failed to create post-stop relative path: %v", err)
	}

	var waitRejected sync.WaitGroup
	waitRejected.Add(1)
	cmm.AddFileToCommit(GitAddTask{Filename: postStopRel, W: w, WaitGroup: &waitRejected})

	rejectedDone := make(chan struct{})
	go func() {
		waitRejected.Wait()
		close(rejectedDone)
	}()

	select {
	case <-rejectedDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("post-stop task was not rejected/done promptly")
	}
}

func TestSaveConfigWaitDuringStoppingReturnsPromptly(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))

	cluster := &testClusterConfig{name: "stopping-cluster"}

	cm.Stop()

	done := make(chan struct{})
	go func() {
		cm.SaveConfig(cluster, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("SaveConfig(wait=true) blocked while manager is stopping")
	}
}

func TestSaveConfigWaitWhenClusterQueueStoppedReturnsPromptly(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	t.Cleanup(cm.Stop)

	cluster := &testClusterConfig{name: "queue-stop-cluster"}
	clmgr, ok := cm.getOrCreateClusterManager(cluster.GetName())
	if !ok {
		t.Fatalf("expected cluster manager creation to succeed")
	}

	close(clmgr.stopCh)
	clmgr.cond.Signal()

	done := make(chan struct{})
	go func() {
		cm.SaveConfig(cluster, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("SaveConfig(wait=true) blocked when cluster queue is stopped")
	}
}

func TestSaveConfigConcurrentClusterCreateAndAccess(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	t.Cleanup(cm.Stop)

	const clusters = 8
	const requests = 200

	clusterList := make([]*testClusterConfig, 0, clusters)
	counters := make([]*atomic.Int64, 0, clusters)
	for i := 0; i < clusters; i++ {
		counter := &atomic.Int64{}
		counters = append(counters, counter)
		clusterName := fmt.Sprintf("cluster-%d", i)
		clusterList = append(clusterList, &testClusterConfig{
			name: clusterName,
			saveFn: func() error {
				counter.Add(1)
				time.Sleep(2 * time.Millisecond)
				return nil
			},
		})
	}

	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		cluster := clusterList[i%clusters]
		go func(c *testClusterConfig) {
			defer wg.Done()
			cm.SaveConfig(c, true)
		}(cluster)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("concurrent SaveConfig calls timed out")
	}

	totalSaves := int64(0)
	for _, counter := range counters {
		totalSaves += counter.Load()
	}
	if totalSaves == 0 {
		t.Fatalf("expected at least one successful save")
	}
}

func TestSaveConfigWaitWithSavePanicReturnsPromptly(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	t.Cleanup(cm.Stop)

	cluster := &testClusterConfig{
		name: "panic-cluster",
		saveFn: func() error {
			panic("save panic")
		},
	}

	done := make(chan struct{})
	go func() {
		cm.SaveConfig(cluster, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("SaveConfig(wait=true) blocked when Save panicked")
	}
}

func TestSaveConfigWorkerSurvivesSavePanic(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	t.Cleanup(cm.Stop)

	var calls atomic.Int64
	cluster := &testClusterConfig{
		name: "panic-recover-cluster",
		saveFn: func() error {
			if calls.Add(1) == 1 {
				panic("first save panic")
			}
			return nil
		},
	}

	firstDone := make(chan struct{})
	go func() {
		cm.SaveConfig(cluster, true)
		close(firstDone)
	}()

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("first SaveConfig(wait=true) blocked when Save panicked")
	}

	secondDone := make(chan struct{})
	go func() {
		cm.SaveConfig(cluster, true)
		close(secondDone)
	}()

	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("second SaveConfig(wait=true) blocked after panic")
	}

	if calls.Load() < 2 {
		t.Fatalf("expected save worker to continue processing after panic")
	}
}

func TestSaveConfigConcurrentStopAndCreateDoesNotBlock(t *testing.T) {
	conf := &config.Config{}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))

	const requests = 100
	var wg sync.WaitGroup
	wg.Add(requests)

	for i := 0; i < requests; i++ {
		idx := i
		go func() {
			defer wg.Done()
			cluster := &testClusterConfig{name: fmt.Sprintf("stop-race-%d", idx)}
			cm.SaveConfig(cluster, true)
		}()
	}

	cm.Stop()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("concurrent SaveConfig + Stop blocked")
	}
}

func TestRefreshGitMetadata_PreservesCurrentBranchReference(t *testing.T) {
	remoteBare := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(remoteBare, true); err != nil {
		t.Fatalf("failed to initialize bare remote repo: %v", err)
	}

	seedDir := t.TempDir()
	seedRepo, err := git.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("failed to initialize seed repo: %v", err)
	}

	seedWorktree, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("failed to open seed worktree: %v", err)
	}

	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("master\n"), 0o644); err != nil {
		t.Fatalf("failed to write seed README: %v", err)
	}
	if _, err := seedWorktree.Add("README.md"); err != nil {
		t.Fatalf("failed to add README: %v", err)
	}
	if _, err := seedWorktree.Commit("initial", &git.CommitOptions{Author: &git_obj.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()}}); err != nil {
		t.Fatalf("failed to commit initial master commit: %v", err)
	}

	if _, err := seedRepo.CreateRemote(&git_config.RemoteConfig{Name: "origin", URLs: []string{remoteBare}}); err != nil {
		t.Fatalf("failed to add seed remote: %v", err)
	}
	if err := seedRepo.Push(&git.PushOptions{RefSpecs: []git_config.RefSpec{"refs/heads/master:refs/heads/master"}}); err != nil {
		t.Fatalf("failed to push master branch: %v", err)
	}

	featureRef := plumbing.NewBranchReferenceName("feature")
	if err := seedWorktree.Checkout(&git.CheckoutOptions{Branch: featureRef, Create: true}); err != nil {
		t.Fatalf("failed to checkout feature branch in seed repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "FEATURE.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("failed to write feature file: %v", err)
	}
	if _, err := seedWorktree.Add("FEATURE.txt"); err != nil {
		t.Fatalf("failed to add feature file: %v", err)
	}
	if _, err := seedWorktree.Commit("feature commit", &git.CommitOptions{Author: &git_obj.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()}}); err != nil {
		t.Fatalf("failed to commit feature branch commit: %v", err)
	}
	if err := seedRepo.Push(&git.PushOptions{RefSpecs: []git_config.RefSpec{"refs/heads/feature:refs/heads/feature"}}); err != nil {
		t.Fatalf("failed to push feature branch: %v", err)
	}

	workDir := t.TempDir()
	if _, err := git.PlainClone(workDir, false, &git.CloneOptions{URL: remoteBare, ReferenceName: featureRef, SingleBranch: true}); err != nil {
		t.Fatalf("failed to clone working repository on feature branch: %v", err)
	}

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      remoteBare,
		GitUsername: "local",
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: ""},
		},
	}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	if err := cm.RefreshGitMetadata(conf); err != nil {
		t.Fatalf("RefreshGitMetadata failed: %v", err)
	}

	r, err := git.PlainOpen(workDir)
	if err != nil {
		t.Fatalf("failed to open refreshed workdir: %v", err)
	}
	headRef, err := r.Head()
	if err != nil {
		t.Fatalf("failed to resolve refreshed HEAD: %v", err)
	}
	if headRef.Name() != featureRef {
		t.Fatalf("expected refreshed HEAD branch %s, got %s", featureRef, headRef.Name())
	}
}

func TestCountAllCommits_FetchErrorContinuesWithLocalCount(t *testing.T) {
	workDir := t.TempDir()
	r, err := git.PlainInit(workDir, false)
	if err != nil {
		t.Fatalf("failed to initialize repository: %v", err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("failed to open worktree: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workDir, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatalf("failed to write local commit file: %v", err)
	}
	if _, err := w.Add("local.txt"); err != nil {
		t.Fatalf("failed to add local commit file: %v", err)
	}
	if _, err := w.Commit("local commit", &git.CommitOptions{Author: &git_obj.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()}}); err != nil {
		t.Fatalf("failed to create local commit: %v", err)
	}

	if _, err := r.CreateRemote(&git_config.RemoteConfig{Name: "origin", URLs: []string{"https://127.0.0.1:1/invalid.git"}}); err != nil {
		t.Fatalf("failed to add invalid origin remote: %v", err)
	}

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUsername: "user",
		LogGit:      true,
		LogGitLevel: config.NumLvlWarn,
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: "token"},
		},
	}

	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableColors: true})
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	count, err := cm.CountAllCommits(conf)
	if err != nil {
		t.Fatalf("CountAllCommits returned unexpected error on fetch failure: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least one local commit to be counted, got %d", count)
	}
	if !strings.Contains(logs.String(), "fetch failed") {
		t.Fatalf("expected warning log about fetch failure, got logs: %s", logs.String())
	}
}

func TestSwapGitMetadataWithRenamer_EXDEVFallbackCopiesMetadata(t *testing.T) {
	workDir := t.TempDir()
	activeGitDir := filepath.Join(workDir, ".git")
	if err := os.MkdirAll(activeGitDir, 0o755); err != nil {
		t.Fatalf("failed to create active .git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeGitDir, "HEAD"), []byte("old-head\n"), 0o644); err != nil {
		t.Fatalf("failed to write active HEAD: %v", err)
	}

	stagedRoot := t.TempDir()
	stagedGitDir := filepath.Join(stagedRoot, ".git")
	if err := os.MkdirAll(filepath.Join(stagedGitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatalf("failed to create staged .git structure: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagedGitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatalf("failed to write staged HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagedGitDir, "refs", "heads", "feature"), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatalf("failed to write staged feature ref: %v", err)
	}

	conf := &config.Config{LogGit: true, LogGitLevel: config.NumLvlWarn}
	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	renamer := func(oldpath, newpath string) error {
		if oldpath == stagedGitDir && newpath == activeGitDir {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
		return os.Rename(oldpath, newpath)
	}

	if err := cm.swapGitMetadataWithRenamer(workDir, stagedGitDir, renamer); err != nil {
		t.Fatalf("swapGitMetadataWithRenamer EXDEV fallback failed: %v", err)
	}

	headRaw, err := os.ReadFile(filepath.Join(activeGitDir, "HEAD"))
	if err != nil {
		t.Fatalf("failed to read active HEAD after fallback: %v", err)
	}
	if string(headRaw) != "ref: refs/heads/feature\n" {
		t.Fatalf("active HEAD not copied from staged metadata, got %q", string(headRaw))
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("failed to read workdir entries: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".git.backup.") {
			t.Fatalf("unexpected leftover backup directory after successful fallback: %s", entry.Name())
		}
	}
}

func TestRollbackGitMetadata_RemovesPartialActiveThenRestoresBackup(t *testing.T) {
	workDir := t.TempDir()
	activeGitDir := filepath.Join(workDir, ".git")
	backupGitDir := filepath.Join(workDir, ".git.backup.test")

	if err := os.MkdirAll(activeGitDir, 0o755); err != nil {
		t.Fatalf("failed to create active .git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeGitDir, "PARTIAL"), []byte("partial\n"), 0o644); err != nil {
		t.Fatalf("failed to write partial marker: %v", err)
	}

	if err := os.MkdirAll(backupGitDir, 0o755); err != nil {
		t.Fatalf("failed to create backup .git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupGitDir, "HEAD"), []byte("ref: refs/heads/master\n"), 0o644); err != nil {
		t.Fatalf("failed to write backup HEAD: %v", err)
	}

	if err := rollbackGitMetadata(backupGitDir, activeGitDir, os.Rename); err != nil {
		t.Fatalf("rollbackGitMetadata failed: %v", err)
	}

	if _, err := os.Stat(backupGitDir); !os.IsNotExist(err) {
		t.Fatalf("expected backup dir to be moved away, got err=%v", err)
	}

	headRaw, err := os.ReadFile(filepath.Join(activeGitDir, "HEAD"))
	if err != nil {
		t.Fatalf("failed reading restored HEAD: %v", err)
	}
	if string(headRaw) != "ref: refs/heads/master\n" {
		t.Fatalf("unexpected restored HEAD content: %q", string(headRaw))
	}

	if _, err := os.Stat(filepath.Join(activeGitDir, "PARTIAL")); !os.IsNotExist(err) {
		t.Fatalf("expected partial marker to be removed during rollback")
	}
}

func TestIsRepositoryNotFoundError_MatchesWrappedError(t *testing.T) {
	err := fmt.Errorf("clone failed: %w", transport.ErrRepositoryNotFound)
	if !isRepositoryNotFoundError(err) {
		t.Fatalf("expected wrapped ErrRepositoryNotFound to match")
	}

	nonMatchErr := errors.New("different error")
	if isRepositoryNotFoundError(nonMatchErr) {
		t.Fatalf("unexpected match for unrelated error")
	}
}
