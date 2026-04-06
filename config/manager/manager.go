package manager

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/sirupsen/logrus"
)

type ClusterConfig interface {
	GetName() string
	Save() error
}

// ConfigSaveTask holds the parameters for saving a config and additional waiting functionality
type ConfigSaveTask struct {
	Cluster   ClusterConfig
	WaitGroup *sync.WaitGroup // Pointer to sync.WaitGroup for additional waiting
}

type ConfigGitTask struct {
	TaskType    string
	conf        *config.Config
	clusterList []string
	WaitGroup   *sync.WaitGroup // Pointer to sync.WaitGroup for additional waiting
}

type GitAddTask struct {
	Cluster   string
	Filename  string
	W         *git.Worktree
	WaitGroup *sync.WaitGroup
}

// ClusterManager holds the necessary fields for each cluster
type ClusterManager struct {
	tasks      []*ConfigSaveTask // Slice of tasks for the cluster
	mutex      *sync.Mutex       // Mutex for safe access to tasks
	cond       *sync.Cond        // Condition variable for waiting and notifying tasks
	stopCh     chan struct{}     // Stop channel to signal the goroutine to stop
	isStopping bool              // Flag to indicate if the cluster manager is stopping
}

// Push Manager
type GitManager struct {
	logger        *config.LogrusWrapper
	IsPushing     bool             // Flag to indicate if a push is in progress
	isStopping    bool             // Flag to indicate if the push manager is stopping
	tasks         []*ConfigGitTask // Slice of tasks for the push queue
	mutex         *sync.Mutex      // Mutex for safe access to tasks
	cond          *sync.Cond       // Condition variable for waiting and notifying tasks
	stopCh        chan struct{}    // Stop channel to signal the goroutine to stop
	PullCh        chan struct{}    // Signal to start pull
	DonePullCh    chan struct{}    // Signal to finish pull
	CommitManager *CommitManager   // Commit manager
}

func NewGitManager(logger *config.LogrusWrapper) *GitManager {
	return &GitManager{
		logger:        logger,
		tasks:         []*ConfigGitTask{},
		mutex:         &sync.Mutex{},
		stopCh:        make(chan struct{}), // Initialize stop channel for the push manager
		PullCh:        make(chan struct{}),
		DonePullCh:    make(chan struct{}),
		CommitManager: NewCommitManager(1, 10, logger),
	}
}

func (gm *GitManager) SplitTaskQueue() (*ConfigGitTask, []*ConfigGitTask, []*ConfigGitTask) {
	// Split the tasks into two queues
	// One for pull and one for push
	pullTasks := []*ConfigGitTask{}
	pushTasks := []*ConfigGitTask{}
	currentTask := gm.tasks[0]

	for _, task := range gm.tasks[1:] {
		if task.TaskType == "pull" {
			pullTasks = append(pullTasks, task)
		} else {
			pushTasks = append(pushTasks, task)
		}
	}

	return currentTask, pullTasks, pushTasks
}

type CommitManager struct {
	logger      *config.LogrusWrapper
	W           *git.Worktree
	commitQueue []GitAddTask // Slice for commit tasks
	mu          sync.Mutex   // Mutex to protect commitQueue
	cond        *sync.Cond
	stopCh      chan struct{}
	wg          sync.WaitGroup
	IsStopping  atomic.Bool
}

func NewCommitManager(workerMin, workerLimit int, logger *config.LogrusWrapper) *CommitManager {
	cmm := &CommitManager{
		logger:      logger,
		commitQueue: []GitAddTask{},
		stopCh:      make(chan struct{}),
	}
	cmm.cond = sync.NewCond(&cmm.mu)

	cmm.Start()
	return cmm
}

func (cmm *CommitManager) Start() {
	cmm.wg.Add(1)
	go cmm.processCommitQueue()
}

func (cmm *CommitManager) AddFileToCommit(task GitAddTask) {
	cmm.mu.Lock()
	defer cmm.mu.Unlock()

	select {
	case <-cmm.stopCh:
		cmm.logger.Infof("none", config.ConstLogModGit, "CommitManager is stopping, rejecting task: %s", task.Filename)
		if task.WaitGroup != nil {
			task.WaitGroup.Done()
		}
	default:
		cmm.commitQueue = append(cmm.commitQueue, task)
		cmm.cond.Signal()
	}
}

func (cmm *CommitManager) processCommitQueue() {
	defer LogPanic(cmm.logger, "CommitManager.processCommitQueue", "none", config.ConstLogModGit)
	defer cmm.wg.Done()

	for {
		cmm.mu.Lock()
		for len(cmm.commitQueue) == 0 {
			select {
			case <-cmm.stopCh:
				cmm.mu.Unlock()
				cmm.logger.Infof("none", config.ConstLogModGit, "CommitManager is stopping.")
				return
			default:
			}
			cmm.cond.Wait()
		}

		select {
		case <-cmm.stopCh:
			cmm.mu.Unlock()
			cmm.logger.Infof("none", config.ConstLogModGit, "CommitManager is stopping.")
			return
		default:
		}

		// Invariant: addFileToCommit completes at most one in-flight dequeued task;
		// Stop() drains and resolves all tasks still queued in commitQueue.
		task := cmm.commitQueue[0]
		cmm.commitQueue = cmm.commitQueue[1:]
		cmm.mu.Unlock()

		cmm.logger.Debugf("none", config.ConstLogModGit, "CommitManager processing file: %s", task.Filename)
		cmm.addFileToCommit(task)
	}
}

