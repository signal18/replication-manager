package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/signal18/replication-manager/cluster"
)

func main() {
	// Create a simple example of how the SQL scheduler works
	store := cluster.NewFileJobStore("/tmp")
	
	// Create a mock cluster (for demonstration)
	testCluster := &cluster.Cluster{
		Name: "test-cluster",
	}
	
	// Initialize the scheduler
	testCluster.JobScheduler = testCluster.InitScheduler(store)
	testCluster.JobScheduler.Start()
	defer testCluster.JobScheduler.Stop()
	
	// Create a test job
	job := cluster.ScheduledJob{
		ID:          "test-job-1",
		Name:        "Daily Cleanup",
		Description: "Clean up old temporary tables",
		Schedule:    "0 2 * * *", // Run at 2 AM daily (standard format)
		SQL:         "DELETE FROM temp_table WHERE created_at < NOW() - INTERVAL 7 DAY",
		Database:    "test_db",
		Enabled:     true,
		Timeout:     30 * time.Second,
	}
	
	// Schedule the job
	err := testCluster.JobScheduler.ScheduleJob(job)
	if err != nil {
		log.Fatalf("Failed to schedule job: %v", err)
	}
	
	// List all jobs
	jobs := testCluster.JobScheduler.ListJobs()
	fmt.Printf("Scheduled jobs: %d\n", len(jobs))
	
	// Convert to JSON for demonstration
	jsonData, _ := json.MarshalIndent(jobs, "", "  ")
	fmt.Printf("Jobs JSON:\n%s\n", jsonData)
	
	// Test enabling/disabling
	fmt.Println("\nTesting job enable/disable...")
	
	err = testCluster.JobScheduler.DisableJob("test-job-1")
	if err != nil {
		log.Printf("Error disabling job: %v", err)
	} else {
		fmt.Println("Job disabled successfully")
	}
	
	err = testCluster.JobScheduler.EnableJob("test-job-1")
	if err != nil {
		log.Printf("Error enabling job: %v", err)
	} else {
		fmt.Println("Job enabled successfully")
	}
	
	// Test the API helper methods
	jobJSON, _ := json.Marshal(job)
	newJob, err := testCluster.JobScheduler.CreateJobFromJSON(jobJSON)
	if err != nil {
		log.Printf("Error creating job from JSON: %v", err)
	} else {
		fmt.Printf("Created job from JSON successfully: %s\n", newJob.(cluster.ScheduledJob).Name)
	}
	
	fmt.Println("SQL Scheduler demo completed successfully!")
}
