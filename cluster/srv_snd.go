// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/alert"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/graphite"
)

func (server *ServerMonitor) SendDatabaseStats(slaveStatus *dbhelper.SlaveStatus) error {
	graph, err := graphite.NewGraphite(server.ClusterGroup.Conf.GraphiteCarbonHost, server.ClusterGroup.Conf.GraphiteCarbonPort)

	if err != nil {
		return err
	}
	replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
	hostname := replacer.Replace(server.Variables["HOSTNAME"])
	var metrics = make([]graphite.Metric, 6)
	if server.IsSlave {
		metrics[0] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_seconds_behind_master", hostname), fmt.Sprintf("%d", slaveStatus.SecondsBehindMaster.Int64), time.Now().Unix())
		metrics[1] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_exec_master_log_pos", hostname), fmt.Sprintf("%s", slaveStatus.ExecMasterLogPos.String), time.Now().Unix())
		metrics[2] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_read_master_log_pos", hostname), fmt.Sprintf("%s", slaveStatus.ReadMasterLogPos.String), time.Now().Unix())
		if slaveStatus.SlaveSQLRunning.String == "Yes" {
			metrics[3] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_sql_running", hostname), "1", time.Now().Unix())
		} else {
			metrics[3] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_sql_running", hostname), "0", time.Now().Unix())
		}
		if slaveStatus.SlaveIORunning.String == "Yes" {
			metrics[4] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_io_running", hostname), "1", time.Now().Unix())
		} else {
			metrics[4] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_slave_io_running", hostname), "0", time.Now().Unix())
		}
		metrics[5] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_status_last_errno", hostname), fmt.Sprintf("%s", slaveStatus.LastSQLErrno.String), time.Now().Unix())

		//metrics[3] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_slave_relay_log_pos", hostname), fmt.Sprintf("%d", slaveStatus.re), time.Now().Unix())
	}

	graph.SendMetrics(metrics)

	isNumeric := func(s string) bool {
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}

	var globalstatusmetrics = make([]graphite.Metric, len(server.Status))
	i := 0
	for k, v := range server.Status {
		if isNumeric(v) {
			globalstatusmetrics[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_status_%s", hostname, strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalstatusmetrics)

	var globalvariablesmetrics = make([]graphite.Metric, len(server.Variables))
	i = 0
	for k, v := range server.Variables {
		if isNumeric(v) {
			globalvariablesmetrics[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_variables_%s", hostname, strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalvariablesmetrics)

	var globalinnodbengine = make([]graphite.Metric, len(server.EngineInnoDB))
	i = 0
	for k, v := range server.EngineInnoDB {
		if isNumeric(v) {
			globalinnodbengine[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.engine_innodb_%s", hostname, strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalinnodbengine)

	var queries = make([]graphite.Metric, len(server.Queries))
	i = 0
	for k, v := range server.Queries {
		if isNumeric(v) {
			label := replacer.Replace(k)
			if len(label) > 198 {
				label = label[0:198]
			}
			queries[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.pfs.%s", hostname, label), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(queries)

	graph.Disconnect()

	return nil
}

func (server *ServerMonitor) SendAlert() error {
	if server.ClusterGroup.Status != ConstMonitorActif {
		return nil
	}
	if server.ClusterGroup.Conf.MailTo != "" {
		a := alert.Alert{
			From:        server.ClusterGroup.Conf.MailFrom,
			To:          server.ClusterGroup.Conf.MailTo,
			Type:        server.State,
			Origin:      server.URL,
			Destination: server.ClusterGroup.Conf.MailSMTPAddr,
		}
		err := a.Email()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not send mail alert: %s ", err)
		}
	}
	if server.ClusterGroup.Conf.AlertScript != "" {
		server.ClusterGroup.LogPrintf("INFO", "Calling alert script")
		var out []byte
		out, err := exec.Command(server.ClusterGroup.Conf.AlertScript, server.URL, server.PrevState, server.State).CombinedOutput()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "%s", err)
		}
		server.ClusterGroup.LogPrintf("INFO", "Alert script complete:", string(out))
	}
	return nil
}