func (cmm *CommitManager) addFileToCommit(task GitAddTask) {
	if task.WaitGroup != nil {
		defer task.WaitGroup.Done()
	}

	start := time.Now()
	if _, err := task.W.Add(task.Filename); err == nil {
		cmm.logger.Debugf("none", config.ConstLogModGit, "File %s added in: %s", task.Filename, time.Since(start))
	} else {
		cmm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot add %s: %s", task.Filename, err)
	}
}

func (cmm *CommitManager) Stop() {
	cmm.IsStopping.Store(true)

	cmm.mu.Lock()
	select {
	case <-cmm.stopCh:
		// already closed
	default:
		close(cmm.stopCh)
	}
	cmm.cond.Broadcast()

	pending := cmm.commitQueue
	cmm.commitQueue = make([]GitAddTask, 0)
	cmm.mu.Unlock()

	for _, task := range pending {
		if task.WaitGroup != nil {
			task.WaitGroup.Done()
		}
	}
	cmm.wg.Wait()
	cmm.logger.Infof("none", config.ConstLogModGit, "CommitManager stopped.")
}

// ConfigManager controls config saves & Git push
type ConfigManager struct {
	logger      *config.LogrusWrapper
	configWg    *sync.WaitGroup            // Tracks ongoing config saves
	gitMutex    *sync.Mutex                // Blocks new saves during Git push
	stopOnce    sync.Once                  // Ensures Stop() runs only once
	isStopping  atomic.Bool                // Prevents new saves after stopping
	clusterMu   *sync.RWMutex              // Protects clusterData map access
	clusterData map[string]*ClusterManager // Map of clusters and their respective managers
	gitManager  *GitManager                // Pull Push manager
}

// NewConfigManager initializes the manager
func NewConfigManager(logger *config.LogrusWrapper) *ConfigManager {
	newcm := &ConfigManager{
		logger:      logger,
		clusterData: make(map[string]*ClusterManager),
		clusterMu:   &sync.RWMutex{},
		gitMutex:    &sync.Mutex{},
		configWg:    &sync.WaitGroup{},
		gitManager:  NewGitManager(logger),
	}

	newcm.gitManager.cond = sync.NewCond(newcm.gitManager.mutex)

	go newcm.processGitPush() // Start the persistent goroutine for the push manager

	return newcm
}

func (cm *ConfigManager) GetGitManager() *GitManager {
	return cm.gitManager
}

func (cm *ConfigManager) UpdateLoggerConfig(clustername string, conf *config.Config) {
	cm.logger.UpdateConfig(clustername, conf)
}

func (cm *ConfigManager) CountTasksForCluster(cluster string) int {
	clusterManager, exists := cm.getClusterManager(cluster)
	if exists {
		clusterManager.mutex.Lock()
		defer clusterManager.mutex.Unlock()
		return len(clusterManager.tasks)
	}
	return 0
}

func (cm *ConfigManager) getClusterManager(cluster string) (*ClusterManager, bool) {
	cm.clusterMu.RLock()
	clusterManager, exists := cm.clusterData[cluster]
	cm.clusterMu.RUnlock()
	return clusterManager, exists
}

func (cm *ConfigManager) getOrCreateClusterManager(cluster string) (*ClusterManager, bool) {
	cm.clusterMu.Lock()
	if cm.isStopping.Load() {
		cm.clusterMu.Unlock()
		return nil, false
	}

	clusterManager, exists := cm.clusterData[cluster]
	if !exists {
		clusterManager = &ClusterManager{
			tasks:  []*ConfigSaveTask{},
			mutex:  &sync.Mutex{},
			stopCh: make(chan struct{}),
		}
		clusterManager.cond = sync.NewCond(clusterManager.mutex)
		cm.clusterData[cluster] = clusterManager
	}
	cm.clusterMu.Unlock()

	if !exists {
		go cm.processClusterQueue(cluster, clusterManager)
	}

	return clusterManager, true
}

// SaveConfig allows concurrent saves but respects stopping
func (cm *ConfigManager) SaveConfig(cluster ClusterConfig, wait bool) {
	clustername := cluster.GetName()

	if cm.isStopping.Load() {
		cm.logger.Debugf(clustername, config.ConstLogModGeneral, "[%s] Save blocked: system is stopping.\n", cluster)
		return
	}

	clusterManager, ok := cm.getOrCreateClusterManager(clustername)
	if !ok {
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save blocked while creating queue: system is stopping.\n", cluster)
		return
	}
	configSaveTask := &ConfigSaveTask{Cluster: cluster}

	// Lock the cluster's mutex to safely add to the task slice
	clusterManager.mutex.Lock()
	if cm.isStopping.Load() {
		clusterManager.mutex.Unlock()
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save blocked while queueing: system is stopping.\n", configSaveTask.Cluster)
		return
	}
	select {
	case <-clusterManager.stopCh:
		clusterManager.mutex.Unlock()
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save blocked: cluster manager is stopping.\n", configSaveTask.Cluster)
		return
	default:
	}

	if wait {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		configSaveTask.WaitGroup = wg
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save with wait requested.\n", configSaveTask.Cluster)
	}

	cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Appending to save queue.\n", clustername)

	clusterManager.tasks = append(clusterManager.tasks, configSaveTask)
	// Signal the goroutine that a new task is available
	clusterManager.cond.Signal()
	clusterManager.mutex.Unlock()

	// If a WaitGroup pointer is provided, add to the wait group
	if configSaveTask.WaitGroup != nil {
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request waiting.\n", configSaveTask.Cluster)
		configSaveTask.WaitGroup.Wait()
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request wait over.\n", configSaveTask.Cluster)
	}

	cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request completed.\n", configSaveTask.Cluster)
}

