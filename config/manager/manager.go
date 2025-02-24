package manager

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v5"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/config"
)

// ConfigSaveTask holds the parameters for saving a config and additional waiting functionality
type ConfigSaveTask struct {
	Cluster   string          // The cluster to which the task belongs
	SaveFunc  func() error    // The function that performs the save
	WaitGroup *sync.WaitGroup // Pointer to sync.WaitGroup for additional waiting
}

type ConfigPushTask struct {
	conf        config.Config
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
	tasks  []ConfigSaveTask // Slice of tasks for the cluster
	mutex  *sync.Mutex      // Mutex for safe access to tasks
	cond   *sync.Cond       // Condition variable for waiting and notifying tasks
	stopCh chan struct{}    // Stop channel to signal the goroutine to stop
}

// Push Manager
type PushManager struct {
	logger        *config.LogrusWrapper
	IsPushing     bool             // Flag to indicate if a push is in progress
	tasks         []ConfigPushTask // Slice of tasks for the push queue
	mutex         *sync.Mutex      // Mutex for safe access to tasks
	cond          *sync.Cond       // Condition variable for waiting and notifying tasks
	stopCh        chan struct{}    // Stop channel to signal the goroutine to stop
	CommitManager *CommitManager   // Commit manager
}

func NewPushManager(logger *config.LogrusWrapper) *PushManager {
	return &PushManager{
		logger:        logger,
		tasks:         []ConfigPushTask{},
		mutex:         &sync.Mutex{},
		stopCh:        make(chan struct{}), // Initialize stop channel for the push manager
		CommitManager: NewCommitManager(1, 10, logger),
	}
}

type CommitManager struct {
	logger        *config.LogrusWrapper
	W             *git.Worktree
	commitQueue   []GitAddTask // Slice for commit tasks
	mu            sync.Mutex   // Mutex to protect commitQueue
	stopCh        chan struct{}
	wg            sync.WaitGroup
	workerLimit   int
	workerMin     int
	currentWorker atomic.Int32
	IsStopping    bool
}

func NewCommitManager(workerMin, workerLimit int, logger *config.LogrusWrapper) *CommitManager {
	cmm := &CommitManager{
		logger:      logger,
		commitQueue: []GitAddTask{},
		stopCh:      make(chan struct{}),
		workerLimit: workerLimit,
		workerMin:   workerMin,
	}

	cmm.Start()
	return cmm
}

func (cmm *CommitManager) Start() {
	for i := 0; i < cmm.workerMin; i++ {
		cmm.wg.Add(1)
		cmm.currentWorker.Add(1)
		go cmm.processCommitQueue()
	}
}

func (cmm *CommitManager) AddFileToCommit(task GitAddTask) {
	cmm.mu.Lock()
	defer cmm.mu.Unlock()

	select {
	case <-cmm.stopCh:
		cmm.logger.Infof("default", config.ConstLogModGit, "CommitManager is stopping, rejecting task: %s", task.Filename)
		if task.WaitGroup != nil {
			task.WaitGroup.Done()
		}
	default:
		cmm.commitQueue = append(cmm.commitQueue, task)
		// Start a new worker if the queue is not empty and the current worker count is less than the limit
		if len(cmm.commitQueue) > int(cmm.currentWorker.Load()) && int(cmm.currentWorker.Load()) < cmm.workerLimit {
			cmm.currentWorker.Add(1)
			cmm.wg.Add(1)
			go cmm.processCommitQueue()
		}
	}
}

