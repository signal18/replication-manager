package backupmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"
	"github.com/signal18/replication-manager/utils/s3helper"
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
	RestoreTask
	CheckTask
	CopyTask
)

type MoveType string

const (
	MoveFirst MoveType = "first"
	MoveAfter MoveType = "after"
	MoveLast  MoveType = "last"
)

// initPolicy controls whether automatic repository initialization may be attempted.
type initPolicy int

const (
	// initPolicyPassive classifies repo state only; never attempts auto-init.
	initPolicyPassive initPolicy = iota
	// initPolicyBoot allows one auto-init attempt during manager startup.
	initPolicyBoot
	// initPolicyPreBackup allows an auto-init retry immediately before backup.
	initPolicyPreBackup
)

func GetTaskName(taskType TaskType) string {
	switch taskType {
	case InitTask:
		return "init"
	case FetchTask:
		return "fetch"
	case BackupTask:
		return "backup"
	case PurgeTask:
		return "purge"
	case UnlockTask:
		return "unlock"
	case ChangePassTask:
		return "changepass"
	case RestoreTask:
		return "restore"
	case CheckTask:
		return "check"
	case CopyTask:
		return "copy"
	default:
		return "Unknown"
	}
}

// ResticBackupOption holds the configuration for backup
type ResticBackupOption struct {
	DirPath           string   `json:"dir_path"`
	Tags              []string `json:"tags"`
	Exclude           []string `json:"exclude,omitempty"`             // Exclude patterns
	ExcludeFile       []string `json:"exclude_file,omitempty"`        // Files containing exclude patterns
	ExcludeCaches     bool     `json:"exclude_caches,omitempty"`      // Exclude cache directories
	ExcludeIfPresent  []string `json:"exclude_if_present,omitempty"`  // Exclude dirs containing these files
	ExcludeLargerThan string   `json:"exclude_larger_than,omitempty"` // Max file size (e.g., "100M")
	FilesFrom         []string `json:"files_from,omitempty"`          // Read files to backup from file
	Host              string   `json:"host,omitempty"`                // Override hostname
	Parent            string   `json:"parent,omitempty"`              // Parent snapshot for incremental
	OneFileSystem     bool     `json:"one_file_system,omitempty"`     // Don't cross filesystem boundaries
	IgnoreCtime       bool     `json:"ignore_ctime,omitempty"`        // Ignore ctime changes
	IgnoreInode       bool     `json:"ignore_inode,omitempty"`        // Ignore inode changes
	Time              string   `json:"time,omitempty"`                // Backup timestamp (e.g., '2012-11-01 22:08:41')
	DryRun            bool     `json:"dry_run,omitempty"`             // Don't upload, just show what would be done
}

// ResticUnlockOption holds the configuration for unlock
type ResticUnlockOption struct {
	RemoveAll bool `json:"remove_all,omitempty"` // Remove all locks, including from other hosts
}

// ResticChangePassOption holds the configuration for password change
type ResticChangePassOption struct {
	NewPassFile string `json:"-"`
}

// ResticInitOption holds the configuration for init
type ResticInitOption struct {
	Force             bool   `json:"force,omitempty"`
	AllowEmptyPrefix  bool   `json:"allow_empty_prefix,omitempty"`
	RepositoryVersion string `json:"repository_version,omitempty"` // e.g., "stable", "latest", "1", "2"
	CopyChunkerParams bool   `json:"copy_chunker_params,omitempty"`
	FromRepo          string `json:"from_repo,omitempty"`
}

// ResticFetchOption holds the configuration for fetch
type ResticFetchOption struct {
	SkipStats bool `json:"skip_stats,omitempty"`
}

// ResticCheckOption holds the configuration for repository integrity check
type ResticCheckOption struct {
	ReadData       bool   `json:"read_data,omitempty"`        // Read all data blobs (slow, comprehensive)
	ReadDataSubset string `json:"read_data_subset,omitempty"` // Read subset of data (e.g., "10%", "5G", "1/5")
	WithCache      bool   `json:"with_cache,omitempty"`       // Use cache (default: false for check)
	CheckUnused    bool   `json:"check_unused,omitempty"`     // Check for unused blobs (removed in newer versions)
}

// Task represents a queue task
type ResticTask struct {
	ID            int                     `json:"task_id"`
	Type          TaskType                `json:"task_type"`
	BackupOpt     *ResticBackupOption     `json:"backup_opt,omitempty"`     // Options for BackupTask
	PurgeOpt      *ResticPurgeOption      `json:"purge_opt,omitempty"`      // Options for PurgeTask
	RestoreOpt    *ResticRestoreOption    `json:"restore_opt,omitempty"`    // Options for RestoreTask
	UnlockOpt     *ResticUnlockOption     `json:"unlock_opt,omitempty"`     // Options for UnlockTask
	ChangePassOpt *ResticChangePassOption `json:"changepass_opt,omitempty"` // Options for ChangePassTask
	InitOpt       *ResticInitOption       `json:"init_opt,omitempty"`       // Options for InitTask
	FetchOpt      *ResticFetchOption      `json:"fetch_opt,omitempty"`      // Options for FetchTask
	CheckOpt      *ResticCheckOption      `json:"check_opt,omitempty"`      // Options for CheckTask
	CopyOpt       *ResticCopyOption       `json:"-"`                        // Options for CopyTask — excluded from JSON to prevent credential leakage
	resultCh      chan ResticResult
}

type ResticLsEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

// ResticResult holds the output or error of a task
type ResticResult struct {
	TaskID     int
	TaskType   TaskType
	Error      error
	SnapshotID string // Snapshot ID returned from backup operation
}

// ResticPurgeOption holds the configuration for purge
type ResticPurgeOption struct {
	SnapshotID        string            `json:"snapshot_id,omitempty"`
	GroupBy           string            `json:"group_by,omitempty"`
	KeepLast          int               `json:"keep_last,omitempty"`
	KeepHourly        int               `json:"keep_hourly,omitempty"`
	KeepDaily         int               `json:"keep_daily,omitempty"`
	KeepWeekly        int               `json:"keep_weekly,omitempty"`
	KeepMonthly       int               `json:"keep_monthly,omitempty"`
	KeepYearly        int               `json:"keep_yearly,omitempty"`
	KeepWithin        string            `json:"keep_within,omitempty"`
	KeepWithinHourly  string            `json:"keep_within_hourly,omitempty"`
	KeepWithinDaily   string            `json:"keep_within_daily,omitempty"`
	KeepWithinWeekly  string            `json:"keep_within_weekly,omitempty"`
	KeepWithinMonthly string            `json:"keep_within_monthly,omitempty"`
	KeepWithinYearly  string            `json:"keep_within_yearly,omitempty"`
	KeepTag           []string          `json:"keep_tag,omitempty"`
	Host              []string          `json:"host,omitempty"`
	Tag               []string          `json:"tag,omitempty"`
	Path              []string          `json:"path,omitempty"`
	Compact           bool              `json:"compact,omitempty"` // Enable compact output during forget
	Prune             bool              `json:"prune"`             // When true, runs prune after forget to reclaim space (defaults to true via NewResticPurgeOption)
	PruneOption       ResticPruneOption `json:"prune_option"`
	DryRun            bool              `json:"dry_run,omitempty"`
}

type ResticPruneOption struct {
	MaxUnused           string `json:"max_unused"`            // e.g., "500M", "5G". Empty means omit.
	MaxRepackSize       string `json:"max_repack_size"`       // e.g., "500M", "5G". Empty means omit.
	RepackCacheableOnly bool   `json:"repack_cacheable_only"` // Only repack cacheable blobs
	RepackSmall         bool   `json:"repack_small"`          // Repack small blobs
	RepackUncompressed  bool   `json:"repack_uncompressed"`   // Repack uncompressed blobs
}

// ResticRestoreOption holds the configuration for restore
type ResticRestoreOption struct {
	SnapshotID string              `json:"snapshot_id,omitempty"`
	TargetDir  string              `json:"target_dir,omitempty"` // Target directory for restore
	Include    []string            `json:"include,omitempty"`    // Include patterns
	Exclude    []string            `json:"exclude,omitempty"`    // Exclude patterns
	IInclude   []string            `json:"iinclude,omitempty"`   // Case-insensitive include patterns
	IExclude   []string            `json:"iexclude,omitempty"`   // Case-insensitive exclude patterns
	Overwrite  string              `json:"overwrite,omitempty"`  // Overwrite policy (non-standard, custom implementation)
	Verify     bool                `json:"verify,omitempty"`     // Verify restored files content
	Host       []string            `json:"host,omitempty"`       // Filter by host for "latest" snapshot
	Path       []string            `json:"path,omitempty"`       // Filter by path for "latest" snapshot
	Tag        []string            `json:"tag,omitempty"`        // Filter by tag for "latest" snapshot
	Global     *ResticGlobalOption `json:"global_opt,omitempty"` // Global restic flags
}

// ResticDumpOption holds the configuration for dump
type ResticDumpOption struct {
	SnapshotID string              `json:"snapshot_id,omitempty"`
	FilePath   string              `json:"file_path,omitempty"`
	Archive    string              `json:"archive,omitempty"`    // tar|zip
	Host       []string            `json:"host,omitempty"`       // Filter by host for "latest" snapshot
	Path       []string            `json:"path,omitempty"`       // Filter by path for "latest" snapshot
	Tag        []string            `json:"tag,omitempty"`        // Filter by tag for "latest" snapshot
	Global     *ResticGlobalOption `json:"global_opt,omitempty"` // Global restic flags
}

// ResticGlobalOption holds global restic flags applied to commands
type ResticGlobalOption struct {
	CACert          string   `json:"ca_cert,omitempty"`
	CacheDir        string   `json:"cache_dir,omitempty"`
	CleanupCache    bool     `json:"cleanup_cache,omitempty"`
	Compression     string   `json:"compression,omitempty"`
	InsecureTLS     bool     `json:"insecure_tls,omitempty"`
	JSON            bool     `json:"json,omitempty"`
	KeyHint         string   `json:"key_hint,omitempty"`
	LimitDownload   int      `json:"limit_download,omitempty"`
	LimitUpload     int      `json:"limit_upload,omitempty"`
	NoCache         bool     `json:"no_cache,omitempty"`
	NoLock          bool     `json:"no_lock,omitempty"`
	Option          []string `json:"option,omitempty"`
	PackSize        uint     `json:"pack_size,omitempty"`
	PasswordCommand string   `json:"password_command,omitempty"`
	PasswordFile    string   `json:"password_file,omitempty"`
	Quiet           bool     `json:"quiet,omitempty"`
	Repo            string   `json:"repo,omitempty"`
	RepositoryFile  string   `json:"repository_file,omitempty"`
	TLSClientCert   string   `json:"tls_client_cert,omitempty"`
	Verbose         int      `json:"verbose,omitempty"`
}

// ResticMountOption holds configuration for mount operation
type ResticMountOption struct {
	// Required
	TargetDir string `json:"target_dir"` // Mount point directory (required)

	// Snapshot Filters
	Host []string `json:"host,omitempty"` // Filter by host(s) (-H, --host)
	Tag  []string `json:"tag,omitempty"`  // Filter by tag(s) (--tag)
	Path []string `json:"path,omitempty"` // Filter by path(s) (--path)

	// Path/Time Templates
	PathTemplate []string `json:"path_template,omitempty"` // Template for path names (--path-template, can be multiple)
	TimeTemplate string   `json:"time_template,omitempty"` // Template for times (--time-template, default: "2006-01-02T15:04:05Z07:00")

	// Permission Options
	AllowOther           bool `json:"allow_other,omitempty"`            // Allow other users to access (--allow-other)
	NoDefaultPermissions bool `json:"no_default_permissions,omitempty"` // Ignore Unix permissions (--no-default-permissions)
	OwnerRoot            bool `json:"owner_root,omitempty"`             // Use root as owner (--owner-root)

	// Repository Options
	NoLock  bool `json:"no_lock,omitempty"` // Don't lock repository (--no-lock, default: true)
	Verbose int  `json:"verbose,omitempty"` // Verbosity level (-v, --verbose, 0-3)
	Quiet   bool `json:"quiet,omitempty"`   // Quiet mode (-q, --quiet)
}

type resticMountState struct {
	Path      string    `json:"path"`
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	RepoPath  string    `json:"repoPath"`
	StartedAt time.Time `json:"startedAt"`
}

// NewResticMountOption creates a ResticMountOption with sensible defaults
func NewResticMountOption(targetDir string) ResticMountOption {
	return ResticMountOption{
		TargetDir:  targetDir,
		NoLock:     true, // Default to no-lock for better concurrency
		AllowOther: true, // Default to allow other users
	}
}

// Validate validates mount options for common issues
func (opt *ResticMountOption) Validate() error {
	if opt.TargetDir == "" {
		return fmt.Errorf("target directory is required")
	}

	if opt.Verbose < 0 || opt.Verbose > 3 {
		return fmt.Errorf("verbose level must be 0-3, got %d", opt.Verbose)
	}

	if opt.Quiet && opt.Verbose > 0 {
		return fmt.Errorf("cannot use both --quiet and --verbose")
	}

	// Validate paths are absolute if specified
	for _, path := range opt.Path {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			if !filepath.IsAbs(trimmed) {
				return fmt.Errorf("path filter must be absolute: %s", trimmed)
			}
		}
	}

	return nil
}

// TaskStatus represents the task state information stored in the JSON flag file
type TaskStatus struct {
	TaskType   TaskType `json:"task_type"`
	StartTime  string   `json:"start_time"`
	Status     string   `json:"status"`                    // e.g., "in-progress", "completed", "failed"
	Completion string   `json:"completion_time,omitempty"` // Only present if completed
}

// ResticTaskState tracks the currently running restic task and its progress.
type ResticTaskState struct {
	TaskID      int        `json:"task_id"`
	TaskType    TaskType   `json:"task_type"`
	Status      string     `json:"status"` // running, completed, failed
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	LastUpdate  *time.Time `json:"last_update,omitempty"`

	PercentDone    float64 `json:"percent_done,omitempty"`
	BytesDone      int64   `json:"bytes_done,omitempty"`
	TotalBytes     int64   `json:"total_bytes,omitempty"`
	FilesDone      int     `json:"files_done,omitempty"`
	TotalFiles     int     `json:"total_files,omitempty"`
	SecondsElapsed int     `json:"seconds_elapsed,omitempty"`

	FilesNew            int     `json:"files_new,omitempty"`
	FilesChanged        int     `json:"files_changed,omitempty"`
	FilesUnmodified     int     `json:"files_unmodified,omitempty"`
	DirsNew             int     `json:"dirs_new,omitempty"`
	DirsChanged         int     `json:"dirs_changed,omitempty"`
	DirsUnmodified      int     `json:"dirs_unmodified,omitempty"`
	DataBlobs           int     `json:"data_blobs,omitempty"`
	TreeBlobs           int     `json:"tree_blobs,omitempty"`
	DataAdded           int64   `json:"data_added,omitempty"`
	TotalFilesProcessed int     `json:"total_files_processed,omitempty"`
	TotalBytesProcessed int64   `json:"total_bytes_processed,omitempty"`
	TotalDuration       float64 `json:"total_duration,omitempty"`
	SnapshotID          string  `json:"snapshot_id,omitempty"`

	Phase              string `json:"phase,omitempty"`               // init_destination or copy
	CompletedSnapshots int    `json:"completed_snapshots,omitempty"` // snapshots completed during copy
	TotalSnapshots     int    `json:"total_snapshots,omitempty"`     // total snapshots to copy
	PacksDone          int    `json:"packs_done,omitempty"`          // packs copied (copy task only)
	TotalPacks         int    `json:"total_packs,omitempty"`         // total packs to copy (copy task only)
}

const resticTaskStateTTL = 60 * time.Second

