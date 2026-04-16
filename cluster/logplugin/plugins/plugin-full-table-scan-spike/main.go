// plugin-full-table-scan-spike detects when the ratio of queries using full
// table scans spikes in the PFS digest statistics.
//
// WARN0304 — raised when full-scan ratio exceeds scan-ratio-threshold AND
// the number of full-scan queries exceeds min-full-scan-count.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	scan-ratio-threshold  float  default: 0.30  — fraction of queries doing full scans  (env: REPMAN_FULL_TABLE_SCAN_SPIKE_SCAN_RATIO_THRESHOLD)
//	min-full-scan-count   int    default: 10    — minimum full-scan executions to fire   (env: REPMAN_FULL_TABLE_SCAN_SPIKE_MIN_FULL_SCAN_COUNT)
//	min-exec-count        int    default: 5     — ignore low-frequency digests           (env: REPMAN_FULL_TABLE_SCAN_SPIKE_MIN_EXEC_COUNT)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	ratioThreshold := wire.CfgFloat(req.Config, "scan-ratio-threshold", wire.EnvFloat("REPMAN_FULL_TABLE_SCAN_SPIKE_SCAN_RATIO_THRESHOLD", 0.30))
	minFullScan := wire.CfgInt(req.Config, "min-full-scan-count", wire.EnvInt("REPMAN_FULL_TABLE_SCAN_SPIKE_MIN_FULL_SCAN_COUNT", 10))
	minExec := wire.CfgInt(req.Config, "min-exec-count", wire.EnvInt("REPMAN_FULL_TABLE_SCAN_SPIKE_MIN_EXEC_COUNT", 5))

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
