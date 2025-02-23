package manager

import (
	"fmt"
	"sync"
)

// ConfigSaveTask holds the parameters for saving a config and additional waiting functionality
type ConfigSaveTask struct {
	Cluster   string          // The cluster to which the task belongs
	SaveFunc  func() error    // The function that performs the save
	WaitGroup *sync.WaitGroup // Pointer to sync.WaitGroup for additional waiting
}

type ConfigPushTask struct {
	PushFunc  func() error    // The function that performs the save
	WaitGroup *sync.WaitGroup // Pointer to sync.WaitGroup for additional waiting
}

// ClusterManager holds the necessary fields for each cluster
type ClusterManager struct {
	tasks  []ConfigSaveTask // Slice of tasks for the cluster
	mutex  sync.Mutex       // Mutex for safe access to tasks
	cond   *sync.Cond       // Condition variable for waiting and notifying tasks
	stopCh chan struct{}    // Stop channel to signal the goroutine to stop
}

// Push Manager
type PushManager struct {
	tasks  []ConfigPushTask // Slice of tasks for the push queue
	mutex  sync.Mutex       // Mutex for safe access to tasks
	cond   *sync.Cond       // Condition variable for waiting and notifying tasks
	stopCh chan struct{}    // Stop channel to signal the goroutine to stop
}

// ConfigManager controls config saves & Git push
type ConfigManager struct {
	configWg    sync.WaitGroup             // Tracks ongoing config saves
	gitMutex    sync.Mutex                 // Blocks new saves during Git push
	stopOnce    sync.Once                  // Ensures Stop() runs only once
	isStopping  bool                       // Prevents new saves after stopping
	clusterData map[string]*ClusterManager // Map of clusters and their respective managers
	pushManager PushManager                // Push manager
}

// NewConfigManager initializes the manager
func NewConfigManager() *ConfigManager {
	newcm := &ConfigManager{
		clusterData: make(map[string]*ClusterManager),
		pushManager: PushManager{
			tasks:  []ConfigPushTask{},
			stopCh: make(chan struct{}), // Initialize stop channel for the push manager
		},
	}

	newcm.pushManager.cond = sync.NewCond(&newcm.pushManager.mutex)
	go newcm.processGitPush() // Start the persistent goroutine for the push manager

	return newcm
}

// SaveConfig allows concurrent saves but respects stopping
func (cm *ConfigManager) SaveConfig(clustername string, saveFunc func() error, wait bool) {
	configSaveTask := ConfigSaveTask{Cluster: clustername, SaveFunc: saveFunc}

	if wait {
		wg := sync.WaitGroup{}
		configSaveTask.WaitGroup = &wg
	}

	if cm.isStopping {
		fmt.Printf("[%s] Save blocked: system is stopping.\n", configSaveTask.Cluster)
		return
	}

	// Ensure each cluster has a ClusterManager (with tasks, mutex, stop channel)
	if _, exists := cm.clusterData[configSaveTask.Cluster]; !exists {
		cm.clusterData[configSaveTask.Cluster] = &ClusterManager{
			tasks:  []ConfigSaveTask{},
			stopCh: make(chan struct{}), // Initialize stop channel for the cluster
		}
		// Initialize the condition variable with the mutex
		cm.clusterData[configSaveTask.Cluster].cond = sync.NewCond(&cm.clusterData[configSaveTask.Cluster].mutex)
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
		configSaveTask.WaitGroup.Add(1)
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
			fmt.Printf("[%s] Stopping goroutine.\n", cluster)
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
				fmt.Printf("[%s] Error during save: %v\n", cluster, err)
			} else {
				fmt.Printf("[%s] Config saved successfully.\n", cluster)
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
func (cm *ConfigManager) GitPush(pushFunc func() error, wait bool) {

	configPushTask := ConfigPushTask{PushFunc: pushFunc}

	if wait {
		wg := sync.WaitGroup{}
		configPushTask.WaitGroup = &wg
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
		configPushTask.WaitGroup.Add(1)
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
			fmt.Println("[Git] Waking up goroutine.")
		}

		// Check for the stop signal before processing
		select {
		case <-cm.pushManager.stopCh: // Stop signal for the goroutine
			fmt.Println("[Git] Stopping goroutine.")
			cm.pushManager.mutex.Unlock()
			return
		default:
			// Process the first task in the queue
			configPushTask := cm.pushManager.tasks[0]
			skippedTasks := cm.pushManager.tasks[1:]
			cm.pushManager.tasks = make([]ConfigPushTask, 0) // remove the current batch since they doing the same thing
			cm.pushManager.mutex.Unlock()

			fmt.Println("Locking git mutex")
			cm.gitMutex.Lock() // Block new config saves
			fmt.Println("Waiting for active saves to finish...")
			cm.configWg.Wait() // Ensure all active saves finish

			// Execute the save function and handle potential errors
			if err := configPushTask.PushFunc(); err != nil {
				// Execute the Git push function and handle potential errors
				fmt.Printf("[Git] Error during push: %v\n", err)
			} else {
				fmt.Println("[Git] Git push completed successfully.")
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
		fmt.Println("[Shutdown] Stopping...")

		cm.isStopping = true // Prevent new saves

		cm.gitMutex.Lock()
		defer cm.gitMutex.Unlock()

		// Send stop signal to all cluster goroutines
		for cluster := range cm.clusterData {
			close(cm.clusterData[cluster].stopCh)
			cm.clusterData[cluster].cond.Signal() // Wake up the cluster goroutine
		}

		fmt.Println("[Shutdown] Waiting for active saves to finish...")
		cm.configWg.Wait()

		close(cm.pushManager.stopCh) // Send stop signal to the push manager
		cm.pushManager.cond.Signal() // Wake up the push manager
		fmt.Println("[Shutdown] Config manager stopped.")
	})
}
