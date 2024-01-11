// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (cluster *Cluster) JobAnalyzeSQL() error {
	var err error
	var logs string
	server := cluster.master

	if server == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Analyze tables cancel as no leader ")
		return errors.New("Analyze tables cancel as no leader")
	}
	if !cluster.Conf.MonitorSchemaChange {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Analyze tables cancel no schema monitor in config")
		return errors.New("Analyze tables cancel no schema monitor in config")
	}
	if cluster.inAnalyzeTables {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Analyze tables cancel already running")
		return errors.New("Analyze tables cancel already running")
	}
	if cluster.master.Tables == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Analyze tables cancel no table list")
		return errors.New("Analyze tables cancel no table list")
	}
	cluster.inAnalyzeTables = true
	defer func() {
		cluster.inAnalyzeTables = false
	}()
	for _, t := range cluster.master.Tables {

		//	for _, s := range cluster.slaves {
		logs, err = dbhelper.AnalyzeTable(server.Conn, server.DBVersion, t.TableSchema+"."+t.TableName)
		cluster.LogSQL(logs, err, server.URL, "Monitor", LvlErr, "Could not get database variables %s %s", server.URL, err)

		//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Analyse table %s on %s", t, s.URL)
		//	}
	}
	return err
}
