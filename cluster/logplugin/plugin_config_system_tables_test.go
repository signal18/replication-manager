// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package logplugin

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

// the line of preprod curepipe db2's logical backup
const curepipeDumpError = "mysqldump: exit status 2 | stderr: -- Connecting to db2.curepipe.svc.cloud18... / mariadb-dump: Couldn't execute 'SHOW FUNCTION STATUS WHERE Db = 'dotnet1'': Column count of mysql.proc is wrong. Expected 22, found 21. Created with MariaDB 110803, now running 130002. Please use mariadb-upgrade to fix this error (1558) / -- next"

// #2022: the last observation decides: a backup whose error names a lagging system table
// opens ERR00116 (config domain, error level); a later backup ended OK, or a
// mariadbupgrade ended OK, closes it; a backup failed for another reason changes
// nothing; the error log counts since the server's start only.
func TestSystemTablesPlugin(t *testing.T) {
	p := &SystemTablesPlugin{}
	mariadb := StdioServerVersion{Flavor: "MariaDB", Major: 13}
	now := time.Now().Unix()
	src := func(jobs []StdioJob, errlog []s18log.HttpMessage, uptime int64) LogSource {
		return LogSource{ServerURL: "db2:3306", ServerVersion: mariadb, Jobs: jobs, ErrorLog: errlog,
			ServerStatus: map[string]string{"uptime": strconv.FormatInt(uptime, 10)}}
	}
	failed := StdioJob{Task: "mysqldump", Done: 1, State: 5, Result: curepipeDumpError, End: now - 600}

	res := p.Evaluate(src([]StdioJob{failed}, nil, 86400))
	if len(res.Findings) != 1 {
		t.Fatalf("the last backup failed on mysql.proc: %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.ErrKey != ErrKeySystemTablesUpgrade || f.Severity != SeverityError || f.Domain != DomainConfig ||
		!strings.Contains(f.Description, "Column count of mysql.proc is wrong. Expected 22, found 21") || strings.Contains(f.Description, "next") ||
		!strings.Contains(f.Description, "mariadb-upgrade") || len(f.Remediations) != 1 || f.Remediations[0].Type != "server_action" {
		t.Fatalf("finding: %+v", f)
	}

	other := StdioJob{Task: "mysqldump", Done: 1, State: 5, Result: "mysqldump: exit status 2 | stderr: Got error: 2013: Lost connection", End: now - 60}
	if res := p.Evaluate(src([]StdioJob{failed, other}, nil, 86400)); len(res.Findings) != 1 {
		t.Fatal("a backup failed for another reason proves nothing: still open")
	}
	ok := StdioJob{Task: "mysqldump", Done: 1, State: 4, Result: "Backup completed", End: now - 60}
	if res := p.Evaluate(src([]StdioJob{failed, ok}, nil, 86400)); len(res.Findings) != 0 {
		t.Fatal("the last backup went through: closed")
	}
	upgraded := StdioJob{Task: "mariadbupgrade", Done: 1, State: 3, End: now - 60}
	if res := p.Evaluate(src([]StdioJob{failed, upgraded}, nil, 86400)); len(res.Findings) != 0 {
		t.Fatal("an upgrade ended OK after it: closed")
	}
	upgradeFailed := StdioJob{Task: "mariadbupgrade", Done: 1, State: 5, End: now - 60}
	if res := p.Evaluate(src([]StdioJob{failed, upgradeFailed}, nil, 86400)); len(res.Findings) != 1 {
		t.Fatal("a failed upgrade keeps it open")
	}

	// the error log, since the start: the MariaDB line format kept by the watcher
	line := s18log.HttpMessage{Timestamp: time.Unix(now-120, 0).UTC().Format("2006-01-02 15:04:05") + " 0", Level: "ERROR",
		Text: "Incorrect definition of table mysql.event: expected column 'sql_mode' at position 14 to have type set(...)"}
	if res := p.Evaluate(src(nil, []s18log.HttpMessage{line}, 600)); len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Description, "the error log") {
		t.Fatalf("the error log since the start opens it: %+v", res.Findings)
	}
	if res := p.Evaluate(src(nil, []s18log.HttpMessage{line}, 60)); len(res.Findings) != 0 {
		t.Fatal("a line from before the server's start does not count")
	}
	if res := p.Evaluate(src([]StdioJob{ok}, []s18log.HttpMessage{line}, 600)); len(res.Findings) != 0 {
		t.Fatal("a backup that went through after the line closes it")
	}

	// nothing for PostgreSQL; MySQL's tool
	if res := p.Evaluate(LogSource{ServerVersion: StdioServerVersion{Flavor: "PostgreSQL"}, Jobs: []StdioJob{failed}}); len(res.Findings) != 0 {
		t.Fatal("PostgreSQL has no such system tables")
	}
	my := src([]StdioJob{failed}, nil, 86400)
	my.ServerVersion = StdioServerVersion{Flavor: "MySQL", Major: 5, Minor: 7}
	if res := p.Evaluate(my); len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Description, "mysql_upgrade") {
		t.Fatalf("MySQL 5.7: %+v", res.Findings)
	}
}

