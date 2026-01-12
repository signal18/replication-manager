package backupmgr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"
	"github.com/sirupsen/logrus"
)

// TaskType defines the type of tasks
type TaskType int

const (
	InitTask TaskType = iota
	FetchTask
	BackupTask
	PurgeTask
	UnlockTask
	ChangePassTask
)

type MoveType string

const (
	MoveFirst MoveType = "first"
	MoveAfter MoveType = "after"
	MoveLast  MoveType = "last"
)

func GetTaskName(TaskType TaskType) string {
	switch TaskType {
	case 0:
		return "init"
	case 1:
		return "fetch"
	case 2:
		return "backup"
	case 3:
		return "purge"
	case 4:
		return "unlock"
	case 5:
		return "changepass"
	default:
		return "Unknown"
	}
}

// Task represents a queue task
type ResticTask struct {
	ID          int               `json:"task_id"`
	Type        TaskType          `json:"task_type"`
	DirPath     string            `json:"dir_path"`
	Tags        []string          `json:"tags"`
	Opt         ResticPurgeOption `json:"opt"`
	NewPassFile string            `json:"-"`
}

type ResticLsEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

// ResticResult holds the output or error of a task
type ResticResult struct {
	TaskID   int
	TaskType TaskType
	Error    error
}

// ResticPurgeOption holds the configuration for purge
type ResticPurgeOption struct {
	SnapshotID        string `json:"snapshot_id,omitempty"`
	KeepLast          int    `json:"keep_last,omitempty"`
	KeepHourly        int    `json:"keep_hourly,omitempty"`
	KeepDaily         int    `json:"keep_daily,omitempty"`
	KeepWeekly        int    `json:"keep_weekly,omitempty"`
	KeepMonthly       int    `json:"keep_monthly,omitempty"`
	KeepYearly        int    `json:"keep_yearly,omitempty"`
	KeepWithin        string `json:"keep_within,omitempty"`
	KeepWithinHourly  string `json:"keep_within_hourly,omitempty"`
	KeepWithinDaily   string `json:"keep_within_daily,omitempty"`
	KeepWithinWeekly  string `json:"keep_within_weekly,omitempty"`
	KeepWithinMonthly string `json:"keep_within_monthly,omitempty"`
	KeepWithinYearly  string `json:"keep_within_yearly,omitempty"`
}

// TaskStatus represents the task state information stored in the JSON flag file
type TaskStatus struct {
	TaskType   TaskType `json:"task_type"`
	StartTime  string   `json:"start_time"`
	Status     string   `json:"status"`                    // e.g., "in-progress", "completed", "failed"
	Completion string   `json:"completion_time,omitempty"` // Only present if completed
}

// ResticManager manages the queue and execution
type ResticManager struct {
	BinaryPath     string
	Env            []string
	Backups        []BackupSnapshot
	BackupStat     BackupStat
	TaskQueue      []*ResticTask
	TaskErrors     map[TaskType]error
	errorMutex     *sync.Mutex
	ResultChan     chan ResticResult
	LogModule      int
	MessageChan    chan sharedlog.Message
	Shutdown       bool
	Mutex          *sync.Mutex
	cond           *sync.Cond    // Condition variable for waiting and notifying tasks
	stopCh         chan struct{} // Stop channel to signal the goroutine to stop
	CanFetch       bool
	CanInitRepo    bool
	NeedPurgeNow   bool
	PurgeNowOption ResticPurgeOption
	isPaused       bool
	isPausedByDisk bool
	HasLocks       bool
	taskID         int
	CurrentID      int
	mountMutex     *sync.Mutex
	mountCmd       *exec.Cmd
	mountPath      string
	mountDone      chan error
}

// NewResticRepo initializes the repository manager
func NewResticRepo(binaryPath string, msgChan chan sharedlog.Message, logmodule int) *ResticManager {
	repo := &ResticManager{
		BinaryPath:  binaryPath,
		Backups:     make([]BackupSnapshot, 0),
		MessageChan: msgChan,
		LogModule:   logmodule,
		TaskQueue:   make([]*ResticTask, 0),
		Mutex:       &sync.Mutex{},
		mountMutex:  &sync.Mutex{},
		TaskErrors:  make(map[TaskType]error),
		errorMutex:  &sync.Mutex{},
		ResultChan:  make(chan ResticResult, 10),
		stopCh:      make(chan struct{}),
		CanFetch:    true,
		CanInitRepo: true,
	}

	repo.cond = sync.NewCond(repo.Mutex)
	go repo.worker() // Start the worker
	return repo
}

