// plugin-slow-query-regression detects when a previously fast query template
// suddenly appears in the slow log with significantly higher average latency.
//
// WARN0301 — raised when a query's average execution time in the current window
// is >= regression-factor (default 3×) higher than its historical average from
// the PFS digest statistics.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	timeframe-hours    int    default: 1   — current observation window in hours  (env: REPMAN_SLOW_QUERY_REGRESSION_TIMEFRAME_HOURS)
//	regression-factor  float  default: 3.0 — slowdown multiplier to flag          (env: REPMAN_SLOW_QUERY_REGRESSION_REGRESSION_FACTOR)
//	min-executions     int    default: 5   — minimum PFS exec_count for baseline  (env: REPMAN_SLOW_QUERY_REGRESSION_MIN_EXECUTIONS)
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

	hours := wire.CfgInt(req.Config, "timeframe-hours", wire.EnvInt("REPMAN_SLOW_QUERY_REGRESSION_TIMEFRAME_HOURS", 1))
	factor := wire.CfgFloat(req.Config, "regression-factor", wire.EnvFloat("REPMAN_SLOW_QUERY_REGRESSION_REGRESSION_FACTOR", 3.0))
	minExec := wire.CfgInt(req.Config, "min-executions", wire.EnvInt("REPMAN_SLOW_QUERY_REGRESSION_MIN_EXECUTIONS", 5))

	type pfsEntry struct {
		digestText    string
		execCount     int64
		execTimeAvgMs float64
	}
	pfsMap := make(map[string]pfsEntry)
	for _, q := range req.PFSQueries {
		if q.ExecCount < int64(minExec) {
			continue
		}
		pfsMap[q.Digest] = pfsEntry{
			digestText:    q.DigestText,
			execCount:     q.ExecCount,
			execTimeAvgMs: q.ExecTimeAvgMs,
		}
	}

	if len(pfsMap) == 0 {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	type slowEntry struct {
		count   int
		totalMs float64
	}
	slowMap := make(map[string]*slowEntry)

	for _, msg := range req.SlowLog {
		if msg.Query == "" {
			continue
		}
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}
		queryTime := msg.TimeMetrics["query_time"] * 1000
		digest := simpleFingerprint(msg.Query)
		if e, ok := slowMap[digest]; ok {
			e.count++
			e.totalMs += queryTime
		} else {
			slowMap[digest] = &slowEntry{count: 1, totalMs: queryTime}
		}
	}

	var findings []wire.Finding
	for digest, pfs := range pfsMap {
		slowDigest := simpleFingerprint(pfs.digestText)
		slow, ok := slowMap[slowDigest]
		if !ok || slow.count == 0 {
			continue
		}
		currentAvgMs := slow.totalMs / float64(slow.count)
		if pfs.execTimeAvgMs < 1 {
			continue
		}
		ratio := currentAvgMs / pfs.execTimeAvgMs
		if ratio >= factor {
			findings = append(findings, wire.Finding{
				ErrKey:   "WARN0301",
				Severity: "WORKLOAD",
				Description: fmt.Sprintf(
					"Server %s: query regression detected — current avg %.0fms vs PFS baseline %.0fms (%.1f× — %d occurrences in last %dh): %s",
					req.ServerURL, currentAvgMs, pfs.execTimeAvgMs, ratio,
					slow.count, hours,
					truncate(pfs.digestText, 120)),
			})
			if len(findings) >= 5 {
				break
			}
		}
		_ = digest
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func simpleFingerprint(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	var b strings.Builder
	inNum := false
	for _, r := range q {
		if r >= '0' && r <= '9' {
			if !inNum {
				b.WriteRune('?')
				inNum = true
			}
		} else {
			inNum = false
			b.WriteRune(r)
		}
	}
	return b.String()
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
	return time.Time{}, fmt.Errorf("unknown timestamp: %q", s)
}
