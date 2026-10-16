//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package clients

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/spf13/cobra"
)

var (
	cliBackupVerifyLevel   string
	cliBackupVerifyWait    bool
	cliBackupVerifyTimeout int
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup operations of a cluster",
}

// backupVerifyCmd starts the verification of a logical backup (#2023) through
// POST /api/clusters/{cluster}/backups/{id}/verify[/{level}]. With --wait it polls the
// backup list until the result is recorded and exits 0 passed, 1 failed, 3 skipped.
var backupVerifyCmd = &cobra.Command{
	Use:   "verify BACKUP_ID",
	Short: "Verify a logical backup: integrity, or a test restore on the verification server",
	Long: `Starts the verification of a logical (mysqldump) backup.
  --level integrity  read the backup as a restore would, no database
  --level restore    integrity, then a test restore on backup-verification-host (grant db-restore)
  no --level         the cluster's backup-verification-level
With --wait the command waits for the result: exit 0 passed, 1 failed, 3 not run (skipped).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid backup ID:", args[0])
			os.Exit(2)
		}
		level := strings.ToLower(strings.TrimSpace(cliBackupVerifyLevel))
		cliInit(true)
		path := fmt.Sprintf("backups/%d/verify", id)
		if level != "" {
			path += "/" + level
		}
		body, status, err := cliClusterRequest(http.MethodPost, path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if status != http.StatusOK {
			fmt.Fprintf(os.Stderr, "verification not started (%d): %s\n", status, strings.TrimSpace(string(body)))
			os.Exit(1)
		}
		fmt.Printf("Backup %d: verification started\n", id)
		if !cliBackupVerifyWait {
			return
		}
		os.Exit(cliBackupVerifyWaitResult(id, time.Duration(cliBackupVerifyTimeout)*time.Second))
	},
}

// cliBackupVerifyWaitResult polls the backup list until backup id has a finished
// verification, prints it and returns the exit code.
func cliBackupVerifyWaitResult(id int64, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		v, err := cliBackupVerification(id)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if v != nil && v.Status != backupmgr.BackupVerificationRunning {
			fmt.Printf("Backup %d: %s (%s level)", id, v.Status, v.Level)
			if v.Error != "" {
				fmt.Printf(": %s", v.Error)
			}
			fmt.Println()
			for _, w := range v.Warnings {
				fmt.Println("  warning:", w)
			}
			for _, n := range v.Notes {
				fmt.Println("  note:", n)
			}
			switch v.Status {
			case backupmgr.BackupVerificationPassed:
				return 0
			case backupmgr.BackupVerificationSkipped:
				return 3
			default:
				return 1
			}
		}
		if timeout > 0 && time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "Backup %d: no verification result after %s\n", id, timeout)
			return 1
		}
		time.Sleep(5 * time.Second)
	}
}

// cliBackupVerification returns the recorded verification of backup id, nil while none.
func cliBackupVerification(id int64) (*backupmgr.BackupVerification, error) {
	body, status, err := cliClusterRequest(http.MethodGet, "backups")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("backup list (%d): %s", status, strings.TrimSpace(string(body)))
	}
	// the list is a map keyed by backup ID
	var backups map[string]struct {
		Verification *backupmgr.BackupVerification `json:"verification"`
	}
	if err := json.Unmarshal(body, &backups); err != nil {
		return nil, fmt.Errorf("cannot parse the backup list: %w", err)
	}
	b, ok := backups[strconv.FormatInt(id, 10)]
	if !ok {
		return nil, fmt.Errorf("backup %d not found", id)
	}
	return b.Verification, nil
}

// cliClusterRequest calls /api/clusters/{cluster}/{path} and returns the body and the status.
func cliClusterRequest(method, path string) ([]byte, int, error) {
	req, err := http.NewRequest(method, "https://"+cliHost+":"+cliPort+"/api/clusters/"+cliClusters[cliClusterIndex]+"/"+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cliToken)
	resp, err := cliConn.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func initBackupFlags(cmd *cobra.Command) {
	initServerApiFlags(backupVerifyCmd)
	backupVerifyCmd.Flags().StringVar(&cliBackupVerifyLevel, "level", "", "integrity|restore (default: the cluster's backup-verification-level)")
	backupVerifyCmd.Flags().BoolVar(&cliBackupVerifyWait, "wait", false, "Wait for the result: exit 0 passed, 1 failed, 3 skipped")
	backupVerifyCmd.Flags().IntVar(&cliBackupVerifyTimeout, "timeout", 14400, "Seconds --wait waits for the result, 0 waits forever")
	cmd.AddCommand(backupVerifyCmd)
	initClusterFlags(cmd)
}