func (repo *ResticManager) GetOldestSnapshot() (*BackupSnapshot, time.Time, error) {
	if len(repo.Backups) == 0 {
		return nil, time.Time{}, errors.New("no backups found")
	}

	var oldest *BackupSnapshot
	var oldestTime time.Time
	for i := len(repo.Backups) - 1; i >= 0; i-- {
		snap := &repo.Backups[i]
		// 2025-12-02T14:49:31.527323782Z
		snaptime, err := time.Parse(time.RFC3339Nano, snap.Time)
		if err != nil {
			continue
		}
		if oldest == nil || snaptime.Before(oldestTime) {
			oldest = snap
			oldestTime = snaptime
		}
	}
	return oldest, oldestTime, nil
}

func (repo *ResticManager) IsPaused() bool {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	return repo.isPaused
}

func (repo *ResticManager) ResumeWorker() {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.isPaused = false
	repo.cond.Broadcast() // Wake up the worker goroutine
}

func (repo *ResticManager) PauseWorker() {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.isPaused = true
}

func (repo *ResticManager) PauseWorkerOnDisk() {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.isPaused = true
	repo.isPausedByDisk = true
	repo.Printf(logrus.WarnLevel, "Pausing Restic worker due to low disk space")
}

func (repo *ResticManager) HasAnyError() bool {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	return len(repo.TaskErrors) > 0
}

func (repo *ResticManager) SetError(task TaskType, err error) {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	repo.TaskErrors[task] = err
}

func (repo *ResticManager) FetchAndClearErrors() map[TaskType]error {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if len(repo.TaskErrors) == 0 {
		return nil
	}

	errs := make(map[TaskType]error)
	for k, v := range repo.TaskErrors {
		errs[k] = v
	}

	for k := range repo.TaskErrors {
		delete(repo.TaskErrors, k)
	}

	return errs
}

func (repo *ResticManager) FetchAndClearError(task TaskType) error {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if len(repo.TaskErrors) == 0 {
		return nil
	}

	errs, exists := repo.TaskErrors[task]
	if !exists {
		return nil
	}

	delete(repo.TaskErrors, task)
	return errs
}

func (repo *ResticManager) SetEnv(env []string) {
	repo.Env = env
}

// UpdateEnvKey updates the environment variable for the Restic repository
func (repo *ResticManager) UpdateEnvKey(key, value string) {
	found := false
	for i, env := range repo.Env {
		if strings.HasPrefix(env, key+"=") {
			repo.Env[i] = fmt.Sprintf("%s=%s", key, value)
			found = true
			break
		}
	}

	if !found {
		repo.Env = append(repo.Env, fmt.Sprintf("%s=%s", key, value))
	}
}

func (repo *ResticManager) GetRepoPath() string {
	for _, env := range repo.Env {
		if strings.HasPrefix(env, "RESTIC_REPOSITORY") {
			return strings.Split(env, "=")[1]
		}
	}

	return ""
}

func (repo *ResticManager) GetCacheDirPath() string {
	for _, env := range repo.Env {
		if strings.HasPrefix(env, "RESTIC_CACHE_DIR") {
			return strings.Split(env, "=")[1]
		}
	}

	return ""
}

// GenerateTaskID ensures unique task IDs
func (repo *ResticManager) GenerateTaskID() int {
	repo.taskID++
	return repo.taskID
}

// SetCanFetch updates CanFetch flag
func (repo *ResticManager) SetCanFetch(value bool) {
	repo.CanFetch = value
}

// GetCanFetch returns CanFetch value
func (repo *ResticManager) GetCanFetch() bool {
	return repo.CanFetch
}

func (repo *ResticManager) Print(level logrus.Level, message string) {
	if repo.MessageChan != nil {
		repo.MessageChan <- sharedlog.Message{
			Module:    repo.LogModule,
			Level:     sharedlog.FromLogrusLevel(uint32(level)),
			Text:      message,
			Timestamp: fmt.Sprint(time.Now().Format("2006/01/02 15:04:05")),
		}
	}
}

func (repo *ResticManager) Printf(level logrus.Level, format string, args ...interface{}) {
	repo.Print(level, fmt.Sprintf(format, args...))
}

func (repo *ResticManager) worker() {

	for {
		repo.Mutex.Lock()

		for (len(repo.TaskQueue) == 0 || repo.isPaused) && !repo.Shutdown && !repo.NeedPurgeNow {
			repo.cond.Wait()
		}

		// Shutdown requested (woken by cond or already set)
		if repo.Shutdown {
			repo.Mutex.Unlock()
			return
		}

		if repo.NeedPurgeNow {
			// Process purge now
			repo.NeedPurgeNow = false
			purgeOption := repo.PurgeNowOption
			repo.PurgeRepo(purgeOption)
		}

		// Check for the stop signal before processing
		if len(repo.TaskQueue) == 0 {
			repo.Mutex.Unlock()
			continue
		}

		// Get the task from TaskQueue
		task := repo.TaskQueue[0]
		repo.TaskQueue = repo.TaskQueue[1:]
		repo.Mutex.Unlock()

		if task == nil {
			continue
		}

		// Process the task
		loglevel := logrus.InfoLevel
		if task.Type == FetchTask {
			loglevel = logrus.DebugLevel
		}

		repo.Printf(loglevel, "Worker processing task ID: %d", task.ID)

		var result ResticResult
		switch task.Type {
		case FetchTask:
			err := repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, Error: err}
		case PurgeTask:
			err := repo.PurgeRepo(task.Opt)
			_ = repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, Error: err}
		case BackupTask:
			err := repo.Backup(task.DirPath, task.Tags)
			_ = repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, Error: err}
		case UnlockTask:
			err := repo.UnlockRepo()
			_ = repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, Error: err}
		default:
			repo.Printf(logrus.WarnLevel, "Unknown task type: %d", task.Type)
			continue
		}

		if result.Error != nil {
			repo.SetError(task.Type, result.Error)
		}

		repo.Printf(loglevel, "Worker finished task ID: %d", task.ID)
	}
}

