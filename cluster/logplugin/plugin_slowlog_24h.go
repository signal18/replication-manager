// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin slowlog raises WARN0202 for slow query log entries.
// Native graphite metric: mysql.<hostname>.mysql_global_status_slow_queries
// (already sent by srv_snd.go). Falls back to synthetic
// mysql.<hostname>.plugin_slowlog_count if the native metric is missing.
//
// Config keys (under [plugin-config.slowlog]):
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

const ErrKeySlowLog24h = "WARN0202"

func init() { Register(&SlowLogPlugin{}) }

type SlowLogPlugin struct{}

func (p *SlowLogPlugin) Name() string { return "slowlog" }

func (p *SlowLogPlugin) Evaluate(src LogSource) EvaluateResult {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	sigma := ConfigFloat(src.Config, "spike-sigma", 2.0)

	now := time.Now()
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	prevCutoff := now.Add(-time.Duration(2*hours) * time.Hour)

	current, previous := 0, 0
	for _, msg := range src.SlowLog {
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

	// Prefer the native MySQL status metric for spike detection.
	// Fall back to a synthetic metric if the native one has no history.
	nativeMetric := ""
	syntheticMetric := ""
	if src.HasGraphite() {
		nativeMetric = fmt.Sprintf("mysql.%s.mysql_global_status_slow_queries", src.GraphiteHostname)
		syntheticMetric = fmt.Sprintf("mysql.%s.plugin_slowlog_count", src.GraphiteHostname)
	}

	res := EvaluateResult{
		CurrentCount:  current,
		PreviousCount: previous,
		MetricName:    syntheticMetric, // backend will send this synthetic metric
	}

	if current == 0 {
		return res
	}

	res.Findings = append(res.Findings, Finding{
		ErrKey:   ErrKeySlowLog24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s: %d slow query/queries in last %dh",
			src.ServerURL, current, hours),
	})

	if src.HasGraphite() {
		correlPrefix := fmt.Sprintf("mysql.%s", src.GraphiteHostname)

		// Try native metric first; fall back to synthetic
		for _, metricName := range []string{nativeMetric, syntheticMetric} {
			if metricName == "" {
				continue
			}
			spike, err := DetectSpike(src.GraphiteAPIURL, metricName, sigma, correlPrefix)
			if err != nil || spike == nil {
				continue
			}
			res.Findings = append(res.Findings, Finding{
				ErrKey:      "WARN0205",
				Severity:    SeverityWarning,
				Description: FormatSpikeDescription(src.ServerURL, metricName, spike),
			})
			break // one spike finding per plugin per tick
		}
	}

	return res
}
