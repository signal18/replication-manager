package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
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