// processClusterQueue processes the tasks in the slice for a given cluster
func (cm *ConfigManager) processClusterQueue(cluster string, clusterManager *ClusterManager) {
	defer LogPanic(cm.logger, fmt.Sprintf("ConfigManager.processClusterQueue[%s]", cluster), cluster, config.ConstLogModConfigLoad)

	for {
		// Lock the cluster's mutex to safely check for tasks
		clusterManager.mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(clusterManager.tasks) == 0 {
			select {
			case <-clusterManager.stopCh: // Stop signal for the goroutine
				cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Stopping goroutine.\n", cluster)
				clusterManager.mutex.Unlock()
				return
			default:
			}

			clusterManager.cond.Wait()
		}

		cm.logger.Debugf(cluster, config.ConstLogModConfigLoad, "[%s] Waking up goroutine.\n", cluster)

		// Check for the stop signal before processing
		select {
		case <-clusterManager.stopCh: // Stop signal for the goroutine
			clusterManager.isStopping = true
			drained := 0
			for _, task := range clusterManager.tasks {
				if task.WaitGroup != nil {
					task.WaitGroup.Done()
				}
				drained++
			}
			clusterManager.tasks = make([]*ConfigSaveTask, 0)
			cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Stop observed, drained %d queued save task(s).", cluster, drained)
			clusterManager.mutex.Unlock()
			return
		default:
			// Process the first task in the queue
			configSaveTask := clusterManager.tasks[0]
			skippedTasks := clusterManager.tasks[1:]
			clusterManager.tasks = make([]*ConfigSaveTask, 0) // remove the current batch since they doing the same thing
			clusterManager.mutex.Unlock()

			cm.gitMutex.Lock() // Prevent Git push conflict
			if cm.isStopping.Load() {
				cm.gitMutex.Unlock()
				if configSaveTask.WaitGroup != nil {
					configSaveTask.WaitGroup.Done()
				}
				for _, task := range skippedTasks {
					if task.WaitGroup != nil {
						task.WaitGroup.Done()
					}
				}
				cm.logger.Infof(cluster, config.ConstLogModConfigLoad, "[%s] Save aborted: system is stopping.", cluster)
				return
			}
			select {
			case <-clusterManager.stopCh:
				cm.gitMutex.Unlock()
				if configSaveTask.WaitGroup != nil {
					configSaveTask.WaitGroup.Done()
				}
				for _, task := range skippedTasks {
					if task.WaitGroup != nil {
						task.WaitGroup.Done()
					}
				}
				cm.logger.Infof(cluster, config.ConstLogModConfigLoad, "[%s] Save aborted: cluster manager is stopping.", cluster)
				return
			default:
			}
			cm.configWg.Add(1)
			cm.gitMutex.Unlock()

			func() {
				defer cm.configWg.Done()
				defer func() {
					if r := recover(); r != nil {
						cm.logger.Errorf(cluster, config.ConstLogModConfigLoad, "Panic during save: %v", r)
					}
				}()
				defer func() {
					if configSaveTask.WaitGroup != nil {
						configSaveTask.WaitGroup.Done()
						cm.logger.Debugf(cluster, config.ConstLogModConfigLoad, "[%s] Save completed.\n", configSaveTask.Cluster)
					}

					for _, task := range skippedTasks {
						if task.WaitGroup != nil {
							task.WaitGroup.Done()
							cm.logger.Debugf(cluster, config.ConstLogModConfigLoad, "[%s] Skipped save completed.\n", task.Cluster)
						}
					}
				}()

				// Execute the save function and handle potential errors
				if err := configSaveTask.Cluster.Save(); err != nil {
					cm.logger.Errorf(cluster, config.ConstLogModConfigLoad, "Error during save: %v", err)
				} else {
					cm.logger.Infof(cluster, config.ConstLogModConfigLoad, "Config saved successfully.")
				}
			}()

			if clusterManager.isStopping {
				cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Cluster manager is stopping, exiting goroutine.", cluster)
				return
			}
		}
	}
}

// GitPush waits for active saves, blocks new ones, and pushes changes
func (cm *ConfigManager) GitPush(conf *config.Config, clusterList []string, wait bool) {

	configGitTask := &ConfigGitTask{conf: conf, clusterList: clusterList, TaskType: "push"}

	if wait {
		wg := sync.WaitGroup{}
		configGitTask.WaitGroup = &wg
		configGitTask.WaitGroup.Add(1)
		cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Push with wait requested.\n")
	}

	cm.logger.Debugln("none", config.ConstLogModGit, "Locking push mutex")
	// Lock the cluster's mutex to safely add to the task slice
	cm.gitManager.mutex.Lock()
	if cm.isStopping.Load() {
		cm.logger.Debugln("none", config.ConstLogModGit, "Rejecting push queue append: manager is stopping")
		cm.gitManager.mutex.Unlock()
		return
	}
	select {
	case <-cm.gitManager.stopCh:
		cm.logger.Debugln("none", config.ConstLogModGit, "Rejecting push queue append: git manager is stopped")
		cm.gitManager.mutex.Unlock()
		return
	default:
	}
	cm.logger.Debugln("none", config.ConstLogModGit, "Appending to push queue")
	cm.gitManager.tasks = append(cm.gitManager.tasks, configGitTask)
	// Signal the goroutine that a new task is available
	cm.logger.Debugln("none", config.ConstLogModGit, "Signal push mutex")
	cm.gitManager.cond.Signal()
	cm.logger.Debugln("none", config.ConstLogModGit, "Unlocking push mutex")
	cm.gitManager.mutex.Unlock()

	// If a WaitGroup pointer is provided, add to the wait group
	if configGitTask.WaitGroup != nil {
		cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Push request waiting.\n")
		configGitTask.WaitGroup.Wait()
		cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Push request wait over.\n")
	}
}