// ResticManager manages the queue and execution
type ResticManager struct {
	BinaryPath           string
	Env                  []string
	envMutex             *sync.RWMutex // Protects Env, read by RunCommand* concurrently with the worker
	AwsAccessKeyID       string
	AwsSecretAccessKey   string
	AwsRegion            string
	AwsEndpoint          string
	AwsBucket            string
	AwsPrefix            string
	Backups              []BackupSnapshot
	BackupMap            map[string]*BackupSnapshot
	BackupStat           BackupStat
	TaskQueue            []*ResticTask
	TaskErrors           map[TaskType]error
	errorMutex           *sync.Mutex
	ResultChan           chan ResticResult
	LogModule            int
	MessageChan          chan sharedlog.Message
	Shutdown             bool
	Mutex                *sync.Mutex
	cond                 *sync.Cond    // Condition variable for waiting and notifying tasks
	stopCh               chan struct{} // Stop channel to signal the goroutine to stop
	CanFetch             bool
	CanInitRepo          bool
	AutoInit             bool
	NeedPurgeNow         bool
	PurgeNowOption       ResticPurgeOption
	isPaused             bool
	isPausedByDisk       bool
	HasLocks             bool
	taskID               int
	CurrentID            int
	mountCmd             *exec.Cmd
	mountPath            string
	mountDone            chan error
	mountPid             int
	unmountRequested     bool                // Set to true when intentional unmount is requested
	mountPinned          bool                // True when mount should remain active until explicitly unpinned
	unmountWhenIdle      bool                // True when mount should auto-unmount after last ref
	unmountInProgress    bool                // True when auto-unmount goroutine is running
	mountRefCount        int                 // Number of active users of the mount
	mountRefMutex        *sync.Mutex         // Protects mount state (cmd/path/pid/done/unmountRequested) and mount users
	mountUsers           map[string]struct{} // Track which operations are using mount (for debugging)
	BackupCount          int                 // Number of backups since last check
	LastCheckTime        time.Time           // Last time repository check was run
	LastFullCheckTime    time.Time           // Last time full data check was run
	DirMode              os.FileMode         // Directory permission mode (default: 0700)
	FileMode             os.FileMode         // File permission mode (default: 0600)
	OperationTimeout     time.Duration       // Timeout for long-running operations (default: 2 hours)
	DumpTimeout          time.Duration       // Timeout for restic dump operations (default: OperationTimeout)
	MountDisabled        bool                // If true, mount operations are disabled (e.g., FUSE unavailable)
	AllowUnsafeMount     bool                // If true, allow using mount created by other process
	MountRecoveryEnabled bool                // If true, recover/cleanup stale mounts on startup
	OnPurgeComplete      func(ResticPurgeOption)
	OnBootFetchSuccess   func() // called once when the startup fetch task succeeds
	currentTask          *ResticTaskState
	currentTaskMutex     *sync.Mutex
	// Error backoff state to prevent log flooding
	lastInitError    error             // Last init error encountered
	initErrorCount   int               // Number of consecutive init errors
	initBackoffUntil time.Time         // Don't retry init until this time
	bootInitDone     bool              // prevents repeated auto-init on subsequent FetchRepo calls
	repoState        ManualCheckStatus // Most recently classified repository state
	// s3existenceProber is a test seam; nil in production.
	// When set, checkS3RepoFiles calls it instead of creating a real S3 client.
	// It receives bucket, configKey, dataPrefix and returns existence + error for each.
	s3existenceProber func(bucket, configKey, dataPrefix string) (configExists bool, configErr error, dataExists bool, dataErr error)
}

// NewResticRepo initializes the repository manager
func NewResticRepo(binaryPath string, msgChan chan sharedlog.Message, logmodule int) *ResticManager {
	repo := &ResticManager{
		BinaryPath:           binaryPath,
		Backups:              make([]BackupSnapshot, 0),
		BackupMap:            make(map[string]*BackupSnapshot),
		MessageChan:          msgChan,
		LogModule:            logmodule,
		TaskQueue:            make([]*ResticTask, 0),
		Mutex:                &sync.Mutex{},
		envMutex:             &sync.RWMutex{},
		currentTaskMutex:     &sync.Mutex{},
		mountRefMutex:        &sync.Mutex{},
		mountUsers:           make(map[string]struct{}),
		TaskErrors:           make(map[TaskType]error),
		errorMutex:           &sync.Mutex{},
		ResultChan:           make(chan ResticResult, 10),
		stopCh:               make(chan struct{}),
		CanFetch:             true,
		CanInitRepo:          true,
		DirMode:              0700,          // Secure default: owner-only directories
		FileMode:             0600,          // Secure default: owner-only files
		OperationTimeout:     2 * time.Hour, // Default: 2 hours for long operations
		MountRecoveryEnabled: true,
	}

	repo.cond = sync.NewCond(repo.Mutex)
	go repo.worker() // Start the worker
	return repo
}

func (repo *ResticManager) UpdateSnapshotList(snapshots []BackupSnapshot) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.Backups = snapshots

	for _, snap := range repo.Backups {
		repo.BackupMap[snap.Id] = &snap
	}
}

// ClearSnapshotList discards the cached snapshot list and stats.
// Used when the repository configuration changes (e.g. backup-archive-mode
// switches to a different backend/path) so stale snapshots from the
// previous repository are no longer displayed until the next fetch.
func (repo *ResticManager) ClearSnapshotList() {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.Backups = make([]BackupSnapshot, 0)
	repo.BackupMap = make(map[string]*BackupSnapshot)
	repo.BackupStat = BackupStat{}
}

func (repo *ResticManager) GetSnapshot(snapshotId string) *BackupSnapshot {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	if len(repo.Backups) == 0 {
		return nil
	}

	for i := range repo.Backups {
		snap := &repo.Backups[i]
		if snap.Id == snapshotId || snap.ShortId == snapshotId {
			return snap
		}
	}

	return nil
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

// GetInitIssue returns the current init error without consuming it.
// It first checks TaskErrors[InitTask], then falls back to lastInitError (which
// persists through backoff periods even after TaskErrors is consumed).
// Returns (error, true) when an issue is present, (nil, false) otherwise.
func (repo *ResticManager) GetInitIssue() (error, bool) {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	if err, exists := repo.TaskErrors[InitTask]; exists && err != nil {
		return err, true
	}
	if repo.lastInitError != nil {
		return repo.lastInitError, true
	}
	return nil, false
}

// GetRepoState returns the most recently classified repository state.
// Returns empty string when the state has not yet been determined.
func (repo *ResticManager) GetRepoState() ManualCheckStatus {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	return repo.repoState
}

func (repo *ResticManager) setRepoState(state ManualCheckStatus) {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	repo.repoState = state
}

// ClearRepoState discards the cached repository state so that the next
// classification attempt (boot, pre-backup, or manual check) starts fresh.
// Call this whenever repo-targeting config changes to prevent a stale
// initialization_required state from suppressing passive fetches indefinitely.
func (repo *ResticManager) ClearRepoState() {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()
	repo.repoState = ""
}

// FetchAndClearErrorsExcept returns and clears all TaskErrors except those for
// the given task types. Excluded tasks are left in the map untouched.
func (repo *ResticManager) FetchAndClearErrorsExcept(excludedTasks ...TaskType) map[TaskType]error {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if len(repo.TaskErrors) == 0 {
		return nil
	}

	excluded := make(map[TaskType]struct{}, len(excludedTasks))
	for _, t := range excludedTasks {
		excluded[t] = struct{}{}
	}

	errs := make(map[TaskType]error)
	for k, v := range repo.TaskErrors {
		if _, skip := excluded[k]; !skip {
			errs[k] = v
		}
	}
	for k := range errs {
		delete(repo.TaskErrors, k)
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// ClearInitErrorBackoffManual clears init error backoff and allows immediate retry
// Useful when configuration has been fixed by the user
func (repo *ResticManager) ClearInitErrorBackoffManual() {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if repo.initErrorCount > 0 {
		repo.Printf(logrus.InfoLevel,
			"Init error backoff manually cleared (was at %d errors, backoff until %v)",
			repo.initErrorCount, repo.initBackoffUntil)
	}

	repo.lastInitError = nil
	repo.initErrorCount = 0
	repo.initBackoffUntil = time.Time{}
}

func (repo *ResticManager) SetEnv(env []string) {
	repo.envMutex.Lock()
	defer repo.envMutex.Unlock()
	repo.Env = env
}

// getEnvCopy returns the process environment combined with repo.Env, suitable
// for assigning to exec.Cmd.Env. It holds envMutex only long enough to copy
// repo.Env, so it can be called freely from RunCommand* without contending
// with SetEnv/UpdateEnvKey for the duration of the command.
func (repo *ResticManager) getEnvCopy() []string {
	repo.envMutex.RLock()
	defer repo.envMutex.RUnlock()
	return append(os.Environ(), repo.Env...)
}

// SetAwsConfig updates AWS settings for S3 repository handling.
func (repo *ResticManager) SetAwsConfig(accessKeyID, secretAccessKey, region, endpoint, bucket, prefix string) {
	repo.AwsAccessKeyID = accessKeyID
	repo.AwsSecretAccessKey = secretAccessKey
	repo.AwsRegion = region
	repo.AwsEndpoint = endpoint
	repo.AwsBucket = bucket
	repo.AwsPrefix = prefix
}

// UpdateEnvKey updates the environment variable for the Restic repository
func (repo *ResticManager) UpdateEnvKey(key, value string) {
	repo.envMutex.Lock()
	defer repo.envMutex.Unlock()

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
	repo.envMutex.RLock()
	defer repo.envMutex.RUnlock()

	for _, env := range repo.Env {
		if strings.HasPrefix(env, "RESTIC_REPOSITORY") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				return parts[1]
			}
		}
	}

	return ""
}

func (repo *ResticManager) GetCacheDirPath() string {
	repo.envMutex.RLock()
	defer repo.envMutex.RUnlock()

	for _, env := range repo.Env {
		if strings.HasPrefix(env, "RESTIC_CACHE_DIR") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				return parts[1]
			}
		}
	}

	return ""
}

// GenerateTaskID ensures unique task IDs
func (repo *ResticManager) GenerateTaskID() int {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

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

// waitCanFetch waits until fetch lock is available or times out.
func (repo *ResticManager) waitCanFetch() error {
	timeout := repo.GetOperationTimeout()
	deadline := time.Now().Add(timeout)

	for !repo.GetCanFetch() {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for restic fetch lock after %v", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// SetPermissions sets the permission modes for restic operations
// dirMode: Directory permission mode (e.g., 0700 for owner-only)
// fileMode: File permission mode (e.g., 0600 for owner-only)
func (repo *ResticManager) SetPermissions(dirMode, fileMode os.FileMode) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.DirMode = dirMode
	repo.FileMode = fileMode
	repo.Printf(logrus.DebugLevel, "Set restic permissions: dir=%#o file=%#o", dirMode, fileMode)
}

// GetPermissions returns the current permission modes
func (repo *ResticManager) GetPermissions() (os.FileMode, os.FileMode) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	dirMode := repo.DirMode
	fileMode := repo.FileMode
	if dirMode == 0 {
		dirMode = 0700 // Secure default
	}
	if fileMode == 0 {
		fileMode = 0600 // Secure default
	}
	return dirMode, fileMode
}

// SetOperationTimeout sets the timeout for long-running operations
// timeout: Duration (e.g., 2*time.Hour for 2 hours)
func (repo *ResticManager) SetOperationTimeout(timeout time.Duration) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.OperationTimeout = timeout
	repo.Printf(logrus.DebugLevel, "Set restic operation timeout: %v", timeout)
}

// GetOperationTimeout returns the operation timeout
func (repo *ResticManager) GetOperationTimeout() time.Duration {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	if repo.OperationTimeout == 0 {
		return 2 * time.Hour // Default: 2 hours
	}
	return repo.OperationTimeout
}

// SetDumpTimeout sets the timeout for restic dump operations
// timeout: Duration (e.g., 30*time.Minute). Zero uses OperationTimeout.
func (repo *ResticManager) SetDumpTimeout(timeout time.Duration) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.DumpTimeout = timeout
	repo.Printf(logrus.DebugLevel, "Set restic dump timeout: %v", timeout)
}

// GetDumpTimeout returns the timeout for restic dump operations
func (repo *ResticManager) GetDumpTimeout() time.Duration {
	repo.Mutex.Lock()
	dumpTimeout := repo.DumpTimeout
	repo.Mutex.Unlock()
	if dumpTimeout > 0 {
		return dumpTimeout
	}
	return repo.GetOperationTimeout()
}

// SetMountDisabled enables or disables mount operations
// Set to true to disable mount operations when FUSE is unavailable
func (repo *ResticManager) SetMountDisabled(disabled bool) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	repo.MountDisabled = disabled
}

// IsMountDisabled returns whether mount operations are disabled
func (repo *ResticManager) IsMountDisabled() bool {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()
	return repo.MountDisabled
}

// CheckFUSEAvailability checks if FUSE is available on the system
// Returns true if FUSE device exists and is accessible
func (repo *ResticManager) CheckFUSEAvailability() bool {
	// Check if /dev/fuse exists (Linux/Unix)
	if _, err := os.Stat("/dev/fuse"); err == nil {
		return true
	}

	// Check if fusermount or fusermount3 is available
	if _, err := exec.LookPath("fusermount"); err == nil {
		return true
	}
	if _, err := exec.LookPath("fusermount3"); err == nil {
		return true
	}

	return false
}

// AutoDetectAndDisableMount checks FUSE availability and disables mount if unavailable
// Returns true if mount operations were disabled
func (repo *ResticManager) AutoDetectAndDisableMount() bool {
	if !repo.CheckFUSEAvailability() {
		repo.SetMountDisabled(true)
		repo.Printf(logrus.WarnLevel, "FUSE not available - mount operations disabled")
		return true
	}
	return false
}

// setRestorePermissions sets secure permissions on restored files and directories
// This walks the entire restored directory tree and applies the configured permissions
func (repo *ResticManager) setRestorePermissions(targetDir string) error {
	dirMode, fileMode := repo.GetPermissions()

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, dirMode)
		}
		return os.Chmod(path, fileMode)
	})

	if err != nil {
		repo.Printf(logrus.WarnLevel, "Failed to set permissions on %s: %v", targetDir, err)
		return nil // Don't fail restore operation
	}

	repo.Printf(logrus.DebugLevel, "Set permissions on restored files (dir=%#o, file=%#o) in %s", dirMode, fileMode, targetDir)
	return nil
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
			// Process purge now without holding the mutex.
			repo.NeedPurgeNow = false
			purgeOption := repo.PurgeNowOption
			repo.Mutex.Unlock()
			repo.PurgeRepo(purgeOption)
			continue
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

		repo.SetCurrentTaskRunning(task)

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
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		case PurgeTask:
			var err error
			if task.PurgeOpt != nil {
				err = repo.PurgeRepo(*task.PurgeOpt)
			} else {
				err = errors.New("PurgeTask requires PurgeOpt to be set")
			}
			_ = repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		case BackupTask:
			var err error
			var snapshotID string
			if task.BackupOpt != nil {
				snapshotID, err = repo.BackupWithOptions(*task.BackupOpt)
				if err == nil && snapshotID != "" {
					// Store snapshot ID in result for retrieval
					result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err, SnapshotID: snapshotID}
				} else {
					result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
				}
			} else {
				err = errors.New("BackupTask requires BackupOpt to be set")
				result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
			}
			_ = repo.FetchRepo()
		case UnlockTask:
			err := repo.UnlockRepo()
			_ = repo.FetchRepo()
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		case RestoreTask:
			var err error
			if task.RestoreOpt != nil {
				err = repo.restoreSnapshotWithOptions(*task.RestoreOpt)
			} else {
				err = errors.New("RestoreTask requires RestoreOpt to be set")
			}
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		case CheckTask:
			// Use CheckOpt if available, otherwise use default (structure check only)
			var opt ResticCheckOption
			if task.CheckOpt != nil {
				opt = *task.CheckOpt
			}
			err := repo.CheckRepo(opt)
			if err == nil {
				// Update check timestamps on success
				isFullCheck := opt.ReadData
				repo.UpdateLastCheckTime(isFullCheck)
			}
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		case CopyTask:
			var err error
			if task.CopyOpt != nil {
				err = repo.CopyRepoWithOptions(*task.CopyOpt)
			} else {
				err = errors.New("CopyTask requires CopyOpt to be set")
			}
			result = ResticResult{TaskID: task.ID, TaskType: task.Type, Error: err}
		default:
			repo.Printf(logrus.WarnLevel, "Unknown task type: %d", task.Type)
			continue
		}

		if result.Error != nil {
			repo.SetError(task.Type, result.Error)
		}

		repo.FinalizeCurrentTask(result)

		if task.resultCh != nil {
			// CRITICAL FIX: Always send result with timeout to prevent goroutine hangs
			// The receiver goroutine in BackupRestic() is waiting on this channel
			// If we use 'default' and skip sending, the receiver gets zero value on close
			sendResult := func() {
				select {
				case task.resultCh <- result:
					// Success - result delivered
				case <-time.After(5 * time.Second):
					// Timeout - receiver may be gone (e.g., during shutdown)
					repo.Printf(logrus.WarnLevel,
						"Timeout sending result for task %d (receiver may be gone)", task.ID)
					// Try to send error result as fallback
					select {
					case task.resultCh <- ResticResult{
						TaskID:   task.ID,
						TaskType: task.Type,
						Error:    fmt.Errorf("result delivery timeout"),
					}:
					default:
						// Give up if channel is full
					}
				}
			}
			sendResult()
			close(task.resultCh)
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

func (repo *ResticManager) prependTask(task *ResticTask) {
	if task == nil {
		return
	}

	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.TaskQueue = append([]*ResticTask{task}, repo.TaskQueue...)

	if task.ID != 0 {
		repo.Printf(logrus.InfoLevel, "Added %s task to the queue, ID: %d", GetTaskName(task.Type), task.ID)
	}

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
		repo.Mutex.Lock()
		if repo.NeedPurgeNow {
			repo.Mutex.Unlock()
			return errors.New("a purge-now task is already scheduled")
		}

		repo.NeedPurgeNow = true
		repo.PurgeNowOption = opt
		repo.cond.Signal()
		repo.Mutex.Unlock()
	} else {
		repo.appendPurgeTask(opt)
	}
	return nil
}

