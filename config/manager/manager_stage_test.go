package manager

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// #1852: staging goes through AddWithOptions{SkipStatus} (no full worktree status per
// file) and only re-stages a file whose content changed since the last successful push.
func TestStageIfChanged_SkipStatusStagesAndUnchangedIsSkipped(t *testing.T) {
	workDir := t.TempDir()
	r, err := git.PlainInit(workDir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	cm := NewConfigManager(config.NewLogrusWrapper(&config.Config{}, logrus.New()))
	t.Cleanup(cm.Stop)

	if err := os.MkdirAll(filepath.Join(workDir, "crm"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("crm", "crm.toml")
	if err := os.WriteFile(filepath.Join(workDir, rel), []byte("a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	if !cm.stageIfChanged(w, "crm", rel, workDir, false, &wg) {
		t.Fatalf("a never-pushed file must be staged")
	}
	wg.Wait()
	st, err := w.Status()
	if err != nil {
		t.Fatal(err)
	}
	if fs := st.File(rel); fs.Staging != git.Added {
		t.Fatalf("file not staged through SkipStatus add: staging=%c", fs.Staging)
	}

	// Same content, promoted as pushed: not staged again.
	cm.promoteStagedHashes()
	if cm.stageIfChanged(w, "crm", rel, workDir, false, &wg) {
		t.Fatalf("an unchanged file must not be re-staged")
	}
	// Forced (the periodic safety push): staged regardless.
	if !cm.stageIfChanged(w, "crm", rel, workDir, true, &wg) {
		t.Fatalf("force must stage an unchanged file")
	}
	wg.Wait()
	// Changed content: staged.
	if err := os.WriteFile(filepath.Join(workDir, rel), []byte("a = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cm.pendingHash = map[string]string{}
	if !cm.stageIfChanged(w, "crm", rel, workDir, false, &wg) {
		t.Fatalf("a changed file must be staged")
	}
	wg.Wait()
	// A failed push keeps the file pending for the next cycle: pendingHash is dropped,
	// pushedHash untouched, so the next cycle re-stages it.
	cm.pendingHash = map[string]string{}
	if !cm.stageIfChanged(w, "crm", rel, workDir, false, &wg) {
		t.Fatalf("after a failed push the changed file must be staged again")
	}
	wg.Wait()
	// A missing file is skipped, never enqueued.
	if cm.stageIfChanged(w, "crm", filepath.Join("crm", "gone.toml"), workDir, false, &wg) {
		t.Fatalf("a missing file must not be staged")
	}
}