// Pulls the latest changes from the git repository for .pull
func (cm *ConfigManager) GitPullDir() {

	configGitTask := &ConfigGitTask{TaskType: "pull"}

	cm.logger.Debugln("none", config.ConstLogModGit, "Locking pull mutex")
	// Lock the cluster's mutex to safely add to the task slice
	cm.gitManager.mutex.Lock()
	if cm.isStopping.Load() {
		cm.logger.Debugln("none", config.ConstLogModGit, "Rejecting pull queue append: manager is stopping")
		cm.gitManager.mutex.Unlock()
		return
	}
	select {
	case <-cm.gitManager.stopCh:
		cm.logger.Debugln("none", config.ConstLogModGit, "Rejecting pull queue append: git manager is stopped")
		cm.gitManager.mutex.Unlock()
		return
	default:
	}
	cm.logger.Debugln("none", config.ConstLogModGit, "Appending to pull queue")
	cm.gitManager.tasks = append(cm.gitManager.tasks, configGitTask)
	// Signal the goroutine that a new task is available
	cm.logger.Debugln("none", config.ConstLogModGit, "Signal pull mutex")
	cm.gitManager.cond.Signal()
	cm.logger.Debugln("none", config.ConstLogModGit, "Unlocking pull mutex")
	cm.gitManager.mutex.Unlock()
}

// processClusterQueue processes the tasks in the slice for a given cluster
func (cm *ConfigManager) processGitPush() {
	defer LogPanic(cm.logger, "ConfigManager.processGitPush", "none", config.ConstLogModGit)

	for {
		cm.gitManager.mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(cm.gitManager.tasks) == 0 {
			select {
			case <-cm.gitManager.stopCh: // Stop signal for the goroutine
				cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Stopping goroutine.")
				cm.gitManager.mutex.Unlock()
				return
			default:
			}
			cm.gitManager.cond.Wait()
			cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Waking up goroutine.")
		}

		// Check for the stop signal before processing
		select {
		case <-cm.gitManager.stopCh: // Stop signal for the goroutine
			cm.gitManager.isStopping = true
			drained := 0
			for _, task := range cm.gitManager.tasks {
				if task.WaitGroup != nil {
					task.WaitGroup.Done()
				}
				drained++
			}
			cm.gitManager.tasks = make([]*ConfigGitTask, 0)
			cm.logger.Infof("none", config.ConstLogModGit, "[Git] Stop observed, drained %d queued git task(s).", drained)
			cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Stopping goroutine.")
			cm.gitManager.mutex.Unlock()
			return
		default:
			// Process the first task in the queue and skip same type tasks
			configGitTask, pull, push := cm.gitManager.SplitTaskQueue()
			var skippedTasks []*ConfigGitTask
			if configGitTask.TaskType == "pull" {
				skippedTasks = pull
				cm.gitManager.tasks = push
			} else {
				skippedTasks = push
				cm.gitManager.tasks = pull
			}
			cm.gitManager.mutex.Unlock()

			cm.logger.Debugf("none", config.ConstLogModGit, "Locking git mutex")
			cm.gitMutex.Lock() // Block new config saves
			cm.logger.Debugf("none", config.ConstLogModGit, "Waiting for active saves to finish...")
			cm.configWg.Wait() // Ensure all active saves finish

			if configGitTask.TaskType == "pull" {
				cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Starting Git pull...")

				// Inform the pull process to start pulling
				cm.gitManager.PullCh <- struct{}{}

				// Wait for the pull process to finish
				<-cm.gitManager.DonePullCh

				cm.logger.Infof("none", config.ConstLogModGit, "[Git] Git pull completed successfully.")

			} else {
				cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Starting Git push...")
				// Execute the save function and handle potential errors
				if err := cm.PushAllConfigsToGit(configGitTask.conf, configGitTask.clusterList); err != nil {
					// Execute the Git push function and handle potential errors
					cm.logger.Errorf("none", config.ConstLogModGit, "[Git] Error during push: %v\n", err)
				} else {
					cm.logger.Infof("none", config.ConstLogModGit, "[Git] Git push completed successfully.")
				}
			}

			// If a WaitGroup pointer is provided, mark the task as done
			if configGitTask.WaitGroup != nil {
				configGitTask.WaitGroup.Done()
			}

			for _, task := range skippedTasks {
				if task.WaitGroup != nil {
					task.WaitGroup.Done()
				}
			}

			cm.gitMutex.Unlock()

			if cm.gitManager.isStopping && len(cm.gitManager.tasks) == 0 {
				cm.logger.Infof("none", config.ConstLogModGit, "[Git] No more tasks in queue, stopping goroutine.")
				return
			}
		}
	}
}

