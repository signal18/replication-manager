// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin config-system-tables raises ERR00116 (config domain, error level) when the
// server's system tables lag its release (#2022): a server upgraded without
// mariadb-upgrade keeps the system tables of its old release, and every dump of it then
// fails ("Column count of mysql.proc is wrong. Expected 22, found 21": the logical
// backups of preprod curepipe db2 on MariaDB 13.0.2).
//
// It reads what was already observed, never queries the server:
//   - the last logical backup of the server (mysqldump, mydumper jobs): its error text;
//   - the last dump of the server by another server's job: a reseed of a replica dumps
//     its source and is recorded on the replica ("mysqldump on <source>", #2044);
//   - the server's error log since its start: the same complaint, or "Incorrect
//     definition of table mysql.<table>" written at start.
//
// The state is the last observation's, or it would never close: the most recent of
// these events decides. A backup ended OK, or a mariadbupgrade job ended OK, closes it;
// a backup failed for another reason proves nothing and is skipped. Its remediation is
// the server action upgrade-system-tables (the mariadbupgrade dbjob).
//
// Config keys (under [plugin-config.config-system-tables]):
//
//	enabled bool default: true
package logplugin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ErrKeySystemTablesUpgrade = "ERR00116"

// systemTablesLagRe: the server's own errors when a system table has the shape of
// another release: the column count (ER_COL_COUNT_DOESNT_MATCH_PLEASE_UPDATE 1558,
// _CORRUPTED_V2 1805) and, in its error log at start, a column definition
var systemTablesLagRe = regexp.MustCompile(`(Column count of mysql\.[A-Za-z0-9_]+ is wrong|Incorrect definition of table mysql\.[A-Za-z0-9_]+)[^/|]*`)

// systemTablesDumpTasks: the jobs that dump this server (its logical backups)
var systemTablesDumpTasks = map[string]bool{"mysqldump": true, "mydumper": true}

// dumpSourceRe: the server a dump ran against, as a reseed failure names it
// ("mysqldump on db2.curepipe.svc.cloud18:3306: exit status 2 ...")
var dumpSourceRe = regexp.MustCompile(`\b(?:mysqldump|mariadb-dump|mydumper) on ([^\s:]+:[0-9]+)`)

// DumpSourceOf returns the server a job's result says it dumped, "" when it names none.
func DumpSourceOf(result string) string {
	if m := dumpSourceRe.FindStringSubmatch(result); m != nil {
		return m[1]
	}
	return ""
}

const (
	systemTablesUpgradeTask = "mariadbupgrade"
	jobStateFinished        = 3
	jobStateSuccess         = 4
)

func init() { Register(&SystemTablesPlugin{}) }

type SystemTablesPlugin struct{}

func (p *SystemTablesPlugin) Name() string { return "config-system-tables" }

// SystemTablesUpgradeTool: the tool that upgrades the system tables of the flavor
func SystemTablesUpgradeTool(mysqlOrPercona bool) string {
	if mysqlOrPercona {
		return "mysql_upgrade"
	}
	return "mariadb-upgrade"
}

// systemTablesEvent is one observation: lagging (with what was said) or proof of the
// contrary (a backup or an upgrade ended OK)
type systemTablesEvent struct {
	at      int64
	lagging bool
	detail  string
	source  string
}

// upgradesAtStart: MySQL and Percona 8.0.16 and later upgrade their system tables at
// start (the server action refuses them), PostgreSQL has none of these tables
func upgradesAtStart(v StdioServerVersion) bool {
	switch strings.ToLower(v.Flavor) {
	case "postgresql":
		return true
	case "mysql", "percona":
		return v.Major > 8 || (v.Major == 8 && (v.Minor > 0 || v.Release >= 16))
	}
	return false
}

