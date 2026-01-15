// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// SQLScriptJob represents a scheduled SQL script execution job
type SQLScriptJob struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	ScriptPath     string    `json:"scriptPath"`
	ScriptContent  string    `json:"scriptContent,omitempty"`
	TargetDatabase string    `json:"targetDatabase"`
	TargetServer   string    `json:"targetServer"` // "master" or specific server URL
	CronSchedule   string    `json:"cronSchedule"`
	Enabled        bool      `json:"enabled"`
	RunOnce        bool      `json:"runOnce"`
	MaxRetries     int       `json:"maxRetries"`
	Timeout        int       `json:"timeout"` // in seconds
	Created        time.Time `json:"created"`
	LastRun        time.Time `json:"lastRun,omitempty"`
	NextRun        time.Time `json:"nextRun,omitempty"`
	LastResult     string    `json:"lastResult,omitempty"`
	LastStatus     string    `json:"lastStatus,omitempty"` // "success", "error", "timeout"
}

// SQLScriptJobResult represents the execution result of a SQL script job
type SQLScriptJobResult struct {
	JobID          int64     `json:"jobId"`
	JobName        string    `json:"jobName"`
	StartTime      time.Time `json:"startTime"`
	EndTime        time.Time `json:"endTime"`
	Duration       float64   `json:"duration"` // in seconds
	Status         string    `json:"status"`   // "success", "error", "timeout"
	RowsAffected   int64     `json:"rowsAffected"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	ServerURL      string    `json:"serverUrl"`
	ScriptPath     string    `json:"scriptPath"`
	TargetDatabase string    `json:"targetDatabase"`
}

// JobExecuteSQLScript executes a SQL script from file or inline content
func (cluster *Cluster) JobExecuteSQLScript(scriptPath string, scriptContent string, targetDB string, targetServer string, timeout int) (*SQLScriptJobResult, error) {
	var server *ServerMonitor
	var err error

	result := &SQLScriptJobResult{
		StartTime:      time.Now(),
		ScriptPath:     scriptPath,
		TargetDatabase: targetDB,
		Status:         "error",
	}

	// Determine target server
	if targetServer == "master" || targetServer == "" {
		server = cluster.GetMaster()
		if server == nil {
			err = errors.New("No master available")
			result.ErrorMessage = err.Error()
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			return result, err
		}
	} else if targetServer == "slave" {
		slaves := cluster.GetSlaves()
		if len(slaves) == 0 {
			err = errors.New("No slave available")
			result.ErrorMessage = err.Error()
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			return result, err
		}
		server = slaves[0]
	} else {
		// Find specific server by URL
		server = cluster.GetServerFromURL(targetServer)
		if server == nil {
			err = fmt.Errorf("Server %s not found in cluster", targetServer)
			result.ErrorMessage = err.Error()
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			return result, err
		}
	}

	result.ServerURL = server.URL

	// Read script content if path is provided
	var script string
	if scriptContent != "" {
		script = scriptContent
	} else if scriptPath != "" {
		scriptBytes, err := os.ReadFile(scriptPath)
		if err != nil {
			result.ErrorMessage = fmt.Sprintf("Failed to read script file: %v", err)
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			return result, err
		}
		script = string(scriptBytes)
	} else {
		err = errors.New("Either scriptPath or scriptContent must be provided")
		result.ErrorMessage = err.Error()
		result.EndTime = time.Now()
		result.Duration = time.Since(result.StartTime).Seconds()
		return result, err
	}

	// Validate script for safety
	if err := cluster.validateSQLScript(script); err != nil {
		result.ErrorMessage = fmt.Sprintf("Script validation failed: %v", err)
		result.EndTime = time.Now()
		result.Duration = time.Since(result.StartTime).Seconds()
		return result, err
	}

	// Create job record
	taskName := "sqlscript"
	if scriptPath != "" {
		taskName = "sqlscript-" + filepath.Base(scriptPath)
	}
	jobID, err := server.JobInsertTask(taskName, server.Port, cluster.RepMgrHostname)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("Failed to create job record: %v", err)
		result.EndTime = time.Now()
		result.Duration = time.Since(result.StartTime).Seconds()
		return result, err
	}
	result.JobID = jobID

	// Get connection
	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		server.JobsUpdateState(taskName, err.Error(), 1, JobStateErrorExec)
		result.ErrorMessage = fmt.Sprintf("Failed to get connection: %v", err)
		result.EndTime = time.Now()
		result.Duration = time.Since(result.StartTime).Seconds()
		return result, err
	}
	defer conn.Close()

	// Switch to target database if specified
	if targetDB != "" {
		_, err = server.ConnExecQueryWithTimeout(conn, 5*time.Second, "USE "+targetDB)
		if err != nil {
			server.JobsUpdateState(taskName, err.Error(), 1, JobStateErrorExec)
			result.ErrorMessage = fmt.Sprintf("Failed to switch to database %s: %v", targetDB, err)
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			return result, err
		}
	}

	// Set timeout
	scriptTimeout := time.Duration(timeout) * time.Second
	if timeout == 0 {
		scriptTimeout = 300 * time.Second // default 5 minutes
	}

	// Execute script - split by semicolon and execute each statement
	statements := cluster.splitSQLStatements(script)
	var totalRowsAffected int64 = 0

	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		sqlResult, err := server.ConnExecQueryWithTimeout(conn, scriptTimeout, stmt)
		if err != nil {
			server.JobsUpdateState(taskName, err.Error(), 1, JobStateErrorExec)
			result.ErrorMessage = fmt.Sprintf("Failed to execute statement: %v", err)
			result.EndTime = time.Now()
			result.Duration = time.Since(result.StartTime).Seconds()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"SQL script execution failed on %s: %v", server.URL, err)
			return result, err
		}

		if sqlResult != nil {
			rowsAffected, _ := sqlResult.RowsAffected()
			totalRowsAffected += rowsAffected
		}
	}

	// Mark success
	result.Status = "success"
	result.RowsAffected = totalRowsAffected
	result.EndTime = time.Now()
	result.Duration = time.Since(result.StartTime).Seconds()

	successMsg := fmt.Sprintf("Script executed successfully. Rows affected: %d, Duration: %.2fs", totalRowsAffected, result.Duration)
	server.JobsUpdateState(taskName, successMsg, 1, JobStateSuccess)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"SQL script %s executed successfully on %s: %d rows affected in %.2fs", scriptPath, server.URL, totalRowsAffected, result.Duration)

	return result, nil
}

// validateSQLScript performs basic safety checks on SQL scripts
func (cluster *Cluster) validateSQLScript(script string) error {
	scriptUpper := strings.ToUpper(script)

	// Check for dangerous operations if configured
	if cluster.Conf.SchedulerSQLScriptsValidate {
		// Check for DROP DATABASE
		if strings.Contains(scriptUpper, "DROP DATABASE") || strings.Contains(scriptUpper, "DROP SCHEMA") {
			if !cluster.Conf.SchedulerSQLScriptsAllowDropDatabase {
				return errors.New("DROP DATABASE/SCHEMA is not allowed")
			}
		}

		// Check for TRUNCATE
		if strings.Contains(scriptUpper, "TRUNCATE") {
			if !cluster.Conf.SchedulerSQLScriptsAllowTruncate {
				return errors.New("TRUNCATE is not allowed")
			}
		}

		// Check for DROP TABLE
		if strings.Contains(scriptUpper, "DROP TABLE") {
			if !cluster.Conf.SchedulerSQLScriptsAllowDropTable {
				return errors.New("DROP TABLE is not allowed")
			}
		}

		// Check for DELETE without WHERE
		if strings.Contains(scriptUpper, "DELETE") && !strings.Contains(scriptUpper, "WHERE") {
			if !cluster.Conf.SchedulerSQLScriptsAllowDeleteAll {
				return errors.New("DELETE without WHERE clause is not allowed")
			}
		}

		// Check for UPDATE without WHERE
		if strings.Contains(scriptUpper, "UPDATE") && !strings.Contains(scriptUpper, "WHERE") {
			if !cluster.Conf.SchedulerSQLScriptsAllowUpdateAll {
				return errors.New("UPDATE without WHERE clause is not allowed")
			}
		}
	}

	// Check script is not empty
	if strings.TrimSpace(script) == "" {
		return errors.New("Script is empty")
	}

	return nil
}

// splitSQLStatements splits a SQL script into individual statements
func (cluster *Cluster) splitSQLStatements(script string) []string {
	var statements []string
	var currentStmt strings.Builder
	var inString bool
	var stringChar rune
	var inComment bool
	var inMultiLineComment bool

	lines := strings.Split(script, "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Handle multi-line comments
		if strings.HasPrefix(trimmedLine, "/*") {
			inMultiLineComment = true
		}
		if inMultiLineComment {
			if strings.HasSuffix(trimmedLine, "*/") {
				inMultiLineComment = false
			}
			continue
		}

		// Skip single-line comments
		if strings.HasPrefix(trimmedLine, "--") || strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		// Process character by character
		for i, char := range line {
			if inComment {
				if char == '\n' {
					inComment = false
				}
				continue
			}

			// Check for comments
			if !inString && char == '-' && i+1 < len(line) && rune(line[i+1]) == '-' {
				inComment = true
				continue
			}

			// Check for strings
			if char == '\'' || char == '"' || char == '`' {
				if !inString {
					inString = true
					stringChar = char
				} else if char == stringChar {
					inString = false
				}
			}

			currentStmt.WriteRune(char)

			// Check for statement delimiter
			if !inString && char == ';' {
				stmt := strings.TrimSpace(currentStmt.String())
				if stmt != "" && stmt != ";" {
					statements = append(statements, stmt)
				}
				currentStmt.Reset()
			}
		}
		currentStmt.WriteRune('\n')
	}

	// Add any remaining statement
	finalStmt := strings.TrimSpace(currentStmt.String())
	if finalStmt != "" && finalStmt != ";" {
		statements = append(statements, finalStmt)
	}

	return statements
}