// Stop gracefully shuts down the system
func (cm *ConfigManager) Stop() {
	cm.stopOnce.Do(func() {
		cm.logger.Infof("none", config.ConstLogModGeneral, "[Shutdown] Stopping...")

		cm.isStopping.Store(true) // Prevent new saves

		cm.gitMutex.Lock()
		defer cm.gitMutex.Unlock()

		cm.clusterMu.RLock()
		clusterManagers := make([]*ClusterManager, 0, len(cm.clusterData))
		for _, clmgr := range cm.clusterData {
			clusterManagers = append(clusterManagers, clmgr)
		}
		cm.clusterMu.RUnlock()

		// Send stop signal to all cluster goroutines
		for _, clmgr := range clusterManagers {
			select {
			case <-clmgr.stopCh:
				// already closed
			default:
				close(clmgr.stopCh)
			}
			clmgr.cond.Signal() // Wake up the cluster goroutine
		}

		cm.logger.Infof("none", config.ConstLogModGeneral, "[Shutdown] Waiting for active saves to finish...")
		cm.configWg.Wait()

		cm.gitManager.CommitManager.Stop()

		close(cm.gitManager.stopCh) // Send stop signal to the push manager
		cm.gitManager.cond.Signal() // Wake up the push manager
		cm.logger.Infof("none", config.ConstLogModGeneral, "[Shutdown] Config manager stopped.")
	})
}

func (cm *ConfigManager) PushAllConfigsToGit(conf *config.Config, clusterList []string) error {
	cm.gitManager.IsPushing = true
	defer func() {
		cm.gitManager.IsPushing = false
	}()

	if conf.GitUrl == "" {
		cm.logger.Infof("none", config.ConstLogModGit, "No Git URL provided, skipping push")
		return nil
	}

	cm.AddPullToGitignore(conf)
	cm.AddTempDirToGitignore(conf)

	cm.logger.Infof("none", config.ConstLogModGit, "Pushing All Configs To Git")

	err := cm.PushConfigToGit(conf, clusterList)
	if err != nil {
		if recoverable, reason := cm.classifyRecoverablePushError(err); recoverable {
			cm.logger.Warnf("none", config.ConstLogModGit, "Recoverable git push failure (%s): %v. Refreshing repository metadata and retrying once.", reason, err)
			if refreshErr := cm.RefreshGitMetadata(conf); refreshErr != nil {
				cm.logger.Errorf("none", config.ConstLogModGit, "Git metadata refresh failed after recoverable push error: %v", refreshErr)
				return refreshErr
			}
			err = cm.PushConfigToGit(conf, clusterList)
		}
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error pushing to git: %v", err)
			return err
		}
	}

	// Count the commits
	commits, err := cm.CountAllCommits(conf)
	if err != nil {
		cm.logger.Warnf("none", config.ConstLogModGit, "Error counting commits: %v", err)
		return err
	}

	if commits >= 10 {
		cm.logger.Infof("none", config.ConstLogModGit, "Refreshing git metadata after %d commits to keep repository shallow", commits)
		err := cm.RefreshGitMetadata(conf)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error refreshing git metadata: %v", err)
			return err
		}
	}

	return nil
}

func (cm *ConfigManager) classifyRecoverablePushError(err error) (bool, string) {
	if err == nil {
		return false, ""
	}

	if errors.Is(err, transport.ErrRepositoryNotFound) {
		return true, "repository not found"
	}
	if errors.Is(err, io.EOF) {
		return true, "unexpected EOF"
	}
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return true, "object not found"
	}
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return true, "reference not found"
	}

	msg := strings.ToLower(err.Error())
	recoverableSubstrings := []struct {
		reason string
		match  string
	}{
		{reason: "object not found", match: "object not found"},
		{reason: "missing object", match: "missing object"},
		{reason: "reference not found", match: "reference not found"},
		{reason: "missing remote ref", match: "couldn't find remote ref"},
		{reason: "reference does not exist", match: "reference does not exist"},
		{reason: "cannot resolve reference", match: "unable to resolve reference"},
	}

	for _, candidate := range recoverableSubstrings {
		if strings.Contains(msg, candidate.match) {
			return true, candidate.reason
		}
	}

	return false, ""
}

func isRepositoryNotFoundError(err error) bool {
	return errors.Is(err, transport.ErrRepositoryNotFound)
}

func (cm *ConfigManager) cloneRepositoryWithBootstrap(path string, conf *config.Config, cloneopt *git.CloneOptions) (*git.Repository, error) {
	repo, cloneErr := git.PlainClone(path, false, cloneopt)
	if isRepositoryNotFoundError(cloneErr) {
		cm.logger.Warnf("none", config.ConstLogModGit, "Remote repository not found. Attempting project bootstrap before retrying clone")
		conf.CreateGitlabProjects()
		repo, cloneErr = git.PlainClone(path, false, cloneopt)
	}

	return repo, cloneErr
}

func (cm *ConfigManager) swapGitMetadata(workDir, stagedGitDir string) error {
	return cm.swapGitMetadataWithRenamer(workDir, stagedGitDir, os.Rename)
}

