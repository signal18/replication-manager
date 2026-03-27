// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin auditlog raises WARN0204 when new query templates appear that were
// not seen in the baseline window (drift detection).
// Spike detection uses mysql.<hostname>.mysql_global_status_queries (total
// workload) to correlate a template explosion with a load spike.
// Synthetic metric: mysql.<hostname>.plugin_auditlog_new_templates
//
// Config keys (under [plugin-config.auditlog]):
//
//	enabled               bool    default: true
//	current-window-hours  int     default: 1
//	baseline-window-hours int     default: 24
//	operations            string  default: "QUERY,QUERY_DML,QUERY_DDL"
//	max-new-templates     int     default: 5
//	spike-sigma           float   default: 2.0
package logplugin

import (
	"fmt"
	"strings"
	"time"

	"github.com/percona/go-mysql/query"
)

const ErrKeyAuditDrift = "WARN0204"

func init() { Register(&AuditLogDriftPlugin{}) }

type AuditLogDriftPlugin struct{}

func (p *AuditLogDriftPlugin) Name() string { return "auditlog" }

func (p *AuditLogDriftPlugin) Evaluate(src LogSource) EvaluateResult {
	currentHours := ConfigInt(src.Config, "current-window-hours", 1)
	baselineHours := ConfigInt(src.Config, "baseline-window-hours", 24)
	opsRaw := ConfigStr(src.Config, "operations", "QUERY,QUERY_DML,QUERY_DDL")
	maxList := ConfigInt(src.Config, "max-new-templates", 5)
	sigma := ConfigFloat(src.Config, "spike-sigma", 2.0)

	allowedOps := parseOpsSet(opsRaw)
	now := time.Now()
	currentCutoff := now.Add(-time.Duration(currentHours) * time.Hour)
	baselineCutoff := now.Add(-time.Duration(baselineHours) * time.Hour)

	currentTemplates := make(map[string]bool)
	baselineTemplates := make(map[string]bool)
	currentCount := 0

	for _, msg := range src.AuditLog {
		if msg.Text == "" {
			continue
		}
		ts, op, sql := parseAuditEntry(msg.Timestamp, msg.Text)
		if sql == "" || !allowedOps[op] {
			continue
		}
		tpl := query.Fingerprint(sql)
		if tpl == "" {
			continue
		}
		inCurrent := ts.IsZero() || ts.After(currentCutoff)
		inBaseline := ts.IsZero() || (ts.After(baselineCutoff) && !ts.After(currentCutoff))
		if inCurrent {
			currentTemplates[tpl] = true
			currentCount++
		}
		if inBaseline {
			baselineTemplates[tpl] = true
		}
	}

	syntheticMetric := ""
	if src.HasGraphite() {
		syntheticMetric = fmt.Sprintf("mysql.%s.plugin_auditlog_new_templates", src.GraphiteHostname)
	}

	res := EvaluateResult{
		CurrentCount: currentCount,
		MetricName:   syntheticMetric,
	}

	if len(currentTemplates) == 0 || len(baselineTemplates) == 0 {
		return res
	}

	var newTemplates []string
	for tpl := range currentTemplates {
		if !baselineTemplates[tpl] {
			newTemplates = append(newTemplates, tpl)
		}
	}

	// Set PreviousCount to number of new templates (used as the graphite metric value)
	res.PreviousCount = len(baselineTemplates)
	res.CurrentCount = len(newTemplates)

	if len(newTemplates) == 0 {
		return res
	}

	listed := newTemplates
	suffix := ""
	if len(listed) > maxList {
		listed = listed[:maxList]
		suffix = fmt.Sprintf(" (and %d more)", len(newTemplates)-maxList)
	}

	res.Findings = append(res.Findings, Finding{
		ErrKey:   ErrKeyAuditDrift,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s: %d new query template(s) in last %dh not seen in previous %dh: %s%s",
			src.ServerURL, len(newTemplates), currentHours, baselineHours,
			strings.Join(listed, " | "), suffix),
	})

	// Spike detection on new-template count via graphite
	if src.HasGraphite() && syntheticMetric != "" {
		correlPrefix := fmt.Sprintf("mysql.%s", src.GraphiteHostname)
		spike, err := DetectSpike(src.GraphiteAPIURL, syntheticMetric, sigma, correlPrefix, src.SpikeCache)
		if err == nil && spike != nil {
			res.Findings = append(res.Findings, Finding{
				ErrKey:      "WARN0205",
				Severity:    SeverityWarning,
				Description: FormatSpikeDescription(src.ServerURL, syntheticMetric, spike),
			})
		}
	}

	return res
}

func parseAuditEntry(rawTimestamp, text string) (ts time.Time, operation, sql string) {
	parts := strings.SplitN(text, ", ", 8)
	if len(parts) < 8 {
		return
	}
	operation = strings.TrimSpace(parts[5])
	sqlAndRetcode := parts[7]
	if idx := strings.LastIndex(sqlAndRetcode, ","); idx != -1 {
		sql = strings.TrimSpace(sqlAndRetcode[:idx])
	} else {
		sql = strings.TrimSpace(sqlAndRetcode)
	}
	sql = strings.Trim(sql, "'")
	ts, _ = parseLogTimestamp(rawTimestamp)
	return
}

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