func (repo *ResticManager) appendTask(task *ResticTask) {
	if task == nil {
		return
	}

	// Add task to slice
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.TaskQueue = append(repo.TaskQueue, task)

	// Log the addition of the tasks with ID
	if task.ID != 0 {
		repo.Printf(logrus.InfoLevel, "Added %s task to the queue, ID: %d", GetTaskName(task.Type), task.ID)
	}

	// Notify the worker that a new task is available if not paused
	repo.cond.Signal()
}

func (repo *ResticManager) AddFetchTask() {
	// Add task to slice
	repo.appendTask(&ResticTask{
		Type: FetchTask,
	})
}

func (repo *ResticManager) AddPurgeTask(opt ResticPurgeOption, immediate bool) error {
	if immediate {
		if repo.NeedPurgeNow {
			return errors.New("a purge-now task is already scheduled")
		}

		repo.Mutex.Lock()
		repo.NeedPurgeNow = true
		repo.PurgeNowOption = opt
		repo.Mutex.Unlock()
	} else {
		repo.appendPurgeTask(opt)
	}
	return nil
}

func (repo *ResticManager) RestoreSnapshot(snapshotID, targetDir string, paths []string) error {
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	if targetDir == "" {
		return fmt.Errorf("target dir is empty")
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.RestoreSnapshot(snapshotID, targetDir, paths)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	if err := repo.CheckResticLocks(); err != nil {
		return err
	}

	args := []string{"restore", snapshotID, "--target", targetDir}
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			args = append(args, "--include", strings.TrimSpace(path))
		}
	}

	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		return fmt.Errorf("failed to restore snapshot: %v, stderr: %s", err, stderr)
	}

	return nil
}

func (repo *ResticManager) ListSnapshot(snapshotID string, paths []string, recursive bool) ([]ResticLsEntry, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshot ID is empty")
	}

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.ListSnapshot(snapshotID, paths, recursive)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	if err := repo.CheckRepoFiles(); err != nil {
		return nil, err
	}

	if err := repo.CheckResticLocks(); err != nil {
		return nil, err
	}

	args := []string{"ls", snapshotID, "--json"}
	if recursive {
		args = append(args, "--recursive")
	}
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			args = append(args, strings.TrimSpace(path))
		}
	}

	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshot: %v, stderr: %s", err, stderr)
	}

	entries := make([]ResticLsEntry, 0)
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ResticLsEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("failed to parse restic ls output: %w", err)
		}
		if entry.Path == "" {
			continue
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read restic ls output: %w", err)
	}

	return entries, nil
}

func (repo *ResticManager) DumpSnapshot(snapshotID, filePath string, writer io.Writer) error {
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	if filePath == "" {
		return fmt.Errorf("file path is empty")
	}
	if writer == nil {
		return fmt.Errorf("output writer is nil")
	}

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.DumpSnapshot(snapshotID, filePath, writer)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	args := []string{"dump", snapshotID, filePath}
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = append(os.Environ(), repo.Env...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Starting command: %s %v", repo.BinaryPath, args)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting command: %w", err)
	}

	var stderrBuf bytes.Buffer
	copyErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	go func() {
		_, copyErr := io.Copy(writer, stdoutPipe)
		copyErrCh <- copyErr
	}()

	go func() {
		_, stderrErr := io.Copy(&stderrBuf, stderrPipe)
		stderrErrCh <- stderrErr
	}()

	copyErr := <-copyErrCh
	stderrErr := <-stderrErrCh

	if stderrErr != nil {
		return fmt.Errorf("failed to read stderr: %w", stderrErr)
	}
	if copyErr != nil {
		return fmt.Errorf("failed to stream output: %w", copyErr)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("command execution failed: %w, stderr: %s", err, stderrBuf.Bytes())
	}

	repo.Printf(logrus.InfoLevel, "Command completed successfully: %s %v", repo.BinaryPath, args)
	return nil
}