func (cm *ConfigManager) swapGitMetadataWithRenamer(workDir, stagedGitDir string, renameFn func(oldpath, newpath string) error) error {
	activeGitDir := filepath.Join(workDir, ".git")

	if _, err := os.Stat(stagedGitDir); err != nil {
		return fmt.Errorf("staged git metadata missing at %s: %w", stagedGitDir, err)
	}

	backupGitDir := filepath.Join(workDir, fmt.Sprintf(".git.backup.%d", time.Now().UnixNano()))
	hasOriginalMetadata := false

	if _, err := os.Stat(activeGitDir); err == nil {
		hasOriginalMetadata = true
		cm.logger.Infof("none", config.ConstLogModGit, "Backing up existing .git metadata to %s", backupGitDir)
		if err := renameFn(activeGitDir, backupGitDir); err != nil {
			return fmt.Errorf("cannot backup existing git metadata: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect current git metadata: %w", err)
	}

	cm.logger.Infof("none", config.ConstLogModGit, "Installing refreshed .git metadata into %s", activeGitDir)
	if err := renameFn(stagedGitDir, activeGitDir); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			cm.logger.Warnf("none", config.ConstLogModGit, "Cross-filesystem install detected while moving refreshed .git (%v). Falling back to recursive copy", err)
			if copyErr := copyDirRecursive(stagedGitDir, activeGitDir); copyErr != nil {
				if hasOriginalMetadata {
					if rollbackErr := rollbackGitMetadata(backupGitDir, activeGitDir, renameFn); rollbackErr != nil {
						cm.logger.Errorf("none", config.ConstLogModGit, "Rollback failed after EXDEV fallback failure. Backup retained at %s: %v", backupGitDir, rollbackErr)
					}
				}
				return fmt.Errorf("cannot install refreshed git metadata via EXDEV fallback copy: %w", copyErr)
			}

			if hasOriginalMetadata {
				if rmErr := os.RemoveAll(backupGitDir); rmErr != nil {
					cm.logger.Warnf("none", config.ConstLogModGit, "Refreshed .git copied successfully after EXDEV, but cleanup of backup metadata failed (%s): %v", backupGitDir, rmErr)
				}
			}
			return nil
		}

		if hasOriginalMetadata {
			cm.logger.Warnf("none", config.ConstLogModGit, "Failed to install refreshed metadata, rolling back .git backup: %v", err)
			if rollbackErr := rollbackGitMetadata(backupGitDir, activeGitDir, renameFn); rollbackErr != nil {
				cm.logger.Errorf("none", config.ConstLogModGit, "Rollback failed: original .git backup still at %s: %v", backupGitDir, rollbackErr)
			} else {
				cm.logger.Infof("none", config.ConstLogModGit, "Rollback succeeded. Original .git metadata restored")
			}
		}
		return fmt.Errorf("cannot install refreshed git metadata: %w", err)
	}

	if hasOriginalMetadata {
		if err := os.RemoveAll(backupGitDir); err != nil {
			cm.logger.Warnf("none", config.ConstLogModGit, "Refreshed .git installed but cleanup of backup metadata failed (%s): %v", backupGitDir, err)
		}
	}

	return nil
}

func rollbackGitMetadata(backupGitDir, activeGitDir string, renameFn func(oldpath, newpath string) error) error {
	if _, statErr := os.Stat(activeGitDir); statErr == nil {
		if rmErr := os.RemoveAll(activeGitDir); rmErr != nil {
			return fmt.Errorf("cannot remove partial active git metadata before rollback: %w", rmErr)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("cannot inspect active git metadata before rollback: %w", statErr)
	}

	if err := renameFn(backupGitDir, activeGitDir); err != nil {
		return err
	}

	return nil
}

func copyDirRecursive(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot prepare destination %s: %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}

		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Sync()
}

func resolveCurrentLocalBranch(workDir string) (plumbing.ReferenceName, bool) {
	r, err := git.PlainOpen(workDir)
	if err != nil {
		return "", false
	}

	headRef, err := r.Head()
	if err == nil && headRef.Name().IsBranch() {
		return headRef.Name(), true
	}

	if _, err := r.Reference(plumbing.NewBranchReferenceName("master"), true); err == nil {
		return plumbing.NewBranchReferenceName("master"), true
	}

	return "", false
}

func isReferenceNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "couldn't find remote ref") || strings.Contains(msg, "reference not found")
}

