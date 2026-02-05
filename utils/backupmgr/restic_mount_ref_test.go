package backupmgr

import (
	"fmt"
	"os/exec"
	"sync"
	"testing"
)

func newTestResticManager(t *testing.T) *ResticManager {
	t.Helper()
	repo := NewResticRepo("", nil, 0)
	repo.PauseWorker()
	t.Cleanup(func() {
		repo.ShutdownWorker()
	})
	return repo
}

func startSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()

	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep binary not found")
	}
	cmd := exec.Command(sleepPath, "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}
	return cmd
}

func activateMountForTest(t *testing.T, repo *ResticManager) func() {
	t.Helper()

	cmd := startSleepProcess(t)
	mountDir := t.TempDir()

	repo.mountRefMutex.Lock()
	repo.mountCmd = cmd
	repo.mountPath = mountDir
	repo.mountPid = cmd.Process.Pid
	repo.mountRefMutex.Unlock()

	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		repo.mountRefMutex.Lock()
		repo.mountCmd = nil
		repo.mountPath = ""
		repo.mountPid = 0
		repo.mountRefMutex.Unlock()
	}
}

func TestAcquireMountRefFailsWithoutMount(t *testing.T) {
	repo := newTestResticManager(t)

	if err := repo.AcquireMountRef("user-1"); err == nil {
		t.Fatalf("expected error when mount is not active")
	}
}

// Run with: go test -race ./utils/backupmgr/...
func TestMountRefConcurrentAcquireRelease(t *testing.T) {
	repo := newTestResticManager(t)
	cleanup := activateMountForTest(t, repo)
	t.Cleanup(cleanup)

	const goroutines = 32
	const iterations = 25
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
	}

	for i := 0; i < goroutines; i++ {
		userID := fmt.Sprintf("user-%d", i)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if err := repo.AcquireMountRef(id); err != nil {
					recordErr(fmt.Errorf("acquire %s: %w", id, err))
					return
				}
				if err := repo.ReleaseMountRef(id); err != nil {
					recordErr(fmt.Errorf("release %s: %w", id, err))
					return
				}
			}
		}(userID)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent mount refs failed: %v", err)
		}
	}

	if repo.GetMountRefCount() != 0 {
		t.Fatalf("expected mount ref count 0, got %d", repo.GetMountRefCount())
	}
	if len(repo.GetMountUsers()) != 0 {
		t.Fatalf("expected no mount users, got %v", repo.GetMountUsers())
	}
}