func (repo *ResticManager) MountRepo(targetDir string) error {
	if targetDir == "" {
		return fmt.Errorf("mount target is empty")
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create mount dir: %w", err)
	}

	repo.mountMutex.Lock()
	if repo.mountCmd != nil && repo.mountCmd.Process != nil {
		repo.mountMutex.Unlock()
		return fmt.Errorf("restic mount already running at %s", repo.mountPath)
	}
	repo.mountMutex.Unlock()

	args := []string{"mount", targetDir}
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = append(os.Environ(), repo.Env...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Starting command: %s %v", repo.BinaryPath, args)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting command: %w", err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	repo.mountMutex.Lock()
	repo.mountCmd = cmd
	repo.mountPath = targetDir
	repo.mountDone = make(chan error, 1)
	done := repo.mountDone
	repo.mountMutex.Unlock()

	go repo.streamMountOutput(stdoutPipe, "[OUT] ", &stdoutBuf)
	go repo.streamMountOutput(stderrPipe, "[ERR] ", &stderrBuf)

	go func() {
		err := cmd.Wait()
		if err != nil {
			repo.Printf(logrus.ErrorLevel, "Restic mount exited: %v", err)
		}
		repo.mountMutex.Lock()
		repo.mountCmd = nil
		repo.mountPath = ""
		if repo.mountDone != nil {
			select {
			case repo.mountDone <- err:
			default:
			}
			close(repo.mountDone)
			repo.mountDone = nil
		}
		repo.mountMutex.Unlock()
	}()

	// Wait a bit for mount to be ready or fail early
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(5 * time.Second)

	for {
		select {
		case err := <-done:
			// Mount process exited prematurely
			var exitCode int
			msg := strings.TrimSpace(stderrBuf.String())
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			if strings.Contains(msg, "fuse") {
				lines := strings.Split(msg, "\n")
				return fmt.Errorf("restic mount failed (fuse): exit=%d, err=%s", exitCode, lines[0])
			}

			return fmt.Errorf("restic mount failed: exit=%d, err=%w", exitCode, err)

		case <-ticker.C:
			// Check if mount point is ready
			if isMountReady(targetDir) {
				repo.Printf(logrus.InfoLevel, "Restic mount started at %s", targetDir)
				return nil
			}

		case <-timeout:
			// Timeout waiting for mount to be ready
			repo.mountMutex.Lock()
			if repo.mountCmd != nil && repo.mountCmd.Process != nil {
				_ = repo.mountCmd.Process.Kill()
			}
			repo.mountMutex.Unlock()
			msg := strings.TrimSpace(stderrBuf.String())
			if msg == "" {
				msg = "mount timeout"
			}
			return fmt.Errorf("restic mount timeout: %s", msg)
		}
	}
}

func isMountReady(mountPath string) bool {
	// Check if mount point is accessible
	entries, err := os.ReadDir(mountPath)
	if err != nil {
		return false
	}
	// Restic mount typically shows snapshot directories
	return len(entries) > 0
}

func (repo *ResticManager) UnmountRepo() error {
	repo.mountMutex.Lock()
	cmd := repo.mountCmd
	done := repo.mountDone
	mountPath := repo.mountPath
	repo.mountMutex.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no restic mount is running")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop restic mount: %w", err)
	}

	if done == nil {
		return nil
	}

	select {
	case err, ok := <-done:
		if !ok {
			// Channel was closed, mount already stopped
			repo.Printf(logrus.InfoLevel, "Restic mount stopped at %s", mountPath)
			return nil
		}
		if err != nil {
			repo.Printf(logrus.ErrorLevel, "Restic mount exited with error: %v", err)
			return fmt.Errorf("restic mount exited with error: %w", err)
		}
	case <-time.After(10 * time.Second):
		repo.mountMutex.Lock()
		if repo.mountCmd != nil && repo.mountCmd.Process != nil {
			_ = repo.mountCmd.Process.Kill()
		}
		repo.mountMutex.Unlock()
		return fmt.Errorf("restic mount shutdown timeout")
	}

	repo.Printf(logrus.InfoLevel, "Restic mount stopped at %s", mountPath)
	return nil
}

func (repo *ResticManager) streamMountOutput(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		if buffer != nil {
			buffer.WriteString(line + "\n")
		}
		repo.Printf(logrus.DebugLevel, "%s: %s", prefix, line)
	}
	if err := scanner.Err(); err != nil {
		repo.Printf(logrus.ErrorLevel, prefix+"Error reading output: %v", err)
	}
}

func (repo *ResticManager) appendPurgeTask(opt ResticPurgeOption) {
	task := ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: PurgeTask,
		Opt:  opt,
	}

	// Add task to slice
	repo.appendTask(&task)
}

