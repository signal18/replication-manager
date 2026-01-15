// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

//go:build clients
// +build clients

package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/spf13/cobra"
)

var sqlScriptCmd = &cobra.Command{
	Use:   "sql-script",
	Short: "SQL script management commands",
	Long:  "Commands for managing and executing SQL scripts",
}

var executeSQLScriptCmd = &cobra.Command{
	Use:   "execute",
	Short: "Execute an SQL script",
	Long:  "Execute an SQL script on the cluster",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)

		// Build request payload
		payload := map[string]interface{}{
			"scriptPath":     cliScriptPath,
			"scriptContent":  cliScriptContent,
			"targetDatabase": cliTargetDatabase,
			"targetServer":   cliTargetServer,
			"timeout":        cliScriptTimeout,
		}

		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Fatal("Failed to marshal request:", err)
		}

		url := fmt.Sprintf("https://%s:%s/api/clusters/%s/actions/execute-sql-script",
			cliHost, cliPort, cfgGroup)

		resp, err := cliConn.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Fatal("Failed to execute request:", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}

		if resp.StatusCode != 200 {
			log.Fatalf("Request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatal("Failed to parse response:", err)
		}

		fmt.Println("SQL Script Execution Result:")
		fmt.Printf("Status: %v\n", result["status"])
		fmt.Printf("Rows Affected: %.0f\n", result["rowsAffected"])
		fmt.Printf("Duration: %.2f seconds\n", result["duration"])
		if errorMsg, ok := result["errorMessage"].(string); ok && errorMsg != "" {
			fmt.Printf("Error: %s\n", errorMsg)
		}
	},
}

var triggerScheduledScriptsCmd = &cobra.Command{
	Use:   "trigger-scheduled",
	Short: "Trigger scheduled SQL scripts manually",
	Long:  "Manually trigger the execution of all scheduled SQL scripts in the configured directory",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)

		url := fmt.Sprintf("https://%s:%s/api/clusters/%s/actions/trigger-scheduled-sql-scripts",
			cliHost, cliPort, cfgGroup)

		resp, err := cliConn.Post(url, "application/json", nil)
		if err != nil {
			log.Fatal("Failed to execute request:", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}

		if resp.StatusCode != 200 {
			log.Fatalf("Request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatal("Failed to parse response:", err)
		}

		fmt.Printf("Status: %s\n", result["status"])
		fmt.Printf("Message: %s\n", result["message"])
	},
}

var listSQLJobsCmd = &cobra.Command{
	Use:   "list-jobs",
	Short: "List all SQL script jobs",
	Long:  "List all saved SQL script job definitions",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)

		url := fmt.Sprintf("https://%s:%s/api/clusters/%s/sql-jobs",
			cliHost, cliPort, cfgGroup)

		resp, err := cliConn.Get(url)
		if err != nil {
			log.Fatal("Failed to execute request:", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}

		if resp.StatusCode != 200 {
			log.Fatalf("Request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var jobs []map[string]interface{}
		if err := json.Unmarshal(body, &jobs); err != nil {
			log.Fatal("Failed to parse response:", err)
		}

		if len(jobs) == 0 {
			fmt.Println("No SQL script jobs found")
			return
		}

		fmt.Printf("Found %d SQL script job(s):\n\n", len(jobs))
		for i, job := range jobs {
			fmt.Printf("Job %d:\n", i+1)
			fmt.Printf("  Name: %v\n", job["name"])
			fmt.Printf("  Script Path: %v\n", job["scriptPath"])
			fmt.Printf("  Target Database: %v\n", job["targetDatabase"])
			fmt.Printf("  Target Server: %v\n", job["targetServer"])
			fmt.Printf("  Cron Schedule: %v\n", job["cronSchedule"])
			fmt.Printf("  Enabled: %v\n", job["enabled"])
			fmt.Printf("  Last Status: %v\n", job["lastStatus"])
			fmt.Println()
		}
	},
}

var saveSQLJobCmd = &cobra.Command{
	Use:   "save-job",
	Short: "Save an SQL script job definition",
	Long:  "Save an SQL script job definition to be scheduled",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)

		// Build job payload
		job := map[string]interface{}{
			"name":           cliJobName,
			"scriptPath":     cliScriptPath,
			"scriptContent":  cliScriptContent,
			"targetDatabase": cliTargetDatabase,
			"targetServer":   cliTargetServer,
			"cronSchedule":   cliCronSchedule,
			"enabled":        cliJobEnabled,
			"runOnce":        cliJobRunOnce,
			"maxRetries":     cliJobMaxRetries,
			"timeout":        cliScriptTimeout,
		}

		jsonData, err := json.Marshal(job)
		if err != nil {
			log.Fatal("Failed to marshal request:", err)
		}

		url := fmt.Sprintf("https://%s:%s/api/clusters/%s/sql-jobs/save",
			cliHost, cliPort, cfgGroup)

		resp, err := cliConn.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Fatal("Failed to execute request:", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}

		if resp.StatusCode != 200 {
			log.Fatalf("Request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatal("Failed to parse response:", err)
		}

		fmt.Printf("Status: %s\n", result["status"])
		fmt.Printf("Message: %s\n", result["message"])
	},
}

