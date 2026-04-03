package manager

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
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
	stopCh      chan struct{}
	wg          sync.WaitGroup
	IsStopping  bool
}

func NewCommitManager(workerMin, workerLimit int, logger *config.LogrusWrapper) *CommitManager {
	cmm := &CommitManager{
		logger:      logger,
		commitQueue: []GitAddTask{},
		stopCh:      make(chan struct{}),
	}

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
	}
}

func (cmm *CommitManager) processCommitQueue() {
	defer LogPanic(cmm.logger)
	defer cmm.wg.Done()

	for {
		select {
		case <-cmm.stopCh:
			cmm.logger.Infof("none", config.ConstLogModGit, "CommitManager is stopping.")
			return
		default:
			cmm.mu.Lock()
			if len(cmm.commitQueue) == 0 {
				cmm.mu.Unlock()

				time.Sleep(100 * time.Millisecond) // Avoid busy waiting
				continue
			}

			// Fetch and remove the first task
			task := cmm.commitQueue[0]
			cmm.commitQueue = cmm.commitQueue[1:]
			cmm.mu.Unlock()

			cmm.logger.Debugf("none", config.ConstLogModGit, "CommitManager processing file: %s", task.Filename)
			cmm.addFileToCommit(task)
		}
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
	close(cmm.stopCh)
	cmm.IsStopping = true
	for _, task := range cmm.commitQueue {
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
	isStopping  bool                       // Prevents new saves after stopping
	clusterData map[string]*ClusterManager // Map of clusters and their respective managers
	gitManager  *GitManager                // Pull Push manager
}

// NewConfigManager initializes the manager
func NewConfigManager(logger *config.LogrusWrapper) *ConfigManager {
	newcm := &ConfigManager{
		logger:      logger,
		clusterData: make(map[string]*ClusterManager),
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
	if clusterManager, exists := cm.clusterData[cluster]; exists {
		clusterManager.mutex.Lock()
		defer clusterManager.mutex.Unlock()
		return len(clusterManager.tasks)
	}
	return 0
}

// SaveConfig allows concurrent saves but respects stopping
func (cm *ConfigManager) SaveConfig(cluster ClusterConfig, wait bool) {
	configSaveTask := &ConfigSaveTask{Cluster: cluster}
	clustername := cluster.GetName()

	if cm.isStopping {
		cm.logger.Debugf(clustername, config.ConstLogModGeneral, "[%s] Save blocked: system is stopping.\n", configSaveTask.Cluster)
		return
	}
	if wait {
		wg := sync.WaitGroup{}
		configSaveTask.WaitGroup = &wg
		configSaveTask.WaitGroup.Add(1)
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save with wait requested.\n", configSaveTask.Cluster)
	}

	if cm.isStopping {
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save blocked: system is stopping.\n", configSaveTask.Cluster)
		return
	}

	// Ensure each cluster has a ClusterManager (with tasks, mutex, stop channel)
	if _, exists := cm.clusterData[clustername]; !exists {
		cm.clusterData[clustername] = &ClusterManager{
			tasks:  []*ConfigSaveTask{},
			mutex:  &sync.Mutex{},
			stopCh: make(chan struct{}), // Initialize stop channel for the cluster
		}
		// Initialize the condition variable with the mutex
		cm.clusterData[clustername].cond = sync.NewCond(cm.clusterData[clustername].mutex)
		go cm.processClusterQueue(clustername) // Start the persistent goroutine for the cluster
	}

	// Lock the cluster's mutex to safely add to the task slice
	cm.clusterData[clustername].mutex.Lock()
	cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Appending to save queue.\n", clustername)

	cm.clusterData[clustername].tasks = append(cm.clusterData[clustername].tasks, configSaveTask)
	// Signal the goroutine that a new task is available
	cm.clusterData[clustername].cond.Signal()
	cm.clusterData[clustername].mutex.Unlock()

	// If a WaitGroup pointer is provided, add to the wait group
	if configSaveTask.WaitGroup != nil {
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request waiting.\n", configSaveTask.Cluster)
		configSaveTask.WaitGroup.Wait()
		cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request wait over.\n", configSaveTask.Cluster)
	}

	cm.logger.Debugf(clustername, config.ConstLogModConfigLoad, "[%s] Save request completed.\n", configSaveTask.Cluster)
}

// processClusterQueue processes the tasks in the slice for a given cluster
func (cm *ConfigManager) processClusterQueue(cluster string) {
	defer LogPanic(cm.logger)

	for {
		// Lock the cluster's mutex to safely check for tasks
		cm.clusterData[cluster].mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(cm.clusterData[cluster].tasks) == 0 {
			select {
			case <-cm.clusterData[cluster].stopCh: // Stop signal for the goroutine
				cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Stopping goroutine.\n", cluster)
				cm.clusterData[cluster].mutex.Unlock()
				return
			default:
			}

			cm.clusterData[cluster].cond.Wait()
		}

		cm.logger.Debugf(cluster, config.ConstLogModConfigLoad, "[%s] Waking up goroutine.\n", cluster)

		// Check for the stop signal before processing
		select {
		case <-cm.clusterData[cluster].stopCh: // Stop signal for the goroutine
			cm.clusterData[cluster].isStopping = true
			cm.clusterData[cluster].mutex.Unlock()
		default:
			// Process the first task in the queue
			configSaveTask := cm.clusterData[cluster].tasks[0]
			skippedTasks := cm.clusterData[cluster].tasks[1:]
			cm.clusterData[cluster].tasks = make([]*ConfigSaveTask, 0) // remove the current batch since they doing the same thing
			cm.clusterData[cluster].mutex.Unlock()

			cm.gitMutex.Lock() // Prevent Git push conflict
			cm.configWg.Add(1)
			cm.gitMutex.Unlock()

			// Execute the save function and handle potential errors
			if err := configSaveTask.Cluster.Save(); err != nil {
				cm.logger.Errorf(cluster, config.ConstLogModConfigLoad, "Error during save: %v", err)
			} else {
				cm.logger.Infof(cluster, config.ConstLogModConfigLoad, "Config saved successfully.")
			}

			// If a WaitGroup pointer is provided, mark the task as done
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

			cm.configWg.Done()
			if cm.clusterData[cluster].isStopping {
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
	defer LogPanic(cm.logger)

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
			cm.logger.Debugf("none", config.ConstLogModGit, "[Git] Stopping goroutine.")
			cm.gitManager.mutex.Unlock()
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

		cm.isStopping = true // Prevent new saves

		cm.gitMutex.Lock()
		defer cm.gitMutex.Unlock()

		// Send stop signal to all cluster goroutines
		for _, clmgr := range cm.clusterData {
			close(clmgr.stopCh)
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
		if err == transport.ErrRepositoryNotFound || err == io.EOF {
			os.RemoveAll(conf.WorkingDir + "/.git")
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
		os.RemoveAll(conf.WorkingDir + "/.git")
		err := cm.ShallowClone(conf)
		if err != nil {
			cm.logger.Errorf("none", config.ConstLogModGit, "Error shallow cloning: %v", err)
			return err
		}
	}

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
		cloneopt := &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
			Depth:             1, // Shallow clone
		}

		if !conf.ConfRestoreOnStart {
			cloneopt.NoCheckout = true
		}

		// Perform shallow clone for better performance
		r, err = git.PlainClone(path, false, cloneopt)

		cm.logger.Debugf("none", config.ConstLogModGit, "Clone took: %s", time.Since(start))

		// Handle repository not found
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, cloneopt)
			}
			if err != nil {
				cm.logger.Errorf("none", config.ConstLogModGit, "Git error: cannot clone %s: %s", url, err)
				return err
			}
		}

		// After cloning with NoCheckout, create local master branch and checkout to fix push issues
		if !conf.ConfRestoreOnStart && err == nil {
			// Get remote master HEAD
			remoteRef, refErr := r.Reference("refs/remotes/origin/master", true)
			if refErr == nil {
				// Create local master branch pointing to remote HEAD
				localRef := plumbing.NewHashReference("refs/heads/master", remoteRef.Hash())
				setErr := r.Storer.SetReference(localRef)
				if setErr == nil {
					w, wtErr := r.Worktree()
					if wtErr == nil {
						// Checkout to set HEAD to local master branch, Keep=true preserves local files
						checkoutErr := w.Checkout(&git.CheckoutOptions{
							Branch: "refs/heads/master",
							Keep:   true,
						})
						if checkoutErr != nil {
							cm.logger.Warnf("none", config.ConstLogModGit, "Git checkout warning (non-fatal): %s", checkoutErr)
						} else {
							cm.logger.Debugf("none", config.ConstLogModGit, "Created local master branch and checked out successfully")
						}
					} else {
						cm.logger.Warnf("none", config.ConstLogModGit, "Git worktree warning (non-fatal): %s", wtErr)
					}
				} else {
					cm.logger.Warnf("none", config.ConstLogModGit, "Git set reference warning (non-fatal): %s", setErr)
				}
			} else {
				cm.logger.Warnf("none", config.ConstLogModGit, "Git remote reference warning (non-fatal): %s", refErr)
			}
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

	if cm.gitManager.CommitManager.IsStopping {
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

	r.Fetch(&git.FetchOptions{Force: true, Auth: &git_https.BasicAuth{Username: conf.GitUsername, Password: conf.GetDecryptedValue("git-acces-token")}})

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

func LogPanic(clogger *config.LogrusWrapper) {
	if r := recover(); r != nil {
		fields := logrus.Fields{
			"cluster":    "none",
			"panic":      r,
			"stacktrace": string(debug.Stack()),
		}
		clogger.Print(fields, "Panic recovered in processClusterQueue")
	}
}