func (p *SystemTablesPlugin) Evaluate(src LogSource) EvaluateResult {
	var res EvaluateResult
	if upgradesAtStart(src.ServerVersion) {
		return res
	}
	var last *systemTablesEvent
	// the most recent observation; on the same second, the proof that the tables are
	// fine wins (an upgrade ended OK closes the finding)
	keep := func(e systemTablesEvent) {
		if last == nil || e.at > last.at || (e.at == last.at && !e.lagging) {
			c := e
			last = &c
		}
	}

	for _, j := range src.Jobs {
		if j.Done != 1 || j.End == 0 {
			continue
		}
		ok := j.State == jobStateFinished || j.State == jobStateSuccess
		dumped := j.Source
		if dumped == "" {
			dumped = DumpSourceOf(j.Result)
		}
		// a job of this server that dumped another one (this server was the reseed
		// destination) says nothing about this server's tables
		if dumped != "" && dumped != src.ServerURL {
			continue
		}
		switch {
		case systemTablesDumpTasks[j.Task] || dumped != "":
			what := "the last " + j.Task + " backup"
			if j.Source != "" {
				what = "the last dump of this server by a " + j.Task + " reseed"
			}
			if m := systemTablesLagRe.FindString(j.Result); m != "" {
				keep(systemTablesEvent{at: j.End, lagging: true, detail: strings.TrimSpace(m), source: what})
			} else if ok {
				keep(systemTablesEvent{at: j.End})
			}
			// a backup failed for another reason proves nothing
		case j.Task == systemTablesUpgradeTask && ok:
			keep(systemTablesEvent{at: j.End})
		}
	}

	// the error log since the server's start: an older complaint predates a restart that
	// may have upgraded the tables
	var started int64
	if up, err := strconv.ParseInt(src.ServerStatus["uptime"], 10, 64); err == nil && up > 0 {
		started = time.Now().Unix() - up
	}
	loc := serverLocation(src.ServerVariables)
	for _, msg := range src.ErrorLog {
		m := systemTablesLagRe.FindString(msg.Text)
		if m == "" {
			continue
		}
		ts, err := parseErrorLogTimestamp(msg.Timestamp, loc)
		if err != nil || ts.Unix() < started {
			continue
		}
		keep(systemTablesEvent{at: ts.Unix(), lagging: true, detail: strings.TrimSpace(m), source: "the error log"})
	}

	if last == nil || !last.lagging {
		return res
	}
	mysql := strings.EqualFold(src.ServerVersion.Flavor, "MySQL") || strings.EqualFold(src.ServerVersion.Flavor, "Percona")
	flavor := src.ServerVersion.Flavor
	if flavor == "" {
		flavor = "MariaDB"
	}
	tool := SystemTablesUpgradeTool(mysql)
	res.Findings = append(res.Findings, Finding{
		ErrKey:      ErrKeySystemTablesUpgrade,
		Severity:    SeverityError,
		Domain:      DomainConfig,
		Description: fmt.Sprintf("%s: the %s system tables need an upgrade (%s), %s says: %s; run the server action upgrade-system-tables", src.ServerURL, flavor, tool, last.source, last.detail),
		Remediations: []Remediation{{
			Type:        "server_action",
			Description: fmt.Sprintf("Upgrade the system tables: the server's jobs container runs %s --upgrade-system-tables (no binary log)", tool),
			ConfigKey:   "upgrade-system-tables",
			Risk:        "moderate",
		}},
	})
	return res
}

// serverLocation: the zone the server writes its error log in, from its variables:
// time_zone when it is an offset ("+02:00"), else system_time_zone when Go knows it
// ("UTC", "Europe/Paris"); UTC otherwise. The error log's local times are compared with
// the server's start and with the jobs' end, which are absolute.
func serverLocation(vars map[string]string) *time.Location {
	if tz := strings.TrimSpace(vars["time_zone"]); len(tz) == 6 && (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' {
		h, errh := strconv.Atoi(tz[1:3])
		m, errm := strconv.Atoi(tz[4:6])
		if errh == nil && errm == nil {
			off := h*3600 + m*60
			if tz[0] == '-' {
				off = -off
			}
			return time.FixedZone(tz, off)
		}
	}
	if name := strings.TrimSpace(vars["system_time_zone"]); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

// parseErrorLogTimestamp: the error log watcher keeps what precedes the level, the
// date and time followed by the thread id on MariaDB ("2026-10-14 11:02:03 0"), in the
// server's zone; an ISO time with its own zone keeps it
func parseErrorLogTimestamp(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 19 {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", s[:19], loc); err == nil {
			return t, nil
		}
	}
	return parseLogTimestamp(s)
}
