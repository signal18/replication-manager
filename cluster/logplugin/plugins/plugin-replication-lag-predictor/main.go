// plugin-replication-lag-predictor detects write bursts in the slow log that
// are likely to cause replication lag before it actually shows up in
// seconds_behind_master.
//
// WARN0303 — raised when the DML write rate in the slow log exceeds
// write-rate-threshold queries/minute within the last window-mins.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	window-mins          int  default: 5   — observation window in minutes          (env: REPMAN_REPLICATION_LAG_PREDICTOR_WINDOW_MINS)
//	write-rate-threshold int  default: 50  — DML queries/min to flag as burst       (env: REPMAN_REPLICATION_LAG_PREDICTOR_WRITE_RATE_THRESHOLD)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

var dmlVerbs = []string{"insert", "update", "delete", "replace", "load data"}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	windowMins := wire.CfgInt(req.Config, "window-mins", wire.EnvInt("REPMAN_REPLICATION_LAG_PREDICTOR_WINDOW_MINS", 5))
	threshold := wire.CfgInt(req.Config, "write-rate-threshold", wire.EnvInt("REPMAN_REPLICATION_LAG_PREDICTOR_WRITE_RATE_THRESHOLD", 50))
	cutoff := time.Now().Add(-time.Duration(windowMins) * time.Minute)

	dmlCount := 0
	totalCount := 0

	for _, msg := range req.SlowLog {
		if msg.Query == "" {
			continue
		}
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}
		totalCount++
		q := strings.ToLower(strings.TrimSpace(msg.Query))
		for _, verb := range dmlVerbs {
			if strings.HasPrefix(q, verb) {
				dmlCount++
				break
			}
		}
	}

	if totalCount == 0 {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	dmlRate := float64(dmlCount) / float64(windowMins)

	var findings []wire.Finding
	if int(dmlRate) >= threshold {
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0303",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: write burst detected — %.0f DML queries/min (%d in %dmin), replication lag likely",
				req.ServerURL, dmlRate, dmlCount, windowMins),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func parseTS(s string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.000000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown ts: %q", s)
}
