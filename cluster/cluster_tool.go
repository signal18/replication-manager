package cluster

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
