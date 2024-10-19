// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"sort"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (cluster *Cluster) JobAnalyzeSQL() error {
	var err error
	var logs string
	server := cluster.master

	if server == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel as no leader ")
		return errors.New("Analyze tables cancel as no leader")
	}
	if !cluster.Conf.MonitorSchemaChange {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no schema monitor in config")
		return errors.New("Analyze tables cancel no schema monitor in config")
	}
	if cluster.inAnalyzeTables {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel already running")
		return errors.New("Analyze tables cancel already running")
	}
	if cluster.master.Tables == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analyze tables cancel no table list")
		return errors.New("Analyze tables cancel no table list")
	}
	cluster.inAnalyzeTables = true
	defer func() {
		cluster.inAnalyzeTables = false
	}()
	for _, t := range cluster.master.Tables {

		//	for _, s := range cluster.slaves {
		logs, err = dbhelper.AnalyzeTable(server.Conn, server.DBVersion, t.TableSchema+"."+t.TableName)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlErr, "Could not get database variables %s %s", server.URL, err)

		//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Analyse table %s on %s", t, s.URL)
		//	}
	}
	return err
}

func (cluster *Cluster) JobsGetEntries() (config.JobEntries, error) {
	var t config.Task
	var entries config.JobEntries = config.JobEntries{
		Header:  config.GetLabelsAsMap(t),
		Servers: make(map[string]config.ServerTaskList),
	}

	for _, s := range cluster.Servers {
		sTask := config.ServerTaskList{
			ServerURL: s.URL,
			Tasks:     make([]config.Task, 0),
		}

		s.JobResults.Range(func(k, v any) bool {
			sTask.Tasks = append(sTask.Tasks, *v.(*config.Task))
			return true
		})
		sort.Sort(config.TaskSorter(sTask.Tasks))
		entries.Servers[s.Id] = sTask
	}

	return entries, nil
}

func (cluster *Cluster) GetSlowLogTable() {
	// Skip if previous cycle is not finished yet
	if !cluster.IsGettingSlowLog {
		cluster.IsGettingSlowLog = true
		wg := new(sync.WaitGroup)
		defer func() {
			cluster.IsGettingSlowLog = false
		}()

		for _, s := range cluster.Servers {
			if s != nil {
				wg.Add(1)
				go func() {
					err := s.GetSlowLogTable(wg)
					if err != nil && !isNoConnPoolError(err) {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "%s", err)
					}
				}()
			}
		}

		wg.Wait()
	}
}
