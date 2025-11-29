package archiver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

// TaskType defines the type of tasks
type TaskType int

const (
	FetchTask TaskType = iota
	PurgeTask
	BackupTask
	UnlockTask
)

func GetTaskName(TaskType TaskType) string {
	switch TaskType {
	case 0:
		return "fetch"
	case 1:
		return "backup"
	case 2:
		return "purge"
	case 3:
		return "unlock"
	case 4:
		return "changepass"
	default:
		return "Unknown"
	}
}

// Task represents a queue task
type ResticTask struct {
	ID          int               `json:"task_id"`
	DirPath     string            `json:"dir_path"`
	NewPassFile string            `json:"new_pass_file"`
	Tags        []string          `json:"tags"`
	Type        TaskType          `json:"task_type"`
	Opt         ResticPurgeOption `json:"opt"`
	ErrorState  state.State       `json:"error_state"`
	Result      chan ResticResult `json:"-"` // Only used if caller needs the result
}

// ResticResult holds the output or error of a task
type ResticResult struct {
	TaskID   int
	TaskType TaskType
	Error    error
}

// ResticPurgeOption holds the configuration for purge
type ResticPurgeOption struct {
	KeepLast          int
	KeepHourly        int
	KeepDaily         int
	KeepWeekly        int
	KeepMonthly       int
	KeepYearly        int
	KeepWithin        string
	KeepWithinHourly  string
	KeepWithinDaily   string
	KeepWithinWeekly  string
	KeepWithinMonthly string
	KeepWithinYearly  string
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
	BinaryPath  string
	Env         []string
	Backups     []Backup
	BackupStat  BackupStat
	Logger      *logrus.Logger
	LogFields   logrus.Fields
	LogLevel    logrus.Level
	TaskQueue   []*ResticTask
	ResultChan  chan ResticResult
	Shutdown    bool
	Mutex       *sync.Mutex
	cond        *sync.Cond    // Condition variable for waiting and notifying tasks
	stopCh      chan struct{} // Stop channel to signal the goroutine to stop
	CanFetch    bool
	CanInitRepo bool
	HasLocks    bool
	taskID      int
	CurrentID   int
}

// NewResticRepo initializes the repository manager
func NewResticRepo(binaryPath string, logger *logrus.Logger, logfields logrus.Fields, loglevel logrus.Level) *ResticManager {
	repo := &ResticManager{
		BinaryPath:  binaryPath,
		Backups:     make([]Backup, 0),
		Logger:      logger,
		LogFields:   logfields,
		LogLevel:    loglevel,
		TaskQueue:   make([]*ResticTask, 0),
		ResultChan:  make(chan ResticResult, 10),
		Mutex:       &sync.Mutex{},
		stopCh:      make(chan struct{}),
		CanFetch:    true,
		CanInitRepo: true,
	}

	repo.cond = sync.NewCond(repo.Mutex)
	go repo.worker() // Start the worker
	return repo
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

func (repo *ResticManager) SetLogFields(fields logrus.Fields) {
	repo.LogFields = fields
}

// SetLogLevel updates the log level for archive module. This is different with logrus.SetLevel which sets the global log level.
func (repo *ResticManager) SetLogLevel(level logrus.Level) {
	repo.LogLevel = level
}

func (repo *ResticManager) Print(level logrus.Level, message string, args ...interface{}) {
	if repo.LogLevel >= level {
		repo.Logger.WithFields(repo.LogFields).Logf(level, message, args...)
	}
}

func (repo *ResticManager) worker() {

	for {
		repo.Mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(repo.TaskQueue) == 0 {
			select {
			case <-repo.stopCh: // Stop signal for the goroutine
				repo.Print(logrus.InfoLevel, "Worker stopping...")
				repo.Mutex.Unlock()
				return
			default:
			}
			repo.cond.Wait()
		}

		// Check for the stop signal before processing
		select {
		case <-repo.stopCh: // Stop signal for the goroutine
			repo.Shutdown = true
			repo.Print(logrus.InfoLevel, "Worker stopping...")
			repo.Mutex.Unlock()
			return
		default:

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

			repo.Print(loglevel, "Worker processing task ID: %d", task.ID)

			var result ResticResult
			switch task.Type {
			case FetchTask:
				err := repo.FetchRepo()
				result = ResticResult{TaskID: task.ID, Error: err}
			case PurgeTask:
				err := repo.PurgeRepo(task.Opt)
				result = ResticResult{TaskID: task.ID, Error: err}
			case BackupTask:
				err := repo.Backup(task.DirPath, task.Tags)
				result = ResticResult{TaskID: task.ID, Error: err}
			default:
				result = ResticResult{TaskID: task.ID, Error: fmt.Errorf("unknown task type")}
			}

			// Send result to per-task channel (if waiting)
			if task.Result != nil {
				task.Result <- result
			} else {
				repo.ResultChan <- result
			}

			repo.Print(loglevel, "Worker finished task ID: %d", task.ID)
		}
	}
}

func (repo *ResticManager) appendTask(task *ResticTask) {
	repo.Mutex.Lock()
	repo.TaskQueue = append(repo.TaskQueue, task)
	repo.Mutex.Unlock()
}

func (repo *ResticManager) AddFetchTask(waitForResult bool) (*ResticResult, error) {
	task := ResticTask{
		Type: FetchTask,
	}

	var resultChan chan ResticResult
	if waitForResult {
		resultChan = make(chan ResticResult, 1)
		task.Result = resultChan
	}

	// Add task to slice
	repo.appendTask(&task)

	if waitForResult {
		result := <-resultChan
		return &result, result.Error
	}
	return nil, nil
}

func (repo *ResticManager) AddPurgeTask(opt ResticPurgeOption) error {
	task := ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: PurgeTask,
		Opt:  opt,
	}

	// Add task to slice
	repo.appendTask(&task)

	repo.Print(logrus.InfoLevel, "Restic purge task queued (Task ID: %d)", task.ID)

	return nil
}