func (repo *ResticManager) RestoreSnapshot(snapshotID, targetDir string, paths []string, overwrite string) error {
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	if targetDir == "" {
		return fmt.Errorf("target dir is empty")
	}

	repo.Mutex.Lock()
	paused := repo.isPaused
	shutdown := repo.Shutdown
	repo.Mutex.Unlock()

	if paused || shutdown {
		return repo.restoreSnapshotWithOptions(ResticRestoreOption{
			SnapshotID: snapshotID,
			TargetDir:  targetDir,
			Include:    paths,
			Overwrite:  overwrite,
		})
	}

	repo.AddRestoreTask(snapshotID, targetDir, paths, overwrite)

	return nil
}

func (repo *ResticManager) RestoreSnapshotSync(snapshotID, targetDir string, paths []string, overwrite string) error {
	return repo.restoreSnapshotWithOptions(ResticRestoreOption{
		SnapshotID: snapshotID,
		TargetDir:  targetDir,
		Include:    paths,
		Overwrite:  overwrite,
	})
}

// RestoreSnapshotSyncWithOptions restores a snapshot using full restore options.
func (repo *ResticManager) RestoreSnapshotSyncWithOptions(opt ResticRestoreOption) error {
	return repo.restoreSnapshotWithOptions(opt)
}

var resticRestoreOverwriteModes = map[string]struct{}{
	"always":                   {},
	"if-changed":               {},
	"if-newer":                 {},
	"if-newer-or-size-changed": {},
}

func normalizeResticRestoreOverwrite(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "", nil
	}
	if _, ok := resticRestoreOverwriteModes[trimmed]; !ok {
		return "", fmt.Errorf("invalid restic overwrite policy: %s", value)
	}
	return trimmed, nil
}

func trimResticValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		trimmed = append(trimmed, item)
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}

func (repo *ResticManager) shortSnapshotID(snapshotID string) string {
	trimmed := strings.TrimSpace(snapshotID)
	if trimmed == "" {
		return ""
	}
	if trimmed == "latest" {
		return trimmed
	}
	if repo != nil {
		snap := repo.GetSnapshot(trimmed)
		if snap != nil {
			shortID := strings.TrimSpace(snap.ShortId)
			if shortID != "" {
				return shortID
			}
		}
	}
	if len(trimmed) > 8 {
		return trimmed[:8]
	}
	return trimmed
}

func appendResticArgs(args []string, flag string, values []string) []string {
	for _, value := range trimResticValues(values) {
		args = append(args, flag, value)
	}
	return args
}

func (repo *ResticManager) buildResticGlobalArgs(opt ResticGlobalOption) []string {
	args := []string{}
	if strings.TrimSpace(opt.CACert) != "" {
		args = append(args, "--cacert", strings.TrimSpace(opt.CACert))
	}
	if strings.TrimSpace(opt.CacheDir) != "" {
		args = append(args, "--cache-dir", strings.TrimSpace(opt.CacheDir))
	}
	if opt.CleanupCache {
		args = append(args, "--cleanup-cache")
	}
	if strings.TrimSpace(opt.Compression) != "" {
		args = append(args, "--compression", strings.TrimSpace(opt.Compression))
	}
	if opt.InsecureTLS {
		args = append(args, "--insecure-tls")
	}
	if opt.JSON {
		args = append(args, "--json")
	}
	if strings.TrimSpace(opt.KeyHint) != "" {
		args = append(args, "--key-hint", strings.TrimSpace(opt.KeyHint))
	}
	if opt.LimitDownload > 0 {
		args = append(args, "--limit-download", fmt.Sprintf("%d", opt.LimitDownload))
	}
	if opt.LimitUpload > 0 {
		args = append(args, "--limit-upload", fmt.Sprintf("%d", opt.LimitUpload))
	}
	if opt.NoCache {
		args = append(args, "--no-cache")
	}
	if opt.NoLock {
		args = append(args, "--no-lock")
	}
	args = appendResticArgs(args, "--option", opt.Option)
	if opt.PackSize > 0 {
		args = append(args, "--pack-size", fmt.Sprintf("%d", opt.PackSize))
	}
	if strings.TrimSpace(opt.PasswordCommand) != "" {
		args = append(args, "--password-command", strings.TrimSpace(opt.PasswordCommand))
	}
	if strings.TrimSpace(opt.PasswordFile) != "" {
		args = append(args, "--password-file", strings.TrimSpace(opt.PasswordFile))
	}
	if opt.Quiet {
		args = append(args, "--quiet")
	}
	if strings.TrimSpace(opt.Repo) != "" {
		args = append(args, "--repo", strings.TrimSpace(opt.Repo))
	}
	if strings.TrimSpace(opt.RepositoryFile) != "" {
		args = append(args, "--repository-file", strings.TrimSpace(opt.RepositoryFile))
	}
	if strings.TrimSpace(opt.TLSClientCert) != "" {
		args = append(args, "--tls-client-cert", strings.TrimSpace(opt.TLSClientCert))
	}
	if opt.Verbose > 0 {
		if opt.Quiet {
			repo.Printf(logrus.WarnLevel, "Ignoring restic verbose flag because quiet is enabled")
		} else if opt.Verbose > 3 {
			repo.Printf(logrus.WarnLevel, "Restic verbose level must be 0-3, got %d", opt.Verbose)
		} else {
			args = append(args, fmt.Sprintf("--verbose=%d", opt.Verbose))
		}
	}
	return args
}

func (repo *ResticManager) restoreSnapshotWithOptions(opt ResticRestoreOption) error {
	snapshotID := strings.TrimSpace(opt.SnapshotID)
	targetDir := strings.TrimSpace(opt.TargetDir)
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	if targetDir == "" {
		return fmt.Errorf("target directory is empty")
	}

	dirMode, _ := repo.GetPermissions()
	if err := os.MkdirAll(targetDir, dirMode); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.restoreSnapshotWithOptions(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	var globalOpt ResticGlobalOption
	if opt.Global != nil {
		globalOpt = *opt.Global
	}

	if !globalOpt.NoLock {
		if err := repo.CheckResticLocks(); err != nil {
			return err
		}
	}

	overwritePolicy, err := normalizeResticRestoreOverwrite(opt.Overwrite)
	if err != nil {
		return err
	}

	args := repo.buildResticGlobalArgs(globalOpt)
	args = append(args, "restore", snapshotID, "--target", targetDir)
	if overwritePolicy != "" {
		args = append(args, "--overwrite", overwritePolicy)
	}
	if opt.Verify {
		args = append(args, "--verify")
	}

	if snapshotID == "latest" {
		args = appendResticArgs(args, "--host", opt.Host)
		args = appendResticArgs(args, "--path", opt.Path)
		args = appendResticArgs(args, "--tag", opt.Tag)
	}

	args = appendResticArgs(args, "--include", opt.Include)
	args = appendResticArgs(args, "--exclude", opt.Exclude)
	args = appendResticArgs(args, "--iinclude", opt.IInclude)
	args = appendResticArgs(args, "--iexclude", opt.IExclude)

	// Add timeout context for long-running restore operations
	timeout := repo.GetOperationTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, stderr, err := repo.RunCommandWithContext(ctx, args, logrus.InfoLevel, false)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("restore operation timeout after %v: %w", timeout, err)
		}
		return fmt.Errorf("failed to restore snapshot: %v, stderr: %s", err, stderr)
	}

	// Set secure permissions on all restored files
	if err := repo.setRestorePermissions(targetDir); err != nil {
		repo.Printf(logrus.WarnLevel, "Permission setting warning: %v", err)
	}

	return nil
}

func (repo *ResticManager) ListSnapshot(snapshotID string, paths []string, recursive bool) ([]ResticLsEntry, error) {
	return repo.ListSnapshotWithLogLevel(snapshotID, paths, recursive, logrus.InfoLevel)
}

func (repo *ResticManager) ListSnapshotWithLogLevel(snapshotID string, paths []string, recursive bool, level logrus.Level) ([]ResticLsEntry, error) {
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

	// Add timeout context for long-running list operations
	timeout := repo.GetOperationTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stdout, stderr, err := repo.RunCommandWithContext(ctx, args, level, true)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("list operation timeout after %v: %w", timeout, err)
		}
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
	return repo.DumpSnapshotWithOptions(ResticDumpOption{
		SnapshotID: snapshotID,
		FilePath:   filePath,
	}, writer, logrus.InfoLevel)
}

func (repo *ResticManager) DumpSnapshotWithLogLevel(snapshotID, filePath string, writer io.Writer, level logrus.Level) error {
	return repo.DumpSnapshotWithOptions(ResticDumpOption{
		SnapshotID: snapshotID,
		FilePath:   filePath,
	}, writer, level)
}

// DumpSnapshotWithOptions dumps a file from a snapshot with full options support.
func (repo *ResticManager) DumpSnapshotWithOptions(opt ResticDumpOption, writer io.Writer, level logrus.Level) error {
	snapshotID := strings.TrimSpace(opt.SnapshotID)
	filePath := strings.TrimSpace(opt.FilePath)
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	if filePath == "" {
		return fmt.Errorf("file path is empty")
	}
	if writer == nil {
		return fmt.Errorf("output writer is nil")
	}

	archive := strings.ToLower(strings.TrimSpace(opt.Archive))
	if archive != "" && archive != "tar" && archive != "zip" {
		return fmt.Errorf("invalid archive format: %s (must be tar or zip)", opt.Archive)
	}

	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.DumpSnapshotWithOptions(opt, writer, level)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	var globalOpt ResticGlobalOption
	if opt.Global != nil {
		globalOpt = *opt.Global
	}

	// Add timeout context for long-running dump operations
	timeout := repo.GetDumpTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := repo.buildResticGlobalArgs(globalOpt)
	args = append(args, "dump")
	if archive != "" {
		args = append(args, "--archive", archive)
	}
	if snapshotID == "latest" {
		args = appendResticArgs(args, "--host", opt.Host)
		args = appendResticArgs(args, "--path", opt.Path)
		args = appendResticArgs(args, "--tag", opt.Tag)
	}
	args = append(args, snapshotID, filePath)

	cmd := exec.CommandContext(ctx, repo.BinaryPath, args...)
	cmd.Env = repo.getEnvCopy()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(level, "Starting command with timeout: %s %v", repo.BinaryPath, args)

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
		// Check if timeout occurred
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("dump operation timeout after %v: %w", timeout, ctx.Err())
		}
		return fmt.Errorf("command execution failed: %w, stderr: %s", err, stderrBuf.Bytes())
	}

	repo.Printf(level, "Command completed successfully: %s %v", repo.BinaryPath, args)
	return nil
}

// DumpSnapshotSyncWithOptions dumps a snapshot using options with default log level.
func (repo *ResticManager) DumpSnapshotSyncWithOptions(opt ResticDumpOption, writer io.Writer) error {
	return repo.DumpSnapshotWithOptions(opt, writer, logrus.InfoLevel)
}

// buildMountArgs constructs restic mount command arguments from options
func (repo *ResticManager) buildMountArgs(opt ResticMountOption) []string {
	args := []string{"mount"}

	// Repository locking
	if opt.NoLock {
		args = append(args, "--no-lock")
	}

	// Verbosity
	if opt.Verbose > 0 {
		// Add multiple -v flags or use --verbose=n
		if opt.Verbose == 1 {
			args = append(args, "-v")
		} else {
			args = append(args, fmt.Sprintf("--verbose=%d", opt.Verbose))
		}
	}

	// Quiet mode
	if opt.Quiet {
		args = append(args, "--quiet")
	}

	// Host filters (can be specified multiple times)
	for _, host := range opt.Host {
		if trimmed := strings.TrimSpace(host); trimmed != "" {
			args = append(args, "--host", trimmed)
		}
	}

	// Tag filters (can be specified multiple times)
	for _, tag := range opt.Tag {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			args = append(args, "--tag", trimmed)
		}
	}

	// Path filters (can be specified multiple times)
	for _, path := range opt.Path {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			args = append(args, "--path", trimmed)
		}
	}

	// Path templates (can be specified multiple times)
	for _, pathTemplate := range opt.PathTemplate {
		if trimmed := strings.TrimSpace(pathTemplate); trimmed != "" {
			args = append(args, "--path-template", trimmed)
		}
	}

	// Time template
	if opt.TimeTemplate != "" {
		args = append(args, "--time-template", strings.TrimSpace(opt.TimeTemplate))
	}

	// Permission options
	if opt.AllowOther {
		args = append(args, "--allow-other")
		repo.Printf(logrus.WarnLevel, "Mount using --allow-other: other users can access mounted data (security consideration)")
	}

	if opt.NoDefaultPermissions {
		args = append(args, "--no-default-permissions")
		if !opt.AllowOther {
			repo.Printf(logrus.WarnLevel, "--no-default-permissions is typically used with --allow-other")
		}
	}

	if opt.OwnerRoot {
		args = append(args, "--owner-root")
		repo.Printf(logrus.InfoLevel, "Mount using --owner-root: all files/dirs will appear owned by root")
	}

	// Target directory must be last
	args = append(args, opt.TargetDir)

	return args
}

type resticMountSnapshot struct {
	cmd              *exec.Cmd
	done             chan error
	path             string
	pid              int
	unmountRequested bool
}

func (repo *ResticManager) mountSnapshotUnsafe() resticMountSnapshot {
	return resticMountSnapshot{
		cmd:              repo.mountCmd,
		done:             repo.mountDone,
		path:             repo.mountPath,
		pid:              repo.mountPid,
		unmountRequested: repo.unmountRequested,
	}
}

func (repo *ResticManager) mountSnapshot() resticMountSnapshot {
	repo.mountRefMutex.Lock()
	defer repo.mountRefMutex.Unlock()
	return repo.mountSnapshotUnsafe()
}