func (cm *ConfigManager) RefreshGitMetadata(conf *config.Config) error {
	url := conf.GitUrl
	tok := conf.GetDecryptedValue("git-acces-token")
	user := conf.GitUsername
	path := conf.WorkingDir

	if url == "" {
		return nil
	}

	auth := &git_https.BasicAuth{
		Username: user,
		Password: tok,
	}

	tmpBase := filepath.Join(path, ".tmp")
	if err := os.MkdirAll(tmpBase, 0o755); err != nil {
		return fmt.Errorf("cannot create metadata temp base %s: %w", tmpBase, err)
	}

	tmpClonePath, err := os.MkdirTemp(tmpBase, "repman-git-refresh-*")
	if err != nil {
		return fmt.Errorf("cannot create metadata temp clone dir: %w", err)
	}
	defer os.RemoveAll(tmpClonePath)

	cloneopt := &git.CloneOptions{
		URL:               url,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth:              auth,
		Depth:             1,
		NoCheckout:        true,
		SingleBranch:      true,
	}

	if refName, ok := resolveCurrentLocalBranch(path); ok {
		cloneopt.ReferenceName = refName
		cloneopt.SingleBranch = true
		cm.logger.Infof("none", config.ConstLogModGit, "Refreshing git metadata using current local branch reference %s", refName)
	} else {
		fallbackRef := plumbing.NewBranchReferenceName("master")
		cloneopt.ReferenceName = fallbackRef
		cloneopt.SingleBranch = true
		cm.logger.Warnf("none", config.ConstLogModGit, "Could not determine current local branch for metadata refresh, falling back to %s", fallbackRef)
	}

	cm.logger.Infof("none", config.ConstLogModGit, "Cloning fresh git metadata into temporary directory for in-place refresh")
	if _, err := cm.cloneRepositoryWithBootstrap(tmpClonePath, conf, cloneopt); err != nil {
		if cloneopt.ReferenceName != "" && isReferenceNotFoundError(err) {
			cm.logger.Warnf("none", config.ConstLogModGit, "Refresh metadata clone with reference %s failed (%v). Retrying with remote default branch", cloneopt.ReferenceName, err)
			if rmErr := os.RemoveAll(tmpClonePath); rmErr != nil {
				return fmt.Errorf("cannot reset temporary clone dir before refresh retry: %w", rmErr)
			}
			if mkErr := os.MkdirAll(tmpClonePath, 0o755); mkErr != nil {
				return fmt.Errorf("cannot recreate temporary clone dir before refresh retry: %w", mkErr)
			}
			cloneopt.ReferenceName = ""
			cloneopt.SingleBranch = false
			if _, retryErr := cm.cloneRepositoryWithBootstrap(tmpClonePath, conf, cloneopt); retryErr != nil {
				return fmt.Errorf("cannot clone refreshed metadata: %w", retryErr)
			}
		} else {
			return fmt.Errorf("cannot clone refreshed metadata: %w", err)
		}
	}

	if err := cm.swapGitMetadata(path, filepath.Join(tmpClonePath, ".git")); err != nil {
		return err
	}

	cm.logger.Infof("none", config.ConstLogModGit, "Git metadata refresh complete. Working tree files preserved in place")
	return nil
}

// Ensures ".pull/" is in .gitignore.
func (cm *ConfigManager) AddPullToGitignore(conf *config.Config) {
	gitignoreFile := conf.WorkingDir + "/.gitignore"
	lineToAdd := ".pull/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error creating .gitignore: %v", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Error opening .gitignore: %v", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Error reading .gitignore: %v", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error appending to .gitignore: %v", err)
		}
	}
}

// Ensures ".tmp/" is in .gitignore.
func (cm *ConfigManager) AddTempDirToGitignore(conf *config.Config) {
	gitignoreFile := conf.WorkingDir + "/.gitignore"
	lineToAdd := ".tmp/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error creating .gitignore: %v", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Error opening .gitignore: %v", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Error reading .gitignore: %v", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error appending to .gitignore: %v", err)
		}
	}
}

func (cm *ConfigManager) PushConfigToGit(conf *config.Config, clusterList []string) error {
	url := conf.GitUrl
	tok := conf.GetDecryptedValue("git-acces-token")
	user := conf.GitUsername
	path := conf.WorkingDir

	// Log basic information
	cm.logger.Debugf("none", config.ConstLogModGit, "Push to git: user=%s, dir=%s, clusters=%v", user, path, clusterList)

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	var r *git.Repository
	start := time.Now()

	// Check if .git directory exists
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		dirEntries, readErr := os.ReadDir(path)
		if readErr != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot read workdir %s: %s", path, readErr)
			return readErr
		}

		cloneopt := &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
			Depth:             1, // Shallow clone
			NoCheckout:        !conf.ConfRestoreOnStart,
			SingleBranch:      true,
		}

		if len(dirEntries) == 0 {
			// Empty dir: clone directly into working dir.
			r, err = cm.cloneRepositoryWithBootstrap(path, conf, cloneopt)
		} else {
			// Non-empty dir: preserve local files by cloning in a temp path and moving only .git.
			tmpBase := filepath.Join(path, ".tmp")
			if mkErr := os.MkdirAll(tmpBase, 0o755); mkErr != nil {
				cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot create temp dir %s: %s", tmpBase, mkErr)
				return mkErr
			}

			tmpClonePath, mkErr := os.MkdirTemp(tmpBase, "repman-git-clone-*")
			if mkErr != nil {
				cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot create temp clone dir: %s", mkErr)
				return mkErr
			}
			defer os.RemoveAll(tmpClonePath)

			if _, err = cm.cloneRepositoryWithBootstrap(tmpClonePath, conf, cloneopt); err == nil {
				if swapErr := cm.swapGitMetadata(path, filepath.Join(tmpClonePath, ".git")); swapErr != nil {
					cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot install cloned .git metadata: %s", swapErr)
					return swapErr
				}
				r, err = git.PlainOpen(path)
			}
		}

		cm.logger.Debugf("none", config.ConstLogModGit, "Clone took: %s", time.Since(start))

		// Handle repository not found
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot clone %s: %s", url, err)
			return err
		}
	} else {
		// Open existing repository
		r, err = git.PlainOpen(path)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot open repo: %s\n", err)
			return err
		}
	}

	// Open the worktree
	w, err := r.Worktree()
	if err != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot get worktree: %s", err)
		return err
	}

	allstart := time.Now()
	cwg := sync.WaitGroup{}
	// Add specific files without using AddGlob
	for _, name := range clusterList {
		dirPath := filepath.Join(path, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error reading directory %s: %s", dirPath, err)
			continue
		}

		// Add .toml files
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".toml" {
				fpath := filepath.Join(name, file.Name())
				_, err := file.Info()
				if err != nil {
					cm.logger.Warnf("none", config.ConstLogModGit, "Error getting file info for %s: %s", fpath, err)
					continue
				}
				cwg.Add(1)
				cm.gitManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: name, Filename: fpath, W: w, WaitGroup: &cwg})
			}
		}

		// Add agents.json and queryrules.json if they exist
		for _, jsonFile := range []string{"agents.json", "queryrules.json", "clusterstate.json"} {
			jsonPath := filepath.Join(name, jsonFile)
			if _, err := os.Stat(filepath.Join(path, jsonPath)); !os.IsNotExist(err) {
				cwg.Add(1)
				cm.gitManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: name, Filename: jsonPath, W: w, WaitGroup: &cwg})
			}
		}

		// Add restic.config.bak if it exists (this will store the restic config which is crucial in case of missing restic config)
		if _, err := os.Stat(filepath.Join(path, name, "restic.config.bak")); !os.IsNotExist(err) {
			cwg.Add(1)
			cm.gitManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: name, Filename: filepath.Join(name, "restic.config.bak"), W: w, WaitGroup: &cwg})
		}
	}

	// Add default.toml if it exists
	defaultToml := "default.toml"
	if _, err := os.Stat(filepath.Join(path, defaultToml)); !os.IsNotExist(err) {
		cwg.Add(1)
		cm.gitManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: "default", Filename: defaultToml, W: w, WaitGroup: &cwg})
	}

	cwg.Wait()

	cm.logger.Debugf("none", config.ConstLogModGit, "Total file add took: %s", time.Since(allstart))

	if cm.gitManager.CommitManager.IsStopping.Load() {
		cm.logger.Info("none", config.ConstLogModGit, "CommitManager is stopping, cancelling commit")
		return nil
	}

	// Commit the changes
	commitStart := time.Now()
	_, err = w.Commit("Update configuration", &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication Manager",
			When: time.Now(),
		},
	})
	cm.logger.Debugf("none", config.ConstLogModGit, "Commit took: %s", time.Since(commitStart))

	if err != nil && !errors.Is(err, git.ErrEmptyCommit) {
		cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot commit: %s", err)
		return err
	}

	// Push changes
	pushStart := time.Now()
	err = r.Push(&git.PushOptions{Auth: auth})
	cm.logger.Debugf("none", config.ConstLogModGit,
		"Push took: %s", time.Since(pushStart))

	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		if errors.Is(err, transport.ErrAuthenticationRequired) {
			cm.RotateGitAccessToken(conf)
		} else {
			cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot push: %s", err)
		}
	}

	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		err = nil
	}
	return err
}

