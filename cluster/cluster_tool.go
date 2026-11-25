package cluster

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func (cluster *Cluster) GetMyDumperCompatibleOptions() []string {
	params := []string{}
	tool := cluster.VersionsMap.Get("mydumper")
	if tool == nil {
		return params
	}

	parts := strings.Split(cluster.Conf.BackupMyDumperOptions, " ")
	if len(parts) == 0 {
		return parts
	}

	var hasSourceData bool
	var hasReplicaData bool

	for _, param := range parts {
		if param == "" {
			continue
		}

		if param == "--source-data" || strings.HasPrefix(param, "--source-data=") {
			hasSourceData = true
		}
		if param == "--replica-data" || strings.HasPrefix(param, "--replica-data=") {
			hasReplicaData = true
		}

		params = append(params, param)
	}

	if !hasSourceData && !hasReplicaData {
		params = append(params, "--source-data=2")
	}

	return params
}

// GetMyLoaderCompatibleOptions filters backup-myloader-options by the detected MyDumper version
// (VersionsMap "mydumper", the executable found on the monitor host, not necessarily the version that produced
// the backup): the legacy rule drops --innodb-optimize-keys=skip for a MyDumper before 0.16.7. It does not read
// the myloader version: only myLoaderRestoreArgs (R0 --source-data=0, R1 --drop-table, R2 --optimize-keys)
// does, from its own bounded probe.
func (cluster *Cluster) GetMyLoaderCompatibleOptions() []string {
	params := []string{}
	tool := cluster.VersionsMap.Get("mydumper")
	if tool == nil {
		return params
	}

	parts := strings.Split(cluster.Conf.BackupMyLoaderOptions, " ")
	if len(parts) == 1 {
		return parts
	}

	for _, param := range parts {
		if param == "" {
			continue
		}

		if tool.Lower("0.16.7") {
			// remove --innodb-optimize-keys=skip
			// --innodb-optimize-keys=skip is not supported in mydumper < 0.16.7
			if param == "--innodb-optimize-keys=skip" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Removing --innodb-optimize-keys=skip from mydumper options for versions before 0.16.7")
				continue
			}
		}

		params = append(params, param)
	}

	return params
}

// myLoaderNoSourceChangeArgs keeps myloader from changing the replication source of the node it
// loads. The mydumper backups of this tool are taken with --source-data=2 to record the binary log
// file, position and GTID set in their metadata, which also marks them
// "myloader_exec_change_source = 1": myloader then sends CHANGE REPLICATION SOURCE TO on its own.
// MariaDB has no such statement (error 1064; myloader 1.0.1 then exits 1 after the data is loaded
// and the restore fails, myloader 0.18.1 only logs the error and completes), and on MySQL/Percona the source of a replica is the
// business of the replication manager, which positions it after the load
// (applyMyLoaderReplicaPosition). --source-data=0 on myloader overrides the metadata and leaves the
// recorded position in place. The backups already taken carry the mark, so this is done on the
// restore and not only on the dump.
//
// Nothing is added when the user set --source-data in backup-myloader-options, or when myloader
// is older than 0.18.1 (it rejects the option) or its version is unknown. The version is the one of
// the myloader the restore runs (myLoaderVersion), not the detected mydumper's: backup-myloader-path
// or the PATH can hold another release, and a 0.17.1 myloader next to a newer mydumper refuses
// the option and fails the restore.
func myLoaderNoSourceChangeArgs(args []string, tool *version.Version) []string {
	if tool == nil || !tool.GreaterEqual("0.18.1") {
		return args
	}
	for _, a := range args {
		if a == "--source-data" || strings.HasPrefix(a, "--source-data=") {
			return args
		}
	}
	return append(args, "--source-data=0")
}

const (
	// myLoaderVersionTimeout bounds the "myloader --version" probe: a hung or unresponsive binary
	// at backup-myloader-path must not hold the reseed job.
	myLoaderVersionTimeout = 5 * time.Second
	// myLoaderVersionWaitDelay bounds the wait for the output pipes once the deadline has killed
	// the process: a child that inherited them would otherwise keep cmd.Output() blocked.
	myLoaderVersionWaitDelay = 2 * time.Second
)