// MountRepoWithOptions mounts the repository with full control over mount options
func (repo *ResticManager) MountRepoWithOptions(opt ResticMountOption) error {
	// Validate options
	if err := opt.Validate(); err != nil {
		return fmt.Errorf("invalid mount options: %w", err)
	}

	// Check if mount operations are disabled
	if repo.IsMountDisabled() {
		return fmt.Errorf("mount operations are disabled (FUSE not available)")
	}

	dirMode, _ := repo.GetPermissions()
	repo.RecoverMountState()
	if isMountReady(opt.TargetDir) && repo.AllowUnsafeMount {
		repo.Printf(logrus.WarnLevel, "Restic mountpoint already mounted at %s; reusing due to unsafe flag", opt.TargetDir)
		repo.mountRefMutex.Lock()
		repo.mountPath = opt.TargetDir
		repo.mountPid = 0
		repo.mountRefMutex.Unlock()
		if err := repo.writeMountState(opt.TargetDir, 0); err != nil {
			repo.Printf(logrus.WarnLevel, "Failed to persist restic mount state: %v", err)
		}
		return nil
	}
	if err := ensureResticMountDir(opt.TargetDir, dirMode); err != nil {
		return fmt.Errorf("failed to create mount dir: %w", err)
	}

	snapshot := repo.mountSnapshot()
	if snapshot.cmd != nil && snapshot.cmd.Process != nil {
		existingPath := snapshot.path
		existingPid := snapshot.cmd.Process.Pid

		// Mount already exists - check if it's the same path
		if existingPath == opt.TargetDir {
			repo.Printf(logrus.DebugLevel, "Restic mount already active at %s, reusing existing mount", existingPath)
			if err := repo.writeMountState(existingPath, existingPid); err != nil {
				repo.Printf(logrus.WarnLevel, "Failed to persist restic mount state: %v", err)
			}
			return nil
		}

		// Different path requested - this is an error
		return fmt.Errorf("restic mount already running at %s (requested: %s)", existingPath, opt.TargetDir)
	}
	if snapshot.path != "" && isMountReady(snapshot.path) {
		existingPath := snapshot.path
		existingPid := snapshot.pid
		if existingPath == opt.TargetDir {
			if existingPid > 0 && isResticMountProcess(existingPid, existingPath) {
				repo.Printf(logrus.DebugLevel, "Restic mount already active at %s (recovered), reusing existing mount", existingPath)
				return nil
			}
			if repo.AllowUnsafeMount {
				repo.Printf(logrus.WarnLevel, "Restic mount at %s not owned by replication-manager; reusing due to unsafe flag", existingPath)
				return nil
			}
			return fmt.Errorf("restic mount already running at %s (not owned by replication-manager)", existingPath)
		}
		return fmt.Errorf("restic mount already running at %s (requested: %s)", existingPath, opt.TargetDir)
	}
	if isMountReady(opt.TargetDir) {
		return fmt.Errorf("restic mountpoint already mounted at %s", opt.TargetDir)
	}

	// Build command arguments
	args := repo.buildMountArgs(opt)
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = repo.getEnvCopy()

	repo.Printf(logrus.InfoLevel, "Starting command: %s %v", repo.BinaryPath, args)
	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("error starting command with pty: %w", err)
	}

	var stderrBuf bytes.Buffer

	repo.mountRefMutex.Lock()
	repo.mountCmd = cmd
	repo.mountPath = opt.TargetDir
	repo.mountPid = cmd.Process.Pid
	repo.mountDone = make(chan error, 1)
	done := repo.mountDone
	repo.mountRefMutex.Unlock()
	if err := repo.writeMountPidFile(cmd.Process.Pid); err != nil {
		repo.Printf(logrus.WarnLevel, "Failed to persist restic mount pid: %v", err)
	}

	go repo.streamMountOutput(ptyFile, "[OUT] ", &stderrBuf)

	go func() {
		err := cmd.Wait()
		_ = ptyFile.Close()

		repo.mountRefMutex.Lock()
		wasIntentional := repo.unmountRequested
		repo.mountRefMutex.Unlock()

		if err != nil {
			// Only log as error if this was not an intentional unmount
			if wasIntentional {
				// Check if it's the expected termination signal
				isExpectedSignal := false
				if exitErr, ok := err.(*exec.ExitError); ok {
					if exitErr.String() == "signal: terminated" {
						isExpectedSignal = true
					}
				}
				if isExpectedSignal {
					repo.Printf(logrus.DebugLevel, "Restic mount terminated as expected")
				} else {
					repo.Printf(logrus.WarnLevel, "Restic mount exited during unmount: %v", err)
				}
			} else {
				// Unexpected exit (not during intentional unmount)
				repo.Printf(logrus.ErrorLevel, "Restic mount exited unexpectedly: %v", err)
			}
		}

		repo.mountRefMutex.Lock()
		done := repo.mountDone
		repo.mountCmd = nil
		repo.mountPath = ""
		repo.mountPid = 0
		repo.unmountRequested = false // Reset the flag
		repo.mountDone = nil
		repo.mountRefMutex.Unlock()

		repo.clearMountState()
		if done != nil {
			select {
			case done <- err:
			default:
			}
			close(done)
		}
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
				return fmt.Errorf("restic mount failed (fuse): exit=%d, err=%v", exitCode, lines)
			}

			return fmt.Errorf("restic mount failed: exit=%d, err=%w", exitCode, err)

		case <-ticker.C:
			// Check if mount point is ready
			if isMountReady(opt.TargetDir) {
				repo.Printf(logrus.InfoLevel, "Restic mount started at %s", opt.TargetDir)
				if err := repo.writeMountState(opt.TargetDir, cmd.Process.Pid); err != nil {
					repo.Printf(logrus.WarnLevel, "Failed to persist restic mount state: %v", err)
				}
				return nil
			}

		case <-timeout:
			// Timeout waiting for mount to be ready
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				time.Sleep(100 * time.Millisecond) // Allow cleanup
			}
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
	stat, err := os.Stat(mountPath)
	if err != nil || !stat.IsDir() {
		return false
	}
	// Ensure mount point is an actual mount (device differs from parent)
	parentPath := filepath.Dir(mountPath)
	if parentPath == mountPath {
		return false
	}
	parentStat, err := os.Stat(parentPath)
	if err != nil {
		return false
	}
	mountSys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	parentSys, ok := parentStat.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if mountSys.Dev != parentSys.Dev {
		return true
	}
	// Fallback: check mountinfo entries for the mount path
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == mountPath {
			return true
		}
	}
	return false
}

func (repo *ResticManager) mountStatePath() string {
	cacheDir := strings.TrimSpace(repo.GetCacheDirPath())
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, "restic_mount_state.json")
}

func (repo *ResticManager) mountPidPath() string {
	cacheDir := strings.TrimSpace(repo.GetCacheDirPath())
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, "restic_mount.pid")
}

func (repo *ResticManager) writeMountState(path string, pid int) error {
	statePath := repo.mountStatePath()
	if statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	state := resticMountState{
		Path:      path,
		PID:       pid,
		Hostname:  hostname,
		RepoPath:  repo.GetRepoPath(),
		StartedAt: time.Now(),
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		return err
	}
	return repo.writeMountPidFile(pid)
}

func (repo *ResticManager) writeMountPidFile(pid int) error {
	pidPath := repo.mountPidPath()
	if pidPath == "" {
		return nil
	}
	if pid <= 0 {
		_ = os.Remove(pidPath)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644)
}

func (repo *ResticManager) readMountPidFile() (int, error) {
	pidPath := repo.mountPidPath()
	if pidPath == "" {
		return 0, fmt.Errorf("mount pid path is empty")
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0, fmt.Errorf("mount pid file is empty")
	}
	pid, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func (repo *ResticManager) clearMountPidFile() {
	pidPath := repo.mountPidPath()
	if pidPath == "" {
		return
	}
	_ = os.Remove(pidPath)
}

func (repo *ResticManager) loadMountState() (*resticMountState, error) {
	statePath := repo.mountStatePath()
	if statePath == "" {
		return nil, fmt.Errorf("mount state path is empty")
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var state resticMountState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (repo *ResticManager) clearMountState() {
	statePath := repo.mountStatePath()
	if statePath == "" {
		return
	}
	_ = os.Remove(statePath)
	repo.clearMountPidFile()
}

func (repo *ResticManager) RecoverMountState() bool {
	state, err := repo.loadMountState()
	if err != nil || state == nil || strings.TrimSpace(state.Path) == "" {
		return false
	}
	if !isMountReady(state.Path) {
		repo.clearMountState()
		return false
	}
	validPid := false
	if state.PID > 0 {
		validPid = isResticMountProcess(state.PID, state.Path)
	}
	repo.mountRefMutex.Lock()
	repo.mountPath = state.Path
	if validPid {
		repo.mountPid = state.PID
	} else {
		repo.mountPid = 0
	}
	pid := repo.mountPid
	repo.mountRefMutex.Unlock()
	repo.Printf(logrus.InfoLevel, "Recovered restic mount state at %s (pid=%d)", state.Path, pid)
	return true
}

// RecoverMountStateOnStartup performs stale mount cleanup then recovers active mounts.
func (repo *ResticManager) RecoverMountStateOnStartup() bool {
	if !repo.MountRecoveryEnabled {
		return repo.RecoverMountState()
	}
	recovered, err := repo.recoverMountStateOnStartup()
	if err != nil {
		repo.Printf(logrus.WarnLevel, "Restic mount recovery encountered issues: %v", err)
	}
	return recovered
}

func (repo *ResticManager) recoverMountStateOnStartup() (bool, error) {
	state, stateErr := repo.loadMountState()
	pidFromFile, pidErr := repo.readMountPidFile()
	if (stateErr != nil || state == nil) && pidErr != nil {
		return false, nil
	}

	mountPath := ""
	pid := 0
	if state != nil {
		mountPath = strings.TrimSpace(state.Path)
		pid = state.PID
	}
	if pid <= 0 && pidFromFile > 0 {
		pid = pidFromFile
	}

	pidMountPath, pidIsRestic := resticMountPathFromPID(pid)
	if mountPath == "" && pidMountPath != "" {
		mountPath = pidMountPath
	}
	if mountPath != "" && pidMountPath != "" && mountPath != pidMountPath {
		repo.Printf(logrus.WarnLevel, "Restic mount state path mismatch (state=%s pid=%s)", mountPath, pidMountPath)
		mountPath = pidMountPath
	}
	if mountPath == "" && pid <= 0 {
		repo.clearMountState()
		return false, nil
	}

	mountReady := mountPath != "" && isMountReady(mountPath)
	pidValid := pid > 0 && mountPath != "" && isResticMountProcess(pid, mountPath)
	if mountReady && pidValid {
		repo.mountRefMutex.Lock()
		repo.mountPath = mountPath
		repo.mountPid = pid
		repo.mountRefMutex.Unlock()
		if err := repo.writeMountState(mountPath, pid); err != nil {
			repo.Printf(logrus.WarnLevel, "Failed to persist restic mount state: %v", err)
		}
		repo.Printf(logrus.InfoLevel, "Recovered restic mount state at %s (pid=%d)", mountPath, pid)
		return true, nil
	}

	return false, repo.cleanupStaleMount(mountPath, pid, mountReady, pidIsRestic, pidMountPath)
}

func (repo *ResticManager) cleanupStaleMount(mountPath string, pid int, mountReady bool, pidIsRestic bool, pidMountPath string) error {
	var errs []string
	if pid > 0 {
		if pidIsRestic {
			if mountPath != "" && pidMountPath != "" && mountPath != pidMountPath {
				repo.Printf(logrus.WarnLevel, "Restic mount pid %d path mismatch (state=%s pid=%s); skipping process kill", pid, mountPath, pidMountPath)
			} else {
				repo.Printf(logrus.WarnLevel, "Stopping stale restic mount process pid=%d", pid)
				if err := repo.stopResticMountProcess(pid); err != nil {
					errs = append(errs, fmt.Sprintf("failed to stop restic mount pid %d: %v", pid, err))
				}
			}
		} else if isProcessRunning(pid) {
			repo.Printf(logrus.WarnLevel, "Mount pid %d does not match restic mount process; leaving it running", pid)
		}
	}

	if mountReady && mountPath != "" {
		repo.Printf(logrus.WarnLevel, "Unmounting stale restic mount at %s", mountPath)
		if err := unmountResticPath(mountPath); err != nil {
			errs = append(errs, fmt.Sprintf("failed to unmount %s: %v", mountPath, err))
		} else {
			repo.Printf(logrus.InfoLevel, "Unmounted stale restic mount at %s", mountPath)
		}
	}

	repo.clearMountState()
	repo.resetMountTracking()

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (repo *ResticManager) resetMountTracking() {
	repo.mountRefMutex.Lock()
	repo.mountCmd = nil
	repo.mountDone = nil
	repo.mountPath = ""
	repo.mountPid = 0
	repo.unmountRequested = false
	repo.mountRefMutex.Unlock()
}

func ensureResticMountDir(targetDir string, mode os.FileMode) error {
	if strings.TrimSpace(targetDir) == "" {
		return fmt.Errorf("mount directory is empty")
	}
	if err := os.MkdirAll(targetDir, mode); err != nil && !os.IsExist(err) {
		return err
	}
	info, statErr := os.Stat(targetDir)
	if statErr != nil {
		if errors.Is(statErr, syscall.ENOTCONN) {
			_ = exec.Command("fusermount", "-u", targetDir).Run()
			_ = unmountPath(targetDir)
			if err := os.MkdirAll(targetDir, mode); err != nil && !os.IsExist(err) {
				return err
			}
			info, statErr = os.Stat(targetDir)
			if statErr == nil {
				// recovered
				goto validateDir
			}
		}
		return statErr
	}

validateDir:
	if !info.IsDir() {
		return fmt.Errorf("mount path exists and is not a directory")
	}
	empty, emptyErr := misc.IsDirEmpty(targetDir)
	if emptyErr != nil {
		return emptyErr
	}
	if !empty {
		return fmt.Errorf("mount directory exists and is not empty")
	}
	return nil
}

func readProcessCmdlineArgs(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid")
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(cmdline), "\x00")
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		args = append(args, part)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty cmdline")
	}
	return args, nil
}

func isResticMountArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	hasMount := false
	for _, arg := range args {
		if arg == "mount" {
			hasMount = true
			break
		}
	}
	if !hasMount {
		return false
	}
	base := filepath.Base(args[0])
	return strings.Contains(base, "restic")
}

func resticMountPathFromArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	idx := -1
	for i, arg := range args {
		if arg == "mount" {
			idx = i
			break
		}
	}
	if idx == -1 || len(args) <= idx+1 {
		return ""
	}
	path := args[len(args)-1]
	if strings.HasPrefix(path, "-") {
		return ""
	}
	return path
}

func resticMountPathFromPID(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	args, err := readProcessCmdlineArgs(pid)
	if err != nil {
		return "", false
	}
	if !isResticMountArgs(args) {
		return "", false
	}
	return resticMountPathFromArgs(args), true
}

func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}

