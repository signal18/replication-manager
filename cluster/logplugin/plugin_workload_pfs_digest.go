// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_workload_pfs produces workload findings from performance_schema
// query digest statistics. Unlike global STATUS counters, digests give
// per-query context: which SQL pattern causes full scans, disk temp tables,
// or bad scan ratios — and how often it executes.
//
// The PFS covering ratio (WTAG0200) compares sum(exec_count) from PFS
// digests with the total queries counted by Graphite over the same window
// (since last PFS truncate). This tells how representative the digest
// data is — if PFS only covers 40% of queries, all digest-based findings
// are relative to that 40%.
//
// Error key range: WTAG0200–WTAG0299 (PFS digest findings)
package logplugin

import (
	"fmt"
	"strconv"
	"time"
)

func init() {
	Register(&WorkloadPFSDigestPlugin{})
}

type WorkloadPFSDigestPlugin struct{}

func (p *WorkloadPFSDigestPlugin) Name() string              { return "workload-pfs-digest" }
func (p *WorkloadPFSDigestPlugin) DefaultSeverity() Severity { return SeverityWorkload }

func (p *WorkloadPFSDigestPlugin) Prerequisites() []Prerequisite {
	return []Prerequisite{{
		ConfigKey:   "monitoring-performance-schema-queries",
		Description: "PFS query digest monitoring must be enabled",
	}}
}

func (p *WorkloadPFSDigestPlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() || len(src.PFSQueries) == 0 {
		return EvaluateResult{}
	}

	var findings []Finding

	findings = appendCoveringRatio(findings, src)
	findings = appendExplainCoverage(findings, src)
	findings = appendExplainFullScan(findings, src)

	return EvaluateResult{
		Findings:     findings,
		CurrentCount: len(findings),
	}
}

// appendCoveringRatio computes what percentage of total queries the PFS
// digest table covers. Uses Graphite to get the queries delta since the
// last PFS truncate, and compares with sum(exec_count) from digests.
func appendCoveringRatio(findings []Finding, src LogSource) []Finding {
	if src.PFSLastTruncate.IsZero() {
		return findings
	}

	pfsTotal := int64(0)
	for _, q := range src.PFSQueries {
		pfsTotal += q.ExecCount
	}
	if pfsTotal == 0 {
		return findings
	}

	graphiteQueries := fetchQueriesSinceTruncate(src)
	if graphiteQueries <= 0 {
		return findings
	}

	pct := float64(pfsTotal) / float64(graphiteQueries) * 100
	if pct > 100 {
		pct = 100
	}

	desc := fmt.Sprintf("PFS digest coverage %.0f%% (%d/%d queries since last truncate)",
		pct, pfsTotal, graphiteQueries)

	findings = append(findings, Finding{
		ErrKey:      "WTAG0200",
		Severity:    SeverityWorkload,
		Description: desc,
		Count:       pfsTotal,
		Total:       graphiteQueries,
	})

	return findings
}

// fetchQueriesSinceTruncate queries Graphite for the total queries delta
// between PFSLastTruncate and now using the integral() function on the
// derivative of the queries counter.
func fetchQueriesSinceTruncate(src LogSource) int64 {
	if src.GraphiteAPIURL == "" || src.GraphiteHostname == "" {
		return 0
	}

	elapsed := time.Since(src.PFSLastTruncate)
	if elapsed < time.Minute {
		return 0
	}

	// Graphite relative time: round up to minutes
	fromMinutes := int(elapsed.Minutes()) + 1
	from := fmt.Sprintf("-%dmin", fromMinutes)

	target := fmt.Sprintf("integral(nonNegativeDerivative(mysql.%s.mysql_global_status_queries))",
		src.GraphiteHostname)

	points, err := FetchGraphiteMetric(src.GraphiteAPIURL, target, from, "now")
	if err != nil || len(points) == 0 {
		return 0
	}

	// The last non-null point of integral() gives the total sum
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Value != nil {
			return int64(*points[i].Value)
		}
	}

	return 0
}

// appendExplainCoverage reports what percentage of PFS digests have
// a cached EXPLAIN plan. Digests without explains are blind spots —
// you can see exec_count and rows_scanned but not the query plan.
func appendExplainCoverage(findings []Finding, src LogSource) []Finding {
	totalDigests := len(src.PFSQueries)
	if totalDigests == 0 {
		return findings
	}

	explained := src.PFSExplainCount
	pct := float64(explained) / float64(totalDigests) * 100

	findings = append(findings, Finding{
		ErrKey:      "WTAG0201",
		Severity:    SeverityWorkload,
		Description: fmt.Sprintf("PFS digest explain coverage %.0f%% (%d/%d digests have EXPLAIN)", pct, explained, totalDigests),
		Count:       int64(explained),
		Total:       int64(totalDigests),
	})

	return findings
}

// appendExplainFullScan computes the cost of full table scans as a
// fraction of total row operations. For each EXPLAIN plan step,
// rows × exec_count gives the estimated row work. Steps with type=ALL
// are full scans. The ratio shows how much of the total row work is
// attributable to full scans.
func appendExplainFullScan(findings []Finding, src LogSource) []Finding {
	if len(src.PFSExplainPlans) == 0 {
		return findings
	}

	var fullScanRows, totalRows int64
	fullScanDigests := 0

	for _, e := range src.PFSExplainPlans {
		hasFullScan := false
		for _, r := range e.Plan {
			rows := parseRows(r.Rows)
			rowWork := rows * e.ExecCount
			totalRows += rowWork
			if r.Type == "ALL" {
				fullScanRows += rowWork
				hasFullScan = true
			}
		}
		if hasFullScan {
			fullScanDigests++
		}
	}

	if fullScanDigests > 0 && totalRows > 0 {
		findings = append(findings, Finding{
			ErrKey:   "WTAG0210",
			Severity: SeverityWorkload,
			Description: fmt.Sprintf("Full scan row cost %d/%d digests (type=ALL in EXPLAIN)",
				fullScanDigests, len(src.PFSExplainPlans)),
			Count: fullScanRows,
			Total: totalRows,
		})
	}

	return findings
}

func parseRows(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
