// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin sqlerrorlog raises WARN0201 when the SQL error log ring buffer
// (MariaDB SQL_ERROR_LOG plugin) contains entries within the configured timeframe.
//
// Config key (under [plugin-config.sqlerrorlog]):
//
//	timeframe-hours  int  default: 24
package logplugin

import (
	"fmt"
	"time"
)

const ErrKeySQLError24h = "WARN0201"

func init() {
	Register(&SqlErrorLogPlugin{})
}

// SqlErrorLogPlugin raises a WARNING when any entry appears in the SQL error log
// within the configured timeframe (default 24h).
type SqlErrorLogPlugin struct{}

func (p *SqlErrorLogPlugin) Name() string { return "sqlerrorlog" }

func (p *SqlErrorLogPlugin) Evaluate(src LogSource) []Finding {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	count := 0

	for _, msg := range src.SqlErrorLog {
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
		ErrKey:   ErrKeySQLError24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s has %d SQL error(s) in SQL error log in the last %dh",
			src.ServerURL, count, hours,
		),
	}}
}
