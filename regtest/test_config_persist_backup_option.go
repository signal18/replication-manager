// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestConfigPersistBackupOption verifies that a changed per-cluster config value
// (here backup-mysqldump-options) actually survives a persist to the datadir
// <cluster>/<cluster>.toml — the file startup merges LAST and that therefore
// wins on restart. This is the reproduction for the "custom backup options lost
// after restart" bug: change the value, persist, and confirm it is on disk (not
// just in memory). It also asserts the LastConfigSaveToDisk instrument advanced,
// so a passing timestamp with a missing value pinpoints strip-on-save, while an
// unchanged timestamp pinpoints the save never running.
func (regtest *RegTest) TestConfigPersistBackupOption(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const marker = "--hex-blob --single-transaction --skip-ssl --REGTEST-PERSIST-MARKER"

	savedFile := cl.Conf.WorkingDir + "/" + cl.Name + "/" + cl.Name + ".toml"
	original := cl.Conf.BackupMysqldumpOptions
	beforeStamp := cl.LastConfigSaveToDisk

	// Always restore the original value and re-persist, pass or fail.
	defer func() {
		cl.Conf.BackupMysqldumpOptions = original
		cl.SaveConfigFile()
	}()

	// 1. Change the value in memory (mirrors what the API/GUI setter does).
	cl.Conf.BackupMysqldumpOptions = marker

	// 2. Persist to the datadir <cluster>.toml.
	changed, err := cl.SaveConfigFile()
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST persist: SaveConfigFile error: %s", err)
		return false
	}
	if !changed {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST persist: SaveConfigFile reported no change for a changed value")
		return false
	}

	// 3. The last-saved-to-disk instrument must have advanced.
	if !cl.LastConfigSaveToDisk.After(beforeStamp) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"TEST persist: LastConfigSaveToDisk did not advance (%s) — the save never reached disk", cl.LastConfigSaveToDisk)
		return false
	}

	// 4. Read the persisted file back — this is the file startup merges last.
	//    If the marker is absent, the value was stripped on save (the bug):
	//    it will "disappear" on the next restart even though it saved.
	data, err := os.ReadFile(savedFile)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST persist: cannot read %s: %s", savedFile, err)
		return false
	}
	if !strings.Contains(string(data), "REGTEST-PERSIST-MARKER") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"TEST persist: backup-mysqldump-options was NOT persisted to %s — it will be lost on restart (config-persistence bug)", savedFile)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Config persistence OK: backup-mysqldump-options reached %s and LastConfigSaveToDisk advanced to %s", savedFile, cl.LastConfigSaveToDisk)
	return true
}