func (repo *ResticManager) stopResticMountProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessRunning(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func unmountResticPath(targetDir string) error {
	path := strings.TrimSpace(targetDir)
	if path == "" {
		return fmt.Errorf("mount path is empty")
	}
	var errs []string
	if bin, err := exec.LookPath("fusermount3"); err == nil {
		if err := exec.Command(bin, "-u", path).Run(); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("fusermount3: %v", err))
		}
	}
	if bin, err := exec.LookPath("fusermount"); err == nil {
		if err := exec.Command(bin, "-u", path).Run(); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("fusermount: %v", err))
		}
	}
	if err := unmountPath(path); err != nil {
		errs = append(errs, fmt.Sprintf("unmount: %v", err))
	} else {
		return nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func isResticMountProcess(pid int, mountPath string) bool {
	if pid <= 0 {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	cmd := string(cmdline)
	return strings.Contains(cmd, "restic") && strings.Contains(cmd, "mount") && strings.Contains(cmd, mountPath)
}

func (repo *ResticManager) UnmountRepo() error {
	// First check if mount exists
	snapshot := repo.mountSnapshot()
	cmd := snapshot.cmd
	done := snapshot.done
	mountPath := snapshot.path
	pid := snapshot.pid
	if cmd == nil || cmd.Process == nil {
		if mountPath == "" {
			repo.RecoverMountState()
			snapshot = repo.mountSnapshot()
			mountPath = snapshot.path
			pid = snapshot.pid
		}
		if mountPath == "" {
			return fmt.Errorf("no restic mount is running")
		}
		if !isMountReady(mountPath) {
			repo.clearMountState()
			repo.mountRefMutex.Lock()
			repo.mountPath = ""
			repo.mountPid = 0
			repo.mountRefMutex.Unlock()
			return fmt.Errorf("no restic mount is running")
		}
		if pid > 0 && isResticMountProcess(pid, mountPath) {
			repo.mountRefMutex.Lock()
			repo.unmountRequested = true
			repo.mountRefMutex.Unlock()
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to stop restic mount: %w", err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if !isMountReady(mountPath) {
					repo.clearMountState()
					repo.mountRefMutex.Lock()
					repo.mountPath = ""
					repo.mountPid = 0
					repo.mountRefMutex.Unlock()
					repo.Printf(logrus.InfoLevel, "Restic mount stopped at %s", mountPath)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("restic mount shutdown timeout")
		}
		if repo.AllowUnsafeMount {
			repo.Printf(logrus.WarnLevel, "Restic mount active but not owned by replication-manager; leaving mount intact")
			return nil
		}
		return fmt.Errorf("restic mount active but not owned by replication-manager")
	}

	// Wait for all active mount users to finish
	timeout := 5 * time.Minute
	deadline := time.Now().Add(timeout)

	repo.Printf(logrus.InfoLevel, "Waiting for active mount users to finish before unmounting...")

	for {
		if repo.CanUnmount() {
			repo.Printf(logrus.DebugLevel, "All mount users finished, proceeding with unmount")
			break
		}

		if time.Now().After(deadline) {
			users := repo.GetMountUsers()
			refCount := repo.GetMountRefCount()
			return fmt.Errorf("unmount timeout: %d active users still using mount: %v", refCount, users)
		}

		// Log active users every 10 seconds
		if time.Now().Unix()%10 == 0 {
			users := repo.GetMountUsers()
			refCount := repo.GetMountRefCount()
			repo.Printf(logrus.InfoLevel, "Waiting for %d active mount users: %v", refCount, users)
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Now mark that we're intentionally unmounting
	repo.mountRefMutex.Lock()
	repo.unmountRequested = true
	repo.mountRefMutex.Unlock()

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
			// Check if this is an expected termination (SIGTERM signal)
			isExpectedTermination := false
			if exitErr, ok := err.(*exec.ExitError); ok {
				// Check if process was terminated by signal (SIGTERM)
				if exitErr.String() == "signal: terminated" {
					isExpectedTermination = true
				}
			}

			if isExpectedTermination {
				// This is expected when we intentionally unmount
				repo.Printf(logrus.DebugLevel, "Restic mount terminated as expected at %s", mountPath)
			} else {
				// Unexpected error during unmount
				repo.Printf(logrus.WarnLevel, "Restic mount exited with unexpected error: %v", err)
				return fmt.Errorf("restic mount exited with error: %w", err)
			}
		}
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("restic mount shutdown timeout")
	}

	repo.Printf(logrus.InfoLevel, "Restic mount stopped at %s", mountPath)
	repo.clearMountState()
	repo.mountRefMutex.Lock()
	repo.mountPath = ""
	repo.mountPid = 0
	repo.mountRefMutex.Unlock()
	return nil
}

func (repo *ResticManager) acquireMountRefUnsafe(userID string) (int, error) {
	if repo.mountUsers == nil {
		repo.mountUsers = make(map[string]struct{})
	}
	if _, exists := repo.mountUsers[userID]; exists {
		return repo.mountRefCount, fmt.Errorf("mount reference already held for user %s", userID)
	}
	repo.mountRefCount++
	repo.mountUsers[userID] = struct{}{}
	return repo.mountRefCount, nil
}

func (repo *ResticManager) releaseMountRefUnsafe(userID string) (int, error) {
	if repo.mountUsers == nil {
		return repo.mountRefCount, fmt.Errorf("mount reference map is uninitialized")
	}
	if _, exists := repo.mountUsers[userID]; !exists {
		return repo.mountRefCount, fmt.Errorf("mount reference not found for user %s", userID)
	}
	delete(repo.mountUsers, userID)
	repo.mountRefCount--
	if repo.mountRefCount < 0 {
		repo.mountRefCount = 0
		return repo.mountRefCount, fmt.Errorf("mount ref count went negative while releasing %s", userID)
	}
	return repo.mountRefCount, nil
}

// AcquireMountRef registers an active user of the mount
// Returns error if mount is not currently active
func (repo *ResticManager) AcquireMountRef(userID string) error {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return fmt.Errorf("cannot acquire mount ref: userID is empty")
	}

	repo.mountRefMutex.Lock()
	if repo.mountUsers == nil {
		repo.mountUsers = make(map[string]struct{})
	}
	if _, exists := repo.mountUsers[trimmed]; exists {
		repo.mountRefMutex.Unlock()
		return fmt.Errorf("mount reference already held for user %s", trimmed)
	}
	cmdMounted := repo.mountCmd != nil && repo.mountCmd.Process != nil
	mountPath := repo.mountPath
	repo.mountRefMutex.Unlock()

	isMounted := cmdMounted
	if !isMounted && repo.AllowUnsafeMount && mountPath != "" {
		isMounted = isMountReady(mountPath)
	}
	if !isMounted {
		return fmt.Errorf("cannot acquire mount ref: mount is not active")
	}

	repo.mountRefMutex.Lock()
	if _, exists := repo.mountUsers[trimmed]; exists {
		repo.mountRefMutex.Unlock()
		return fmt.Errorf("mount reference already held for user %s", trimmed)
	}
	if repo.mountCmd == nil && repo.mountPath == "" {
		repo.mountRefMutex.Unlock()
		return fmt.Errorf("cannot acquire mount ref: mount is not active")
	}
	refCount, err := repo.acquireMountRefUnsafe(trimmed)
	repo.mountRefMutex.Unlock()
	if err != nil {
		return err
	}
	repo.Printf(logrus.DebugLevel, "Acquired mount ref for user %s (refCount=%d)", trimmed, refCount)
	return nil
}

// ReleaseMountRef unregisters an active user of the mount
func (repo *ResticManager) ReleaseMountRef(userID string) error {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return fmt.Errorf("cannot release mount ref: userID is empty")
	}

	repo.mountRefMutex.Lock()
	refCount, err := repo.releaseMountRefUnsafe(trimmed)
	repo.mountRefMutex.Unlock()
	if err != nil {
		return err
	}
	repo.Printf(logrus.DebugLevel, "Released mount ref for user %s (refCount=%d)", trimmed, refCount)
	repo.tryUnmountWhenIdle()
	return nil
}

// SetMountPinned sets whether the mount should remain active until explicitly unpinned.
func (repo *ResticManager) SetMountPinned(pinned bool) {
	repo.mountRefMutex.Lock()
	repo.mountPinned = pinned
	if pinned {
		repo.unmountWhenIdle = false
	}
	repo.mountRefMutex.Unlock()
}

// IsMountPinned reports whether the mount is pinned.
func (repo *ResticManager) IsMountPinned() bool {
	repo.mountRefMutex.Lock()
	defer repo.mountRefMutex.Unlock()
	return repo.mountPinned
}

// RequestUnmountWhenIdle schedules an unmount when no active users remain.
func (repo *ResticManager) RequestUnmountWhenIdle() {
	repo.mountRefMutex.Lock()
	repo.unmountWhenIdle = true
	shouldUnmount := repo.mountRefCount == 0 && !repo.mountPinned && !repo.unmountInProgress
	if shouldUnmount {
		repo.unmountInProgress = true
	}
	repo.mountRefMutex.Unlock()
	if shouldUnmount {
		go repo.unmountWhenIdleAsync()
	}
}

func (repo *ResticManager) tryUnmountWhenIdle() {
	repo.mountRefMutex.Lock()
	shouldUnmount := repo.unmountWhenIdle && repo.mountRefCount == 0 && !repo.mountPinned && !repo.unmountInProgress
	if shouldUnmount {
		repo.unmountInProgress = true
	}
	repo.mountRefMutex.Unlock()
	if shouldUnmount {
		go repo.unmountWhenIdleAsync()
	}
}

func (repo *ResticManager) unmountWhenIdleAsync() {
	err := repo.UnmountRepo()
	repo.mountRefMutex.Lock()
	if err == nil {
		repo.unmountWhenIdle = false
	}
	repo.unmountInProgress = false
	repo.mountRefMutex.Unlock()
	if err != nil {
		repo.Printf(logrus.WarnLevel, "Auto-unmount failed: %v", err)
	}
}

// CanUnmount returns true if no active users are using the mount
func (repo *ResticManager) CanUnmount() bool {
	repo.mountRefMutex.Lock()
	defer repo.mountRefMutex.Unlock()
	return repo.mountRefCount == 0
}

// IsMounted returns true if mount is currently active
func (repo *ResticManager) IsMounted() bool {
	snapshot := repo.mountSnapshot()
	if snapshot.cmd != nil && snapshot.cmd.Process != nil {
		return true
	}
	return repo.AllowUnsafeMount && snapshot.path != "" && isMountReady(snapshot.path)
}

// GetMountPath returns the current mount path (empty if not mounted)
func (repo *ResticManager) GetMountPath() string {
	snapshot := repo.mountSnapshot()
	if snapshot.cmd != nil && snapshot.cmd.Process != nil {
		return snapshot.path
	}
	if repo.AllowUnsafeMount && snapshot.path != "" && isMountReady(snapshot.path) {
		return snapshot.path
	}
	return ""
}

// GetMountRefCount returns the current number of active mount users (for debugging/monitoring)
func (repo *ResticManager) GetMountRefCount() int {
	repo.mountRefMutex.Lock()
	defer repo.mountRefMutex.Unlock()
	return repo.mountRefCount
}

// GetMountUsers returns a copy of active mount users (for debugging/monitoring)
func (repo *ResticManager) GetMountUsers() []string {
	repo.mountRefMutex.Lock()
	defer repo.mountRefMutex.Unlock()
	users := make([]string, 0, len(repo.mountUsers))
	for user := range repo.mountUsers {
		users = append(users, user)
	}
	return users
}

func (repo *ResticManager) streamMountOutput(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		if buffer != nil {
			buffer.WriteString(line + "\n")
		}
		repo.Printf(logrus.DebugLevel, "%s%s", prefix, line)
	}
	if err := scanner.Err(); err != nil {
		// Check if unmount was requested
		repo.mountRefMutex.Lock()
		wasIntentional := repo.unmountRequested
		repo.mountRefMutex.Unlock()

		// PTY read errors are expected when mount process is terminated
		// Only log as error if this wasn't an intentional unmount
		if wasIntentional {
			// Expected error during intentional unmount (PTY closed)
			repo.Printf(logrus.DebugLevel, "%sOutput stream closed (expected during unmount)", prefix)
		} else {
			// Unexpected error reading output
			repo.Printf(logrus.ErrorLevel, prefix+"Error reading output: %v", err)
		}
	}
}

func (repo *ResticManager) appendPurgeTask(opt ResticPurgeOption) {
	task := ResticTask{
		ID:       repo.GenerateTaskID(),
		Type:     PurgeTask,
		PurgeOpt: &opt,
	}

	// Add task to slice
	repo.appendTask(&task)
}

func (repo *ResticManager) AddBackupTask(dirpath string, tags []string, host string) {
	repo.appendTask(&ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: BackupTask,
		BackupOpt: &ResticBackupOption{
			DirPath: dirpath,
			Tags:    tags,
			Host:    strings.TrimSpace(host),
		},
	})
}

// AddBackupTaskWithCallback adds a backup task and returns a channel to receive the result
func (repo *ResticManager) AddBackupTaskWithCallback(dirpath string, tags []string, host string) <-chan ResticResult {
	resultCh := make(chan ResticResult, 1)
	repo.appendTask(&ResticTask{
		ID:   repo.GenerateTaskID(),
		Type: BackupTask,
		BackupOpt: &ResticBackupOption{
			DirPath: dirpath,
			Tags:    tags,
			Host:    strings.TrimSpace(host),
		},
		resultCh: resultCh,
	})
	return resultCh
}

func (repo *ResticManager) AddUnlockTask() {
	task := ResticTask{
		ID:        repo.GenerateTaskID(),
		Type:      UnlockTask,
		UnlockOpt: &ResticUnlockOption{},
	}
	repo.appendTask(&task)
}

func (repo *ResticManager) AddRestoreTask(snapshotID, targetDir string, paths []string, overwrite string) {
	opt := ResticRestoreOption{
		SnapshotID: snapshotID,
		TargetDir:  targetDir,
		Include:    paths,
		Overwrite:  overwrite,
	}
	repo.AddRestoreTaskWithOptions(opt)
}

// AddRestoreTaskWithOptions queues a restore task using a full option struct.
func (repo *ResticManager) AddRestoreTaskWithOptions(opt ResticRestoreOption) {
	task := &ResticTask{
		ID:         repo.GenerateTaskID(),
		Type:       RestoreTask,
		RestoreOpt: &opt,
		resultCh:   make(chan ResticResult, 1),
	}

	repo.prependTask(task)
}

func (repo *ResticManager) AddCheckTask(opt ResticCheckOption) {
	task := &ResticTask{
		ID:       repo.GenerateTaskID(),
		Type:     CheckTask,
		CheckOpt: &opt,
	}
	repo.appendTask(task)
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

// CancelTask removes a task from the queue by its task ID.
// This function is safe against out-of-bounds access by using the array index
// rather than the task ID for slice operations.
func (repo *ResticManager) CancelTask(taskId int) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.Printf(logrus.InfoLevel, "Cancelling restic task ID: %d", taskId)

	// Find the task and track its index in the queue (not the task ID)
	for i, task := range repo.TaskQueue {
		if task.ID == taskId {
			repo.TaskQueue = append(repo.TaskQueue[:i], repo.TaskQueue[i+1:]...)
			repo.Printf(logrus.InfoLevel, "Cancelled restic task ID: %d", taskId)
			return
		}
	}

	repo.Printf(logrus.WarnLevel, "Restic task ID not found: %d", taskId)
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

// getEnvValue extracts value from repo.Env slice by key
func (repo *ResticManager) getEnvValue(key string) string {
	repo.envMutex.RLock()
	defer repo.envMutex.RUnlock()

	prefix := key + "="
	for _, env := range repo.Env {
		if strings.HasPrefix(env, prefix) {
			return strings.TrimPrefix(env, prefix)
		}
	}
	return ""
}

// parseS3URL parses S3 repository URL into components
// Supports formats:
//   - s3:https://endpoint/bucket/prefix (MinIO/custom endpoint)
//   - s3:http://endpoint/bucket/prefix (MinIO dev)
//   - s3:endpoint:port/bucket/prefix (MinIO shorthand)
//   - s3:bucket/prefix (AWS implicit)
//
// Returns: bucket, prefix, endpoint, error
func parseS3URL(repoPath string) (bucket, prefix, endpoint string, err error) {
	// Remove "s3:" prefix
	if !strings.HasPrefix(repoPath, "s3:") {
		return "", "", "", fmt.Errorf("not an S3 URL (missing s3: prefix)")
	}

	path := strings.TrimPrefix(repoPath, "s3:")

	// Handle protocol-prefixed URLs
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		// Format: s3:https://endpoint/bucket/prefix
		// Extract protocol
		var protocol string
		if strings.HasPrefix(path, "https://") {
			protocol = "https://"
			path = strings.TrimPrefix(path, "https://")
		} else {
			protocol = "http://"
			path = strings.TrimPrefix(path, "http://")
		}

		// Now split by / to get endpoint, bucket, prefix
		parts := strings.SplitN(path, "/", 3)
		if len(parts) < 2 {
			return "", "", "", fmt.Errorf("invalid S3 URL format: %s", repoPath)
		}

		endpoint = protocol + parts[0]
		bucket = parts[1]
		if len(parts) > 2 {
			prefix = parts[2]
		}

	} else if strings.Contains(path, "://") {
		return "", "", "", fmt.Errorf("unsupported protocol in S3 URL: %s", repoPath)

	} else {
		// Format: s3:endpoint/bucket/prefix OR s3:bucket/prefix
		parts := strings.SplitN(path, "/", 3)

		if len(parts) < 2 {
			return "", "", "", fmt.Errorf("invalid S3 URL format (need at least bucket/path): %s", repoPath)
		}

		// Detect endpoint vs bucket by looking for ":" or "." (but not *.amazonaws.com)
		firstPart := parts[0]
		isEndpoint := strings.Contains(firstPart, ":") ||
			(strings.Contains(firstPart, ".") && !strings.HasSuffix(firstPart, ".amazonaws.com"))

		if isEndpoint {
			// Format: s3:endpoint:port/bucket/prefix
			endpoint = firstPart
			if !strings.Contains(endpoint, "://") {
				endpoint = "https://" + endpoint
			}
			bucket = parts[1]
			if len(parts) > 2 {
				prefix = parts[2]
			}
		} else {
			// Format: s3:bucket/prefix (implicit AWS)
			endpoint = ""
			bucket = firstPart
			if len(parts) > 1 {
				// Join all remaining parts as prefix
				prefix = strings.Join(parts[1:], "/")
			}
		}
	}

	// Validate
	if bucket == "" {
		return "", "", "", fmt.Errorf("bucket name is empty in S3 URL: %s", repoPath)
	}

	// Normalize prefix (remove leading/trailing slashes)
	prefix = strings.Trim(prefix, "/")

	return bucket, prefix, endpoint, nil
}

func (repo *ResticManager) resolveS3RepoSpec(repopath string) (bucket, prefix, endpoint string, err error) {
	bucket = strings.TrimSpace(repo.AwsBucket)
	prefix = strings.Trim(repo.AwsPrefix, "/")
	endpoint = strings.TrimSpace(repo.AwsEndpoint)
	if bucket == "" {
		return parseS3URL(repopath)
	}
	return bucket, prefix, endpoint, nil
}

// checkS3RepoFiles verifies S3 repository structure without attempting auto-init.
func (repo *ResticManager) checkS3RepoFiles(bucket, prefix, endpoint string) error {
	return repo.checkS3RepoFilesWithPolicy(bucket, prefix, endpoint, initPolicyPassive)
}