// readMyLoaderVersion runs "<path> --version" for at most timeout (plus myLoaderVersionWaitDelay for
// the output pipes) and parses the version as the mydumper version is (RefreshMyDumperVersion).
// The error says why no version was read: not runnable, timed out, or no version in the output.
func readMyLoaderVersion(path string, timeout time.Duration) (*version.Version, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = myLoaderVersionWaitDelay
	out, err := cmd.Output()
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("no answer within %s", timeout)
	}
	if err != nil {
		return nil, err
	}
	v, parsed := version.NewVersionFromString("mydumper", version.ExtractVersionFromOutput(string(out)))
	if parsed == 0 {
		return nil, errors.New("no version in the output")
	}
	return v, nil
}

// myLoaderVersion reads the version of the myloader the restore runs (GetMyLoaderPath). nil when it
// can not be read, in time: nothing is then added to its arguments, and the reason is logged since
// the restore then goes on without --source-data=0.
func (cluster *Cluster) myLoaderVersion() *version.Version {
	path := cluster.GetMyLoaderPath()
	v, err := readMyLoaderVersion(path, myLoaderVersionTimeout)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Version of myloader %s not read (%s): --source-data=0 not added to the restore", path, err)
		return nil
	}
	return v
}

// The shipped backup-myloader-options hold two options that newer MyLoader releases no longer take
// (upstream, read at the release tags):
//   - --overwrite-tables: accepted but without effect from 0.20.1 (the DROP mode is only selected by
//     --drop-table since commit 0ae8ffb9: the restore stops on the first existing table), unknown from 1.0.0;
//     --drop-table=DROP runs the same DROP TABLE / DROP VIEW IF EXISTS;
//   - --innodb-optimize-keys=skip: unknown or refused from 0.19.1, renamed --optimize-keys=SKIP (same code).
const (
	myLoaderDropTableFrom    = "0.20.1"
	myLoaderOptimizeKeysFrom = "0.19.1"

	myLoaderOptOverwriteTables = "--overwrite-tables"
	myLoaderOptDropTable       = "--drop-table=DROP"
	myLoaderOptInnodbSkip      = "--innodb-optimize-keys=skip"
	myLoaderOptOptimizeSkip    = "--optimize-keys=SKIP"
)

// myLoaderManagedDefaultArgs returns args with the two shipped-default options replaced by the ones the
// myloader release takes, and what it replaced. args is not modified. A myloader older than the boundary of
// an option keeps that option.
func myLoaderManagedDefaultArgs(args []string, loader *version.Version) (adapted []string, changes []string) {
	if loader == nil {
		return slices.Clone(args), nil
	}
	adapted = make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == myLoaderOptOverwriteTables && loader.GreaterEqual(myLoaderDropTableFrom):
			adapted = append(adapted, myLoaderOptDropTable)
			changes = append(changes, myLoaderOptOverwriteTables+" -> "+myLoaderOptDropTable)
		case a == myLoaderOptInnodbSkip && loader.GreaterEqual(myLoaderOptimizeKeysFrom):
			adapted = append(adapted, myLoaderOptOptimizeSkip)
			changes = append(changes, myLoaderOptInnodbSkip+" -> "+myLoaderOptOptimizeSkip)
		default:
			adapted = append(adapted, a)
		}
	}
	return adapted, changes
}

// myLoaderRejectedOptions says, for an argument list that is not rewritten (explicit or custom
// configuration), which of its options the myloader release does not take or ignores.
func myLoaderRejectedOptions(args []string, loader *version.Version) []string {
	if loader == nil {
		return nil
	}
	var out []string
	for _, a := range args {
		switch {
		case a == myLoaderOptOverwriteTables && loader.GreaterEqual(myLoaderDropTableFrom):
			out = append(out, myLoaderOptOverwriteTables+" does not drop existing tables since MyLoader "+myLoaderDropTableFrom+" (unknown from 1.0.0): use "+myLoaderOptDropTable)
		case (a == "--innodb-optimize-keys" || strings.HasPrefix(a, "--innodb-optimize-keys=")) && loader.GreaterEqual(myLoaderOptimizeKeysFrom):
			out = append(out, a+" is refused or unknown since MyLoader "+myLoaderOptimizeKeysFrom+": use --optimize-keys (for example "+myLoaderOptOptimizeSkip+")")
		}
	}
	return out
}

