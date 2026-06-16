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
	"strings"
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

	pfsTotal := int64(0)
	for _, q := range src.PFSQueries {
		pfsTotal += q.ExecCount
	}
	graphiteQueries := fetchQueriesSinceTruncate(src)
	graphiteHandlerRows := fetchHandlerRowsSinceTruncate(src)

	digestCoverage := computeDigestCoverage(pfsTotal, graphiteQueries)
	explainCoverage := computeExplainCoverage(src)

	findings = appendCoveringRatio(findings, pfsTotal, graphiteQueries, digestCoverage)
	findings = appendExplainCoverage(findings, src, explainCoverage)
	findings = appendExplainAccessType(findings, src, graphiteHandlerRows)
	findings = appendExplainExtra(findings, src, graphiteHandlerRows)
	findings = appendDigestTmpDisk(findings, src, digestCoverage)

	return EvaluateResult{
		Findings:     findings,
		CurrentCount: len(findings),
	}
}

func computeDigestCoverage(pfsTotal, graphiteQueries int64) float64 {
	if pfsTotal <= 0 || graphiteQueries <= 0 {
		return 0
	}
	r := float64(pfsTotal) / float64(graphiteQueries)
	if r > 1 {
		return 1
	}
	return r
}

// computeExplainCoverage returns the fraction (0.0–1.0) of PFS digests
// that have a cached EXPLAIN plan.
func computeExplainCoverage(src LogSource) float64 {
	total := len(src.PFSQueries)
	if total == 0 {
		return 0
	}
	return float64(src.PFSExplainCount) / float64(total)
}

func appendCoveringRatio(findings []Finding, pfsTotal, graphiteQueries int64, digestCoverage float64) []Finding {
	if digestCoverage <= 0 {
		return findings
	}

	findings = append(findings, Finding{
		ErrKey:      "WTAG0200",
		Severity:    SeverityWorkload,
		Description: fmt.Sprintf("PFS digest coverage %.0f%% (%d/%d queries since last truncate)",
			digestCoverage*100, pfsTotal, graphiteQueries),
	})

	return findings
}