// JobExecuteSQLScriptFromFile executes a SQL script from a file
func (cluster *Cluster) JobExecuteSQLScriptFromFile(scriptPath string, targetDB string, targetServer string, timeout int) (*SQLScriptJobResult, error) {
	return cluster.JobExecuteSQLScript(scriptPath, "", targetDB, targetServer, timeout)
}

// JobExecuteSQLScriptFromContent executes inline SQL content
func (cluster *Cluster) JobExecuteSQLScriptFromContent(scriptContent string, jobName string, targetDB string, targetServer string, timeout int) (*SQLScriptJobResult, error) {
	result, err := cluster.JobExecuteSQLScript("", scriptContent, targetDB, targetServer, timeout)
	if result != nil {
		result.JobName = jobName
	}
	return result, err
}

// GetSQLScriptJobFunction returns a function for scheduled SQL script execution
func (cluster *Cluster) GetSQLScriptJobFunction() func() {
	return func() {
		cluster.ExecuteScheduledSQLScripts()
	}
}

// ExecuteScheduledSQLScripts executes SQL scripts from the configured directory
func (cluster *Cluster) ExecuteScheduledSQLScripts() {
	if cluster.Conf.SchedulerSQLScriptsPath == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"SQL script path not configured, skipping scheduled execution")
		return
	}

	// Check if directory exists
	if _, err := os.Stat(cluster.Conf.SchedulerSQLScriptsPath); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"SQL script directory does not exist: %s", cluster.Conf.SchedulerSQLScriptsPath)
		return
	}

	// Get all .sql files from directory
	pattern := filepath.Join(cluster.Conf.SchedulerSQLScriptsPath, "*.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Failed to list SQL scripts: %v", err)
		return
	}

	if len(files) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
			"No SQL scripts found in %s", cluster.Conf.SchedulerSQLScriptsPath)
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Found %d SQL script(s) to execute", len(files))

	targetServer := "master"
	if cluster.Conf.SchedulerSQLScriptsTargetServer != "" {
		targetServer = cluster.Conf.SchedulerSQLScriptsTargetServer
	}

	for _, file := range files {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Executing SQL script: %s", filepath.Base(file))

		result, err := cluster.JobExecuteSQLScriptFromFile(
			file,
			cluster.Conf.SchedulerSQLScriptsDatabase,
			targetServer,
			cluster.Conf.SchedulerSQLScriptsTimeout,
		)

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"SQL script execution failed: %s - %v", filepath.Base(file), err)

			// Send alert if configured
			if cluster.Conf.SchedulerSQLScriptsAlertOnError {
				cluster.alertSQLScriptError(file, err)
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"SQL script completed: %s - Status: %s, Rows: %d, Duration: %.2fs",
				filepath.Base(file), result.Status, result.RowsAffected, result.Duration)
		}
	}
}

