// plugin-tmp-table-storm detects when PFS digest statistics show a spike in
// queries creating on-disk temporary tables (indicating bad JOINs, missing
// indexes on GROUP BY/ORDER BY, or tmp_table_size too small).
//
// WARN0306 — raised when on-disk tmp table count exceeds disk-tmp-threshold
// or when the disk-to-memory tmp table ratio exceeds ratio-threshold.
//
// Config (environment variables):
//
//	REPMAN_DISK_TMP_THRESHOLD int    default: 20
//	REPMAN_RATIO_THRESHOLD    float  default: 0.20  — 20% of tmp tables go to disk
//	REPMAN_MIN_EXEC_COUNT     int    default: 3
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

	diskThreshold := envInt("REPMAN_DISK_TMP_THRESHOLD", 20)
	ratioThreshold := envFloat("REPMAN_RATIO_THRESHOLD", 0.20)
	minExec := envInt("REPMAN_MIN_EXEC_COUNT", 3)

	var totalDisk, totalMem int64
	type offender struct {
		digest   string
		diskTmp  int64
		memTmp   int64
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
			Severity: "WARNING",
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