// fetchQueriesSinceTruncate queries Graphite for the total queries delta
// between PFSLastTruncate and now using the integral() function on the
// derivative of the queries counter.
func fetchQueriesSinceTruncate(src LogSource) int64 {
	if src.GraphiteAPIURL == "" || src.GraphiteHostname == "" || src.PFSLastTruncate.IsZero() {
		return 0
	}

	elapsed := time.Since(src.PFSLastTruncate)
	if elapsed < time.Minute {
		return 0
	}

	// Graphite relative time: round up to minutes
	fromMinutes := int(elapsed.Minutes()) + 1
	from := fmt.Sprintf("-%dmin", fromMinutes)

	target := fmt.Sprintf("integral(keepLastValue(nonNegativeDerivative(mysql.%s.mysql_global_status_queries)))",
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

// fetchHandlerRowsSinceTruncate queries Graphite for the total handler row
// operations (all handler_read_* counters) since the last PFS truncate.
func fetchHandlerRowsSinceTruncate(src LogSource) int64 {
	if src.GraphiteAPIURL == "" || src.GraphiteHostname == "" || src.PFSLastTruncate.IsZero() {
		return 0
	}

	elapsed := time.Since(src.PFSLastTruncate)
	if elapsed < time.Minute {
		return 0
	}

	fromMinutes := int(elapsed.Minutes()) + 1
	from := fmt.Sprintf("-%dmin", fromMinutes)

	target := fmt.Sprintf("integral(sumSeries(keepLastValue(nonNegativeDerivative(mysql.%s.mysql_global_status_handler_read_*))))",
		src.GraphiteHostname)

	points, err := FetchGraphiteMetric(src.GraphiteAPIURL, target, from, "now")
	if err != nil || len(points) == 0 {
		return 0
	}

	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Value != nil {
			return int64(*points[i].Value)
		}
	}

	return 0
}

func appendExplainCoverage(findings []Finding, src LogSource, explainCoverage float64) []Finding {
	totalDigests := len(src.PFSQueries)
	if totalDigests == 0 {
		return findings
	}

	explained := src.PFSExplainCount

	findings = append(findings, Finding{
		ErrKey:      "WTAG0201",
		Severity:    SeverityWorkload,
		Description: fmt.Sprintf("PFS digest explain coverage %.0f%% (%d/%d digests have EXPLAIN)",
			explainCoverage*100, explained, totalDigests),
	})

	return findings
}

// appendExplainAccessType computes the row-operation cost per EXPLAIN
// access type. For each plan step, rows × exec_count gives the estimated
// row work. Tags are emitted for full scan (ALL), range, and ref access.
func appendExplainAccessType(findings []Finding, src LogSource, graphiteHandlerRows int64) []Finding {
	if len(src.PFSExplainPlans) == 0 {
		return findings
	}

	type accessStats struct {
		rows    int64
		digests int
	}
	stats := make(map[string]*accessStats)
	var totalExplainRows int64

	for _, e := range src.PFSExplainPlans {
		seen := make(map[string]bool)
		for _, r := range e.Plan {
			rows := parseRows(r.Rows)
			rowWork := rows * e.ExecCount
			totalExplainRows += rowWork

			bucket := classifyAccess(r.Type)
			if bucket == "" {
				continue
			}
			s := stats[bucket]
			if s == nil {
				s = &accessStats{}
				stats[bucket] = s
			}
			s.rows += rowWork
			if !seen[bucket] {
				s.digests++
				seen[bucket] = true
			}
		}
	}

	if totalExplainRows == 0 {
		return findings
	}

	// Row-based coverage: explain rows vs total handler rows from Graphite
	rowCoverageSuffix := ""
	if graphiteHandlerRows > 0 {
		rowCoverage := float64(totalExplainRows) / float64(graphiteHandlerRows) * 100
		if rowCoverage > 100 {
			rowCoverage = 100
		}
		rowCoverageSuffix = fmt.Sprintf(" observed %.0f%% of row operations", rowCoverage)
	}

	type tagDef struct {
		errKey string
		bucket string
		label  string
	}
	tags := []tagDef{
		{"WTAG0210", "full_scan", "Full scan (type=ALL)"},
		{"WTAG0211", "range", "Range scan (type=range)"},
		{"WTAG0212", "ref", "Index lookup (type=ref/eq_ref/const)"},
	}

	for _, t := range tags {
		s := stats[t.bucket]
		if s == nil || s.rows == 0 {
			continue
		}
		pct := float64(s.rows) / float64(totalExplainRows) * 100
		desc := fmt.Sprintf("%s row cost %.0f%% (%d/%d digests)%s",
			t.label, pct, s.digests, len(src.PFSExplainPlans), rowCoverageSuffix)

		findings = append(findings, Finding{
			ErrKey:      t.errKey,
			Severity:    SeverityWorkload,
			Description: desc,
			Count:       s.rows,
			Total:       totalExplainRows,
		})
	}

	return findings
}

// appendExplainExtra computes row cost for optimizer features visible
// in the EXPLAIN Extra column: ICP, filesort, temporary, covering index.
func appendExplainExtra(findings []Finding, src LogSource, graphiteHandlerRows int64) []Finding {
	if len(src.PFSExplainPlans) == 0 {
		return findings
	}

	type extraStats struct {
		rows    int64
		digests int
	}

	type extraTag struct {
		errKey  string
		label   string
		pattern string
	}
	tags := []extraTag{
		{"WTAG0230", "ICP", "Using index condition"},
		{"WTAG0231", "Filesort", "Using filesort"},
		{"WTAG0232", "Temporary table", "Using temporary"},
		{"WTAG0233", "Covering index", "Using index"},
	}

	stats := make(map[string]*extraStats)
	var totalExplainRows int64

	for _, e := range src.PFSExplainPlans {
		seen := make(map[string]bool)
		for _, r := range e.Plan {
			rows := parseRows(r.Rows)
			rowWork := rows * e.ExecCount
			totalExplainRows += rowWork

			for _, t := range tags {
				if strings.Contains(r.Extra, t.pattern) && !seen[t.errKey] {
					s := stats[t.errKey]
					if s == nil {
						s = &extraStats{}
						stats[t.errKey] = s
					}
					s.rows += rowWork
					s.digests++
					seen[t.errKey] = true
				}
			}
		}
	}

	if totalExplainRows == 0 {
		return findings
	}

	rowCoverageSuffix := ""
	if graphiteHandlerRows > 0 {
		rowCoverage := float64(totalExplainRows) / float64(graphiteHandlerRows) * 100
		if rowCoverage > 100 {
			rowCoverage = 100
		}
		rowCoverageSuffix = fmt.Sprintf(" observed %.0f%% of row operations", rowCoverage)
	}

	for _, t := range tags {
		s := stats[t.errKey]
		if s == nil || s.rows == 0 {
			continue
		}
		pct := float64(s.rows) / float64(totalExplainRows) * 100
		findings = append(findings, Finding{
			ErrKey:   t.errKey,
			Severity: SeverityWorkload,
			Description: fmt.Sprintf("%s row cost %.0f%% (%d/%d digests)%s",
				t.label, pct, s.digests, len(src.PFSExplainPlans), rowCoverageSuffix),
			Count: s.rows,
			Total: totalExplainRows,
		})
	}

	return findings
}

func classifyAccess(typ string) string {
	switch typ {
	case "ALL":
		return "full_scan"
	case "range", "index":
		return "range"
	case "ref", "eq_ref", "const", "system":
		return "ref"
	default:
		return ""
	}
}

func parseRows(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// appendDigestTmpDisk computes the percentage of query executions that
// create on-disk temp tables, weighted by exec_count from PFS digests.
func appendDigestTmpDisk(findings []Finding, src LogSource, digestCoverage float64) []Finding {
	if len(src.PFSQueries) == 0 {
		return findings
	}

	var tmpDiskExec, totalExec int64
	tmpDiskDigests := 0

	for _, q := range src.PFSQueries {
		totalExec += q.ExecCount
		if q.PlanTmpDisk > 0 {
			tmpDiskExec += q.ExecCount
			tmpDiskDigests++
		}
	}

	if tmpDiskDigests > 0 && totalExec > 0 {
		coverageSuffix := ""
		if digestCoverage > 0 {
			coverageSuffix = fmt.Sprintf(" observed %.0f%% of workload", digestCoverage*100)
		}

		findings = append(findings, Finding{
			ErrKey:   "WTAG0220",
			Severity: SeverityWorkload,
			Description: fmt.Sprintf("Disk tmp table queries %d/%d digests%s",
				tmpDiskDigests, len(src.PFSQueries), coverageSuffix),
			Count: tmpDiskExec,
			Total: totalExec,
		})
	}

	return findings
}