// checkS3RepoFilesWithPolicy verifies S3 repository structure.
// When policy is not passive and AutoInit is true, it may attempt initialization.
func (repo *ResticManager) checkS3RepoFilesWithPolicy(bucket, prefix, endpoint string, policy initPolicy) error {
	configKey := "config"
	if prefix != "" {
		configKey = prefix + "/config"
	}
	dataPrefix := "data/"
	if prefix != "" {
		dataPrefix = prefix + "/data/"
	}

	endpointDisplay := endpoint
	if endpointDisplay == "" {
		endpointDisplay = "AWS S3"
	}
	repo.Printf(logrus.DebugLevel, "Checking S3 repository: endpoint=%s bucket=%s prefix=%s",
		endpointDisplay, bucket, prefix)

	// Probe existence: use test seam if set, otherwise use real S3 client.
	var configExists bool
	var getDataExists func() (bool, error)

	if repo.s3existenceProber != nil {
		ce, ceErr, de, deErr := repo.s3existenceProber(bucket, configKey, dataPrefix)
		if ceErr != nil {
			repo.CanInitRepo = false
			repo.SetError(InitTask, ceErr)
			repo.setInitErrorBackoff(ceErr)
			repo.setRepoState(ManualCheckStatusError)
			return ceErr
		}
		configExists = ce
		getDataExists = func() (bool, error) { return de, deErr }
	} else {
		client, err := s3helper.NewClient(repo.AwsAccessKeyID, repo.AwsSecretAccessKey, "", repo.AwsRegion, endpoint)
		if err != nil {
			repo.CanInitRepo = false
			err = fmt.Errorf("failed to create S3 client: %w", err)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			repo.setRepoState(ManualCheckStatusError)
			return err
		}
		var checkErr error
		configExists, checkErr = s3helper.CheckObjectExists(client, bucket, configKey)
		if checkErr != nil {
			repo.CanInitRepo = false
			repo.SetError(InitTask, checkErr)
			repo.setInitErrorBackoff(checkErr)
			repo.setRepoState(ManualCheckStatusError)
			return checkErr
		}
		getDataExists = func() (bool, error) {
			return s3helper.ListPrefixHasRealObjects(client, bucket, dataPrefix)
		}
	}

	if !configExists {
		// Config missing - check for data directory
		errstr := "repo config is missing"

		dataExists, err := getDataExists()
		if err != nil {
			repo.CanInitRepo = false
			err = fmt.Errorf("%s and failed to check repo data: %w", errstr, err)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			repo.setRepoState(ManualCheckStatusError)
			return err
		}

		if dataExists {
			// Data exists without config - corrupted repo
			errstr += " but data exists"
			repo.CanInitRepo = false
			err = errors.New(errstr)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			repo.setRepoState(ManualCheckStatusError)
			return err
		}

		// No config, no data - fresh repository
		repo.CanInitRepo = true
		if policy != initPolicyPassive && repo.AutoInit {
			return repo.InitRepoWithOptions(ResticInitOption{})
		}
		repo.setRepoState(ManualCheckStatusInitRequired)
		err = errors.New("repository initialization required: " + errstr)
		repo.SetError(InitTask, err)
		repo.setInitErrorBackoff(err)
		return err
	}

	// Config exists
	repo.CanInitRepo = true
	delete(repo.TaskErrors, InitTask)
	repo.clearInitErrorBackoff()
	repo.setRepoState(ManualCheckStatusOK)

	return nil
}

// setInitErrorBackoff records an init error and sets exponential backoff
func (repo *ResticManager) setInitErrorBackoff(err error) {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	now := time.Now()

	// Check if this is the same error repeating
	if repo.lastInitError != nil && repo.lastInitError.Error() == err.Error() {
		repo.initErrorCount++
	} else {
		// New error type, reset count
		repo.initErrorCount = 1
	}

	repo.lastInitError = err
	// Exponential backoff: 10s, 30s, 1m, 2m, 5m, 10m, 30m (max)
	var backoff time.Duration
	switch {
	case repo.initErrorCount == 1:
		backoff = 10 * time.Second
	case repo.initErrorCount == 2:
		backoff = 30 * time.Second
	case repo.initErrorCount == 3:
		backoff = 1 * time.Minute
	case repo.initErrorCount == 4:
		backoff = 2 * time.Minute
	case repo.initErrorCount == 5:
		backoff = 5 * time.Minute
	case repo.initErrorCount == 6:
		backoff = 10 * time.Minute
	default:
		backoff = 30 * time.Minute
	}

	repo.initBackoffUntil = now.Add(backoff)

	// Log only once per unique error or at increasing intervals
	if repo.initErrorCount == 1 || repo.initErrorCount%10 == 0 {
		repo.Printf(logrus.ErrorLevel,
			"Repository initialization failed (attempt %d), backing off for %v: %v",
			repo.initErrorCount, backoff, err)
	}
}

// clearInitErrorBackoff clears the init error backoff state on success
func (repo *ResticManager) clearInitErrorBackoff() {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if repo.initErrorCount > 0 {
		repo.Printf(logrus.InfoLevel,
			"Repository initialization succeeded after %d failed attempts",
			repo.initErrorCount)
	}

	repo.lastInitError = nil
	repo.initErrorCount = 0
	repo.initBackoffUntil = time.Time{}
}

// shouldSkipInitDueToBackoff returns true if init should be skipped due to backoff
func (repo *ResticManager) shouldSkipInitDueToBackoff() bool {
	repo.errorMutex.Lock()
	defer repo.errorMutex.Unlock()

	if repo.initBackoffUntil.IsZero() {
		return false
	}

	now := time.Now()
	if now.Before(repo.initBackoffUntil) {
		// Still in backoff period
		return true
	}

	// Backoff period expired, allow retry
	return false
}

// CheckRepoFiles validates repository state without attempting auto-init.
// It is safe to call from periodic/background paths.
func (repo *ResticManager) CheckRepoFiles() error {
	return repo.checkRepoFilesWithPolicy(initPolicyPassive)
}

// checkRepoFilesWithPolicy validates repository state and optionally attempts
// automatic initialization depending on policy:
//   - initPolicyPassive: classify state only; respects backoff; never inits
//   - initPolicyBoot: may init once at startup; bypasses backoff
//   - initPolicyPreBackup: may init before backup; bypasses backoff
func (repo *ResticManager) checkRepoFilesWithPolicy(policy initPolicy) error {
	// Passive mode respects backoff to avoid log flooding from periodic checks.
	// Boot and pre-backup policies bypass backoff so they can retry.
	if policy == initPolicyPassive && repo.shouldSkipInitDueToBackoff() {
		repo.errorMutex.Lock()
		cachedErr := repo.lastInitError
		repo.errorMutex.Unlock()
		if cachedErr != nil {
			return cachedErr
		}
		return errors.New("repository initialization in backoff period")
	}

	repopath := repo.GetRepoPath()
	if config.IsS3ResticRepository(repopath) || repo.AwsBucket != "" {
		bucket, prefix, endpoint, err := repo.resolveS3RepoSpec(repopath)
		if err != nil {
			repo.CanInitRepo = false
			err = fmt.Errorf("failed to parse S3 repo path: %w", err)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			return err
		}
		if policy != initPolicyPassive {
			client, err := s3helper.NewClient(repo.AwsAccessKeyID, repo.AwsSecretAccessKey, "", repo.AwsRegion, endpoint)
			if err != nil {
				repo.CanInitRepo = false
				err = fmt.Errorf("failed to create S3 client: %w", err)
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
			if err := s3helper.EnsurePrefixMarker(client, bucket, prefix); err != nil {
				repo.CanInitRepo = false
				err = fmt.Errorf("failed to create S3 prefix marker: %w", err)
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
		}
		return repo.checkS3RepoFilesWithPolicy(bucket, prefix, endpoint, policy)
	}

	if config.IsSftpResticRepository(repopath) {
		// Repo lives on a remote host via SSH/SFTP; skip local filesystem
		// existence checks and let actual restic commands surface
		// init-required errors naturally.
		// Do not clear prior init failure state — a passive SFTP check cannot
		// confirm the repository is healthy.
		repo.CanInitRepo = true
		return nil
	}

	// Local filesystem check
	if _, err := os.Stat(filepath.Join(repopath, "config")); os.IsNotExist(err) {
		errstr := "repo config is missing"
		_, err := os.Stat(filepath.Join(repopath, "data"))
		if err == nil {
			errstr += " but data exists"
			repo.CanInitRepo = false
			err = errors.New(errstr)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			repo.setRepoState(ManualCheckStatusError)
			return err
		} else if err != nil && !os.IsNotExist(err) {
			errstr += " and failed to check repo data: " + err.Error()
			repo.CanInitRepo = false
			err = errors.New(errstr)
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			repo.setRepoState(ManualCheckStatusError)
			return err
		}
		// Fresh repo: no config, no data.
		repo.CanInitRepo = true
		if policy != initPolicyPassive && repo.AutoInit {
			return repo.InitRepoWithOptions(ResticInitOption{})
		}
		repo.setRepoState(ManualCheckStatusInitRequired)
		err = errors.New("repository initialization required: " + errstr)
		repo.SetError(InitTask, err)
		return err
	} else if err != nil {
		repo.CanInitRepo = false
		err = fmt.Errorf("failed to check repo config: %w", err)
		repo.SetError(InitTask, err)
		repo.setRepoState(ManualCheckStatusError)
		return err
	}

	repo.CanInitRepo = true
	delete(repo.TaskErrors, InitTask)
	repo.clearInitErrorBackoff()
	repo.setRepoState(ManualCheckStatusOK)

	return nil
}

// ManualCheckStatus indicates the outcome of a manual repository validation.
type ManualCheckStatus string

const (
	ManualCheckStatusOK           ManualCheckStatus = "ok"
	ManualCheckStatusInitRequired ManualCheckStatus = "initialization_required"
	ManualCheckStatusError        ManualCheckStatus = "error"
)

// ManualCheckResult holds the outcome of a manual repository configuration check.
type ManualCheckResult struct {
	Status      ManualCheckStatus `json:"status"`
	Message     string            `json:"message"`
	CanInit     bool              `json:"can_init"`
	FetchQueued bool              `json:"fetch_queued"`
	RepoPath    string            `json:"repo_path,omitempty"`
}

// isResticInitRequiredError returns true when restic stderr indicates the repository
// is not yet initialized (as opposed to a credential or network failure).
func isResticInitRequiredError(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "repository does not exist") ||
		strings.Contains(lower, "is there a repository") ||
		strings.Contains(lower, "please initialize the repository")
}

// validateRepoAccessReadOnly runs a lightweight read-only restic snapshots command to
// confirm live repository access. Returns initRequired=true when stderr signals the
// repository is not initialized, or a non-nil accessErr on any other failure.
func (repo *ResticManager) validateRepoAccessReadOnly() (initRequired bool, accessErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, stderr, runErr := repo.RunCommandWithContext(ctx, []string{"snapshots", "--no-lock", "--json"}, logrus.DebugLevel, false)
	if runErr != nil {
		stderrStr := strings.TrimSpace(string(stderr))
		if isResticInitRequiredError(stderrStr) {
			return true, nil
		}
		if stderrStr != "" {
			return false, errors.New(stderrStr)
		}
		return false, runErr
	}
	return false, nil
}

// validateLocalRepoManual classifies a local filesystem repository state read-only and
// confirms access with a restic command when the config file is present.
func (repo *ResticManager) validateLocalRepoManual(repoPath string) ManualCheckResult {
	_, configErr := os.Stat(filepath.Join(repoPath, "config"))
	if os.IsNotExist(configErr) {
		_, dataErr := os.Stat(filepath.Join(repoPath, "data"))
		if dataErr == nil {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: "repository config is missing but data directory exists (corrupt repository)",
				CanInit: false,
			}
		} else if os.IsNotExist(dataErr) {
			return ManualCheckResult{
				Status:  ManualCheckStatusInitRequired,
				Message: "repository is not yet initialized",
				CanInit: true,
			}
		}
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: fmt.Sprintf("failed to check repository data directory: %v", dataErr),
			CanInit: false,
		}
	} else if configErr != nil {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: fmt.Sprintf("failed to check repository config: %v", configErr),
			CanInit: false,
		}
	}

	initRequired, err := repo.validateRepoAccessReadOnly()
	if initRequired {
		return ManualCheckResult{
			Status:  ManualCheckStatusInitRequired,
			Message: "repository is not yet initialized",
			CanInit: true,
		}
	}
	if err != nil {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: err.Error(),
			CanInit: false,
		}
	}
	return ManualCheckResult{
		Status:  ManualCheckStatusOK,
		Message: "repository configuration verified",
		CanInit: true,
	}
}

// validateS3RepoManual classifies an S3 repository state using read-only probes only
// (no EnsurePrefixMarker writes) and confirms access with a restic command when the
// config object is present.
func (repo *ResticManager) validateS3RepoManual(repoPath string) ManualCheckResult {
	bucket, prefix, endpoint, err := repo.resolveS3RepoSpec(repoPath)
	if err != nil {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: fmt.Sprintf("failed to parse S3 repository URL: %v", err),
		}
	}

	configKey := "config"
	if prefix != "" {
		configKey = prefix + "/config"
	}
	dataPrefix := "data/"
	if prefix != "" {
		dataPrefix = prefix + "/data/"
	}

	var configExists bool
	var dataExists bool

	if repo.s3existenceProber != nil {
		ce, ceErr, de, deErr := repo.s3existenceProber(bucket, configKey, dataPrefix)
		if ceErr != nil {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: fmt.Sprintf("failed to probe S3 config key: %v", ceErr),
				CanInit: false,
			}
		}
		if deErr != nil {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: fmt.Sprintf("failed to probe S3 data prefix: %v", deErr),
				CanInit: false,
			}
		}
		configExists = ce
		dataExists = de
	} else {
		client, clientErr := s3helper.NewClient(repo.AwsAccessKeyID, repo.AwsSecretAccessKey, "", repo.AwsRegion, endpoint)
		if clientErr != nil {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: fmt.Sprintf("failed to create S3 client: %v", clientErr),
				CanInit: false,
			}
		}
		configExists, err = s3helper.CheckObjectExists(client, bucket, configKey)
		if err != nil {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: fmt.Sprintf("failed to check S3 config key: %v", err),
				CanInit: false,
			}
		}
		if !configExists {
			dataExists, err = s3helper.ListPrefixHasRealObjects(client, bucket, dataPrefix)
			if err != nil {
				return ManualCheckResult{
					Status:  ManualCheckStatusError,
					Message: fmt.Sprintf("failed to check S3 data prefix: %v", err),
					CanInit: false,
				}
			}
		}
	}

	if !configExists {
		if dataExists {
			return ManualCheckResult{
				Status:  ManualCheckStatusError,
				Message: "S3 repository config is missing but data objects exist (corrupt repository)",
				CanInit: false,
			}
		}
		return ManualCheckResult{
			Status:  ManualCheckStatusInitRequired,
			Message: "S3 repository is not yet initialized",
			CanInit: true,
		}
	}

	initRequired, err := repo.validateRepoAccessReadOnly()
	if initRequired {
		return ManualCheckResult{
			Status:  ManualCheckStatusInitRequired,
			Message: "S3 repository is not yet initialized",
			CanInit: true,
		}
	}
	if err != nil {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: err.Error(),
			CanInit: false,
		}
	}
	return ManualCheckResult{
		Status:  ManualCheckStatusOK,
		Message: "S3 repository configuration verified",
		CanInit: true,
	}
}

// ValidateRepoConfigManual performs a read-only manual check of the current repository
// configuration. It never attempts auto-initialization and never alters auto-init
// policy state or error backoff state. It updates repoState so that the general
// fetch suppression guard reflects the latest manual classification.
func (repo *ResticManager) ValidateRepoConfigManual() ManualCheckResult {
	result := repo.classifyRepoManual()
	repo.setRepoState(result.Status)
	return result
}

// classifyRepoManual performs the read-only classification logic for ValidateRepoConfigManual.
func (repo *ResticManager) classifyRepoManual() ManualCheckResult {
	repoPath := repo.GetRepoPath()
	if strings.TrimSpace(repoPath) == "" {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: "repository path is not configured",
			CanInit: false,
		}
	}
	if strings.TrimSpace(repo.BinaryPath) == "" {
		return ManualCheckResult{
			Status:  ManualCheckStatusError,
			Message: "restic binary path is not configured",
			CanInit: false,
		}
	}

	if config.IsS3ResticRepository(repoPath) || repo.AwsBucket != "" {
		result := repo.validateS3RepoManual(repoPath)
		result.RepoPath = repoPath
		return result
	}

	if config.IsSftpResticRepository(repoPath) {
		initRequired, err := repo.validateRepoAccessReadOnly()
		if initRequired {
			return ManualCheckResult{
				Status:   ManualCheckStatusInitRequired,
				Message:  "SFTP repository is not yet initialized",
				CanInit:  true,
				RepoPath: repoPath,
			}
		}
		if err != nil {
			return ManualCheckResult{
				Status:   ManualCheckStatusError,
				Message:  err.Error(),
				CanInit:  false,
				RepoPath: repoPath,
			}
		}
		return ManualCheckResult{
			Status:   ManualCheckStatusOK,
			Message:  "SFTP repository configuration verified",
			CanInit:  true,
			RepoPath: repoPath,
		}
	}

	result := repo.validateLocalRepoManual(repoPath)
	result.RepoPath = repoPath
	return result
}

