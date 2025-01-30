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
	PurgeTask TaskType = iota
	BackupTask
	FetchTask
)

func GetTaskName(TaskType TaskType) string {
	switch TaskType {
	case 0:
		return "purge"
	case 1:
		return "backup"
	case 2:
		return "fetch"
	default:
		return "Unknown"
	}
}

// Task represents a queue task
type ResticTask struct {
	ID         int
	DirPath    string
	Tags       []string
	Type       TaskType
	Opt        ResticPurgeOption
	ErrorState state.State
	Stream     bool
	Result     chan ResticResult // Only used if caller needs the result
}

// ResticResult holds the output or error of a task
type ResticResult struct {
	TaskID   int
	TaskType TaskType
	Error    error
}

// ResticPurgeOption holds the configuration for purge
type ResticPurgeOption struct {
	KeepLast    int
	KeepHourly  int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
}

// TaskStatus represents the task state information stored in the JSON flag file
type TaskStatus struct {
	TaskType   TaskType `json:"task_type"`
	StartTime  string   `json:"start_time"`
	Status     string   `json:"status"`                    // e.g., "in-progress", "completed", "failed"
	Completion string   `json:"completion_time,omitempty"` // Only present if completed
}

// CreateTaskFlagFile creates a JSON file that indicates the task status
func (repo *ResticRepo) CreateTaskFlagFile(taskType TaskType) error {
	// Define the directory and file path for the task status JSON file
	if err := os.MkdirAll(repo.CookieDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create task directory: %v", err)
	}

	// Define the JSON filename for the task status
	flagFile := filepath.Join(repo.CookieDir, "task-status.json")

	// Create a TaskStatus struct to store the task's start time and status
	taskStatus := TaskStatus{
		TaskType:  taskType,
		StartTime: time.Now().String(),
		Status:    "in-progress",
	}

	// Open or create the task status file
	file, err := os.Create(flagFile)
	if err != nil {
		return fmt.Errorf("failed to create flag file for task: %v", err)
	}
	defer file.Close()

	// Write the task status as JSON into the file
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(taskStatus); err != nil {
		return fmt.Errorf("failed to write task status to file: %v", err)
	}

	repo.Print(logrus.InfoLevel, "Task flag file created with status 'in-progress'")
	return nil
}

// CheckTaskStatus reads the task status from the JSON flag file
func (repo *ResticRepo) CheckTaskStatus() (*TaskStatus, error) {
	// Define the directory and file path for the task status JSON file
	taskDir := repo.CookieDir
	flagFile := filepath.Join(taskDir, "task-status.json")

	// Open the task status file
	file, err := os.Open(flagFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open task status file: %v", err)
	}
	defer file.Close()

	// Decode the task status from the JSON file
	var taskStatus TaskStatus
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&taskStatus); err != nil {
		return nil, fmt.Errorf("failed to decode task status: %v", err)
	}

	// Return the decoded task status
	return &taskStatus, nil
}

// UpdateTaskFlagFile updates the status of the task in the JSON file
func (repo *ResticRepo) UpdateTaskFlagFile(status string) error {
	// Define the directory and file path for the task status JSON file
	flagFile := filepath.Join(repo.CookieDir, "task-status.json")

	// Open the existing task status file
	file, err := os.OpenFile(flagFile, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open flag file for updating: %v", err)
	}
	defer file.Close()

	// Decode the current task status
	var taskStatus TaskStatus
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&taskStatus); err != nil {
		return fmt.Errorf("failed to decode task status: %v", err)
	}

	// Update the task status and optionally set a completion time
	taskStatus.Status = status
	if status == "completed" {
		taskStatus.Completion = time.Now().String()
	}

	// Move the file pointer back to the start and overwrite the file with the updated status
	file.Seek(0, 0)
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(taskStatus); err != nil {
		return fmt.Errorf("failed to write updated task status: %v", err)
	}

	repo.Print(logrus.InfoLevel, "Task flag file updated to status '%s'", status)
	return nil
}

// ResticRepo manages the queue and execution
type ResticRepo struct {
	BinaryPath  string
	CookieDir   string
	Env         []string
	Backups     []Backup
	BackupStat  BackupStat
	Logger      *logrus.Logger
	LogFields   logrus.Fields
	LogLevel    logrus.Level
	TaskQueue   chan ResticTask
	ResultChan  chan ResticResult
	Shutdown    chan struct{}
	Mutex       sync.Mutex
	CanFetch    bool
	CanInitRepo bool
	HasLocks    bool
	taskID      int
	CurrentID   int
}

