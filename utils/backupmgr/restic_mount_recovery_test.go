package backupmgr

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

const resticMountHelperEnv = "RESTIC_MOUNT_HELPER_PROCESS"

func TestResticMountHelperProcess(t *testing.T) {
	if os.Getenv(resticMountHelperEnv) != "1" {
		return
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
}

func startResticMountHelper(t *testing.T, mountPath string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("get executable: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Args = []string{
		"restic",
		"-test.run=TestResticMountHelperProcess",
		"--",
		"mount",
		mountPath,
	}
	cmd.Env = append(os.Environ(), resticMountHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	return cmd
}

func waitForCmdExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
		return
	case <-time.After(timeout):
		t.Fatalf("process did not exit within %v", timeout)
	}
}

func TestMountPidFileTracking(t *testing.T) {
	repo := newTestResticManager(t)

	if _, err := repo.readMountPidFile(); err == nil {
		t.Fatalf("expected error when cache dir is unset")
	}

	cacheDir := t.TempDir()
	repo.UpdateEnvKey("RESTIC_CACHE_DIR", cacheDir)
	pidPath := repo.mountPidPath()
	if pidPath == "" {
		t.Fatalf("expected mount pid path to be set")
	}

	if _, err := repo.readMountPidFile(); err == nil {
		t.Fatalf("expected error when pid file is missing")
	}

	if err := repo.writeMountPidFile(4242); err != nil {
		t.Fatalf("writeMountPidFile: %v", err)
	}
	if got, err := repo.readMountPidFile(); err != nil || got != 4242 {
		t.Fatalf("readMountPidFile: got=%d err=%v", got, err)
	}

	if err := os.WriteFile(pidPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty pid file: %v", err)
	}
	if _, err := repo.readMountPidFile(); err == nil {
		t.Fatalf("expected error for empty pid file")
	}

	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write invalid pid file: %v", err)
	}
	if _, err := repo.readMountPidFile(); err == nil {
		t.Fatalf("expected error for invalid pid file")
	}

	if err := repo.writeMountPidFile(0); err != nil {
		t.Fatalf("clear pid file: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, got %v", err)
	}
}

func TestRecoverMountStateOnStartupStopsStaleMount(t *testing.T) {
	repo := newTestResticManager(t)
	cacheDir := t.TempDir()
	repo.UpdateEnvKey("RESTIC_CACHE_DIR", cacheDir)

	mountDir := t.TempDir()
	if isMountReady(mountDir) {
		t.Skip("mount path unexpectedly reported as ready")
	}

	cmd := startResticMountHelper(t, mountDir)
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	pid := cmd.Process.Pid

	if err := repo.writeMountState(mountDir, pid); err != nil {
		t.Fatalf("writeMountState: %v", err)
	}

	pidPath := repo.mountPidPath()
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("expected pid file: %v", err)
	}

	pathFromPid, isRestic := resticMountPathFromPID(pid)
	if !isRestic || pathFromPid != mountDir {
		t.Fatalf("unexpected restic pid detection: path=%s isRestic=%t", pathFromPid, isRestic)
	}

	recovered, err := repo.recoverMountStateOnStartup()
	if err != nil {
		t.Fatalf("recoverMountStateOnStartup: %v", err)
	}
	if recovered {
		t.Fatalf("expected stale mount cleanup, got recovered=true")
	}

	waitForCmdExit(t, cmd, 4*time.Second)
	if isProcessRunning(pid) {
		t.Fatalf("expected stale restic mount pid to stop")
	}

	statePath := repo.mountStatePath()
	if statePath == "" {
		t.Fatalf("expected mount state path")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected mount state file removed, got %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, got %v", err)
	}
	if repo.GetMountPath() != "" {
		t.Fatalf("expected mount path cleared, got %s", repo.GetMountPath())
	}
}

func TestCleanupStaleMountLeavesNonResticProcess(t *testing.T) {
	repo := newTestResticManager(t)

	cmd := startSleepProcess(t)
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	pid := cmd.Process.Pid

	if !isProcessRunning(pid) {
		t.Fatalf("expected sleep process running")
	}

	if err := repo.cleanupStaleMount("", pid, false, false, ""); err != nil {
		t.Fatalf("cleanupStaleMount: %v", err)
	}

	if !isProcessRunning(pid) {
		t.Fatalf("expected non-restic pid to remain running")
	}
}