func (repo *ResticManager) AddBackupTask(dirpath string, tags []string) (*ResticResult, error) {
	task := ResticTask{
		ID:      repo.GenerateTaskID(),
		Type:    BackupTask,
		DirPath: dirpath,
		Tags:    tags,
	}

	// Add task to slice
	repo.appendTask(&task)
	repo.Print(logrus.InfoLevel, "Restic backup task queued (Task ID: %d)", task.ID)

	return nil, nil
}

func (repo *ResticManager) HasFetchQueue() bool {
	if len(repo.TaskQueue) > 0 {
		for _, task := range repo.TaskQueue {
			if task.Type == FetchTask {
				return true
			}
		}
	}

	return false
}

func (repo *ResticManager) ClearQueue() {
	// Clear the task queue
	repo.Print(logrus.InfoLevel, "Emptying task queue...")

	repo.Mutex.Lock()
	for _, task := range repo.TaskQueue {
		if task.Result != nil {
			task.Result <- ResticResult{TaskID: task.ID, Error: fmt.Errorf("task cancelled due to queue emptying")}
		}
	}

	repo.TaskQueue = make([]*ResticTask, 0)
	repo.Mutex.Unlock()

	repo.Print(logrus.InfoLevel, "Task queue emptied.")
}

func (repo *ResticManager) ShutdownWorker() {
	close(repo.stopCh)
	repo.cond.Signal() // Wake up the cluster goroutine
}

// WaitForResults waits for the task results
func (repo *ResticManager) WaitForResults() {
	// Wait for task completion and handle results
	for result := range repo.ResultChan {
		if result.Error != nil {
			repo.Print(logrus.ErrorLevel, "TaskID: %d Task: %s Error: %v", result.TaskID, GetTaskName(result.TaskType), result.Error)
		} else {
			repo.Print(logrus.InfoLevel, "TaskID: %d Task: %s Result: Completed", result.TaskID, GetTaskName(result.TaskType))
		}
	}
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

	repo.Print(loglevel, "Starting command: %s %v", repo.BinaryPath, args)

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
			repo.Print(logrus.ErrorLevel, prefix+"Error reading output:", err)
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

	repo.Print(loglevel, "Command completed successfully: %s %v", repo.BinaryPath, args)

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

	// Prepare the arguments for the "forget" command
	args := []string{"init"}

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false) // Don't capture output
	if err != nil {
		// Update the repo flag to prevent further fetch attempts
		repo.CanInitRepo = false

		// Handle error (including stderr)
		return fmt.Errorf("failed to init repo: %v, stderr: %s", err, stderr)
	}

	return nil
}

// ResticFetchRepoStat performs the statistic fetch
func (repo *ResticManager) FetchRepoStat() error {
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

// ResticFetchRepo performs the fetch
func (repo *ResticManager) FetchRepo() error {
	// Check if the repo is able to fetch and initialized
	if !repo.GetCanFetch() {
		return nil
	}

	// Check if the repo is initialized
	repopath := repo.GetRepoPath()
	if _, err := os.Stat(filepath.Join(repopath, "config")); os.IsNotExist(err) {
		// Check the repo data
		_, err := os.Stat(filepath.Join(repopath, "data"))
		if os.IsNotExist(err) {
			err = repo.InitRepo(false)
			if err != nil {
				repo.CanInitRepo = false
				return fmt.Errorf("failed to init repo: %w", err)
			}
		} else if err != nil {
			repo.CanInitRepo = false
			return fmt.Errorf("failed to check repo path: %w", err)
		} else {
			repo.CanInitRepo = false
			// Repo data exists, but config does not
			return fmt.Errorf("repo config is missing but data exists")
		}
	} else if err != nil {
		repo.CanInitRepo = false
		return fmt.Errorf("failed to check repo path: %w", err)
	}

	repo.CanInitRepo = true

	// Check latest lock in repository
	err := repo.CheckResticLocks()
	if err != nil {
		return fmt.Errorf("failed to check locks: %w", err)
	}

	// Prevent fetching if there are locks
	if repo.HasLocks {
		return nil
	}

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

	var backups []Backup
	err = json.Unmarshal(stdout, &backups)
	if err != nil {
		return fmt.Errorf("failed to unmarshal backups: %w", err)
	}

	// Update the Backups field with the fetched backups
	repo.Backups = backups

	err = repo.FetchRepoStat()
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

// ResticPurgeRepo performs the actual purging of the repository
func (repo *ResticManager) PurgeRepo(opt ResticPurgeOption) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.PurgeRepo(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)
	// Prepare the arguments for the "forget" command
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
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.CheckResticLocks()
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

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

	if repo.HasLocks != haslock {
		if haslock {
			repo.Print(logrus.InfoLevel, "Repository has locks")
		} else {
			repo.Print(logrus.InfoLevel, "Repository locks has unlocked")
		}
		repo.HasLocks = haslock
	}

	return nil
}

// ResticUnlockRepo unlocks the repository
func (repo *ResticManager) UnlockRepo(force bool) error {
	if force {
		if !repo.GetCanFetch() {
			time.Sleep(time.Second)
			return repo.UnlockRepo(force)
		}

		repo.SetCanFetch(false)
		defer repo.SetCanFetch(true)
	}

	if !repo.HasLocks {
		return nil
	}

	if force {
		repo.Print(logrus.InfoLevel, "Forcing unlock of repository locks")
	}

	return repo.unlockRepo()

}

func (repo *ResticManager) unlockRepo() error {
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
