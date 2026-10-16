//go:build clients
// +build clients

package clients

import (
	"testing"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// --wait finds its own run in the backup's run history, by sequence and id, never another
// run's record; it ends with the run's exit code, or at once with 4 when the run expired from
// the history
func TestBackupVerifyRunOutcome(t *testing.T) {
	run := func(seq uint64, id, status string) backupmgr.BackupVerificationRun {
		return backupmgr.BackupVerificationRun{Sequence: seq, RunID: id, Level: "integrity", Status: status}
	}
	for _, tc := range []struct {
		name           string
		runs           []backupmgr.BackupVerificationRun
		ended, expired bool
		code           int
	}{
		{"not recorded yet", nil, false, false, 0},
		{"running", []backupmgr.BackupVerificationRun{run(5, "a", backupmgr.BackupVerificationRunning)}, false, false, 0},
		// run A ended, run B started before A's next poll: A still resolves A
		{"ended, a later run started", []backupmgr.BackupVerificationRun{run(5, "a", backupmgr.BackupVerificationPassed), run(6, "b", backupmgr.BackupVerificationRunning)}, true, false, 0},
		{"failed", []backupmgr.BackupVerificationRun{run(5, "a", backupmgr.BackupVerificationFailed)}, true, false, 1},
		{"skipped", []backupmgr.BackupVerificationRun{run(4, "z", backupmgr.BackupVerificationPassed), run(5, "a", backupmgr.BackupVerificationSkipped)}, true, false, 3},
		{"another run's record with the same sequence", []backupmgr.BackupVerificationRun{run(5, "other", backupmgr.BackupVerificationPassed)}, false, false, 0},
		// dropped by backup-verification-run-history: the oldest kept is later
		{"expired", []backupmgr.BackupVerificationRun{run(7, "c", backupmgr.BackupVerificationPassed), run(8, "d", backupmgr.BackupVerificationRunning)}, false, true, 4},
		{"only earlier runs: not recorded yet", []backupmgr.BackupVerificationRun{run(3, "x", backupmgr.BackupVerificationPassed)}, false, false, 0},
	} {
		_, ended, expired, code := backupVerifyRunOutcome(tc.runs, 5, "a")
		if ended != tc.ended || expired != tc.expired || code != tc.code {
			t.Errorf("%s: ended %v expired %v code %d, want %v %v %d", tc.name, ended, expired, code, tc.ended, tc.expired, tc.code)
		}
	}
}
