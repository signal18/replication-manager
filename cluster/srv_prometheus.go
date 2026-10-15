// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"
	"strings"
)

// GetPrometheusMetrics writes the server's Graphite series (GetDatabaseMetrics) as
// Prometheus text lines, for /api/prometheus. The cluster's plan_dbu is not here: it is a
// cluster series every server emits, written once per cluster by GetPrometheusMetrics on
// the cluster (#2055).
func (server *ServerMonitor) GetPrometheusMetrics() string {
	var b strings.Builder
	for _, m := range server.GetDatabaseMetrics() {
		if line, ok := prometheusLine(m.Name, m.Value); ok {
			b.WriteString(line)
		}
	}
	return b.String()
}

// GetPrometheusMetrics writes the cluster series: plan_dbu, once per cluster, with the raw
// cluster name the dbu series carry in their cluster label.
func (cluster *Cluster) GetPrometheusMetrics() string {
	return prometheusFormat("plan_dbu", [][2]string{{"cluster", cluster.Name}}, strconv.FormatFloat(float64(cluster.GetPlanDbu()), 'f', 4, 64))
}

// prometheusLine maps one Graphite series to its Prometheus line, by the name scheme
// GetDatabaseMetrics writes:
//   - mysql.<host>.<name>        -> <name>{instance="<host>"}
//   - mysql.<host>.pfs.<digest>  -> pfs_<digest>{instance="<host>"}
//   - dbu.<cluster>.<host>.<name> -> <name>{cluster="<cluster>",instance="<host>"}
//
// Any other series (resourcemanager.<CLUSTER>.plan_dbu, written by the cluster) is not
// written: read as mysql.<host>.<name>, a new scheme would put the wrong segment in the
// name, as dbu.* did in 3.1.43 (#2055).
func prometheusLine(name, value string) (string, bool) {
	v := strings.Split(name, ".")
	switch {
	case len(v) == 4 && v[0] == "mysql" && v[2] == "pfs":
		return prometheusFormat("pfs_"+v[3], [][2]string{{"instance", v[1]}}, value), true
	case len(v) == 3 && v[0] == "mysql":
		return prometheusFormat(v[2], [][2]string{{"instance", v[1]}}, value), true
	case len(v) >= 4 && v[0] == "dbu":
		// the host has no dot (GetDatabaseMetrics' replacer), the raw cluster name may
		return prometheusFormat(v[len(v)-1], [][2]string{{"cluster", strings.Join(v[1:len(v)-2], ".")}, {"instance", v[len(v)-2]}}, value), true
	}
	return "", false
}

func prometheusFormat(name string, labels [][2]string, value string) string {
	var b strings.Builder
	b.WriteString(prometheusName(name))
	b.WriteByte('{')
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l[0])
		b.WriteString(`="`)
		b.WriteString(prometheusLabelValue(l[1]))
		b.WriteByte('"')
	}
	b.WriteString("} ")
	b.WriteString(value)
	b.WriteByte('\n')
	return b.String()
}

// prometheusName replaces what a metric name cannot hold ([a-zA-Z_:][a-zA-Z0-9_:]*) by _:
// a pfs digest keeps the - GetDatabaseMetrics' replacer puts in it.
func prometheusName(name string) string {
	b := []byte(name)
	for i, c := range b {
		if !(c == '_' || c == ':' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

// prometheusLabelValue escapes \, " and new lines, as the text format asks.
func prometheusLabelValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}
