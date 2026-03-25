// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Plugin errorlog raises WARN0200 when the database error log ring buffer
// contains any ERROR-level line within the configured timeframe.
//
// Config key (under [plugin-config.errorlog]):
//
//	timeframe-hours  int  default: 24
package logplugin

import (
	"fmt"
	"strings"
	"time"
)

const ErrKeyDBError24h = "WARN0200"

func init() {
	Register(&ErrorLogPlugin{})
}

// ErrorLogPlugin raises a WARNING when any ERROR-level line has appeared in
// the database error log within the configured timeframe (default 24h).
type ErrorLogPlugin struct{}

func (p *ErrorLogPlugin) Name() string { return "errorlog" }

func (p *ErrorLogPlugin) Evaluate(src LogSource) []Finding {
	hours := ConfigInt(src.Config, "timeframe-hours", 24)
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	count := 0

	for _, msg := range src.ErrorLog {
		if msg.Text == "" {
			continue
		}
		if !isErrorLevel(msg.Level) {
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
		ErrKey:   ErrKeyDBError24h,
		Severity: SeverityWarning,
		Description: fmt.Sprintf(
			"Server %s has %d ERROR entry/entries in database error log in the last %dh",
			src.ServerURL, count, hours,
		),
	}}
}

func isErrorLevel(level string) bool {
	up := strings.ToUpper(strings.TrimSpace(level))
	return up == "ERROR" || up == "ERR"
}

// parseLogTimestamp tries common MariaDB/MySQL error-log timestamp formats.
func parseLogTimestamp(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006/01/02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown timestamp format: %q", s)
}