// RunCommand executes a command within the context of a Restic repository, capturing stdout and stderr.
// It uses the ResticRepo's BinaryPath as the first parameter, along with any additional args.
// Optionally, you can skip capturing stdout to save memory (stderr is always captured for error reporting).
func (repo *ResticManager) RunCommand(args []string, loglevel logrus.Level, captureOutput bool) ([]byte, []byte, error) {
	// Set up the command
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = repo.getEnvCopy()

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
	streamOutput := func(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer, capture bool) {
		defer wg.Done() // Mark goroutine as done

		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, prefix+line)
			if capture {
				buffer.WriteString(line + "\n")
			}
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, prefix+"Error reading output:", err)
		}
	}

	// Start streaming stdout and stderr in separate goroutines
	go streamOutput(stdoutPipe, "[OUT] ", &stdoutBuf, captureOutput)
	go streamOutput(stderrPipe, "[ERR] ", &stderrBuf, true)

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

	return nil, stderrBuf.Bytes(), nil
}

// RunCommandWithContext executes a restic command with timeout context
func (repo *ResticManager) RunCommandWithContext(ctx context.Context, args []string, loglevel logrus.Level, captureOutput bool) ([]byte, []byte, error) {
	// Set up the command with context
	cmd := exec.CommandContext(ctx, repo.BinaryPath, args...)
	cmd.Env = repo.getEnvCopy()

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

	repo.Printf(loglevel, "Starting command with timeout: %s %v", repo.BinaryPath, args)

	// Start the command execution
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("error starting command: %w", err)
	}

	// Use WaitGroup to ensure we read both stdout and stderr before cmd.Wait()
	var wg sync.WaitGroup
	wg.Add(2)

	// Function to read output
	streamOutput := func(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer, capture bool) {
		defer wg.Done() // Mark goroutine as done

		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, prefix+line)
			if capture {
				buffer.WriteString(line + "\n")
			}
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, prefix+"Error reading output:", err)
		}
	}

	// Start streaming stdout and stderr in separate goroutines
	go streamOutput(stdoutPipe, "[OUT] ", &stdoutBuf, captureOutput)
	go streamOutput(stderrPipe, "[ERR] ", &stderrBuf, true)

	// Wait for both output goroutines to finish reading
	wg.Wait()

	// Now that all output is read, we can wait for the process to finish
	if err := cmd.Wait(); err != nil {
		// Check if timeout occurred
		if ctx.Err() == context.DeadlineExceeded {
			return stdoutBuf.Bytes(), stderrBuf.Bytes(), fmt.Errorf("command timeout after %v: %w", repo.GetOperationTimeout(), ctx.Err())
		}
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), fmt.Errorf("command execution failed: %w", err)
	}

	repo.Printf(loglevel, "Command completed successfully: %s %v", repo.BinaryPath, args)

	// Return captured stdout and stderr if needed
	if captureOutput {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), nil
	}

	return nil, stderrBuf.Bytes(), nil
}

// InitRepo initializes the repository with backward-compatible signature
// For backward compatibility, accepts bool. New code should use InitRepoWithOptions
func (repo *ResticManager) InitRepo(force bool) error {
	return repo.InitRepoWithOptions(ResticInitOption{Force: force})
}

// InitRepoWithOptions initializes the repository with full options
func (repo *ResticManager) InitRepoWithOptions(opt ResticInitOption) error {
	repopath := repo.GetRepoPath()
	if opt.Force {
		if config.IsS3ResticRepository(repopath) || repo.AwsBucket != "" {
			bucket, prefix, endpoint, err := repo.resolveS3RepoSpec(repopath)
			if err != nil {
				repo.CanInitRepo = false
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
			if prefix == "" && !opt.AllowEmptyPrefix {
				repo.CanInitRepo = false
				err = fmt.Errorf("refusing to force init S3 repository with empty prefix (entire bucket). set allow_empty_prefix to proceed")
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
			client, err := s3helper.NewClient(repo.AwsAccessKeyID, repo.AwsSecretAccessKey, "", repo.AwsRegion, endpoint)
			if err != nil {
				repo.CanInitRepo = false
				err = fmt.Errorf("failed to create S3 client: %w", err)
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
			deleteOptions := s3helper.DeletePrefixOptions{RequireNonEmptyPrefix: !opt.AllowEmptyPrefix}
			if err := s3helper.DeletePrefixWithOptions(client, bucket, prefix, deleteOptions); err != nil {
				repo.CanInitRepo = false
				err = fmt.Errorf("failed to delete S3 repository prefix: %w", err)
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
			if err := s3helper.EnsurePrefixMarker(client, bucket, prefix); err != nil {
				repo.CanInitRepo = false
				err = fmt.Errorf("failed to create S3 prefix marker: %w", err)
				repo.SetError(InitTask, err)
				repo.setInitErrorBackoff(err)
				return err
			}
		} else if config.IsSftpResticRepository(repopath) {
			repo.CanInitRepo = false
			err := fmt.Errorf("force re-initialization is not supported for sftp repositories; remove the remote repository path manually over ssh before re-initializing")
			repo.SetError(InitTask, err)
			repo.setInitErrorBackoff(err)
			return err
		} else {
			err := os.RemoveAll(repopath)
			if err != nil {
				return fmt.Errorf("failed to remove repo: %w", err)
			}

			os.MkdirAll(repopath, 0755)
		}
	}

	// Prepare the arguments for the "init" command
	args := []string{"init"}

	if opt.RepositoryVersion != "" {
		args = append(args, "--repository-version", opt.RepositoryVersion)
	}

	if opt.CopyChunkerParams && opt.FromRepo != "" {
		args = append(args, "--copy-chunker-params")
		args = append(args, "--from-repo", opt.FromRepo)
	}

	// Execute the Restic "init" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false) // Don't capture output
	if err != nil {
		// Update the repo flag to prevent further fetch attempts
		repo.CanInitRepo = false
		err = errors.New(string(stderr))

		repo.SetError(InitTask, err)
		repo.setInitErrorBackoff(err)

		return err
	}

	// Only add fetch task on successful initialization
	delete(repo.TaskErrors, InitTask)
	repo.AddFetchTask()
	repo.clearInitErrorBackoff()

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

	// restic stats --json writes a progress line to stdout before the JSON object
	// (e.g. "[0:00] 100% ..."). Skip everything before the first '{'.
	jsonStart := bytes.IndexByte(stdout, '{')
	if jsonStart < 0 {
		return fmt.Errorf("no JSON object in stats output: %q", stdout)
	}
	var backupstat BackupStat
	err = json.Unmarshal(stdout[jsonStart:], &backupstat)
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
		// Handle error (including stderr)
		return fmt.Errorf("failed to fetch repo: %v, stderr: %s", err, stderr)
	}

	var backups []BackupSnapshot
	err = json.Unmarshal(stdout, &backups)
	if err != nil {
		return fmt.Errorf("failed to unmarshal backups: %w", err)
	}

	// Update the Backups field with the fetched backups
	repo.UpdateSnapshotList(backups)

	return nil // Success
}

// FetchRepo performs the fetch for snapshots and stats.
// The first call uses the boot auto-init policy (one-shot); subsequent calls
// are passive and will not retry initialization automatically.
// When the first (boot) fetch succeeds, OnBootFetchSuccess is called once in a
// goroutine so the cluster layer can promote "auto" to the concrete S3 mode that
// was actually used.
func (repo *ResticManager) FetchRepo() error {
	// Check if the repo is able to fetch and initialized
	if !repo.GetCanFetch() {
		return nil
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Determine policy: boot-time init is allowed once per manager lifetime.
	repo.errorMutex.Lock()
	bootDone := repo.bootInitDone
	if !bootDone {
		repo.bootInitDone = true
	}
	repo.errorMutex.Unlock()

	wasBootFetch := !bootDone
	policy := initPolicyPassive
	if wasBootFetch {
		policy = initPolicyBoot
	}

	if err := repo.checkRepoFilesWithPolicy(policy); err != nil {
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

	// Boot fetch succeeded: fire the one-shot callback regardless of which task
	// path led here (UnlockTask→FetchRepo, an explicit FetchTask, etc.).
	if wasBootFetch {
		repo.Mutex.Lock()
		cb := repo.OnBootFetchSuccess
		repo.Mutex.Unlock()
		if cb != nil {
			go cb()
		}
	}

	return nil
}

// S3ProbeUsable reports whether a ProbeS3Candidate result means the S3
// configuration is usable.  A nil error means the repo is initialized and
// accessible.  An "initialization required" error means the bucket is reachable
// but contains no restic repository yet — still usable (auto-init can finish it).
// Any other error means the config is not reachable.
func S3ProbeUsable(err error) bool {
	if err == nil {
		return true
	}
	return strings.Contains(err.Error(), "initialization required")
}

// ProbeS3Candidate performs a read-only S3 existence check against the given
// parameters without permanently altering manager state.  It saves all mutable
// fields, temporarily applies the probe parameters, runs a passive S3 check,
// then restores everything.
//
// Callers should check the returned error with S3ProbeUsable to determine
// whether the config is usable.
//
// ProbeS3Candidate is safe to call before the worker is started.  It must NOT
// be called while other goroutines are actively mutating the manager's S3 or
// error-backoff state.
func (repo *ResticManager) ProbeS3Candidate(bucket, prefix, endpoint, accessKeyID, secretKey, region string) error {
	// Save unguarded mutable fields.
	savedCanInit := repo.CanInitRepo
	savedBucket := repo.AwsBucket
	savedPrefix := repo.AwsPrefix
	savedEndpoint := repo.AwsEndpoint
	savedKeyID := repo.AwsAccessKeyID
	savedSecret := repo.AwsSecretAccessKey
	savedRegion := repo.AwsRegion

	// Save errorMutex-guarded fields (repoState is also guarded by errorMutex,
	// matching GetRepoState / setRepoState).
	repo.errorMutex.Lock()
	savedLastErr := repo.lastInitError
	savedErrCount := repo.initErrorCount
	savedBackoff := repo.initBackoffUntil
	savedRepoState := repo.repoState
	savedTaskErrors := make(map[TaskType]error, len(repo.TaskErrors))
	for k, v := range repo.TaskErrors {
		savedTaskErrors[k] = v
	}
	repo.errorMutex.Unlock()

	// Apply probe parameters.
	repo.AwsBucket = bucket
	repo.AwsPrefix = prefix
	repo.AwsEndpoint = endpoint
	repo.AwsAccessKeyID = accessKeyID
	repo.AwsSecretAccessKey = secretKey
	repo.AwsRegion = region

	probeErr := repo.checkS3RepoFilesWithPolicy(bucket, prefix, endpoint, initPolicyPassive)

	// Restore all mutable state.
	repo.CanInitRepo = savedCanInit
	repo.AwsBucket = savedBucket
	repo.AwsPrefix = savedPrefix
	repo.AwsEndpoint = savedEndpoint
	repo.AwsAccessKeyID = savedKeyID
	repo.AwsSecretAccessKey = savedSecret
	repo.AwsRegion = savedRegion

	repo.errorMutex.Lock()
	repo.lastInitError = savedLastErr
	repo.initErrorCount = savedErrCount
	repo.initBackoffUntil = savedBackoff
	repo.repoState = savedRepoState
	repo.TaskErrors = savedTaskErrors
	repo.errorMutex.Unlock()

	return probeErr
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

func GetPruneOptions(prune bool, opt ResticPruneOption) []string {
	var args []string
	if opt.MaxRepackSize != "" {
		args = append(args, "--max-repack-size", opt.MaxRepackSize)
	}
	if opt.MaxUnused != "" {
		args = append(args, "--max-unused", opt.MaxUnused)
	}
	if opt.RepackCacheableOnly {
		args = append(args, "--repack-cacheable-only")
	}
	if opt.RepackSmall {
		args = append(args, "--repack-small")
	}
	if opt.RepackUncompressed {
		args = append(args, "--repack-uncompressed")
	}
	return args
}

func (repo *ResticManager) purgeSingleSnapshot(opt ResticPurgeOption) error {
	repo.Printf(logrus.InfoLevel, "Purging single snapshot ID: %s", repo.shortSnapshotID(opt.SnapshotID))

	args := []string{"forget"}

	if opt.Prune {
		args = append(args, "--prune")
		// Add --prune flag to reclaim disk space after forgetting snapshots
		args = append(args, GetPruneOptions(opt.Prune, opt.PruneOption)...)
	}

	if opt.DryRun {
		args = append(args, "--dry-run")
	}

	args = append(args, opt.SnapshotID)

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

	args := buildForgetArgs(opt)

	// Execute the Restic "forget" command using RunCommand
	_, stderr, err := repo.RunCommand(args, logrus.InfoLevel, false)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to purge repo: %v, stderr: %s", err, stderr)
	}

	return nil
}

func buildForgetArgs(opt ResticPurgeOption) []string {
	args := []string{"forget"}

	// Add --prune flag to reclaim disk space after forgetting snapshots
	if opt.Prune {
		args = append(args, "--prune")

		// Add --prune flag to reclaim disk space after forgetting snapshots
		args = append(args, GetPruneOptions(opt.Prune, opt.PruneOption)...)
	}

	groupBy := strings.TrimSpace(opt.GroupBy)
	if groupBy != "" && !strings.EqualFold(groupBy, "default") {
		if strings.EqualFold(groupBy, "none") {
			args = append(args, "--group-by", "")
		} else {
			args = append(args, "--group-by", groupBy)
		}
	}

	// Add keep-tag filters
	for _, tag := range opt.KeepTag {
		if strings.TrimSpace(tag) != "" {
			args = append(args, "--keep-tag", strings.TrimSpace(tag))
		}
	}

	// Add host filters
	for _, host := range opt.Host {
		if strings.TrimSpace(host) != "" {
			args = append(args, "--host", strings.TrimSpace(host))
		}
	}

	// Add tag filters
	for _, tag := range opt.Tag {
		if strings.TrimSpace(tag) != "" {
			args = append(args, "--tag", strings.TrimSpace(tag))
		}
	}

	// Add path filters
	for _, path := range opt.Path {
		if strings.TrimSpace(path) != "" {
			args = append(args, "--path", strings.TrimSpace(path))
		}
	}

	keepWithin, useWithin := GetKeepWithinTime(
		opt.KeepWithin,
		opt.KeepWithinHourly,
		opt.KeepWithinDaily,
		opt.KeepWithinWeekly,
		opt.KeepWithinMonthly,
		opt.KeepWithinYearly,
	)
	if useWithin {
		args = append(args, keepWithin...)
	}

	keep, useKeep := GetKeepN(opt.KeepLast, opt.KeepHourly, opt.KeepDaily, opt.KeepWeekly, opt.KeepMonthly, opt.KeepYearly)
	if useKeep {
		args = append(args, keep...)
	}

	if opt.DryRun {
		args = append(args, "--dry-run")
	}

	return args
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
		err := repo.purgeSingleSnapshot(opt)
		if err != nil {
			return err
		}
	} else {
		err := repo.purgeWithPolicy(opt)
		if err != nil {
			return err
		}
	}

	if repo.OnPurgeComplete != nil && !opt.DryRun {
		go repo.OnPurgeComplete(opt)
	}

	return nil
}

// Backup is a backward-compatible wrapper. New code should use BackupWithOptions
func (repo *ResticManager) Backup(dirpath string, tags []string) (string, error) {
	return repo.BackupWithOptions(ResticBackupOption{
		DirPath: dirpath,
		Tags:    tags,
	})
}

// ResticBackupSummary represents the JSON summary output from restic backup
type ResticBackupSummary struct {
	MessageType         string  `json:"message_type"`
	FilesNew            int     `json:"files_new"`
	FilesChanged        int     `json:"files_changed"`
	FilesUnmodified     int     `json:"files_unmodified"`
	DirsNew             int     `json:"dirs_new"`
	DirsChanged         int     `json:"dirs_changed"`
	DirsUnmodified      int     `json:"dirs_unmodified"`
	DataBlobs           int     `json:"data_blobs"`
	TreeBlobs           int     `json:"tree_blobs"`
	DataAdded           int64   `json:"data_added"`
	TotalFilesProcessed int     `json:"total_files_processed"`
	TotalBytesProcessed int64   `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`
}

// ResticBackupStatus represents the JSON status output from restic backup
type ResticBackupStatus struct {
	MessageType    string  `json:"message_type"`
	SecondsElapsed int     `json:"seconds_elapsed"`
	PercentDone    float64 `json:"percent_done"`
	TotalFiles     int     `json:"total_files"`
	FilesDone      int     `json:"files_done"`
	TotalBytes     int64   `json:"total_bytes"`
	BytesDone      int64   `json:"bytes_done"`
}

// BackupWithOptions performs backup with full options support
// Returns the snapshot ID if successful
func (repo *ResticManager) BackupWithOptions(opt ResticBackupOption) (string, error) {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.BackupWithOptions(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Ensure the repository is ready before backup; retry init if fresh and
	// auto-init is enabled. This bypasses passive backoff so a real backup
	// attempt can recover from a failed boot init.
	if err := repo.checkRepoFilesWithPolicy(initPolicyPreBackup); err != nil {
		return "", err
	}

	// Prepare the arguments for the "backup" command
	args := []string{"backup", "--json"}

	// Add tags
	for _, tag := range opt.Tags {
		if tag != "" {
			args = append(args, "--tag", tag)
		}
	}

	// Add exclude patterns
	for _, pattern := range opt.Exclude {
		if strings.TrimSpace(pattern) != "" {
			args = append(args, "--exclude", strings.TrimSpace(pattern))
		}
	}

	// Add exclude files
	for _, file := range opt.ExcludeFile {
		if strings.TrimSpace(file) != "" {
			args = append(args, "--exclude-file", strings.TrimSpace(file))
		}
	}

	// Add exclude-caches
	if opt.ExcludeCaches {
		args = append(args, "--exclude-caches")
	}

	// Add exclude-if-present
	for _, file := range opt.ExcludeIfPresent {
		if strings.TrimSpace(file) != "" {
			args = append(args, "--exclude-if-present", strings.TrimSpace(file))
		}
	}

	// Add exclude-larger-than
	if opt.ExcludeLargerThan != "" {
		args = append(args, "--exclude-larger-than", opt.ExcludeLargerThan)
	}

	// Add files-from
	for _, file := range opt.FilesFrom {
		if strings.TrimSpace(file) != "" {
			args = append(args, "--files-from", strings.TrimSpace(file))
		}
	}

	// Add host override
	if opt.Host != "" {
		args = append(args, "--host", opt.Host)
	}

	// Add parent snapshot
	if opt.Parent != "" {
		args = append(args, "--parent", opt.Parent)
	}

	// Add one-file-system
	if opt.OneFileSystem {
		args = append(args, "--one-file-system")
	}

	// Add ignore-ctime
	if opt.IgnoreCtime {
		args = append(args, "--ignore-ctime")
	}

	// Add ignore-inode
	if opt.IgnoreInode {
		args = append(args, "--ignore-inode")
	}

	// Add time
	if opt.Time != "" {
		args = append(args, "--time", opt.Time)
	}

	// Add dry-run
	if opt.DryRun {
		args = append(args, "--dry-run")
	}

	// Add the directory path
	args = append(args, opt.DirPath)

	// Execute the Restic "backup" command with streaming to minimize memory usage
	lastLine, stderr, err := repo.runBackupCommand(args)
	if err != nil {
		// Handle error (including stderr)
		return "", fmt.Errorf("failed to backup repo: %v, stderr: %s", err, stderr)
	}

	// Parse the last JSON line to extract snapshot ID
	snapshotID := repo.parseBackupSummary(lastLine)

	return snapshotID, nil
}

// runBackupCommand executes restic backup and streams output, only keeping the last line
// This minimizes memory usage for large backups that produce many JSON status lines
func (repo *ResticManager) runBackupCommand(args []string) ([]byte, []byte, error) {
	cmd := exec.Command(repo.BinaryPath, args...)
	cmd.Env = repo.getEnvCopy()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Starting command: %s %v", repo.BinaryPath, args)

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("error starting command: %w", err)
	}

	// Stream stdout line-by-line, keeping only the last non-empty line (the summary)
	var lastLine []byte
	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout and keep only the last line
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Bytes()
			// Log for debugging, but don't accumulate in memory
			repo.Print(logrus.DebugLevel, "[OUT] "+string(line))
			// Restic JSON progress output is emitted on stdout.
			repo.UpdateCurrentTaskFromJSON(line)
			// Only keep the last non-empty line
			if len(bytes.TrimSpace(line)) > 0 {
				lastLine = append([]byte(nil), line...) // Copy the line
			}
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, "[OUT] Error reading output: %v", err)
		}
	}()

	// Capture stderr for error reporting
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, "[ERR] "+line)
			stderrBuf.WriteString(line + "\n")
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, "[ERR] Error reading output: %v", err)
		}
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return lastLine, stderrBuf.Bytes(), fmt.Errorf("command execution failed: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Command completed successfully: %s %v", repo.BinaryPath, args)

	return lastLine, stderrBuf.Bytes(), nil
}

