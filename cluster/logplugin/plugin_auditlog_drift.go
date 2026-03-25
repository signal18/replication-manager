// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin auditlog raises WARN0204 when query templates (fingerprints) seen in
// the current window are not present in the baseline window — indicating new
// or unusual query patterns that the server has not seen recently.
//
// It uses the percona/go-mysql query.Fingerprint function (already used by the
// slow-log subsystem) to normalise SQL into templates, e.g.:
//
//	SELECT * FROM users WHERE id = 42  →  SELECT * FROM users WHERE id = ?
//
// Config keys (under [plugin-config.auditlog]):
//
//	current-window-hours   int     default: 1   — "how recent is new"
//	baseline-window-hours  int     default: 24  — "what is normal"
//	operations             string  default: "QUERY,QUERY_DML,QUERY_DDL"
//	max-new-templates      int     default: 5   — max templates listed in description
//
// MariaDB SERVER_AUDIT CSV format stored in AuditLog ring buffer:
// After AuditLogWatcher parsing, each HttpMessage has:
//   .Timestamp = raw timestamp string (parts[0] of CSV)
//   .Text      = ", "-joined parts[1:8] i.e.:
//                serverhost, username, host, connid, queryid, OPERATION, database, SQL_OR_OBJECT
//
// Field index after SplitN(.Text, ", ", 8):
//   [0] serverhost
//   [1] username
//   [2] host/ip
//   [3] connectionid
//   [4] queryid
//   [5] operation   ← QUERY / QUERY_DML / QUERY_DDL / CONNECT / TABLE …
//   [6] database
//   [7] sql_text_and_retcode  ← "SELECT 1, 0"  (retcode appended after last comma)
package logplugin

import (
	"fmt"
	"strings"
	"time"

	"github.com/percona/go-mysql/query"
)

const ErrKeyAuditDrift = "WARN0204"

func init() {
	Register(&AuditLogDriftPlugin{})
}

// AuditLogDriftPlugin raises WARN0204 when new query templates appear in the
// current window that were not seen during the baseline window.
type AuditLogDriftPlugin struct{}

func (p *AuditLogDriftPlugin) Name() string { return "auditlog" }

func (p *AuditLogDriftPlugin) Evaluate(src LogSource) []Finding {
	currentHours := ConfigInt(src.Config, "current-window-hours", 1)
	baselineHours := ConfigInt(src.Config, "baseline-window-hours", 24)
	opsRaw := ConfigStr(src.Config, "operations", "QUERY,QUERY_DML,QUERY_DDL")
	maxList := ConfigInt(src.Config, "max-new-templates", 5)

	allowedOps := parseOpsSet(opsRaw)

	now := time.Now()
	currentCutoff := now.Add(-time.Duration(currentHours) * time.Hour)
	baselineCutoff := now.Add(-time.Duration(baselineHours) * time.Hour)

	currentTemplates := make(map[string]bool)
	baselineTemplates := make(map[string]bool)

	for _, msg := range src.AuditLog {
		if msg.Text == "" {
			continue
		}

		ts, op, sql := parseAuditEntry(msg.Timestamp, msg.Text)
		if sql == "" {
			continue
		}
		if !allowedOps[op] {
			continue
		}

		tpl := query.Fingerprint(sql)
		if tpl == "" {
			continue
		}

		// Classify into windows.
		// An entry in the current window may also count toward baseline.
		inCurrent := ts.IsZero() || ts.After(currentCutoff)
		inBaseline := ts.IsZero() || (ts.After(baselineCutoff) && ts.Before(currentCutoff))

		if inCurrent {
			currentTemplates[tpl] = true
		}
		if inBaseline {
			baselineTemplates[tpl] = true
		}
	}

	if len(currentTemplates) == 0 || len(baselineTemplates) == 0 {
		// Not enough data to compare — do not raise a false positive.
		return nil
	}

	// New templates = in current window but NOT in baseline window.
	var newTemplates []string
	for tpl := range currentTemplates {
		if !baselineTemplates[tpl] {
			newTemplates = append(newTemplates, tpl)
		}
	}

	if len(newTemplates) == 0 {
		return nil
	}

	// Build a human-readable list, capped at maxList.
	listed := newTemplates
	suffix := ""
	if len(listed) > maxList {
		listed = listed[:maxList]
		suffix = fmt.Sprintf(" (and %d more)", len(newTemplates)-maxList)
	}

	return []Finding{{
		ErrKey:   ErrKeyAuditDrift,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s: %d new query template(s) in last %dh not seen in previous %dh baseline: %s%s",
			src.ServerURL,
			len(newTemplates),
			currentHours,
			baselineHours,
			strings.Join(listed, " | "),
			suffix,
		),
	}}
}

// parseAuditEntry extracts (timestamp, operation, sql) from a single AuditLog
// HttpMessage produced by AuditLogWatcher.
//
// .Text = "serverhost, username, host, connid, queryid, OPERATION, database, SQL,retcode"
// (joined with ", " from parts[1:] of the original 9-part CSV split)
func parseAuditEntry(rawTimestamp, text string) (ts time.Time, operation, sql string) {
	parts := strings.SplitN(text, ", ", 8)
	if len(parts) < 8 {
		return
	}

	operation = strings.TrimSpace(parts[5])

	// parts[7] = "SQL_TEXT,retcode" — strip the trailing ",<retcode>"
	sqlAndRetcode := parts[7]
	if idx := strings.LastIndex(sqlAndRetcode, ","); idx != -1 {
		sql = strings.TrimSpace(sqlAndRetcode[:idx])
	} else {
		sql = strings.TrimSpace(sqlAndRetcode)
	}

	// Strip surrounding single-quotes that MariaDB audit plugin adds.
	sql = strings.Trim(sql, "'")

	// Parse timestamp — ignore error, zero time = conservatively included.
	ts, _ = parseLogTimestamp(rawTimestamp)
	return
}

// parseOpsSet builds a set of uppercase operation names from a comma-separated string.
func parseOpsSet(raw string) map[string]bool {
	set := make(map[string]bool)
	for _, op := range strings.Split(raw, ",") {
		op = strings.TrimSpace(strings.ToUpper(op))
		if op != "" {
			set[op] = true
		}
	}
	return set
}
