package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

	if len(req.PFSQueries) == 0 {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	var findings []wire.Finding

	pfsTotal := int64(0)
	for _, q := range req.PFSQueries {
		pfsTotal += q.ExecCount
	}

	pfsLastTruncate := parseTruncateTime(req.PFSLastTruncate)
	graphiteQueries := fetchQueriesSinceTruncate(req, pfsLastTruncate)
	graphiteHandlerRows := fetchHandlerRowsSinceTruncate(req, pfsLastTruncate)

	digestCoverage := computeDigestCoverage(pfsTotal, graphiteQueries)
	explainCoverage := computeExplainCoverage(req)

	findings = appendCoveringRatio(findings, pfsTotal, graphiteQueries, digestCoverage)
	findings = appendExplainCoverage(findings, req, explainCoverage)
	findings = appendExplainAccessType(findings, req, graphiteHandlerRows)
	findings = appendExplainExtra(findings, req, graphiteHandlerRows)
	findings = appendDigestTmpDisk(findings, req, digestCoverage)
	findings = appendDigestSortRows(findings, req, graphiteHandlerRows)

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func parseTruncateTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
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

func computeExplainCoverage(req wire.Request) float64 {
	total := len(req.PFSQueries)
	if total == 0 {
		return 0
	}
	return float64(req.PFSExplainCount) / float64(total)
}

func appendCoveringRatio(findings []wire.Finding, pfsTotal, graphiteQueries int64, digestCoverage float64) []wire.Finding {
	if digestCoverage <= 0 {
		return findings
	}
	findings = append(findings, wire.Finding{
		ErrKey:      "WTAG0200",
		Severity:    "WORKLOAD",
		Description: fmt.Sprintf("PFS digest coverage %.0f%% (%d/%d queries since last truncate)", digestCoverage*100, pfsTotal, graphiteQueries),
	})
	return findings
}

func appendExplainCoverage(findings []wire.Finding, req wire.Request, explainCoverage float64) []wire.Finding {
	totalDigests := len(req.PFSQueries)
	if totalDigests == 0 {
		return findings
	}
	findings = append(findings, wire.Finding{
		ErrKey:      "WTAG0201",
		Severity:    "WORKLOAD",
		Description: fmt.Sprintf("PFS digest explain coverage %.0f%% (%d/%d digests have EXPLAIN)", explainCoverage*100, req.PFSExplainCount, totalDigests),
	})
	return findings
}

func appendExplainAccessType(findings []wire.Finding, req wire.Request, graphiteHandlerRows int64) []wire.Finding {
	if len(req.PFSExplainPlans) == 0 {
		return findings
	}

	type accessStats struct {
		rows    int64
		digests int
	}
	stats := make(map[string]*accessStats)
	var totalExplainRows int64

	for _, e := range req.PFSExplainPlans {
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
		findings = append(findings, wire.Finding{
			ErrKey:      t.errKey,
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("%s row cost %.0f%% (%d/%d digests)%s", t.label, pct, s.digests, len(req.PFSExplainPlans), rowCoverageSuffix),
			Count:       s.rows,
			Total:       totalExplainRows,
		})
	}

	return findings
}

func appendExplainExtra(findings []wire.Finding, req wire.Request, graphiteHandlerRows int64) []wire.Finding {
	if len(req.PFSExplainPlans) == 0 {
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

	for _, e := range req.PFSExplainPlans {
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
		findings = append(findings, wire.Finding{
			ErrKey:      t.errKey,
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("%s row cost %.0f%% (%d/%d digests)%s", t.label, pct, s.digests, len(req.PFSExplainPlans), rowCoverageSuffix),
			Count:       s.rows,
			Total:       totalExplainRows,
		})
	}

	return findings
}

func appendDigestTmpDisk(findings []wire.Finding, req wire.Request, digestCoverage float64) []wire.Finding {
	if len(req.PFSQueries) == 0 {
		return findings
	}

	var tmpDiskExec, totalExec int64
	tmpDiskDigests := 0

	for _, q := range req.PFSQueries {
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
		findings = append(findings, wire.Finding{
			ErrKey:      "WTAG0220",
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("Disk tmp table queries %d/%d digests%s", tmpDiskDigests, len(req.PFSQueries), coverageSuffix),
			Count:       tmpDiskExec,
			Total:       totalExec,
		})
	}

	return findings
}

func appendDigestSortRows(findings []wire.Finding, req wire.Request, graphiteHandlerRows int64) []wire.Finding {
	if len(req.PFSQueries) == 0 {
		return findings
	}

	var totalSortRows int64
	sortDigests := 0

	for _, q := range req.PFSQueries {
		if q.SortRows > 0 {
			totalSortRows += q.SortRows
			sortDigests++
		}
	}

	if totalSortRows <= 0 {
		return findings
	}

	rowCoverageSuffix := ""
	if graphiteHandlerRows > 0 {
		pct := float64(totalSortRows) / float64(graphiteHandlerRows) * 100
		if pct > 100 {
			pct = 100
		}
		rowCoverageSuffix = fmt.Sprintf(" %.0f%% of row operations", pct)
	}

	findings = append(findings, wire.Finding{
		ErrKey:      "WTAG0240",
		Severity:    "WORKLOAD",
		Description: fmt.Sprintf("Sorted rows (%d/%d digests)%s", sortDigests, len(req.PFSQueries), rowCoverageSuffix),
		Count:       totalSortRows,
		Total:       graphiteHandlerRows,
	})

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

// ---- Graphite helpers ----

var graphiteClient = &http.Client{Timeout: 10 * time.Second}

func fetchQueriesSinceTruncate(req wire.Request, pfsLastTruncate time.Time) int64 {
	if req.GraphiteAPIURL == "" || req.GraphiteHostname == "" || pfsLastTruncate.IsZero() {
		return 0
	}
	elapsed := time.Since(pfsLastTruncate)
	if elapsed < time.Minute {
		return 0
	}
	from := fmt.Sprintf("-%dmin", int(elapsed.Minutes())+1)
	target := fmt.Sprintf("integral(keepLastValue(nonNegativeDerivative(mysql.%s.mysql_global_status_queries)))", req.GraphiteHostname)
	return fetchLastIntegral(req.GraphiteAPIURL, target, from)
}

func fetchHandlerRowsSinceTruncate(req wire.Request, pfsLastTruncate time.Time) int64 {
	if req.GraphiteAPIURL == "" || req.GraphiteHostname == "" || pfsLastTruncate.IsZero() {
		return 0
	}
	elapsed := time.Since(pfsLastTruncate)
	if elapsed < time.Minute {
		return 0
	}
	from := fmt.Sprintf("-%dmin", int(elapsed.Minutes())+1)
	target := fmt.Sprintf("integral(sumSeries(keepLastValue(nonNegativeDerivative(mysql.%s.mysql_global_status_handler_read_*))))", req.GraphiteHostname)
	return fetchLastIntegral(req.GraphiteAPIURL, target, from)
}

type graphiteDatapoint struct {
	Value     *float64
	Timestamp int64
}

func (d *graphiteDatapoint) UnmarshalJSON(data []byte) error {
	var tmp [2]json.RawMessage
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	var v *float64
	json.Unmarshal(tmp[0], &v)
	d.Value = v
	json.Unmarshal(tmp[1], &d.Timestamp)
	return nil
}

type graphiteSeries struct {
	Target     string              `json:"target"`
	Datapoints []graphiteDatapoint `json:"datapoints"`
}

func fetchLastIntegral(apiURL, target, from string) int64 {
	u, err := url.Parse(apiURL + "/render/")
	if err != nil {
		return 0
	}
	q := u.Query()
	q.Set("target", target)
	q.Set("from", from)
	q.Set("until", "now")
	q.Set("format", "json")
	q.Set("noCache", "1")
	u.RawQuery = q.Encode()

	resp, err := graphiteClient.Get(u.String())
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}

	var series []graphiteSeries
	if err := json.Unmarshal(body, &series); err != nil || len(series) == 0 {
		return 0
	}

	points := series[0].Datapoints
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Value != nil {
			return int64(*points[i].Value)
		}
	}
	return 0
}
