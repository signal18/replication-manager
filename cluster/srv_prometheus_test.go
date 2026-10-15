// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"regexp"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// one Prometheus text line: name{label="value",...} value
var prometheusLineRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)\{((?:[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\]|\\.)*",?)*)\} (\S+)$`)

// #2055: every name scheme GetDatabaseMetrics writes gives the right name and labels
func TestPrometheusLine(t *testing.T) {
	cases := []struct{ graphite, want string }{
		{"mysql.LXC-MARIADB-01.mysql_global_variables_max_connections", `mysql_global_variables_max_connections{instance="LXC-MARIADB-01"} 1` + "\n"},
		{"mysql.LXC-MARIADB-01.pfs.3f2a-b1", `pfs_3f2a_b1{instance="LXC-MARIADB-01"} 1` + "\n"},
		{"dbu.mariadb_test.LXC-MARIADB-01.dbu_cpu", `dbu_cpu{cluster="mariadb_test",instance="LXC-MARIADB-01"} 1` + "\n"},
		{"dbu.my.cluster.LXC-MARIADB-01.service_mem", `service_mem{cluster="my.cluster",instance="LXC-MARIADB-01"} 1` + "\n"},
		{"dbu.a\"b.LXC-MARIADB-01.dbu", `dbu{cluster="a\"b",instance="LXC-MARIADB-01"} 1` + "\n"},
	}
	for _, tc := range cases {
		got, ok := prometheusLine(tc.graphite, "1")
		if !ok || got != tc.want {
			t.Errorf("%s: got %q (%v), want %q", tc.graphite, got, ok, tc.want)
		}
	}
	// a scheme the export does not know is not written with a wrong name
	for _, name := range []string{"resourcemanager.MARIADB_TEST.plan_dbu", "apu.c.app.dbu", "mysql.host"} {
		if line, ok := prometheusLine(name, "1"); ok {
			t.Errorf("%s written as %q", name, line)
		}
	}
}

// #2055: the export of a cluster with two servers is valid Prometheus text, no series twice
func TestPrometheusExportValid(t *testing.T) {
	var out strings.Builder
	for _, host := range []string{"LXC-MARIADB-01", "LXC-MARIADB-02"} {
		// every shape of srv_snd.go
		for _, name := range []string{
			"mysql.%h.mysql_slave_status_seconds_behind_master",
			"mysql.%h.mysql_global_status_threads_running",
			"mysql.%h.engine_innodb_history_list_lenght_inside_innodb",
			"mysql.%h.top_cpu_user",
			"mysql.%h.pfs.ab12-cd",
			"dbu.mariadb_test.%h.dbu", "dbu.mariadb_test.%h.dbu_cpu", "dbu.mariadb_test.%h.dbu_mem",
			"dbu.mariadb_test.%h.dbu_io", "dbu.mariadb_test.%h.dbu_disk",
			"dbu.mariadb_test.%h.service_cpu", "dbu.mariadb_test.%h.service_mem",
			"dbu.mariadb_test.%h.service_io", "dbu.mariadb_test.%h.service_disk",
			"resourcemanager.MARIADB_TEST.plan_dbu",
		} {
			if line, ok := prometheusLine(strings.Replace(name, "%h", host, 1), "1.0000"); ok {
				out.WriteString(line)
			}
		}
	}
	cl := &Cluster{Name: "mariadb_test", Conf: &config.Config{}}
	out.WriteString(cl.GetPrometheusMetrics())

	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		m := prometheusLineRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("not a Prometheus line: %q", line)
			continue
		}
		series := m[1] + "{" + m[2] + "}"
		if seen[series] {
			t.Errorf("series written twice: %s", series)
		}
		seen[series] = true
	}
	if !seen[`plan_dbu{cluster="mariadb_test"}`] {
		t.Errorf("no plan_dbu line in:\n%s", out.String())
	}
}