func (repo *ResticManager) AddBackupTask(dirpath string, tags []string) {
	task := ResticTask{
		ID:      repo.GenerateTaskID(),
		Type:    BackupTask,
		DirPath: dirpath,
		Tags:    tags,
	}

	// Add task to slice
	repo.appendTask(&task)
}

func (repo *ResticManager) AddUnlockTask() {
	task := ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: UnlockTask,
	}
	repo.appendTask(&task)
}

func (repo *ResticManager) MoveTask(mvType string, taskID, afterTaskID int) error {
	moveType := MoveType(mvType)
	switch moveType {
	case MoveFirst, MoveAfter, MoveLast:
		return repo.moveTask(moveType, taskID, afterTaskID)
	default:
		return errors.New("invalid move type")
	}
}

func (repo *ResticManager) moveTask(moveType MoveType, taskID, afterTaskID int) error {
	repo.Mutex.Lock()

	waspaused := repo.isPaused
	if !waspaused {
		repo.isPaused = true
	}

	defer func() {
		if !waspaused {
			repo.isPaused = false
			repo.cond.Broadcast()
		}
		repo.Mutex.Unlock()
	}()

	var taskToMove *ResticTask
	var taskIndex int
	for i, task := range repo.TaskQueue {
		if task.ID == taskID {
			taskToMove = task
			taskIndex = i
			break
		}
	}

	if taskToMove == nil {
		return errors.New("task not found")
	}

	switch moveType {
	case MoveFirst:
		if taskIndex == 0 {
			return nil // Already first
		}
		repo.TaskQueue = append(repo.TaskQueue[:taskIndex], repo.TaskQueue[taskIndex+1:]...)
		repo.TaskQueue = append([]*ResticTask{taskToMove}, repo.TaskQueue...)
	case MoveAfter:
		if afterTaskID == 0 {
			return errors.New("afterTaskID is required for MoveAfter")
		}

		var afterIndex int = -1
		for i, task := range repo.TaskQueue {
			if task.ID == afterTaskID {
				afterIndex = i
				break
			}
		}

		if afterIndex == -1 {
			return errors.New("afterTaskID not found")
		}

		if taskIndex == afterIndex+1 {
			return nil // Already after the specified task
		}

		if taskIndex < afterIndex {
			afterIndex-- // Adjust index since we will remove the task first
		}

		repo.TaskQueue = append(repo.TaskQueue[:taskIndex], repo.TaskQueue[taskIndex+1:]...)
		repo.TaskQueue = append(repo.TaskQueue[:afterIndex+1], append([]*ResticTask{taskToMove}, repo.TaskQueue[afterIndex+1:]...)...)
	case MoveLast:
		if taskIndex == len(repo.TaskQueue)-1 {
			return nil // Already last
		}
		repo.TaskQueue = append(repo.TaskQueue[:taskIndex], repo.TaskQueue[taskIndex+1:]...)
		repo.TaskQueue = append(repo.TaskQueue, taskToMove)
	}

	return nil
}

func (repo *ResticManager) HasFetchQueue() bool {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	for _, task := range repo.TaskQueue {
		if task.Type == FetchTask {
			return true
		}
	}

	return false
}

func (repo *ResticManager) CancelTask(taskId int) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.Printf(logrus.InfoLevel, "Cancelling restic task ID: %d", taskId)

	var taskToCancel *ResticTask
	for _, task := range repo.TaskQueue {
		if task.ID == taskId {
			taskToCancel = task
			break
		}
	}

	if taskToCancel != nil {
		repo.TaskQueue = append(repo.TaskQueue[:taskToCancel.ID], repo.TaskQueue[taskToCancel.ID+1:]...)
		repo.Printf(logrus.InfoLevel, "Cancelled restic task ID: %d", taskId)
	} else {
		repo.Printf(logrus.WarnLevel, "Restic task ID not found: %d", taskId)
	}
}

func (repo *ResticManager) ClearQueue() {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.Printf(logrus.InfoLevel, "Emptying task queue...")

	tasklist := []string{}
	for _, task := range repo.TaskQueue {
		tasklist = append(tasklist, fmt.Sprintf("ID: %d, Type: %s", task.ID, GetTaskName(task.Type)))
	}

	if len(tasklist) > 0 {
		repo.Printf(logrus.InfoLevel, "Clearing tasks: %s", strings.Join(tasklist, "; "))
	}

	repo.TaskQueue = repo.TaskQueue[:0]

	repo.Printf(logrus.InfoLevel, "Task queue emptied.")
}

func (repo *ResticManager) ShutdownWorker() {
	repo.Mutex.Lock()
	repo.Shutdown = true
	repo.Mutex.Unlock()

	repo.cond.Broadcast()
}