// myLoaderOptionsExplicit reports whether a configuration source set backup-myloader-options: a config
// file, the environment or a flag (the immutable map) or the dynamic map. Both maps are read through their
// synchronized accessors. A value set through the API is not recorded in either map, so it is not detected.
func (cluster *Cluster) myLoaderOptionsExplicit() bool {
	if _, ok := cluster.Conf.GetImmuableFlag("backup-myloader-options"); ok {
		return true
	}
	if _, ok := cluster.Conf.GetDynamicFlag("backup-myloader-options"); ok {
		return true
	}
	return false
}

// adaptMyLoaderDefaults adapts the arguments of a restore to the myloader release: only when the configured
// value is the exact shipped default AND no configuration source set it. Any other value is left as it is,
// with a warning for each option the release rejects. Persistent configuration is never written.
func (cluster *Cluster) adaptMyLoaderDefaults(args []string, loader *version.Version) []string {
	if cluster.Conf.BackupMyLoaderOptions == config.DefaultBackupMyLoaderOptions && !cluster.myLoaderOptionsExplicit() {
		adapted, changes := myLoaderManagedDefaultArgs(args, loader)
		if len(changes) > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "myloader %s: the shipped default of backup-myloader-options adapted for this release: %s", loader.ToString(), strings.Join(changes, ", "))
		}
		return adapted
	}
	for _, msg := range myLoaderRejectedOptions(args, loader) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "myloader %s: backup-myloader-options is configured and left as it is: %s", loader.ToString(), msg)
	}
	return args
}

// myLoaderRestoreArgs builds the myloader arguments of a restore. The version of the myloader that runs is
// read once (bounded probe) and used for every rule: the managed-default adaptation (only when adaptDefaults,
// the ordinary replica reseed) and the replication source guard (myLoaderNoSourceChangeArgs). An unknown
// version adapts nothing.
func (cluster *Cluster) myLoaderRestoreArgs(adaptDefaults bool) []string {
	loader := cluster.myLoaderVersion()
	args := cluster.GetMyLoaderCompatibleOptions()
	if adaptDefaults && loader != nil {
		args = cluster.adaptMyLoaderDefaults(args, loader)
	}
	return myLoaderNoSourceChangeArgs(args, loader)
}

// isOrdinaryMyLoaderReseed tells whether the logical reseed ProcessReseedLogical runs takes the adaptation of the
// managed default options: the shared logical replica reseed path, minus what the reliable execution context
// excludes when it runs: a point-in-time restore (server.PointInTimeMeta.IsInPITR) and the staging refresh
// (cluster.IsRefreshStaging, held for the whole refresh, or the configured staging server). A reseed armed by the
// automatic rejoin (autorejoin-logical-backup) or by the logical-backup cookie reaches ProcessReseedLogical through
// the same task as the operator's request and carries no origin (reseedFromRejoin is set after the arming): it
// cannot be told apart and takes the adaptation too.
func isOrdinaryMyLoaderReseed(isPITR, refreshingStaging, stagingServer bool) bool {
	return !isPITR && !refreshingStaging && !stagingServer
}

// adaptsMyLoaderDefaults tells whether a restore gets the managed-default adaptation: the shared logical replica
// reseed path only (replicaReseed, set by its call site, see isOrdinaryMyLoaderReseed). A restore on the master (jobReseedMyLoader adds
// --enable-binlog there), the seeding of a child from a parent cluster and the rejoin by logical backup keep
// their arguments as they are.
func adaptsMyLoaderDefaults(replicaReseed, restoreOnMaster, fromParent bool) bool {
	return replicaReseed && !restoreOnMaster && !fromParent
}
