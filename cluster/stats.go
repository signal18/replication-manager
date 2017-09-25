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
	"time"

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
		metrics[0] = graphite.NewMetric(fmt.Sprintf("server%d.replication.delay", server.ServerID), fmt.Sprintf("%d", slaveStatus.SecondsBehindMaster.Int64), time.Now().Unix())
	}
	metrics[2] = graphite.NewMetric(fmt.Sprintf("server%d.status.ThreadsRunning", server.ServerID), server.Status["THREADS_RUNNING"], time.Now().Unix())
	metrics[1] = graphite.NewMetric(fmt.Sprintf("server%d.status.Queries", server.ServerID), server.Status["QUERIES"], time.Now().Unix())
	metrics[3] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesOut", server.ServerID), server.Status["BYTES_SENT"], time.Now().Unix())
	metrics[4] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesIn", server.ServerID), server.Status["BYTES_RECEIVED"], time.Now().Unix())
	//	metrics[5] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[6] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[7] = graphite.NewMetric(, time.Now().Unix())
	//	metrics[8] = graphite.NewMetric(, time.Now().Unix())
	graph.SendMetrics(metrics)
	graph.Disconnect()

	return nil
}
