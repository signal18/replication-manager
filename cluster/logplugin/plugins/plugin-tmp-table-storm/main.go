// plugin-tmp-table-storm detects when PFS digest statistics show a spike in
// queries creating on-disk temporary tables (indicating bad JOINs, missing
// indexes on GROUP BY/ORDER BY, or tmp_table_size too small).
//
// WARN0306 — raised when on-disk tmp table count exceeds disk-tmp-threshold
// or when the disk-to-memory tmp table ratio exceeds ratio-threshold.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	disk-tmp-threshold  int    default: 20    — absolute on-disk tmp table count to trigger  (env: REPMAN_TMP_TABLE_STORM_DISK_TMP_THRESHOLD)
//	ratio-threshold     float  default: 0.20  — disk/total tmp ratio to trigger              (env: REPMAN_TMP_TABLE_STORM_RATIO_THRESHOLD)
//	min-exec-count      int    default: 3     — ignore low-frequency digests                 (env: REPMAN_TMP_TABLE_STORM_MIN_EXEC_COUNT)
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

	diskThreshold := wire.CfgInt(req.Config, "disk-tmp-threshold", wire.EnvInt("REPMAN_TMP_TABLE_STORM_DISK_TMP_THRESHOLD", 20))
	ratioThreshold := wire.CfgFloat(req.Config, "ratio-threshold", wire.EnvFloat("REPMAN_TMP_TABLE_STORM_RATIO_THRESHOLD", 0.20))
	minExec := wire.CfgInt(req.Config, "min-exec-count", wire.EnvInt("REPMAN_TMP_TABLE_STORM_MIN_EXEC_COUNT", 3))

	var totalDisk, totalMem int64
	type offender struct {
		digest    string
		diskTmp   int64
		memTmp    int64
		execCount int64
	}
	var offenders []offender

	for _, q := range req.PFSQueries {
		if q.ExecCount < int64(minExec) {
			continue
		}
		totalDisk += q.PlanTmpDisk
		totalMem += q.PlanTmpMem
		if q.PlanTmpDisk > 0 {
			offenders = append(offenders, offender{
				digest:    q.DigestText,
				diskTmp:   q.PlanTmpDisk,
				memTmp:    q.PlanTmpMem,
				execCount: q.ExecCount,
			})
		}
	}

	var findings []wire.Finding

	totalTmp := totalDisk + totalMem
	ratio := 0.0
	if totalTmp > 0 {
		ratio = float64(totalDisk) / float64(totalTmp)
	}

	if totalDisk >= int64(diskThreshold) || (totalTmp > 0 && ratio >= ratioThreshold) {
		sort.Slice(offenders, func(i, j int) bool {
			return offenders[i].diskTmp > offenders[j].diskTmp
		})
		var top []string
		for i, o := range offenders {
			if i >= 3 {
				break
			}
			top = append(top, fmt.Sprintf("%s (disk:%d mem:%d)",
				truncate(o.digest, 60), o.diskTmp, o.memTmp))
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0306",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: on-disk tmp table storm — %d disk / %d mem (%.0f%% on disk). Top queries: %s",
				req.ServerURL, totalDisk, totalMem, ratio*100,
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