// review of #2030: the error log is in the server's zone, MySQL 8.0.16+ upgrades at
// start, and on the same second the proof that the tables are fine wins
func TestSystemTablesPluginZonesAndTies(t *testing.T) {
	p := &SystemTablesPlugin{}
	now := time.Now().Unix()
	plus2 := time.FixedZone("+02:00", 2*3600)
	line := func(at int64, loc *time.Location) s18log.HttpMessage {
		return s18log.HttpMessage{Timestamp: time.Unix(at, 0).In(loc).Format("2006-01-02 15:04:05") + " 0", Level: "ERROR",
			Text: "Incorrect definition of table mysql.event: expected column 'sql_mode'"}
	}
	upgraded := StdioJob{Task: "mariadbupgrade", Done: 1, State: 3, End: now - 3600}
	for _, vars := range []map[string]string{{"time_zone": "+02:00"}, {"time_zone": "SYSTEM", "system_time_zone": "Europe/Paris"}} {
		loc := serverLocation(vars)
		// a complaint from 2 hours ago, written in the server's local time: older than the
		// upgrade an hour ago, so closed (read as UTC it looked newer)
		src := LogSource{ServerVersion: StdioServerVersion{Flavor: "MariaDB", Major: 11}, ServerVariables: vars,
			ServerStatus: map[string]string{"uptime": "86400"}, Jobs: []StdioJob{upgraded},
			ErrorLog: []s18log.HttpMessage{line(now-7200, loc)}}
		if res := p.Evaluate(src); len(res.Findings) != 0 {
			t.Errorf("%v: a complaint older than the upgrade, in local time, must not reopen it: %+v", vars, res.Findings)
		}
	}
	if serverLocation(map[string]string{"time_zone": "+02:00"}).String() != plus2.String() {
		t.Fatal("time_zone offset")
	}
	if serverLocation(nil) != time.UTC || serverLocation(map[string]string{"system_time_zone": "CEST"}) != time.UTC {
		t.Fatal("unknown zone: UTC")
	}

	failed := StdioJob{Task: "mysqldump", Done: 1, State: 5, Result: curepipeDumpError, End: now - 60}
	if res := p.Evaluate(LogSource{ServerVersion: StdioServerVersion{Flavor: "MySQL", Major: 8, Minor: 0, Release: 36}, Jobs: []StdioJob{failed}}); len(res.Findings) != 0 {
		t.Fatal("MySQL 8.0.36 upgrades at start: no finding the action would refuse")
	}
	if res := p.Evaluate(LogSource{ServerVersion: StdioServerVersion{Flavor: "MySQL", Major: 8, Minor: 0, Release: 15}, Jobs: []StdioJob{failed}}); len(res.Findings) != 1 {
		t.Fatal("MySQL 8.0.15 still needs mysql_upgrade")
	}

	tie := StdioJob{Task: "mariadbupgrade", Done: 1, State: 4, End: now - 60}
	if res := p.Evaluate(LogSource{ServerVersion: StdioServerVersion{Flavor: "MariaDB", Major: 11}, Jobs: []StdioJob{failed, tie}}); len(res.Findings) != 0 {
		t.Fatal("same second: the upgrade ended OK wins")
	}
}