var deleteSQLJobCmd = &cobra.Command{
	Use:   "delete-job",
	Short: "Delete an SQL script job definition",
	Long:  "Delete a saved SQL script job definition",
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)

		url := fmt.Sprintf("https://%s:%s/api/clusters/%s/sql-jobs/%s",
			cliHost, cliPort, cfgGroup, cliJobName)

		req, err := http.NewRequest("DELETE", url, nil)
		if err != nil {
			log.Fatal("Failed to create request:", err)
		}

		resp, err := cliConn.Do(req)
		if err != nil {
			log.Fatal("Failed to execute request:", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}

		if resp.StatusCode != 200 {
			log.Fatalf("Request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatal("Failed to parse response:", err)
		}

		fmt.Printf("Status: %s\n", result["status"])
		fmt.Printf("Message: %s\n", result["message"])
	},
}

var (
	cliScriptPath     string
	cliScriptContent  string
	cliTargetDatabase string
	cliTargetServer   string
	cliScriptTimeout  int
	cliJobName        string
	cliCronSchedule   string
	cliJobEnabled     bool
	cliJobRunOnce     bool
	cliJobMaxRetries  int
)

func init() {
	rootClientCmd.AddCommand(sqlScriptCmd)
	sqlScriptCmd.AddCommand(executeSQLScriptCmd)
	sqlScriptCmd.AddCommand(triggerScheduledScriptsCmd)
	sqlScriptCmd.AddCommand(listSQLJobsCmd)
	sqlScriptCmd.AddCommand(saveSQLJobCmd)
	sqlScriptCmd.AddCommand(deleteSQLJobCmd)

	// Execute command flags
	initServerApiFlags(executeSQLScriptCmd)
	executeSQLScriptCmd.Flags().StringVar(&cliScriptPath, "script-path", "", "Path to SQL script file")
	executeSQLScriptCmd.Flags().StringVar(&cliScriptContent, "script-content", "", "Inline SQL script content")
	executeSQLScriptCmd.Flags().StringVar(&cliTargetDatabase, "target-database", "", "Target database name")
	executeSQLScriptCmd.Flags().StringVar(&cliTargetServer, "target-server", "master", "Target server: master or specific URL")
	executeSQLScriptCmd.Flags().IntVar(&cliScriptTimeout, "timeout", 300, "Script execution timeout in seconds")

	// Trigger scheduled scripts flags
	initServerApiFlags(triggerScheduledScriptsCmd)

	// List jobs flags
	initServerApiFlags(listSQLJobsCmd)

	// Save job flags
	initServerApiFlags(saveSQLJobCmd)
	saveSQLJobCmd.Flags().StringVar(&cliJobName, "name", "", "Job name (required)")
	saveSQLJobCmd.Flags().StringVar(&cliScriptPath, "script-path", "", "Path to SQL script file")
	saveSQLJobCmd.Flags().StringVar(&cliScriptContent, "script-content", "", "Inline SQL script content")
	saveSQLJobCmd.Flags().StringVar(&cliTargetDatabase, "target-database", "", "Target database name")
	saveSQLJobCmd.Flags().StringVar(&cliTargetServer, "target-server", "master", "Target server: master or specific URL")
	saveSQLJobCmd.Flags().StringVar(&cliCronSchedule, "cron-schedule", "", "Cron schedule expression")
	saveSQLJobCmd.Flags().BoolVar(&cliJobEnabled, "enabled", true, "Enable the job")
	saveSQLJobCmd.Flags().BoolVar(&cliJobRunOnce, "run-once", false, "Run only once")
	saveSQLJobCmd.Flags().IntVar(&cliJobMaxRetries, "max-retries", 3, "Maximum number of retries")
	saveSQLJobCmd.Flags().IntVar(&cliScriptTimeout, "timeout", 300, "Script execution timeout in seconds")
	saveSQLJobCmd.MarkFlagRequired("name")

	// Delete job flags
	initServerApiFlags(deleteSQLJobCmd)
	deleteSQLJobCmd.Flags().StringVar(&cliJobName, "name", "", "Job name to delete (required)")
	deleteSQLJobCmd.MarkFlagRequired("name")
}