func (cmm *CommitManager) processCommitQueue() {
	defer cmm.wg.Done()

	for {
		select {
		case <-cmm.stopCh:
			cmm.logger.Infof("default", config.ConstLogModGit, "CommitManager is stopping.")
			return
		default:
			cmm.mu.Lock()
			if len(cmm.commitQueue) == 0 {
				cmm.mu.Unlock()

				if int(cmm.currentWorker.Load()) > cmm.workerMin {
					cmm.currentWorker.Add(-1)
					return
				}

				time.Sleep(100 * time.Millisecond) // Avoid busy waiting
				continue
			}

			// Fetch and remove the first task
			task := cmm.commitQueue[0]
			cmm.commitQueue = cmm.commitQueue[1:]
			cmm.mu.Unlock()

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
		cmm.logger.Debugf("default", config.ConstLogModGit, "File %s added in: %s", task.Filename, time.Since(start))
	} else {
		cmm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot add %s: %s", task.Filename, err)
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
	cmm.logger.Infof("default", config.ConstLogModGit, "CommitManager stopped.")
}

// ConfigManager controls config saves & Git push
type ConfigManager struct {
	logger      *config.LogrusWrapper
	configWg    *sync.WaitGroup            // Tracks ongoing config saves
	gitMutex    *sync.Mutex                // Blocks new saves during Git push
	stopOnce    sync.Once                  // Ensures Stop() runs only once
	isStopping  bool                       // Prevents new saves after stopping
	clusterData map[string]*ClusterManager // Map of clusters and their respective managers
	pushManager *PushManager               // Push manager
}

// NewConfigManager initializes the manager
func NewConfigManager(logger *config.LogrusWrapper) *ConfigManager {
	newcm := &ConfigManager{
		logger:      logger,
		clusterData: make(map[string]*ClusterManager),
		gitMutex:    &sync.Mutex{},
		configWg:    &sync.WaitGroup{},
		pushManager: NewPushManager(logger),
	}

	newcm.pushManager.cond = sync.NewCond(newcm.pushManager.mutex)

	go newcm.processGitPush() // Start the persistent goroutine for the push manager

	return newcm
}

func (cm *ConfigManager) UpdateLoggerConfig(clustername string, conf *config.Config) {
	cm.logger.UpdateConfig(clustername, conf)
}

// SaveConfig allows concurrent saves but respects stopping
func (cm *ConfigManager) SaveConfig(clustername string, saveFunc func() error, wait bool) {
	configSaveTask := ConfigSaveTask{Cluster: clustername, SaveFunc: saveFunc}

	if wait {
		wg := sync.WaitGroup{}
		configSaveTask.WaitGroup = &wg
		configSaveTask.WaitGroup.Add(1)
	}

	if cm.isStopping {
		cm.logger.Debugf(clustername, config.ConstLogModGeneral, "[%s] Save blocked: system is stopping.\n", configSaveTask.Cluster)
		return
	}

	// Ensure each cluster has a ClusterManager (with tasks, mutex, stop channel)
	if _, exists := cm.clusterData[configSaveTask.Cluster]; !exists {
		cm.clusterData[configSaveTask.Cluster] = &ClusterManager{
			tasks:  []ConfigSaveTask{},
			mutex:  &sync.Mutex{},
			stopCh: make(chan struct{}), // Initialize stop channel for the cluster
		}
		// Initialize the condition variable with the mutex
		cm.clusterData[configSaveTask.Cluster].cond = sync.NewCond(cm.clusterData[configSaveTask.Cluster].mutex)
		go cm.processClusterQueue(configSaveTask.Cluster) // Start the persistent goroutine for the cluster
	}

	// Lock the cluster's mutex to safely add to the task slice
	cm.clusterData[configSaveTask.Cluster].mutex.Lock()
	cm.clusterData[configSaveTask.Cluster].tasks = append(cm.clusterData[configSaveTask.Cluster].tasks, configSaveTask)
	// Signal the goroutine that a new task is available
	cm.clusterData[configSaveTask.Cluster].mutex.Unlock()
	cm.clusterData[configSaveTask.Cluster].cond.Signal()

	// If a WaitGroup pointer is provided, add to the wait group
	if configSaveTask.WaitGroup != nil {
		configSaveTask.WaitGroup.Wait()
	}
}

// processClusterQueue processes the tasks in the slice for a given cluster
func (cm *ConfigManager) processClusterQueue(cluster string) {
	for {
		// Lock the cluster's mutex to safely check for tasks
		cm.clusterData[cluster].mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(cm.clusterData[cluster].tasks) == 0 {
			cm.clusterData[cluster].cond.Wait()
		}

		// Check for the stop signal before processing
		select {
		case <-cm.clusterData[cluster].stopCh: // Stop signal for the goroutine
			cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Stopping goroutine.\n", cluster)
			cm.clusterData[cluster].mutex.Unlock()
			return
		default:
			// Process the first task in the queue
			configSaveTask := cm.clusterData[cluster].tasks[0]
			skippedTasks := cm.clusterData[cluster].tasks[1:]
			cm.clusterData[cluster].tasks = make([]ConfigSaveTask, 0) // remove the current batch since they doing the same thing
			cm.clusterData[cluster].mutex.Unlock()

			cm.gitMutex.Lock() // Prevent Git push conflict
			cm.configWg.Add(1)
			cm.gitMutex.Unlock()

			// Execute the save function and handle potential errors
			if err := configSaveTask.SaveFunc(); err != nil {
				cm.logger.Errorf(cluster, config.ConstLogModGeneral, "[%s] Error during save: %v\n", cluster, err)
			} else {
				cm.logger.Infof(cluster, config.ConstLogModGeneral, "[%s] Config saved successfully.\n", cluster)
			}

			// If a WaitGroup pointer is provided, mark the task as done
			if configSaveTask.WaitGroup != nil {
				configSaveTask.WaitGroup.Done()
			}

			for _, task := range skippedTasks {
				if task.WaitGroup != nil {
					task.WaitGroup.Done()
				}
			}

			cm.configWg.Done()
		}
	}
}

// GitPush waits for active saves, blocks new ones, and pushes changes
func (cm *ConfigManager) GitPush(conf config.Config, clusterList []string, wait bool) {

	configPushTask := ConfigPushTask{conf: conf, clusterList: clusterList}

	if wait {
		wg := sync.WaitGroup{}
		configPushTask.WaitGroup = &wg
		configPushTask.WaitGroup.Add(1)
	}

	fmt.Println("Locking push mutex")
	// Lock the cluster's mutex to safely add to the task slice
	cm.pushManager.mutex.Lock()
	fmt.Println("Appending to push queue")
	cm.pushManager.tasks = append(cm.pushManager.tasks, configPushTask)
	// Signal the goroutine that a new task is available
	cm.pushManager.mutex.Unlock()
	cm.pushManager.cond.Signal()

	// If a WaitGroup pointer is provided, add to the wait group
	if configPushTask.WaitGroup != nil {
		configPushTask.WaitGroup.Wait()
	}
}

// processClusterQueue processes the tasks in the slice for a given cluster
func (cm *ConfigManager) processGitPush() {
	for {
		cm.pushManager.mutex.Lock()

		// Wait until there is at least one task in the queue
		for len(cm.pushManager.tasks) == 0 {
			cm.pushManager.cond.Wait()
			cm.logger.Debugf("default", config.ConstLogModGit, "[Git] Waking up goroutine.")
		}

		// Check for the stop signal before processing
		select {
		case <-cm.pushManager.stopCh: // Stop signal for the goroutine
			cm.logger.Debugf("default", config.ConstLogModGit, "[Git] Stopping goroutine.")
			cm.pushManager.mutex.Unlock()
			return
		default:
			// Process the first task in the queue
			configPushTask := cm.pushManager.tasks[0]
			skippedTasks := cm.pushManager.tasks[1:]
			cm.pushManager.tasks = make([]ConfigPushTask, 0) // remove the current batch since they doing the same thing
			cm.pushManager.mutex.Unlock()

			cm.logger.Debugf("default", config.ConstLogModGit, "Locking git mutex")
			cm.gitMutex.Lock() // Block new config saves
			cm.logger.Debugf("default", config.ConstLogModGit, "Waiting for active saves to finish...")
			cm.configWg.Wait() // Ensure all active saves finish

			cm.logger.Debugf("default", config.ConstLogModGit, "[Git] Starting Git push...")
			// Execute the save function and handle potential errors
			if err := cm.PushAllConfigsToGit(configPushTask.conf, configPushTask.clusterList); err != nil {
				// Execute the Git push function and handle potential errors
				cm.logger.Errorf("default", config.ConstLogModGit, "[Git] Error during push: %v\n", err)
			} else {
				cm.logger.Infof("default", config.ConstLogModGit, "[Git] Git push completed successfully.")
			}

			// If a WaitGroup pointer is provided, mark the task as done
			if configPushTask.WaitGroup != nil {
				configPushTask.WaitGroup.Done()
			}

			for _, task := range skippedTasks {
				if task.WaitGroup != nil {
					task.WaitGroup.Done()
				}
			}

			cm.gitMutex.Unlock()
		}
	}
}

// Stop gracefully shuts down the system
func (cm *ConfigManager) Stop() {
	cm.stopOnce.Do(func() {
		cm.logger.Infof("default", config.ConstLogModGeneral, "[Shutdown] Stopping...")

		cm.isStopping = true // Prevent new saves

		cm.gitMutex.Lock()
		defer cm.gitMutex.Unlock()

		// Send stop signal to all cluster goroutines
		for cluster := range cm.clusterData {
			close(cm.clusterData[cluster].stopCh)
			cm.clusterData[cluster].cond.Signal() // Wake up the cluster goroutine
		}

		cm.logger.Infof("default", config.ConstLogModGeneral, "[Shutdown] Waiting for active saves to finish...")
		cm.configWg.Wait()

		close(cm.pushManager.stopCh) // Send stop signal to the push manager
		cm.pushManager.cond.Signal() // Wake up the push manager
		cm.logger.Infof("default", config.ConstLogModGeneral, "[Shutdown] Config manager stopped.")
	})
}

func (cm *ConfigManager) PushAllConfigsToGit(conf config.Config, clusterList []string) error {
	defer func() {
		if r := recover(); r != nil {
			cm.logger.Errorf("default", config.ConstLogModGeneral, "Error pushing to git: %v", r)
		}
	}()

	cm.pushManager.IsPushing = true
	defer func() {
		cm.pushManager.IsPushing = false
	}()

	if conf.GitUrl == "" {
		cm.logger.Infof("default", config.ConstLogModGit, "No Git URL provided, skipping push")
		return nil
	}

	cm.AddPullToGitignore(&conf)
	cm.AddTempDirToGitignore(&conf)

	cm.logger.Infof("default", config.ConstLogModGit, "Pushing All Configs To Git")

	err := cm.PushConfigToGit(&conf, clusterList)
	if err != nil && err == transport.ErrRepositoryNotFound {
		os.RemoveAll(conf.WorkingDir + "/.git")
		err := cm.PushConfigToGit(&conf, clusterList)
		if err != nil {
			cm.logger.Errorf("default", config.ConstLogModGit, "Error pushing to git: %v", err)
			return err
		}
	}

	// Count the commits
	commits, err := cm.CountAllCommits(&conf)
	if err != nil {
		cm.logger.Warnf("default", config.ConstLogModGit, "Error counting commits: %v", err)
		return err
	}

	if commits >= 10 {
		os.RemoveAll(conf.WorkingDir + "/.git")
		err := cm.ShallowClone(&conf)
		if err != nil {
			cm.logger.Errorf("default", config.ConstLogModGit, "Error shallow cloning: %v", err)
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
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
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
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
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
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
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
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
		}
	}
}

func (cm *ConfigManager) PushConfigToGit(conf *config.Config, clusterList []string) error {
	url := conf.GitUrl
	tok := conf.GetDecryptedValue("git-acces-token")
	user := conf.GitUsername
	path := conf.WorkingDir

	// Log basic information
	cm.logger.Debugf("default", config.ConstLogModGit, "Push to git: user=%s, dir=%s, clusters=%v", user, path, clusterList)

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

		cm.logger.Debugf("default", config.ConstLogModGit, "Clone took: %s", time.Since(start))

		// Handle repository not found
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, cloneopt)
			}
			if err != nil {
				cm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot clone %s: %s", url, err)
				return err
			}
		}
	} else {
		// Open existing repository
		r, err = git.PlainOpen(path)
		if err != nil {
			cm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot open repo: %s\n", err)
			return err
		}
	}

	// Open the worktree
	w, err := r.Worktree()
	if err != nil {
		cm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot get worktree: %s", err)
		return err
	}

	allstart := time.Now()
	cwg := sync.WaitGroup{}
	// Add specific files without using AddGlob
	for _, name := range clusterList {
		dirPath := filepath.Join(path, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			cm.logger.Errorf("default", config.ConstLogModGit, "Error reading directory %s: %s", dirPath, err)
			continue
		}

		// Add .toml files
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".toml" {
				cwg.Add(1)
				fpath := filepath.Join(name, file.Name())
				cm.pushManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: name, Filename: fpath, W: w, WaitGroup: &cwg})
			}
		}

		// Add agents.json and queryrules.json if they exist
		for _, jsonFile := range []string{"agents.json", "queryrules.json"} {
			jsonPath := filepath.Join(name, jsonFile)
			if _, err := os.Stat(filepath.Join(path, jsonPath)); !os.IsNotExist(err) {
				cwg.Add(1)
				cm.pushManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: name, Filename: jsonPath, W: w, WaitGroup: &cwg})
			}
		}
	}

	// Add default.toml if it exists
	defaultToml := "default.toml"
	if _, err := os.Stat(filepath.Join(path, defaultToml)); !os.IsNotExist(err) {
		cwg.Add(1)
		cm.pushManager.CommitManager.AddFileToCommit(GitAddTask{Cluster: "default", Filename: defaultToml, W: w, WaitGroup: &cwg})
	}

	cwg.Wait()

	cm.logger.Debugf("default", config.ConstLogModGit, "Total file add took: %s", time.Since(allstart))

	if cm.pushManager.CommitManager.IsStopping {
		cm.logger.Info("default", config.ConstLogModGit, "CommitManager is stopping, cancelling commit")
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
	cm.logger.Debugf("default", config.ConstLogModGit, "Commit took: %s", time.Since(commitStart))

	if err != nil {
		cm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot commit: %s", err)
		return err
	}

	// Push changes
	pushStart := time.Now()
	err = r.Push(&git.PushOptions{Auth: auth})
	cm.logger.Debugf("default", config.ConstLogModGit,
		"Push took: %s", time.Since(pushStart))

	if err != nil {
		cm.logger.Errorf("default", config.ConstLogModGit, "Git error: cannot push: %s", err)
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

	cm.logger.Debugf("default", config.ConstLogModGit, "Shallow clone took: %s", time.Since(clonestart))

	return err
}