type resticMessageEnvelope struct {
	MessageType string `json:"message_type"`
}

func (repo *ResticManager) SetCurrentTaskRunning(task *ResticTask) {
	if task == nil {
		return
	}

	now := time.Now().UTC()
	state := &ResticTaskState{
		TaskID:     task.ID,
		TaskType:   task.Type,
		Status:     "running",
		StartedAt:  &now,
		LastUpdate: &now,
	}

	repo.currentTaskMutex.Lock()
	repo.currentTask = state
	repo.currentTaskMutex.Unlock()
}

// UpdateCurrentTaskPhase sets the phase on the current task (e.g., "init_destination", "copy").
// No-op when no task is running.
func (repo *ResticManager) UpdateCurrentTaskPhase(phase string) {
	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()
	if repo.currentTask == nil {
		return
	}
	now := time.Now().UTC()
	repo.currentTask.Phase = phase
	repo.currentTask.LastUpdate = &now
}

// UpdateCurrentTaskCopyLine parses a single line of restic copy output and updates
// the current task state. It handles:
//   - progress lines: "[0:00] 100.00%  2 / 2 packs copied"
//   - snapshot saved lines: "snapshot X saved, copied from source snapshot Y"
//   - snapshot skipped lines: "skipping snapshot X"
func (repo *ResticManager) UpdateCurrentTaskCopyLine(line string) {
	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()
	if repo.currentTask == nil {
		return
	}
	now := time.Now().UTC()
	if m := copyProgressLineRegex.FindStringSubmatch(line); m != nil {
		if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
			repo.currentTask.PercentDone = pct / 100.0
		}
		if done, err := strconv.Atoi(m[2]); err == nil {
			repo.currentTask.PacksDone = done
		}
		if total, err := strconv.Atoi(m[3]); err == nil {
			repo.currentTask.TotalPacks = total
		}
		repo.currentTask.LastUpdate = &now
		return
	}
	if copySnapshotSavedRegex.MatchString(line) || copySnapshotSkippedRegex.MatchString(line) {
		repo.currentTask.CompletedSnapshots++
		repo.currentTask.LastUpdate = &now
	}
}

func (repo *ResticManager) UpdateCurrentTaskFromJSON(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}

	var envelope resticMessageEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return
	}

	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()

	if repo.currentTask == nil {
		return
	}

	switch envelope.MessageType {
	case "status":
		var status ResticBackupStatus
		if err := json.Unmarshal(trimmed, &status); err != nil {
			return
		}
		repo.currentTask.PercentDone = status.PercentDone
		repo.currentTask.BytesDone = status.BytesDone
		repo.currentTask.TotalBytes = status.TotalBytes
		repo.currentTask.FilesDone = status.FilesDone
		repo.currentTask.TotalFiles = status.TotalFiles
		repo.currentTask.SecondsElapsed = status.SecondsElapsed
		now := time.Now().UTC()
		repo.currentTask.LastUpdate = &now
	case "summary":
		var summary ResticBackupSummary
		if err := json.Unmarshal(trimmed, &summary); err != nil {
			return
		}
		repo.currentTask.FilesNew = summary.FilesNew
		repo.currentTask.FilesChanged = summary.FilesChanged
		repo.currentTask.FilesUnmodified = summary.FilesUnmodified
		repo.currentTask.DirsNew = summary.DirsNew
		repo.currentTask.DirsChanged = summary.DirsChanged
		repo.currentTask.DirsUnmodified = summary.DirsUnmodified
		repo.currentTask.DataBlobs = summary.DataBlobs
		repo.currentTask.TreeBlobs = summary.TreeBlobs
		repo.currentTask.DataAdded = summary.DataAdded
		repo.currentTask.TotalFilesProcessed = summary.TotalFilesProcessed
		repo.currentTask.TotalBytesProcessed = summary.TotalBytesProcessed
		repo.currentTask.TotalDuration = summary.TotalDuration
		repo.currentTask.SnapshotID = summary.SnapshotID
		now := time.Now().UTC()
		repo.currentTask.LastUpdate = &now
	}
}

func (repo *ResticManager) FinalizeCurrentTask(result ResticResult) {
	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()

	if repo.currentTask == nil {
		return
	}

	if result.SnapshotID != "" {
		repo.currentTask.SnapshotID = result.SnapshotID
	}

	if result.Error != nil {
		repo.currentTask.Status = "failed"
		repo.currentTask.Error = result.Error.Error()
	} else {
		repo.currentTask.Status = "completed"
		repo.currentTask.Error = ""
	}

	now := time.Now().UTC()
	completionTime := now
	repo.currentTask.CompletedAt = &completionTime
	repo.currentTask.LastUpdate = &completionTime

	taskID := repo.currentTask.TaskID
	go repo.clearCompletedTaskAfterTTL(taskID, completionTime)
}

func (repo *ResticManager) clearCompletedTaskAfterTTL(taskID int, completionTime time.Time) {
	if resticTaskStateTTL <= 0 {
		return
	}

	timer := time.NewTimer(resticTaskStateTTL)
	defer timer.Stop()
	<-timer.C

	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()

	if repo.currentTask == nil {
		return
	}

	if repo.currentTask.TaskID != taskID {
		return
	}

	if repo.currentTask.CompletedAt == nil || !repo.currentTask.CompletedAt.Equal(completionTime) {
		return
	}

	if repo.currentTask.Status != "completed" && repo.currentTask.Status != "failed" {
		return
	}

	repo.currentTask = nil
}

func (repo *ResticManager) GetCurrentTaskState() *ResticTaskState {
	repo.currentTaskMutex.Lock()
	defer repo.currentTaskMutex.Unlock()

	if repo.currentTask == nil {
		return nil
	}

	copyState := *repo.currentTask
	return &copyState
}

// parseBackupSummary extracts the snapshot ID from restic backup JSON summary line
func (repo *ResticManager) parseBackupSummary(summaryLine []byte) string {
	if len(summaryLine) == 0 {
		repo.Printf(logrus.WarnLevel, "No summary line found in backup output")
		return ""
	}

	var summary ResticBackupSummary
	if err := json.Unmarshal(summaryLine, &summary); err != nil {
		repo.Printf(logrus.WarnLevel, "Failed to parse backup summary: %v", err)
		return ""
	}

	if summary.MessageType == "summary" && summary.SnapshotID != "" {
		repo.Printf(logrus.InfoLevel, "Backup completed: snapshot %s created", repo.shortSnapshotID(summary.SnapshotID))
		return summary.SnapshotID
	}

	repo.Printf(logrus.WarnLevel, "Could not extract snapshot ID from backup summary")
	return ""
}

func (repo *ResticManager) CheckResticLocks() error {
	// Prepare the arguments for the "backup" command
	args := []string{"list", "locks", "--no-lock", "-q"}

	// Execute the Restic "list locks" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.DebugLevel, true)
	if err != nil {
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
	if err := repo.waitCanFetch(); err != nil {
		return err
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Prepare the arguments for the "backup" command
	args := []string{"unlock"}

	// Execute the Restic "list locks" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("failed to check repo locks: %v, stderr: %s", err, stderr)
	}

	output := string(stdout) + "\n" + string(stderr)
	if !strings.Contains(output, "successfully removed") && !strings.Contains(output, "no locks were found") {
		return fmt.Errorf("failed to unlock repo: %s. stderr: %s", stdout, stderr)
	}

	repo.HasLocks = false
	return nil
}

// CheckRepo verifies repository integrity with various check options
func (repo *ResticManager) CheckRepo(opt ResticCheckOption) error {
	if !repo.GetCanFetch() {
		time.Sleep(time.Second)
		return repo.CheckRepo(opt)
	}

	repo.SetCanFetch(false)
	defer repo.SetCanFetch(true)

	// Check if the repo is initialized
	if err := repo.CheckRepoFiles(); err != nil {
		return err
	}

	// Prepare the arguments for the "check" command
	args := []string{"check"}

	// Add --read-data flag for full data verification (slow)
	if opt.ReadData {
		args = append(args, "--read-data")
	}

	// Add --read-data-subset for partial data verification
	if opt.ReadDataSubset != "" {
		args = append(args, "--read-data-subset", opt.ReadDataSubset)
	}

	// Add --with-cache if explicitly requested (default is without cache)
	if opt.WithCache {
		args = append(args, "--with-cache")
	}

	// Execute the Restic "check" command using RunCommand
	stdout, stderr, err := repo.RunCommand(args, logrus.InfoLevel, true)
	if err != nil {
		// Handle error (including stderr)
		return fmt.Errorf("repository check failed: %v, stderr: %s", err, stderr)
	}

	// Log success message
	repo.Printf(logrus.InfoLevel, "Repository check completed successfully: %s", strings.TrimSpace(string(stdout)))

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

	// Temporarily add the new password file to the environment. originalEnv is
	// a copy, since UpdateEnvKey may overwrite the RESTIC_PASSWORD entry in
	// place rather than appending, which would otherwise mutate it too.
	repo.envMutex.RLock()
	originalEnv := append([]string(nil), repo.Env...)
	repo.envMutex.RUnlock()
	repo.UpdateEnvKey("RESTIC_PASSWORD", newpass)
	defer repo.SetEnv(originalEnv) // Restore original env after function

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

// ShouldRunStructureCheck returns true if a structure check should be run based on backup count
// Best practice: Run structure check after every N backups (default: 7)
func (repo *ResticManager) ShouldRunStructureCheck(backupInterval int) bool {
	if backupInterval <= 0 {
		backupInterval = 7 // Default: weekly check
	}
	return repo.BackupCount >= backupInterval
}

// ShouldRunFullCheck returns true if a full data check should be run based on time
// Best practice: Run full check monthly (default: 30 days)
func (repo *ResticManager) ShouldRunFullCheck(checkIntervalDays int) bool {
	if checkIntervalDays <= 0 {
		checkIntervalDays = 30 // Default: monthly check
	}

	if repo.LastFullCheckTime.IsZero() {
		return true // Never checked
	}

	duration := time.Since(repo.LastFullCheckTime)
	return duration >= time.Duration(checkIntervalDays)*24*time.Hour
}

// ScheduleCheckAfterBackup should be called after successful backups
// This implements best practice: structure check after N backups
func (repo *ResticManager) ScheduleCheckAfterBackup(checkEveryNBackups int) {
	repo.Mutex.Lock()
	repo.BackupCount++
	backupCount := repo.BackupCount
	repo.Mutex.Unlock()

	if checkEveryNBackups <= 0 {
		checkEveryNBackups = 7 // Default
	}

	if backupCount >= checkEveryNBackups {
		repo.Printf(logrus.InfoLevel, "Scheduling structure check after %d backups", backupCount)
		repo.AddCheckTask(ResticCheckOption{
			ReadData: false, // Quick structure check only
		})
		repo.Mutex.Lock()
		repo.BackupCount = 0 // Reset counter
		repo.Mutex.Unlock()
	}
}

// ScheduleFullCheck schedules a comprehensive data check
// Best practice: Run monthly or during maintenance windows
func (repo *ResticManager) ScheduleFullCheck() {
	repo.Printf(logrus.InfoLevel, "Scheduling full data check")
	repo.AddCheckTask(ResticCheckOption{
		ReadData: true, // Full data verification
	})
	repo.Mutex.Lock()
	repo.LastFullCheckTime = time.Now()
	repo.Mutex.Unlock()
}

// ScheduleSubsetCheck schedules a partial data check
// Best practice: Weekly verification of a subset (e.g., 10%)
func (repo *ResticManager) ScheduleSubsetCheck(subset string) {
	if subset == "" {
		subset = "10%" // Default: check 10% of data
	}
	repo.Printf(logrus.InfoLevel, "Scheduling subset check: %s", subset)
	repo.AddCheckTask(ResticCheckOption{
		ReadDataSubset: subset,
	})
}

// UpdateLastCheckTime should be called after successful check completion
func (repo *ResticManager) UpdateLastCheckTime(isFullCheck bool) {
	repo.Mutex.Lock()
	defer repo.Mutex.Unlock()

	repo.LastCheckTime = time.Now()
	if isFullCheck {
		repo.LastFullCheckTime = time.Now()
	}
}