// NewResticRepo initializes the repository manager
func NewResticRepo(binaryPath string, logger *logrus.Logger, logfields logrus.Fields, loglevel logrus.Level) *ResticRepo {
	repo := &ResticRepo{
		BinaryPath:  binaryPath,
		Backups:     make([]Backup, 0),
		Logger:      logger,
		LogFields:   logfields,
		LogLevel:    loglevel,
		TaskQueue:   make(chan ResticTask, 10),
		ResultChan:  make(chan ResticResult, 10),
		Shutdown:    make(chan struct{}),
		CanFetch:    true,
		CanInitRepo: true,
	}
	go repo.worker() // Start the worker
	go repo.WaitForResults()
	return repo
}

func (repo *ResticRepo) SetEnv(env []string) {
	repo.Env = env
}

func (repo *ResticRepo) GetRepoPath() string {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	for _, env := range repo.Env {
		if strings.HasPrefix(env, "RESTIC_REPOSITORY") {
			return strings.Split(env, "=")[1]
		}
	}

	return ""
}

// GenerateTaskID ensures unique task IDs
func (repo *ResticRepo) GenerateTaskID() int {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.taskID++
	return repo.taskID
}

// SetCanFetch updates CanFetch flag
func (repo *ResticRepo) SetCanFetch(value bool) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.CanFetch = value
}

// GetCanFetch returns CanFetch value
func (repo *ResticRepo) GetCanFetch() bool {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	return repo.CanFetch
}

func (repo *ResticRepo) SetLogFields(fields logrus.Fields) {
	repo.LogFields = fields
}

// SetLogLevel updates the log level for archive module. This is different with logrus.SetLevel which sets the global log level.
func (repo *ResticRepo) SetLogLevel(level logrus.Level) {
	repo.LogLevel = level
}

func (repo *ResticRepo) Print(level logrus.Level, message string, args ...interface{}) {
	if repo.LogLevel >= level {
		repo.Logger.WithFields(repo.LogFields).Logf(level, message, args...)
	}
}

// worker processes the task queue FIFO
func (repo *ResticRepo) worker() {
	for {
		select {
		case task := <-repo.TaskQueue:
			printLevel := logrus.InfoLevel
			if task.ID == 0 {
				printLevel = logrus.DebugLevel
			}
			// Prevent logging for fetching tasks
			repo.Print(printLevel, "Worker processing task ID: %d", task.ID)

			var result ResticResult
			switch task.Type {
			case PurgeTask:
				err := repo.ResticPurgeRepo(task.Opt)
				result = ResticResult{Error: err}
			case BackupTask:
				err := repo.ResticBackup(task.DirPath, task.Tags)
				result = ResticResult{Error: err}
			case FetchTask:
				err := repo.ResticFetchRepo()
				result = ResticResult{Error: err}
			default:
				result = ResticResult{TaskID: task.ID, Error: fmt.Errorf("unknown task type")}
			}

			// Send result to per-task channel (if waiting)
			if task.Result != nil {
				task.Result <- result
			} else {
				// Otherwise, send result to global log
				repo.ResultChan <- result
			}

			// Log the completion of the task
			repo.Print(printLevel, "Worker finished task ID: %d", task.ID)

		case <-repo.Shutdown:
			repo.Print(logrus.InfoLevel, "Worker shutting down.")
			return
		}
	}
}

func (repo *ResticRepo) AddFetchTask(waitForResult bool) (*ResticResult, error) {
	task := ResticTask{
		Type: FetchTask,
	}

	var resultChan chan ResticResult
	if waitForResult {
		resultChan = make(chan ResticResult, 1) // If waiting, create result channel
		task.Result = resultChan
	}

	repo.TaskQueue <- task // Add task to queue

	if waitForResult {
		result := <-resultChan // Wait for result
		return &result, result.Error
	}
	return nil, nil
}

func (repo *ResticRepo) AddPurgeTask(opt ResticPurgeOption, waitForResult bool) (*ResticResult, error) {
	task := ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: PurgeTask,
		Opt:  opt,
	}

	var resultChan chan ResticResult
	if waitForResult {
		resultChan = make(chan ResticResult, 1) // If waiting, create result channel
		task.Result = resultChan
	}

	repo.TaskQueue <- task // Add task to queue
	repo.Print(logrus.InfoLevel, "Task %d submitted (Wait: %v)", task.ID, waitForResult)

	if waitForResult {
		result := <-resultChan // Wait for result
		return &result, result.Error
	}
	return nil, nil
}