// alertSQLScriptError sends an alert when SQL script execution fails
func (cluster *Cluster) alertSQLScriptError(scriptPath string, err error) {
	alertMsg := fmt.Sprintf("SQL script execution failed: %s - %v", filepath.Base(scriptPath), err)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, alertMsg)
	// The alert system will pick this up from the logs
}

// SaveSQLScriptJob saves a SQL script job definition to file
func (cluster *Cluster) SaveSQLScriptJob(job *SQLScriptJob) error {
	if cluster.Conf.WorkingDir == "" {
		return errors.New("Working directory not configured")
	}

	jobsDir := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "sql_jobs")
	if err := os.MkdirAll(jobsDir, 0755); err != nil {
		return fmt.Errorf("Failed to create jobs directory: %v", err)
	}

	jobFile := filepath.Join(jobsDir, fmt.Sprintf("%s.json", job.Name))
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("Failed to marshal job: %v", err)
	}

	if err := os.WriteFile(jobFile, data, 0644); err != nil {
		return fmt.Errorf("Failed to write job file: %v", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Saved SQL script job: %s", job.Name)

	return nil
}

// LoadSQLScriptJobs loads all SQL script job definitions
func (cluster *Cluster) LoadSQLScriptJobs() ([]*SQLScriptJob, error) {
	if cluster.Conf.WorkingDir == "" {
		return nil, errors.New("Working directory not configured")
	}

	jobsDir := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "sql_jobs")
	if _, err := os.Stat(jobsDir); os.IsNotExist(err) {
		return []*SQLScriptJob{}, nil
	}

	pattern := filepath.Join(jobsDir, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("Failed to list job files: %v", err)
	}

	jobs := make([]*SQLScriptJob, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to read job file %s: %v", filepath.Base(file), err)
			continue
		}

		var job SQLScriptJob
		if err := json.Unmarshal(data, &job); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to parse job file %s: %v", filepath.Base(file), err)
			continue
		}

		jobs = append(jobs, &job)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Loaded %d SQL script job(s)", len(jobs))

	return jobs, nil
}

// DeleteSQLScriptJob deletes a SQL script job definition
func (cluster *Cluster) DeleteSQLScriptJob(jobName string) error {
	if cluster.Conf.WorkingDir == "" {
		return errors.New("Working directory not configured")
	}

	jobsDir := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "sql_jobs")
	jobFile := filepath.Join(jobsDir, fmt.Sprintf("%s.json", jobName))

	if err := os.Remove(jobFile); err != nil {
		return fmt.Errorf("Failed to delete job file: %v", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Deleted SQL script job: %s", jobName)

	return nil
}
