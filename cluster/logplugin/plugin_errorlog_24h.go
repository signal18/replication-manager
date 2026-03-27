// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin errorlog raises WARN0200 for lines in the database error log whose
// severity is at or above min-log-level.
//
// MySQL/MariaDB error log severity levels (lowest to highest):
//
//	System  — startup/shutdown, always present
//	Note    — informational
//	Warning — potentially problematic
//	ERROR   — operation failed
//
// Config keys (under [plugin-config.errorlog]):
//
//	enabled         bool    default: true
//	timeframe-hours int     default: 24
//	min-log-level   string  default: "Warning"  (Warning + ERROR counted)
//	spike-sigma     float   default: 2.0
package logplugin

import (
	"fmt"
	"strings"
	"time"
)

const ErrKeyDBError24h = "WARN0200"

func init() { Register(&ErrorLogPlugin{}) }

type ErrorLogPlugin struct{}

func (p *ErrorLogPlugin) Name() string { return "errorlog" }

func (p *ErrorLogPlugin) Evaluate(src LogSource) EvaluateResult {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	sigma := ConfigFloat(src.Config, "spike-sigma", 2.0)
	minWeight := MinLogLevelWeight(src.Config) // default: Warning (3)

	now := time.Now()
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	prevCutoff := now.Add(-time.Duration(2*hours) * time.Hour)

	current, previous := 0, 0
	for _, msg := range src.ErrorLog {
		if msg.Text == "" {
			continue
		}
		// Filter by minimum log level
		if LogLevelWeight(msg.Level) < minWeight {
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
		metricName = fmt.Sprintf("mysql.%s.plugin_errorlog_count", src.GraphiteHostname)
	}

	res := EvaluateResult{
		CurrentCount:  current,
		PreviousCount: previous,
		MetricName:    metricName,
	}

	if current == 0 {
		return res
	}

	minLevel := ConfigStr(src.Config, "min-log-level", "Warning")
	res.Findings = append(res.Findings, Finding{
		ErrKey:   ErrKeyDBError24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s: %d %s+ entry/entries in error log in last %dh",
			src.ServerURL, current, minLevel, hours),
	})

	// Dynamic spike detection via graphite history
	if src.HasGraphite() && metricName != "" {
		correlPrefix := fmt.Sprintf("mysql.%s", src.GraphiteHostname)
		spike, err := DetectSpike(src.GraphiteAPIURL, metricName, sigma, correlPrefix, src.SpikeCache)
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

func isErrorLevel(level string) bool {
	up := strings.ToUpper(strings.TrimSpace(level))
	return up == "ERROR" || up == "ERR"
}

func parseLogTimestamp(s string) (time.Time, error) {
	for _, f := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006/01/02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown timestamp: %q", s)
}
