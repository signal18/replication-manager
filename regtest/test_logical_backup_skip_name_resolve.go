// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// T13 scenario of #2436 (see doc/implementation/cluster/ACCOUNT_EXPORT_SKIP_NAME_RESOLVE.md): a logical backup of a MariaDB
// whose skip_name_resolve ignores an account completes, with that account named as left out, and keeps exporting the
// accounts the server does show, wildcard hosts included.

package regtest

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

const (
	skipNameResolveHiddenUser = "regtest_snr_hidden"
	skipNameResolveHiddenHost = "regtest.example.test"
	skipNameResolveWildUser   = "regtest_snr_wild"
	skipNameResolveWildHost   = "%.example.test"
)

// TestLogicalBackupSkipNameResolve needs a MariaDB master started with skip_name_resolve=ON (the variable is not dynamic: a
// cluster without it is reported as not applicable, not as a pass). It creates one account the server ignores and one wildcard
// account it shows, takes a mysqldump --system=all backup, and checks: the backup completes; the job result and the tracked
// warning name the ignored account and only it; the artifact holds the wildcard account and not the ignored one; the next backup,
// the ignored account dropped, leaves nothing out and closes the warning. It removes its accounts.
func (regtest *RegTest) TestLogicalBackupSkipNameResolve(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for the skip_name_resolve backup test")
		return false
	}
	if !master.IsMariaDB() || !strings.EqualFold(master.Variables.Get("SKIP_NAME_RESOLVE"), "ON") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Not applicable: %s is not a MariaDB with skip_name_resolve=ON", master.URL)
		return true
	}

	hidden := fmt.Sprintf("'%s'@'%s'", skipNameResolveHiddenUser, skipNameResolveHiddenHost)
	wild := fmt.Sprintf("'%s'@'%s'", skipNameResolveWildUser, skipNameResolveWildHost)
	cleanup := func() {
		for _, q := range []string{
			"DELETE FROM mysql.global_priv WHERE user IN ('" + skipNameResolveHiddenUser + "','" + skipNameResolveWildUser + "')",
			"FLUSH PRIVILEGES",
		} {
			_ = master.ExecQueryNoBinLog(q, 10*time.Second)
		}
	}
	defer cleanup()
	cleanup()

	for _, q := range []string{
		"CREATE USER " + hidden + " IDENTIFIED BY 'x'",
		"CREATE USER " + wild + " IDENTIFIED BY 'x'",
		"GRANT SELECT ON *.* TO " + wild,
		"FLUSH PRIVILEGES", // the server reloads its privileges: the DNS-host account is now ignored
	} {
		if err := master.ExecQueryNoBinLog(q, 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Setup %q failed: %s", q, err)
			return false
		}
	}

	oldType := cl.Conf.BackupLogicalType
	cl.Conf.BackupLogicalType = config.ConstBackupLogicalTypeMysqldump
	defer func() { cl.Conf.BackupLogicalType = oldType }()
	task := config.ConstBackupLogicalTypeMysqldump

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Logical backup of %s with %s ignored by skip_name_resolve", master.URL, hidden)
	if err := master.JobBackupLogical(context.Background()); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The backup must complete, the ignored account being left out: %s", err)
		return false
	}
	if t := master.JobResults.Get(task); t == nil || t.State != 3 || !strings.Contains(t.Result, hidden) || strings.Contains(t.Result, wild) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The job result must name the ignored account and only it: %+v", t)
		return false
	}
	if gotTask, accounts := master.LastAccountsOmittedJob(); gotTask != task || !strings.Contains(accounts, hidden) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The omission warning must be open (task=%q accounts=%q)", gotTask, accounts)
		return false
	}
	artifact := filepath.Join(master.GetMyBackupDirectory(), "mysqldump.sql.gz")
	has, err := artifactHasAccounts(artifact, "`"+skipNameResolveWildUser+"`@`"+skipNameResolveWildHost+"`", "`"+skipNameResolveHiddenUser+"`@`"+skipNameResolveHiddenHost+"`")
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read of the artifact %s failed: %s", artifact, err)
		return false
	}
	if !has[0] || has[1] {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The artifact must hold the wildcard account (%v) and not the ignored one (%v)", has[0], has[1])
		return false
	}

	// the ignored account dropped: the next backup leaves nothing out and closes the warning
	if err := master.ExecQueryNoBinLog("DELETE FROM mysql.global_priv WHERE user='"+skipNameResolveHiddenUser+"'", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Drop of the ignored account failed: %s", err)
		return false
	}
	_ = master.ExecQueryNoBinLog("FLUSH PRIVILEGES", 10*time.Second)
	if err := master.JobBackupLogical(context.Background()); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The second backup failed: %s", err)
		return false
	}
	if gotTask, accounts := master.LastAccountsOmittedJob(); gotTask != "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "The omission warning must be closed once a backup leaves nothing out (task=%q accounts=%q)", gotTask, accounts)
		return false
	}
	return true
}

// artifactHasAccounts reports, for each needle, whether an account-section statement of the gzip dump at path contains it.
func artifactHasAccounts(path string, needles ...string) ([]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	found := make([]bool, len(needles))
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<30)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "CREATE USER") && !strings.HasPrefix(line, "GRANT ") {
			continue
		}
		for i, n := range needles {
			if strings.Contains(line, n) {
				found[i] = true
			}
		}
	}
	return found, sc.Err()
}
