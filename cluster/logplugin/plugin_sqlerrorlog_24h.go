// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin sqlerrorlog raises WARN0201 for SQL_ERROR_LOG plugin entries.
// Synthetic graphite metric: mysql.<hostname>.plugin_sqlerrorlog_count
//
// Config keys (under [plugin-config.sqlerrorlog]):
//
//	enabled         bool    default: true
//	timeframe-hours int     default: 24
//	spike-sigma     float   default: 2.0
package logplugin

import (
	"fmt"
	"strings"
	"time"
)

const ErrKeySQLError24h = "WARN0201"

func init() { Register(&SqlErrorLogPlugin{}) }

type SqlErrorLogPlugin struct{}

func (p *SqlErrorLogPlugin) Name() string { return "sqlerrorlog" }

func (p *SqlErrorLogPlugin) Evaluate(src LogSource) EvaluateResult {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	sigma := ConfigFloat(src.Config, "spike-sigma", 2.0)

	now := time.Now()
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	prevCutoff := now.Add(-time.Duration(2*hours) * time.Hour)

	current, previous := 0, 0
	for _, msg := range src.SqlErrorLog {
		if msg.Text == "" {
			continue
		}
		ts, err := parseLogTimestamp(msg.Timestamp)
		if err != nil || ts.After(cutoff) {
			current++
		} else if ts.After(prevCutoff) {
			previous++
		}
	}

	metricName := ""
	if src.HasGraphite() {
		metricName = fmt.Sprintf("mysql.%s.plugin_sqlerrorlog_count", src.GraphiteHostname)
	}

	res := EvaluateResult{
		CurrentCount:  current,
		PreviousCount: previous,
		MetricName:    metricName,
	}

	if current == 0 {
		return res
	}

	res.Findings = append(res.Findings, Finding{
		ErrKey:   ErrKeySQLError24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s: %d SQL error(s) in last %dh",
			src.ServerURL, current, hours),
	})

	if src.HasGraphite() && metricName != "" {
		correlPrefix := fmt.Sprintf("mysql.%s", src.GraphiteHostname)
		spike, err := DetectSpike(src.GraphiteAPIURL, metricName, sigma, correlPrefix)
		if err == nil && spike != nil {
			res.Findings = append(res.Findings, Finding{
				ErrKey:      "WARN0205",
				Severity:    SeverityWarning,
				Description: FormatSpikeDescription(src.ServerURL, metricName, spike),
			})
		}
	}

	return res
}
