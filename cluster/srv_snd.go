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

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/alert"
)

func (server *ServerMonitor) GetDatabaseMetrics() []graphite.Metric {
	cluster := server.GetCluster()
	cg := cluster.ClusterGraphite

	replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
	hostname := replacer.Replace(server.Variables.Get("HOSTNAME"))
	var metrics []graphite.Metric
	if server.IsSlave && server.GetCluster().GetTopology() != config.TopoMultiMasterWsrep && server.GetCluster().GetTopology() != config.TopoMultiMasterGrouprep {
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

	// DBU (Database Unit) consumed — computed by repman from the system-level
	// sensor push (cgroup + df) in handlerMuxServerDBUConsumed. Emitted
	// unconditionally like the slave-status block above (a first-class signal,
	// not whitelist-gated), one value per monitor loop. dbu is the pivot = the
	// peak DBU of the last period; the per-axis values show which one binds
	// (the biggest contributor is server.DBUConsumed.Binding, kept in the JSON).
	// Emitted EVERY tick (not only on a fresh sensor push) so the series is continuous
	// (no gaps -> no flapping). Two views: the DBU duplicate (dbu_*), always >= 1 per
	// axis -- even for a stopped service, which keeps its reserved restart minimum
	// (ConsumedDBUForEmit) -- and the RAW resource series (service_*), the real
	// measurement, which DOES go to 0 when the service is down (RawResourceForEmit).
	{
		ts := time.Now().Unix()
		dbu, cpu, mem, io, disk := server.ConsumedDBUForEmit()
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.dbu", hostname), strconv.FormatFloat(dbu, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.dbu_cpu", hostname), strconv.FormatFloat(cpu, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.dbu_mem", hostname), strconv.FormatFloat(mem, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.dbu_io", hostname), strconv.FormatFloat(io, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.dbu_disk", hostname), strconv.FormatFloat(disk, 'f', 4, 64), ts))

		// Raw resource values (native units), the real measurement -- NOT floored
		// (the dbu_* series above are the DBU duplicate carrying the min-1 rule).
		// 0 on a down server. Same "service" unit the resource model reasons in.
		cores, memBytes, iops, diskBytes := server.RawResourceForEmit()
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.service_cpu", hostname), strconv.FormatFloat(cores, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.service_mem", hostname), strconv.FormatFloat(memBytes, 'f', 0, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.service_io", hostname), strconv.FormatFloat(iops, 'f', 4, 64), ts))
		metrics = append(metrics, graphite.NewMetric(fmt.Sprintf("mysql.%s.service_disk", hostname), strconv.FormatFloat(diskBytes, 'f', 0, 64), ts))
	}

	// Cluster-level PLAN series, emitted ONCE per cluster (from the master). The plan
	// (prov-service-plan-dbu) is a CLUSTER contract -- it exists nowhere per-server, so
	// unlike consumed (mysql.<host>.dbu, summed at query time via sumSeries) it MUST be
	// emitted here to be graphed over time, as resourcemanager.<CLUSTER>.plan_dbu. The
	// token is uppercased like the mysql.<HOST> series so the GUI scopes both the same way.
	if server.IsMaster() {
		ctoken := strings.ToUpper(replacer.Replace(cluster.Name))
		metrics = append(metrics, graphite.NewMetric(
			fmt.Sprintf("resourcemanager.%s.plan_dbu", ctoken),
			strconv.FormatFloat(float64(cluster.Conf.ProvServicePlanDbu), 'f', 4, 64),
			time.Now().Unix()))
	}
	return metrics
}

func (server *ServerMonitor) FetchDatabaseStats() {
	server.GetCluster().AddMetrics(server.GetDatabaseMetrics())
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
		Instance:  server.GetCluster().GetInstanceAddress(),
	}

	return server.ClusterGroup.SendAlert(a)
}
