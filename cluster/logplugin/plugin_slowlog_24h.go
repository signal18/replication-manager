// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin slowlog raises WARN0202 when the slow-query log ring buffer contains
// entries within the configured timeframe.
//
// Config key (under [plugin-config.slowlog]):
//
//	timeframe-hours  int  default: 24
package logplugin

import (
	"fmt"
	"time"
)

const ErrKeySlowLog24h = "WARN0202"

func init() {
	Register(&SlowLogPlugin{})
}

// SlowLogPlugin raises a WARNING when slow queries appear in the slow-query log
// ring buffer within the configured timeframe (default 24h).
type SlowLogPlugin struct{}

func (p *SlowLogPlugin) Name() string { return "slowlog" }

func (p *SlowLogPlugin) Evaluate(src LogSource) []Finding {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	count := 0

	for _, msg := range src.SlowLog {
		if msg.Text == "" {
			continue
		}
		if msg.Timestamp != "" {
			t, err := parseLogTimestamp(msg.Timestamp)
			if err == nil && t.Before(cutoff) {
				continue
			}
		}
		count++
	}

	if count == 0 {
		return nil
	}

	return []Finding{{
		ErrKey:   ErrKeySlowLog24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s has %d slow query/queries in slow query log in the last %dh",
			src.ServerURL, count, hours,
		),
	}}
}