func (repo *ResticManager) CheckRepoFiles() error {
	repopath := repo.GetRepoPath()

	if _, err := os.Stat(filepath.Join(repopath, "config")); os.IsNotExist(err) {
		// Check the repo data
		errstr := "repo config is missing"
		_, err := os.Stat(filepath.Join(repopath, "data"))
		if err == nil {
			errstr += " but data exists"
			repo.CanInitRepo = false
			err = errors.New(errstr)
			repo.SetError(InitTask, err)
			return err
		} else if err != nil && !os.IsNotExist(err) { // Not a not-exist error (i.e., other error)
			errstr += " and failed to check repo data: " + err.Error()
			repo.CanInitRepo = false
			err = errors.New(errstr)
			repo.SetError(InitTask, err)
			return err
		} else { // Repo data does not exist (can init)
			// Initialize the repo
			err = repo.InitRepo(false)
			if err != nil {
				return err
			}
		}
	} else if err != nil {
		repo.CanInitRepo = false
		err = fmt.Errorf("failed to check repo config: %w", err)
		repo.SetError(InitTask, err)
		return err
	}

	repo.CanInitRepo = true
	delete(repo.TaskErrors, InitTask) // Clear any previous init errors

	return nil
}

// RunCommand executes a command within the context of a Restic repository, capturing stdout and stderr.
// It uses the ResticRepo's BinaryPath as the first parameter, along with any additional args.
// Optionally, you can skip capturing the output to save memory.
func (repo *ResticManager) RunCommand(args []string, loglevel logrus.Level, captureOutput bool) ([]byte, []byte, error) {
	// Set up the command
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = append(os.Environ(), repo.Env...)

	// Buffers for stdout and stderr
	var stdoutBuf, stderrBuf bytes.Buffer

	// Create pipes for command output
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(loglevel, "Starting command: %s %v", repo.BinaryPath, args)

	// Start the command execution
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("error starting command: %w", err)
	}

	// Use WaitGroup to ensure we read both stdout and stderr before cmd.Wait()
	var wg sync.WaitGroup
	wg.Add(2)

	// Function to read output
	streamOutput := func(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer) {
		defer wg.Done() // Mark goroutine as done

		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, prefix+line)
			if captureOutput {
				buffer.WriteString(line + "\n")
			}
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, prefix+"Error reading output:", err)
		}
	}

	// Start streaming stdout and stderr in separate goroutines
	go streamOutput(stdoutPipe, "[OUT] ", &stdoutBuf)
	go streamOutput(stderrPipe, "[ERR] ", &stderrBuf)

	// Wait for both output goroutines to finish reading
	wg.Wait()

	// Now that all output is read, we can wait for the process to finish
	if err := cmd.Wait(); err != nil {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), fmt.Errorf("command execution failed: %w", err)
	}

	repo.Printf(loglevel, "Command completed successfully: %s %v", repo.BinaryPath, args)

	// Return captured stdout and stderr if needed
	if captureOutput {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), nil
	}
	return nil, nil, nil
}

func (repo *ResticManager) InitRepo(force bool) error {
	repopath := repo.GetRepoPath()
	if force {
		err := os.RemoveAll(repopath)
		if err != nil {
			return fmt.Errorf("failed to remove repo: %w", err)
		}

		os.MkdirAll(repopath, 0755)
	}

	defer repo.AddFetchTask()
	// Prepare the arguments for the "forget" command
	args := []string{"init"}

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false) // Don't capture output
	if err != nil {
		// Update the repo flag to prevent further fetch attempts
		repo.CanInitRepo = false
		err = errors.New(string(stderr))

		repo.SetError(InitTask, err)

		return err
	}

	return nil
}

// fetchRepoStat performs the statistic fetch
func (repo *ResticManager) fetchRepoStat() error {
	// Prepare the arguments for the "forget" command
	args := []string{"stats", "--mode", "raw-data", "--json"}

	// Execute the Restic "stats" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true) // Capture output
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to fetch repo: %v, stderr: %s", err, stderr)
	}

	var backupstat BackupStat
	err = json.Unmarshal(stdout, &backupstat)
	if err != nil {
		return fmt.Errorf("failed to unmarshal backup stat: %w", err)
	}

	// Update the Backups field with the fetched backups
	repo.BackupStat = backupstat

	// Return the captured stdout (which contains the result)
	return nil
}

// fetchRepoSnapshots performs the snapshot fetch
func (repo *ResticManager) fetchRepoSnapshots() error {
	// Proceed with fetch
	args := []string{"snapshots", "--json"}
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
		if strings.Contains(string(stderr), "no such file or directory") {
			_ = repo.InitRepo(false)
		}
		// Handle error (including stderr)
		return fmt.Errorf("failed to fetch repo: %v, stderr: %s", err, stderr)
	}

	var backups []BackupSnapshot
	err = json.Unmarshal(stdout, &backups)
	if err != nil {
		return fmt.Errorf("failed to unmarshal backups: %w", err)
	}

	// Update the Backups field with the fetched backups
	repo.Backups = backups

	return nil // Success
}

