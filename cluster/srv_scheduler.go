package cluster

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"log"
	"sync"
	"time"
)

type ScheduledJob struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Schedule    string        `json:"schedule"` // Cron expression
	SQL         string        `json:"sql"`
	Database    string        `json:"database"`
	ServerID    string        `json:"server_id"`  // Target server for execution
	Enabled     bool          `json:"enabled"`
	Timeout     time.Duration `json:"timeout"`
	LastRun     time.Time     `json:"last_run"`
	NextRun     time.Time     `json:"next_run"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Results     string        `json:"results"`     // Store last execution results
	Status      string        `json:"status"`      // success, error, running
}

type JobScheduler struct {
	cron      *cron.Cron
	jobs      map[string]cron.EntryID
	jobObjs   map[string]*ScheduledJob // Track ScheduledJob objects
	cluster   *Cluster
	mutex     sync.Mutex
	store     JobStore
}

// JobStore interface for persisting scheduled jobs
type JobStore interface {
	SaveJobs(jobs []ScheduledJob) error
	LoadJobs() ([]ScheduledJob, error)
}

// InitScheduler initializes a new job scheduler for the cluster
func (cluster *Cluster) InitScheduler(store JobStore) *JobScheduler {
	scheduler := &JobScheduler{
		cron:    cron.New(),
		jobs:    make(map[string]cron.EntryID),
		jobObjs: make(map[string]*ScheduledJob),
		cluster: cluster,
		store:   store,
	}
	
	// Load existing jobs from storage
	if store != nil {
		if jobs, err := store.LoadJobs(); err == nil {
			for _, job := range jobs {
				// Store the job object first
				scheduler.jobObjs[job.ID] = &job
				
				if job.Enabled {
					scheduler.scheduleJobInternal(job)
				}
			}
		}
	}
	
	return scheduler
}

// Start begins the scheduler
func (js *JobScheduler) Start() {
	if js.cron != nil {
		js.cron.Start()
	}
}

// Stop terminates the scheduler
func (js *JobScheduler) Stop() {
	if js.cron != nil {
		js.cron.Stop()
	}
}

// ScheduleJob adds or updates a scheduled SQL job
func (js *JobScheduler) ScheduleJob(job ScheduledJob) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	// Validate required fields
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	if job.Schedule == "" {
		return fmt.Errorf("schedule cannot be empty")
	}
	if job.SQL == "" {
		return fmt.Errorf("SQL cannot be empty")
	}

	// Set timestamps
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now

	// Set default timeout if not specified
	if job.Timeout == 0 {
		job.Timeout = 30 * time.Second
	}

	// Parse the cron schedule to validate it
	_, err := cron.ParseStandard(job.Schedule)
	if err != nil {
		// If standard parsing fails, try parsing with seconds
		parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		_, err = parser.Parse(job.Schedule)
		if err != nil {
			return fmt.Errorf("invalid cron expression: %v", err)
		}
	}

	// Remove existing job if it exists
	if entryID, exists := js.jobs[job.ID]; exists {
		js.cron.Remove(entryID)
		delete(js.jobs, job.ID)
	}

	// Store the job object
	js.jobObjs[job.ID] = &job

	// Schedule the job if enabled
	if job.Enabled {
		if err := js.scheduleJobInternal(job); err != nil {
			return err
		}
	}

	// Persist to storage
	if js.store != nil {
		jobs := js.getAllJobs()
		js.store.SaveJobs(jobs)
	}

	return nil
}

// scheduleJobInternal handles the actual cron scheduling
func (js *JobScheduler) scheduleJobInternal(job ScheduledJob) error {
	// Create the job function that will execute the SQL
	jobFunc := func() {
		js.executeJob(job)
	}

	// Schedule the job with cron
	entryID, err := js.cron.AddFunc(job.Schedule, jobFunc)
	if err != nil {
		return fmt.Errorf("failed to schedule job: %v", err)
	}

	// Store the job reference
	js.jobs[job.ID] = entryID

	// Calculate next run time
	var sched cron.Schedule
	sched, err = cron.ParseStandard(job.Schedule)
	if err != nil {
		parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		sched, _ = parser.Parse(job.Schedule)
	}
	if sched != nil {
		js.jobObjs[job.ID].NextRun = sched.Next(time.Now())
	}

	return nil
}

// executeJob runs a scheduled SQL job
func (js *JobScheduler) executeJob(job ScheduledJob) {
	startTime := time.Now()
	
	// Update job status
	js.mutex.Lock()
	if jobObj, exists := js.jobObjs[job.ID]; exists {
		jobObj.LastRun = startTime
		jobObj.Status = "running"
		jobObj.Results = ""
	}
	js.mutex.Unlock()

	defer func() {
		js.mutex.Lock()
		if jobObj, exists := js.jobObjs[job.ID]; exists {
			// Calculate next run time
			var sched cron.Schedule
			sched, err := cron.ParseStandard(job.Schedule)
			if err != nil {
				parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
				sched, _ = parser.Parse(job.Schedule)
			}
			if sched != nil {
				jobObj.NextRun = sched.Next(time.Now())
			}
		}
		js.mutex.Unlock()
	}()

	// Find target server
	var targetServer *ServerMonitor
	if job.ServerID != "" {
		for _, server := range js.cluster.Servers {
			if server.Id == job.ServerID {
				targetServer = server
				break
			}
		}
		if targetServer == nil {
			js.updateJobStatus(job.ID, "error", fmt.Sprintf("Server with ID %s not found", job.ServerID))
			log.Printf("[Scheduler] Error: Server with ID %s not found for job %s", job.ServerID, job.ID)
			return
		}
	} else {
		// Use master server if no specific server is specified
		if js.cluster.GetMaster() != nil {
			targetServer = js.cluster.GetMaster()
		} else {
			js.updateJobStatus(job.ID, "error", "No master server available")
			log.Printf("[Scheduler] Error: No master server available for job %s", job.ID)
			return
		}
	}

	// Get a database connection
	conn, err := targetServer.GetConnNoBinlog(targetServer.Conn)
	if err != nil {
		js.updateJobStatus(job.ID, "error", fmt.Sprintf("Error getting connection: %v", err))
		log.Printf("[Scheduler] Error getting connection for job %s: %v", job.ID, err)
		return
	}
	defer conn.Close()

	// Set database context if specified
	if job.Database != "" {
		_, err = targetServer.ConnExecQueryWithTimeout(conn, job.Timeout, "USE "+job.Database)
		if err != nil {
			js.updateJobStatus(job.ID, "error", fmt.Sprintf("Error setting database: %v", err))
			log.Printf("[Scheduler] Error setting database for job %s: %v", job.ID, err)
			return
		}
	}

	// Execute the SQL
	result, err := targetServer.ConnExecQueryWithTimeout(conn, job.Timeout, job.SQL)
	if err != nil {
		js.updateJobStatus(job.ID, "error", fmt.Sprintf("Error executing SQL: %v", err))
		log.Printf("[Scheduler] Error executing job %s: %v", job.ID, err)
		return
	}

	// Get result information
	var resultMsg string
	if result != nil {
		if rowsAffected, err := result.RowsAffected(); err == nil {
			resultMsg = fmt.Sprintf("Rows affected: %d", rowsAffected)
		}
		if lastInsertId, err := result.LastInsertId(); err == nil && lastInsertId > 0 {
			resultMsg += fmt.Sprintf(", Last insert ID: %d", lastInsertId)
		}
	}

	js.updateJobStatus(job.ID, "success", resultMsg)
	log.Printf("[Scheduler] Successfully executed job %s on server %s", job.ID, targetServer.Id)
}

// updateJobStatus updates the status and results of a job
func (js *JobScheduler) updateJobStatus(jobID, status, results string) {
	js.mutex.Lock()
	defer js.mutex.Unlock()
	
	if jobObj, exists := js.jobObjs[jobID]; exists {
		jobObj.Status = status
		jobObj.Results = results
		jobObj.UpdatedAt = time.Now()
		
		// Persist to storage
		if js.store != nil {
			jobs := js.getAllJobs()
			js.store.SaveJobs(jobs)
		}
	}
}

// UnscheduleJob removes a scheduled job
func (js *JobScheduler) UnscheduleJob(jobID string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	if entryID, exists := js.jobs[jobID]; exists {
		js.cron.Remove(entryID)
		delete(js.jobs, jobID)
	}
	
	delete(js.jobObjs, jobID)
	
	// Persist to storage
	if js.store != nil {
		jobs := js.getAllJobs()
		js.store.SaveJobs(jobs)
	}
	
	return nil
}

// ListJobs returns all scheduled jobs
func (js *JobScheduler) ListJobs() []ScheduledJob {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	return js.getAllJobs()
}

// getAllJobs returns all jobs (internal method, assumes mutex is held)
func (js *JobScheduler) getAllJobs() []ScheduledJob {
	jobs := make([]ScheduledJob, 0, len(js.jobObjs))
	for _, job := range js.jobObjs {
		jobs = append(jobs, *job)
	}
	return jobs
}

// GetJob returns a specific scheduled job
func (js *JobScheduler) GetJob(jobID string) (*ScheduledJob, error) {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	if job, exists := js.jobObjs[jobID]; exists {
		return job, nil
	}
	return nil, fmt.Errorf("job with ID %s not found", jobID)
}

// GetJobsByServer returns jobs for a specific server
func (js *JobScheduler) GetJobsByServer(serverID string) []ScheduledJob {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	jobs := make([]ScheduledJob, 0)
	for _, job := range js.jobObjs {
		if job.ServerID == serverID || (job.ServerID == "" && js.cluster.GetMaster() != nil && js.cluster.GetMaster().Id == serverID) {
			jobs = append(jobs, *job)
		}
	}
	return jobs
}

// EnableJob enables a job
func (js *JobScheduler) EnableJob(jobID string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	job, exists := js.jobObjs[jobID]
	if !exists {
		return fmt.Errorf("job with ID %s not found", jobID)
	}

	if !job.Enabled {
		job.Enabled = true
		job.UpdatedAt = time.Now()
		
		// Schedule the job
		if err := js.scheduleJobInternal(*job); err != nil {
			job.Enabled = false
			return err
		}

		// Persist to storage
		if js.store != nil {
			jobs := js.getAllJobs()
			js.store.SaveJobs(jobs)
		}
	}

	return nil
}

// DisableJob disables a job
func (js *JobScheduler) DisableJob(jobID string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	job, exists := js.jobObjs[jobID]
	if !exists {
		return fmt.Errorf("job with ID %s not found", jobID)
	}

	if job.Enabled {
		job.Enabled = false
		job.UpdatedAt = time.Now()
		
		// Remove from cron scheduler
		if entryID, exists := js.jobs[jobID]; exists {
			js.cron.Remove(entryID)
			delete(js.jobs, jobID)
		}

		// Persist to storage
		if js.store != nil {
			jobs := js.getAllJobs()
			js.store.SaveJobs(jobs)
		}
	}

	return nil
}

// API helper methods that can be called from the server package

// CreateJobFromJSON creates a scheduled job from JSON data
func (js *JobScheduler) CreateJobFromJSON(jsonData []byte) (interface{}, error) {
	var job ScheduledJob
	if err := json.Unmarshal(jsonData, &job); err != nil {
		return nil, err
	}
	
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	
	if err := js.ScheduleJob(job); err != nil {
		return nil, err
	}
	
	return job, nil
}

// UpdateJobFromJSON updates a scheduled job from JSON data
func (js *JobScheduler) UpdateJobFromJSON(jobID string, jsonData []byte) (interface{}, error) {
	var job ScheduledJob
	if err := json.Unmarshal(jsonData, &job); err != nil {
		return nil, err
	}
	
	job.ID = jobID
	job.UpdatedAt = time.Now()
	
	if err := js.ScheduleJob(job); err != nil {
		return nil, err
	}
	
	return job, nil
}

// GetJobAsInterface returns a job as interface{} for API responses
func (js *JobScheduler) GetJobAsInterface(jobID string) (interface{}, error) {
	job, err := js.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	return *job, nil
}

// ListJobsAsInterface returns jobs as interface{} for API responses
func (js *JobScheduler) ListJobsAsInterface() interface{} {
	jobs := js.ListJobs()
	result := make([]interface{}, len(jobs))
	for i, job := range jobs {
		result[i] = job
	}
	return result
}
