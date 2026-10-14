// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// #2022: the upgrade of a server's system tables is a dbjob (the upgrade tool of the
// server's own release, in its jobs container): queued as a wait marker in API mode,
// consumed once by the script's needs check, cleared by the restart reconciliation;
// refused for a server down, MySQL 8.0.16+ and PostgreSQL.
func TestJobMariadbUpgrade(t *testing.T) {
	if !config.IsRemoteTask(config.ConstTaskMariadbUpgrade, nil) {
		t.Fatal("mariadbupgrade runs in the jobs container only")
	}

	server := newTestServerForAPIJobs(t)
	server.DBVersion, _ = version.NewVersion("MariaDB", 13, 0, 2)
	if _, err := server.JobMariadbUpgrade(); err != nil {
		t.Fatalf("MariaDB 13.0.2: %v", err)
	}
	if !server.HasWaitMariadbUpgradeCookie() {
		t.Fatal("API mode: the task waits for the jobs script")
	}
	if ok, err := server.CheckTaskNeeded(string(config.ConstTaskMariadbUpgrade)); err != nil || !ok {
		t.Fatalf("the script's needs check takes the task: %v %v", ok, err)
	}
	if ok, _ := server.CheckTaskNeeded(string(config.ConstTaskMariadbUpgrade)); ok || server.HasWaitMariadbUpgradeCookie() {
		t.Fatal("the task is taken once")
	}
	if _, err := server.JobMariadbUpgrade(); err == nil {
		t.Fatal("a second request while the first runs must be refused")
	}

	// the restart reconciliation clears a pending task's marker
	again := newTestServerForAPIJobs(t)
	again.DBVersion = server.DBVersion
	if _, err := again.JobMariadbUpgrade(); err != nil {
		t.Fatal(err)
	}
	again.ReconcileRestoredAPIJobs()
	if again.HasWaitMariadbUpgradeCookie() {
		t.Fatal("a reconciled task's wait marker is removed")
	}

	for _, c := range []struct {
		name  string
		setup func(*ServerMonitor)
		want  string
	}{
		{"down", func(s *ServerMonitor) { s.State = stateFailed }, "down"},
		{"mysql 8.0.36", func(s *ServerMonitor) { s.DBVersion, _ = version.NewVersion("MySQL", 8, 0, 36) }, "at start"},
		{"postgresql", func(s *ServerMonitor) { s.DBVersion, _ = version.NewVersion("PostgreSQL", 16, 4) }, "PostgreSQL"},
	} {
		s := newTestServerForAPIJobs(t)
		s.DBVersion, _ = version.NewVersion("MariaDB", 11, 8, 3)
		c.setup(s)
		if _, err := s.JobMariadbUpgrade(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal expected (%q), got %v", c.name, c.want, err)
		}
		if s.HasWaitMariadbUpgradeCookie() {
			t.Errorf("%s: nothing queued", c.name)
		}
	}
}

// The jobs script knows the task: in its job list, a case that runs the upgrade tool on
// the system tables only, and the tool's exit code as the outcome in both modes.
func TestDbjobsScriptMariadbUpgrade(t *testing.T) {
	b, err := os.ReadFile("../share/scripts/dbjobs_new.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"optimize" "mariadbupgrade"`,
		"mariadbupgrade)\n            run_mariadb_upgrade",
		"--upgrade-system-tables --force",
		`if [[ "$job" == "mariadbupgrade" ]] && ! mariadb_upgrade_succeeded; then`,
		"mariadbupgrade)\n                if ! mariadb_upgrade_succeeded; then",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("dbjobs_new.sh lacks %q", want)
		}
	}
}

// #2022: ERR00116, a config-domain finding, lands in the ConfigStateMachine (not the main
// one) and its remediation is the server action upgrade-system-tables.
func TestSystemTablesRemediation(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	server.DBVersion, _ = version.NewVersion("MariaDB", 13, 0, 2)
	server.Id = "db17582624883473042384"
	cl := server.ClusterGroup
	cl.Name = "curepipe"
	cl.Servers = []*ServerMonitor{server}
	st := state.State{ErrKey: "ERR00116", ErrType: "ERROR", ErrDesc: "lagging", ServerUrl: server.URL}
	cl.ConfigStates = []state.State{st}
	plan := cl.GetConfigRemediationPlan()
	if len(plan.Remediations) != 1 || plan.Remediations[0].Server != server.URL || len(plan.Remediations[0].Fixes) != 1 ||
		plan.Remediations[0].Fixes[0].Type != "server_action" ||
		plan.Remediations[0].Fixes[0].URL != "clusters/curepipe/servers/db17582624883473042384/actions/upgrade-system-tables" ||
		!strings.Contains(plan.Remediations[0].Fixes[0].Description, "mariadb-upgrade") {
		t.Fatalf("the remediation is the server action: %+v", plan.Remediations)
	}
	server.DBVersion, _ = version.NewVersion("MySQL", 5, 7, 44)
	if plan := cl.GetConfigRemediationPlan(); !strings.Contains(plan.Remediations[0].Fixes[0].Description, "mysql_upgrade") {
		t.Fatalf("MySQL 5.7: %+v", plan.Remediations[0].Fixes[0])
	}
}

// #2022: a failed backup's error carries the last stderr lines, bounded
func TestLineTail(t *testing.T) {
	tail := newLineTail(3)
	for _, l := range []string{"-- a", "", "-- b", "-- c", "mariadb-dump: Couldn't execute 'SHOW FUNCTION STATUS': Column count of mysql.proc is wrong"} {
		tail.add(l)
	}
	tail.add("Got error: 2013 " + strings.Repeat("x", 2000))
	got := tail.lines()
	// the verbose progress lines are not kept, a long line is cut
	if len(got) != 2 || !strings.Contains(got[0], "Column count of mysql.proc") || len(got[1]) > lineTailMaxBytes+len("…") {
		t.Fatalf("error lines only, bounded: %q", got)
	}
}
