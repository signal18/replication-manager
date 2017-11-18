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
	graph, err := graphite.NewGraphite(server.ClusterGroup.conf.GraphiteCarbonHost, server.ClusterGroup.conf.GraphiteCarbonPort)

	if err != nil {
		return err
	}

	var metrics = make([]graphite.Metric, 5)
	if server.IsSlave {
		metrics[0] = graphite.NewMetric(fmt.Sprintf("%s.replication.delay", server.Variables["HOSTNAME"]), fmt.Sprintf("%d", slaveStatus.SecondsBehindMaster.Int64), time.Now().Unix())
	}
	metrics[2] = graphite.NewMetric(fmt.Sprintf("server%s.status.ThreadsRunning", server.Variables["HOSTNAME"]), server.Status["THREADS_RUNNING"], time.Now().Unix())
	metrics[1] = graphite.NewMetric(fmt.Sprintf("server%s.status.Queries", server.Variables["HOSTNAME"]), server.Status["QUERIES"], time.Now().Unix())
	metrics[3] = graphite.NewMetric(fmt.Sprintf("server%s.status.BytesOut", server.Variables["HOSTNAME"]), server.Status["BYTES_SENT"], time.Now().Unix())
	metrics[4] = graphite.NewMetric(fmt.Sprintf("server%s.status.BytesIn", server.Variables["HOSTNAME"]), server.Status["BYTES_RECEIVED"], time.Now().Unix())

	//	metrics[5] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[6] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[7] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[8] = graphite.NewMetric(, time.Now().Unix())
	graph.SendMetrics(metrics)

	isNumeric := func(s string) bool {
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}

	var globalstatusmetrics = make([]graphite.Metric, len(server.Status))
	i := 0
	for k, v := range server.Status {
		if isNumeric(v) {
			globalstatusmetrics[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_status_%s", server.Variables["HOSTNAME"], strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalstatusmetrics)

	var globalvariablesmetrics = make([]graphite.Metric, len(server.Variables))
	i = 0
	for k, v := range server.Variables {
		if isNumeric(v) {
			globalvariablesmetrics[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.mysql_global_variables_%s", server.Variables["HOSTNAME"], strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalvariablesmetrics)

	var globalinnodbengine = make([]graphite.Metric, len(server.EngineInnoDB))
	i = 0
	for k, v := range server.EngineInnoDB {
		if isNumeric(v) {
			globalinnodbengine[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.engine_innodb_%s", server.Variables["HOSTNAME"], strings.ToLower(k)), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(globalinnodbengine)

	var queries = make([]graphite.Metric, len(server.Queries))
	i = 0
	for k, v := range server.Queries {
		if isNumeric(v) {
			replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
			label := replacer.Replace(k)
			if len(label) > 198 {
				label = label[0:198]
			}
			queries[i] = graphite.NewMetric(fmt.Sprintf("mysql.%s.pfs.%s", server.Variables["HOSTNAME"], label), v, time.Now().Unix())
		}
		i++
	}
	graph.SendMetrics(queries)

	graph.Disconnect()

	return nil
}

func (server *ServerMonitor) SendAlert() error {
	if server.ClusterGroup.conf.MailTo != "" {
		a := alert.Alert{
			From:        server.ClusterGroup.conf.MailFrom,
			To:          server.ClusterGroup.conf.MailTo,
			Type:        server.State,
			Origin:      server.URL,
			Destination: server.ClusterGroup.conf.MailSMTPAddr,
		}
		err := a.Email()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not send mail alert: %s ", err)
		}
	}
	if server.ClusterGroup.conf.AlertScript != "" {
		server.ClusterGroup.LogPrintf("INFO", "Calling alert script")
		var out []byte
		out, err := exec.Command(server.ClusterGroup.conf.AlertScript, server.URL, server.PrevState, server.State).CombinedOutput()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "%s", err)
		}
		server.ClusterGroup.LogPrintf("INFO", "Alert script complete:", string(out))
	}
	return nil
}