func (cm *ConfigManager) CountAllCommits(conf *config.Config) (int, error) {
	mainPath := conf.WorkingDir

	// Open the repository
	r, err := git.PlainOpen(mainPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open repository at %s: %w", mainPath, err)
	}

	fetchErr := r.Fetch(&git.FetchOptions{Force: true, Auth: &git_https.BasicAuth{Username: conf.GitUsername, Password: conf.GetDecryptedValue("git-acces-token")}})
	if fetchErr != nil && !errors.Is(fetchErr, git.NoErrAlreadyUpToDate) {
		cm.logger.Warnf("none", config.ConstLogModGit, "CountAllCommits: fetch failed, counting local commits only: %v", fetchErr)
	}

	commitIter, err := r.Log(&git.LogOptions{All: true})
	if err != nil {
		return 0, fmt.Errorf("failed to get commit iterator: %w", err)
	}

	commitCount := 0
	// Count commits for this branch/tag
	err = commitIter.ForEach(func(c *git_obj.Commit) error {
		commitCount++
		return nil
	})

	return commitCount, nil
}

func (cm *ConfigManager) ShallowClone(conf *config.Config) error {
	url := conf.GitUrl
	tok := conf.GetDecryptedValue("git-acces-token")
	user := conf.GitUsername
	path := conf.WorkingDir

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	clonestart := time.Now()

	// Perform shallow clone for better performance
	_, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:               url,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth:              auth,
		Depth:             1, // Shallow clone
		NoCheckout:        true,
	})

	cm.logger.Debugf("none", config.ConstLogModGit, "Shallow clone took: %s", time.Since(clonestart))

	return err
}

func (cm *ConfigManager) RotateGitAccessToken(conf *config.Config) error {
	acces_tok, err := githelper.GetGitLabTokenBasicAuth(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
	if err != nil {
		cm.logger.Errorf("none", config.ConstLogModGit, "Error getting GitLab token: %v", err)
		conf.Cloud18 = false
		return err
	}

	tokenName := conf.Cloud18Domain + "-" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
	personal_access_token, _ := githelper.GetGitLabTokenOAuth(acces_tok, tokenName, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
	if personal_access_token == "" {
		personal_access_token, err = githelper.CreatePersonalAccessTokenCSRF(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), tokenName)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error creating personal access token: %v", err)
			return err
		}
	}

	var Secrets config.Secret
	Secrets.Value = personal_access_token
	Secrets.OldValue = conf.Secrets["git-acces-token"].Value
	conf.GitAccesToken = personal_access_token
	conf.Secrets["git-acces-token"] = Secrets

	return nil
}

func LogPanic(clogger *config.LogrusWrapper, component, cluster string, module int) {
	if r := recover(); r != nil {
		clogger.Logger.WithFields(logrus.Fields{
			"cluster":    cluster,
			"module":     config.GetTagsForLog(module),
			"component":  component,
			"panic":      fmt.Sprintf("%v", r),
			"panic_type": fmt.Sprintf("%T", r),
			"stacktrace": string(debug.Stack()),
		}).Error("Recovered panic in manager worker")
	}
}