// FetchRepo performs the fetch for snapshots and stats
func (repo *ResticManager) FetchRepo() error {
	// Check if the repo is able to fetch and initialized
	if !repo.GetCanFetch() {
		return nil
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Check if the repo is initialized
	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	// Check latest lock in repository
	err := repo.CheckResticLocks()
	if err != nil {
		return err
	}

	// Fetch snapshots
	err = repo.fetchRepoSnapshots()
	if err != nil {
		return fmt.Errorf("failed to fetch repo snapshots: %w", err)
	}

	err = repo.fetchRepoStat()
	if err != nil {
		return fmt.Errorf("failed to fetch repo stat: %w", err)
	}

	return nil // Success
}

// GetKeepWithinTime returns the arguments to keep within
func GetKeepWithinTime(keepWithin string, keepWithinHourly string, keepWithinDaily string, keepWithinWeekly string, keepWithinMonthly string, keepWithinYearly string) ([]string, bool) {
	useWithin := false
	var within []string

	if keepWithin != "" {
		useWithin = true
		within = append(within, "--keep-within", keepWithin)
	}

	if keepWithinHourly != "" {
		useWithin = true
		within = append(within, "--keep-within-hourly", keepWithinHourly)
	}

	if keepWithinDaily != "" {
		useWithin = true
		within = append(within, "--keep-within-daily", keepWithinDaily)
	}

	if keepWithinWeekly != "" {
		useWithin = true
		within = append(within, "--keep-within-weekly", keepWithinWeekly)
	}

	if keepWithinMonthly != "" {
		useWithin = true
		within = append(within, "--keep-within-monthly", keepWithinMonthly)
	}

	if keepWithinYearly != "" {
		useWithin = true
		within = append(within, "--keep-within-yearly", keepWithinYearly)
	}

	return within, useWithin
}

// GetKeepN returns the numeric arguments to keep
func GetKeepN(keepLast int, keepHourly int, keepDaily int, keepWeekly int, keepMonthly int, keepYearly int) ([]string, bool) {
	useKeep := false
	var keep []string

	if keepLast > 0 {
		useKeep = true
		keep = append(keep, "--keep-last", fmt.Sprintf("%d", keepLast))
	}

	if keepHourly > 0 {
		useKeep = true
		keep = append(keep, "--keep-hourly", fmt.Sprintf("%d", keepHourly))
	}

	if keepDaily > 0 {
		useKeep = true
		keep = append(keep, "--keep-daily", fmt.Sprintf("%d", keepDaily))
	}

	if keepWeekly > 0 {
		useKeep = true
		keep = append(keep, "--keep-weekly", fmt.Sprintf("%d", keepWeekly))
	}

	if keepMonthly > 0 {
		useKeep = true
		keep = append(keep, "--keep-monthly", fmt.Sprintf("%d", keepMonthly))
	}

	if keepYearly > 0 {
		useKeep = true
		keep = append(keep, "--keep-yearly", fmt.Sprintf("%d", keepYearly))
	}

	return keep, useKeep
}

func (repo *ResticManager) purgeSingleSnapshot(snapshotID string) error {
	repo.Printf(logrus.InfoLevel, "Purging single snapshot ID: %s", snapshotID)

	args := []string{"forget", "--prune", snapshotID}

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to purge repo: %v, stderr: %s", err, stderr)
	}

	return nil
}

func (repo *ResticManager) purgeWithPolicy(opt ResticPurgeOption) error {
	repo.Printf(logrus.InfoLevel, "Purging snapshots with policy: %+v", opt)

	args := []string{"forget", "--prune"}

	// Get the arguments for the "keep" options
	keepWithin, useWithin := GetKeepWithinTime(opt.KeepWithin, opt.KeepWithinHourly, opt.KeepWithinDaily, opt.KeepWithinWeekly, opt.KeepWithinMonthly, opt.KeepWithinYearly)
	if useWithin {
		args = append(args, keepWithin...)
	}

	keep, useKeep := GetKeepN(opt.KeepLast, opt.KeepHourly, opt.KeepDaily, opt.KeepWeekly, opt.KeepMonthly, opt.KeepYearly)
	if useKeep {
		args = append(args, keep...)
	}

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to purge repo: %v, stderr: %s", err, stderr)
	}

	return nil
}

// ResticPurgeRepo performs the actual purging of the repository
func (repo *ResticManager) PurgeRepo(opt ResticPurgeOption) error {
	// Check if the repo is able to fetch and initialized
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.PurgeRepo(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Check if the repo is initialized
	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	// Check latest lock in repository
	err := repo.CheckResticLocks()
	if err != nil {
		return err
	}

	// Prepare the arguments for the "forget" command
	if opt.SnapshotID != "" {
		err := repo.purgeSingleSnapshot(opt.SnapshotID)
		if err != nil {
			return err
		}
	} else {
		err := repo.purgeWithPolicy(opt)
		if err != nil {
			return err
		}
	}

	return nil
}

func (repo *ResticManager) Backup(dirpath string, tags []string) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.Backup(dirpath, tags)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "backup" command
	args := []string{"backup"}

	for _, tag := range tags {
		if tag != "" {
			args = append(args, "--tag")
			args = append(args, tag)
		}
	}

	args = append(args, dirpath)

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to backup repo: %v, stderr: %s", err, stderr)
	}

	return nil
}