func (repo *ResticRepo) AddBackupTask(taskType TaskType, dirpath string, tags []string, waitForResult bool) (*ResticResult, error) {
	task := ResticTask{
		ID:      repo.GenerateTaskID(),
		Type:    taskType,
		DirPath: dirpath,
		Tags:    tags,
	}

	var resultChan chan ResticResult
	if waitForResult {
		resultChan = make(chan ResticResult, 1) // If waiting, create result channel
		task.Result = resultChan
	}

	repo.TaskQueue <- task // Add task to queue
	repo.Print(logrus.InfoLevel, "Task %d submitted (Wait: %v)", task.ID, waitForResult)

	if waitForResult {
		result := <-resultChan // Wait for result
		return &result, result.Error
	}
	return nil, nil
}

// ShutdownWorker stops the worker gracefully
func (repo *ResticRepo) ShutdownWorker() {
	close(repo.Shutdown)
	close(repo.TaskQueue)
	close(repo.ResultChan)
}

// WaitForResults waits for the task results
func (repo *ResticRepo) WaitForResults() {
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
func (repo *ResticRepo) RunCommand(args []string, loglevel logrus.Level, captureOutput bool) ([]byte, []byte, error) {
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

func (repo *ResticRepo) ResticInitRepo() error {
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
func (repo *ResticRepo) ResticFetchRepoStat() error {
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
func (repo *ResticRepo) ResticFetchRepo() error {
	// Check if the repo is able to fetch and initialized
	if !repo.GetCanFetch() || !repo.CanInitRepo {
		return nil
	}

	// Check latest lock in repository
	err := repo.CheckLocks()
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
			_ = repo.ResticInitRepo()
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

	err = repo.ResticFetchRepoStat()
	if err != nil {
		return fmt.Errorf("failed to fetch repo stat: %w", err)
	}

	return nil // Success
}

// ResticPurgeRepo performs the actual purging of the repository
func (repo *ResticRepo) ResticPurgeRepo(opt ResticPurgeOption) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.ResticPurgeRepo(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Create the task flag file
	err := repo.CreateTaskFlagFile(PurgeTask)
	if err != nil {
		return fmt.Errorf("failed to create flag file for purge task: %v", err)
	}

	// Prepare the arguments for the "forget" command
	args := []string{
		"forget", "--prune",
		"--keep-last", fmt.Sprintf("%d", opt.KeepLast),
		"--keep-hourly", fmt.Sprintf("%d", opt.KeepHourly),
		"--keep-daily", fmt.Sprintf("%d", opt.KeepDaily),
		"--keep-weekly", fmt.Sprintf("%d", opt.KeepWeekly),
		"--keep-monthly", fmt.Sprintf("%d", opt.KeepMonthly),
		"--keep-yearly", fmt.Sprintf("%d", opt.KeepYearly),
	}

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		// Update the flag file to mark the task as completed
		_ = repo.UpdateTaskFlagFile("failed")
		// Handle error (including stderr)
		return fmt.Errorf("failed to purge repo: %v, stderr: %s", err, stderr)
	}

	// Update the flag file to mark the task as completed
	err = repo.UpdateTaskFlagFile("completed")
	if err != nil {
		return fmt.Errorf("failed to update flag file for completed backup task: %v", err)
	}

	return nil
}

// ResticBackup performs the backup (mock implementation for now)
func (repo *ResticRepo) ResticBackup(dirpath string, tags []string) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.ResticBackup(dirpath, tags)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Create the task flag file
	err := repo.CreateTaskFlagFile(BackupTask)
	if err != nil {
		return fmt.Errorf("failed to create flag file for backup task: %v", err)
	}

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
		_ = repo.UpdateTaskFlagFile("failed")
		// Handle error (including stderr)
		return fmt.Errorf("failed to backup repo: %v, stderr: %s", err, stderr)
	}

	// Update the flag file to mark the task as completed
	err = repo.UpdateTaskFlagFile("completed")
	if err != nil {
		return fmt.Errorf("failed to update flag file for completed backup task: %v", err)
	}

	return nil
}

// ResticBackup performs the backup (mock implementation for now)
func (repo *ResticRepo) CheckLocks() error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.CheckLocks()
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "backup" command
	args := []string{"list", "locks", "--no-lock", "-q"}

	// Execute the Restic "list locks" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
		if strings.Contains(string(stderr), "no such file or directory") {
			_ = repo.ResticInitRepo()
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
