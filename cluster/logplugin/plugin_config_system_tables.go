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

func (p *SystemTablesPlugin) Evaluate(src LogSource) EvaluateResult {
	var res EvaluateResult
	if strings.EqualFold(src.ServerVersion.Flavor, "PostgreSQL") {
		return res
	}
	var last *systemTablesEvent
	keep := func(e systemTablesEvent) {
		if last == nil || e.at >= last.at {
			c := e
			last = &c
		}
	}

	for _, j := range src.Jobs {
		if j.Done != 1 || j.End == 0 {
			continue
		}
		ok := j.State == jobStateFinished || j.State == jobStateSuccess
		switch {
		case systemTablesDumpTasks[j.Task]:
			if m := systemTablesLagRe.FindString(j.Result); m != "" {
				keep(systemTablesEvent{at: j.End, lagging: true, detail: strings.TrimSpace(m), source: "the last " + j.Task + " backup"})
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
	for _, msg := range src.ErrorLog {
		m := systemTablesLagRe.FindString(msg.Text)
		if m == "" {
			continue
		}
		ts, err := parseErrorLogTimestamp(msg.Timestamp)
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

// parseErrorLogTimestamp: the error log watcher keeps what precedes the level, the
// date and time followed by the thread id on MariaDB ("2026-10-14 11:02:03 0")
func parseErrorLogTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		if t, err := parseLogTimestamp(s[:19]); err == nil {
			return t, nil
		}
	}
	return parseLogTimestamp(s)
}
