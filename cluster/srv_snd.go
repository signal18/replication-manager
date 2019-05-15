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

	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/alert"
)

func (server *ServerMonitor) GetDatabaseMetrics() []graphite.Metric {

	replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
	hostname := replacer.Replace(server.Variables["HOSTNAME"])
	var metrics []graphite.Metric
	if server.IsSlave {
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

	for k, v := range server.Status {
		if isNumeric(v) {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_status_%s", hostname, strings.ToLower(k)), v, time.Now().Unix()))
		}
	}

	for k, v := range server.Variables {
		if isNumeric(v) {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_variables_%s", hostname, strings.ToLower(k)), v, time.Now().Unix()))
		}

	}
	for k, v := range server.EngineInnoDB {
		if isNumeric(v) {
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.engine_innodb_%s", hostname, strings.ToLower(k)), v, time.Now().Unix()))
		}

	}

	for _, v := range server.PFSQueries {
		if isNumeric(v.Value) {
			label := replacer.Replace(v.Digest)
			if len(label) > 198 {
				label = label[0:198]
			}
			metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.pfs.%s", hostname, label), v.Value, time.Now().Unix()))
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
	if server.ClusterGroup.Conf.MailTo != "" {
		a := alert.Alert{
			From:        server.ClusterGroup.Conf.MailFrom,
			To:          server.ClusterGroup.Conf.MailTo,
			State:       server.State,
			PrevState:   server.PrevState,
			Origin:      server.URL,
			Destination: server.ClusterGroup.Conf.MailSMTPAddr,
			User:        server.ClusterGroup.Conf.MailSMTPUser,
			Password:    server.ClusterGroup.Conf.MailSMTPPassword,
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
