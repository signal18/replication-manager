// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/alert"
)

func (server *ServerMonitor) GetDatabaseMetrics() []graphite.Metric {
	cluster := server.GetCluster()
	cg := cluster.ClusterGraphite

	replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
	hostname := replacer.Replace(server.Variables.Get("HOSTNAME"))
	var metrics []graphite.Metric
	if server.IsSlave && server.GetCluster().GetTopology() != topoMultiMasterWsrep && server.GetCluster().GetTopology() != topoMultiMasterGrouprep {
		m := graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_seconds_behind_master", hostname), fmt.Sprintf("%d", server.SlaveStatus.SecondsBehindMaster.Int64), time.Now().Unix())
		metrics = append(metrics, m)
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_exec_master_log_pos", hostname), fmt.Sprintf("%s", server.SlaveStatus.ExecMasterLogPos.String), time.Now().Unix()))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_read_master_log_pos", hostname), fmt.Sprintf("%s", server.SlaveStatus.ReadMasterLogPos.String), time.Now().Unix()))
		if server.SlaveStatus.SlaveSQLRunning.String == "Yes" {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_sql_running", hostname), "1", time.Now().Unix()))
		} else {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_sql_running", hostname), "0", time.Now().Unix()))
		}
		if server.SlaveStatus.SlaveIORunning.String == "Yes" {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_io_running", hostname), "1", time.Now().Unix()))
		} else {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_io_running", hostname), "0", time.Now().Unix()))
		}
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_last_errno", hostname), fmt.Sprintf("%s", server.SlaveStatus.LastSQLErrno.String), time.Now().Unix()))

	}

	isNumeric := func(s string) bool {
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}

	for k, v := range server.Status.ToNewMap() {
		if isNumeric(v) {
			mname := fmt.Sprintf("mysql.%s.mysql_global_status_%s", hostname, strings.ToLower(k))
			if cg.MatchList(mname) {
				metrics = append(metrics, graphite.NewMetric(mname, v, time.Now().Unix()))
			}
		}
	}

	for k, v := range server.Variables.ToNewMap() {
		if isNumeric(v) {
			mname := fmt.Sprintf("mysql.%s.mysql_global_variables_%s", hostname, strings.ToLower(k))
			if cg.MatchList(mname) {
				metrics = append(metrics, graphite.NewMetric(mname, v, time.Now().Unix()))
			}
		}
	}

	for k, v := range server.EngineInnoDB.ToNewMap() {
		if isNumeric(v) {
			mname := fmt.Sprintf("mysql.%s.engine_innodb_%s", hostname, strings.ToLower(k))
			if cg.MatchList(mname) {
				metrics = append(metrics, graphite.NewMetric(mname, v, time.Now().Unix()))
			}
		}
	}

	for _, v := range server.PFSQueries.ToNewMap() {
		if isNumeric(v.Value) {
			label := replacer.Replace(v.Digest)
			if len(label) > 198 {
				label = label[0:198]
			}
			mname := fmt.Sprintf("mysql.%s.pfs.%s", hostname, label)
			if cg.MatchList(mname) {
				metrics = append(metrics, graphite.NewMetric(mname, v.Value, time.Now().Unix()))
			}
		}
	}
	return metrics
}

func (server *ServerMonitor) SendDatabaseStats() error {
	metrics := server.GetDatabaseMetrics()
	graph, err := graphite.NewGraphite(server.ClusterGroup.Conf.GraphiteCarbonHost, server.ClusterGroup.Conf.GraphiteCarbonPort)

	if err != nil {
		return err
	}
	graph.SendMetrics(metrics)

	graph.Disconnect()

	return nil
}

func (server *ServerMonitor) SendAlert() error {
	if server.ClusterGroup.Status != ConstMonitorActif && server.ClusterGroup.IsDiscovered() {
		return nil
	}
	if server.State == server.PrevState {
		return nil
	}

	a := alert.Alert{
		State:     server.State,
		PrevState: server.PrevState,
		Host:      server.URL,
		Cluster:   server.GetCluster().Name,
	}

	return server.ClusterGroup.SendAlert(a)
}
