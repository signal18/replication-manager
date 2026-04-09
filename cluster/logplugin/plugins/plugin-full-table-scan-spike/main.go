// plugin-full-table-scan-spike detects when the ratio of queries using full
// table scans spikes in the PFS digest statistics.
//
// WARN0304 — raised when full-scan ratio exceeds scan-ratio-threshold AND
// the number of full-scan queries exceeds min-full-scan-count.
//
// Config (environment variables):
//
//	REPMAN_SCAN_RATIO_THRESHOLD float  default: 0.30  — 30% of queries doing full scans
//	REPMAN_MIN_FULL_SCAN_COUNT  int    default: 10    — minimum count to trigger
//	REPMAN_MIN_EXEC_COUNT       int    default: 5     — ignore low-frequency digests
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	ratioThreshold := envFloat("REPMAN_SCAN_RATIO_THRESHOLD", 0.30)
	minFullScan := envInt("REPMAN_MIN_FULL_SCAN_COUNT", 10)
	minExec := envInt("REPMAN_MIN_EXEC_COUNT", 5)

	var totalExec, fullScanExec int64
	type offender struct {
		digest    string
		execCount int64
	}
	var offenders []offender

	for _, q := range req.PFSQueries {
		if q.ExecCount < int64(minExec) {
			continue
		}
		totalExec += q.ExecCount
		if strings.ToUpper(q.PlanFullScan) == "YES" {
			fullScanExec += q.ExecCount
			offenders = append(offenders, offender{digest: q.DigestText, execCount: q.ExecCount})
		}
	}

	if totalExec == 0 {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	ratio := float64(fullScanExec) / float64(totalExec)

	var findings []wire.Finding
	if ratio >= ratioThreshold && fullScanExec >= int64(minFullScan) {
		// Sort offenders by exec count desc
		sort.Slice(offenders, func(i, j int) bool {
			return offenders[i].execCount > offenders[j].execCount
		})
		var top []string
		for i, o := range offenders {
			if i >= 3 {
				break
			}
			top = append(top, fmt.Sprintf("%s (%d×)", truncate(o.digest, 60), o.execCount))
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0304",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: full-table-scan spike — %.0f%% of queries (%d/%d executions) use full scan. Top offenders: %s",
				req.ServerURL, ratio*100, fullScanExec, totalExec,
				strings.Join(top, " | ")),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
