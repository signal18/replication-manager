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
With --wait the command waits for the outcome of the run it started: exit 0 passed,
1 failed, 3 not run (skipped), 4 the run expired from the backup's verification history.`,
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
		// the run's sequence and id: it ends in its own record of the backup's run history,
		// --wait waits for that record and no other
		var started struct {
			RunID    string `json:"runId"`
			Sequence uint64 `json:"sequence"`
		}
		if err := json.Unmarshal(body, &started); err != nil || started.RunID == "" || started.Sequence == 0 {
			fmt.Fprintf(os.Stderr, "verification started, but the API returned no run: %s\n", strings.TrimSpace(string(body)))
			os.Exit(1)
		}
		fmt.Printf("Backup %d: verification run %d (%s) started\n", id, started.Sequence, started.RunID)
		if !cliBackupVerifyWait {
			return
		}
		os.Exit(cliBackupVerifyWaitResult(id, started.Sequence, started.RunID, time.Duration(cliBackupVerifyTimeout)*time.Second))
	},
}

// backupVerifyRunOutcome finds run (seq, runID) in a backup's run history. ended: the run
// finished, with its record and exit code (0 passed, 1 failed, 3 skipped); expired: the run
// is no longer in the history, the oldest kept being later (exit 4). Neither: still running,
// or not recorded yet. Another run's record, earlier or later, is never taken for it.
func backupVerifyRunOutcome(runs []backupmgr.BackupVerificationRun, seq uint64, runID string) (run *backupmgr.BackupVerificationRun, ended, expired bool, code int) {
	for i := range runs {
		r := &runs[i]
		if r.Sequence != seq || r.RunID != runID {
			continue
		}
		switch r.Status {
		case backupmgr.BackupVerificationRunning:
			return r, false, false, 0
		case backupmgr.BackupVerificationPassed:
			return r, true, false, 0
		case backupmgr.BackupVerificationSkipped:
			return r, true, false, 3
		default:
			return r, true, false, 1
		}
	}
	if len(runs) > 0 && runs[0].Sequence > seq {
		return nil, false, true, 4
	}
	return nil, false, false, 0
}

// cliBackupVerifyPoll is how often --wait reads the backup list (the API has no
// single-backup read).
const cliBackupVerifyPoll = 10 * time.Second

// cliBackupVerifyWaitResult polls the backup list until run (seq, runID) of backup id ends,
// prints it and returns the exit code.
func cliBackupVerifyWaitResult(id int64, seq uint64, runID string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		b, err := cliBackupVerificationRuns(id)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		r, ended, expired, code := backupVerifyRunOutcome(b.VerificationRuns, seq, runID)
		if expired {
			fmt.Fprintf(os.Stderr, "Backup %d: run %d (%s) is no longer in its verification history (backup-verification-run-history): its result expired\n", id, seq, runID)
			return code
		}
		if ended {
			fmt.Printf("Backup %d: run %d %s (%s level)", id, seq, r.Status, r.Level)
			if r.Error != "" {
				fmt.Printf(": %s", r.Error)
			}
			fmt.Println()
			// the details of a run that ran are in the backup's verification, while it is
			// still this run's
			if v := b.Verification; v != nil && v.RunID == runID && v.RunSequence == seq {
				for _, w := range v.Warnings {
					fmt.Println("  warning:", w)
				}
				for _, n := range v.Notes {
					fmt.Println("  note:", n)
				}
			}
			return code
		}
		if timeout > 0 && time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "Backup %d: run %d (%s) has no result after %s\n", id, seq, runID, timeout)
			return 1
		}
		time.Sleep(cliBackupVerifyPoll)
	}
}

// cliBackupVerificationState is what --wait reads of a backup in the backup list.
type cliBackupVerificationState struct {
	Verification     *backupmgr.BackupVerification     `json:"verification"`
	VerificationRuns []backupmgr.BackupVerificationRun `json:"verificationRuns"`
}

// cliBackupVerificationRuns returns backup id's verification and run history.
func cliBackupVerificationRuns(id int64) (cliBackupVerificationState, error) {
	var none cliBackupVerificationState
	body, status, err := cliClusterRequest(http.MethodGet, "backups")
	if err != nil {
		return none, err
	}
	if status != http.StatusOK {
		return none, fmt.Errorf("backup list (%d): %s", status, strings.TrimSpace(string(body)))
	}
	// the list is a map keyed by backup ID
	var backups map[string]cliBackupVerificationState
	if err := json.Unmarshal(body, &backups); err != nil {
		return none, fmt.Errorf("cannot parse the backup list: %w", err)
	}
	b, ok := backups[strconv.FormatInt(id, 10)]
	if !ok {
		return none, fmt.Errorf("backup %d not found", id)
	}
	return b, nil
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
	backupVerifyCmd.Flags().BoolVar(&cliBackupVerifyWait, "wait", false, "Wait for the run's outcome: exit 0 passed, 1 failed, 3 skipped, 4 expired from the history")
	backupVerifyCmd.Flags().IntVar(&cliBackupVerifyTimeout, "timeout", 14400, "Seconds --wait waits for the result, 0 waits forever")
	cmd.AddCommand(backupVerifyCmd)
	initClusterFlags(cmd)
}