func (repo *ResticManager) CheckResticLocks() error {
	// Prepare the arguments for the "backup" command
	args := []string{"list", "locks", "--no-lock", "-q"}

	// Execute the Restic "list locks" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
		if strings.Contains(string(stderr), "no such file or directory") {
			_ = repo.InitRepo(false)
		}
		// Handle error (including stderr)
		return fmt.Errorf("failed to check repo locks: %v, stderr: %s", err, stderr)
	}

	haslock := len(stdout) > 0

	if haslock {
		err = fmt.Errorf("repository has locks:\n%s", string(stdout))
	}

	if repo.HasLocks != haslock {
		repo.HasLocks = haslock
		if haslock {
			return err
		} else {
			repo.Printf(logrus.InfoLevel, "Repository locks have been cleared")
		}
	}

	return nil
}

// ResticUnlockRepo unlocks the repository
func (repo *ResticManager) UnlockRepo() error {

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.UnlockRepo()
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "backup" command
	args := []string{"unlock"}

	// Execute the Restic "list locks" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		if strings.Contains(string(stderr), "no such file or directory") {
			_ = repo.InitRepo(false)
		}
		// Handle error (including stderr)
		return fmt.Errorf("failed to check repo locks: %v, stderr: %s", err, stderr)
	}

	if !strings.Contains(string(stdout), "successfully removed locks") {
		return fmt.Errorf("failed to unlock repo: %s. stderr: %s", stdout, stderr)
	}

	repo.HasLocks = false
	return nil
}

// ResticChangePassword changes the repository password
func (repo *ResticManager) AddRepoKey(newpassfile string) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.AddRepoKey(newpassfile)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "backup" command
	args := []string{"key", "add", "--new-password-file", newpassfile}

	// Execute the Restic "key add" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		return fmt.Errorf("failed to add repo password: %v, stderr: %s", err, stderr)
	}

	if !strings.Contains(string(stdout), "saved new key as") {
		return fmt.Errorf("failed to add new key: %s. stderr: %s", stdout, stderr)
	}

	return nil
}

type ResticKey struct {
	Current  bool   `json:"current"`
	Id       string `json:"id"`
	UserName string `json:"userName"`
	HostName string `json:"hostName"`
	Created  string `json:"created"`
}

func (repo *ResticManager) GetRepoKeyList() ([]ResticKey, error) {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.GetRepoKeyList()
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "key list" command
	args := []string{"key", "list", "--json"}

	// Execute the Restic "key list" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
		if strings.Contains(string(stderr), "no such file or directory") {
			_ = repo.InitRepo(false)
		}
		// Handle error (including stderr)
		return nil, fmt.Errorf("failed to list repo keys: %v, stderr: %s", err, stderr)
	}

	var keys []ResticKey
	if err := json.Unmarshal(stdout, &keys); err != nil {
		return nil, fmt.Errorf("failed to unmarshal repo keys: %v", err)
	}

	return keys, nil
}

func (repo *ResticManager) RemoveRepoKey(keyid string) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.RemoveRepoKey(keyid)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "key remove" command
	args := []string{"key", "remove", keyid}

	// Execute the Restic "key remove" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		return fmt.Errorf("failed to remove repo key: %v, stderr: %s", err, stderr)
	}

	if !strings.Contains(string(stdout), "removed key") {
		return fmt.Errorf("failed to remove key: %s. stderr: %s", stdout, stderr)
	}

	return nil
}

func (repo *ResticManager) TestPassword(newpass string) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.TestPassword(newpass)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Temporarily add the new password file to the environment
	originalEnv := repo.Env
	repo.UpdateEnvKey("RESTIC_PASSWORD", newpass)
	defer func() { repo.Env = originalEnv }() // Restore original env after function

	// Test the new password by listing keys
	args := []string{"key", "list", "--json"}

	// Execute the Restic "key list" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
		return fmt.Errorf("failed to test repo password: %v, stderr: %s", err, stderr)
	}

	return nil
}

func (repo *ResticManager) PurgeOldestBackup() error {
	oldestSnap, _, err := repo.GetOldestSnapshot()
	if err != nil {
		return err
	}

	if oldestSnap == nil {
		return errors.New("no snapshots found to purge")
	}

	repo.AddPurgeTask(ResticPurgeOption{
		SnapshotID: oldestSnap.Id,
	}, true)

	return nil
}
