// plugin-error-storm detects when the same error message template appears
// repeatedly in a short window — indicating an application-level storm rather
// than a single isolated failure.
//
// WARN0302 — raised when any error template appears >= storm-threshold times
// within storm-window-minutes.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	storm-threshold    int  default: 10  — occurrences to trigger          (env: REPMAN_ERROR_STORM_THRESHOLD)
//	storm-window-mins  int  default: 5   — rolling window in minutes       (env: REPMAN_ERROR_STORM_WINDOW_MINS)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	threshold := wire.CfgInt(req.Config, "storm-threshold", wire.EnvInt("REPMAN_ERROR_STORM_THRESHOLD", 10))
	windowMins := wire.CfgInt(req.Config, "storm-window-mins", wire.EnvInt("REPMAN_ERROR_STORM_WINDOW_MINS", 5))
	cutoff := time.Now().Add(-time.Duration(windowMins) * time.Minute)

	counts := make(map[string]int)
	samples := make(map[string]string)

	for _, msg := range req.ErrorLog {
		if msg.Text == "" {
			continue
		}
		if !isError(msg.Level) {
			continue
		}
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}
		tpl := errorTemplate(msg.Text)
		counts[tpl]++
		if _, seen := samples[tpl]; !seen {
			samples[tpl] = msg.Text
		}
	}

	for _, msg := range req.SqlErrorLog {
		if msg.Text == "" {
			continue
		}
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}
		tpl := errorTemplate(msg.Text)
		counts[tpl]++
		if _, seen := samples[tpl]; !seen {
			samples[tpl] = msg.Text
		}
	}

	var findings []wire.Finding
	for tpl, count := range counts {
		if count >= threshold {
			findings = append(findings, wire.Finding{
				ErrKey:   "WARN0302",
				Severity: "WORKLOAD",
				Description: fmt.Sprintf(
					"Server %s: error storm — %d occurrences in last %dmin: %s",
					req.ServerURL, count, windowMins,
					truncate(samples[tpl], 150)),
			})
		}
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func errorTemplate(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			b.WriteByte('N')
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		case c == '\'' || c == '"':
			b.WriteByte('S')
			quote := c
			i++
			for i < len(s) && s[i] != quote {
				i++
			}
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isError(level string) bool {
	up := strings.ToUpper(strings.TrimSpace(level))
	return up == "ERROR" || up == "ERR"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseTS(s string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.000000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown ts: %q", s)
}
