package main

import (
	"encoding/json"
	"fmt"
	"os"
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

	var findings []wire.Finding
	questions := getStatus(req.ServerStatus, "queries")
	if questions == 0 {
		questions = getStatus(req.ServerStatus, "questions")
	}

	findings = appendFeatureTag(findings, req.ServerStatus, "feature_cte", "WTAG0001", "Common Table Expressions (WITH)")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_window_functions", "WTAG0002", "Window functions (OVER)")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_json", "WTAG0003", "JSON functions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_dynamic_columns", "WTAG0004", "Dynamic columns")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_fulltext", "WTAG0005", "Full-text search")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_gis", "WTAG0006", "GIS / spatial functions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_locale", "WTAG0007", "Locale-dependent functions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_subquery", "WTAG0008", "Subqueries")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_timezone", "WTAG0009", "Timezone conversions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_trigger", "WTAG0010", "Triggers")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_xml", "WTAG0011", "XML functions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_system_versioning", "WTAG0012", "System-versioned tables")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_application_time_periods", "WTAG0013", "Application-time period tables")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_insert_returning", "WTAG0014", "INSERT ... RETURNING")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_into_outfile", "WTAG0015", "SELECT INTO OUTFILE")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_custom_aggregate_functions", "WTAG0016", "Custom aggregate functions")
	findings = appendFeatureTag(findings, req.ServerStatus, "feature_invisible_columns", "WTAG0017", "Invisible columns")

	findings = appendHandlerTag(findings, req.ServerStatus)
	findings = appendFeatureTag(findings, req.ServerStatus, "prepared_stmt_count", "WTAG0030", "Prepared statements")
	findings = appendFeatureTag(findings, req.ServerStatus, "com_xa_start", "WTAG0031", "XA transactions")
	findings = appendFeatureTag(findings, req.ServerStatus, "com_call_procedure", "WTAG0032", "Stored procedures")

	if getVar(req.ServerVariables, "event_scheduler") == "ON" {
		findings = append(findings, wire.Finding{
			ErrKey:      "WTAG0033",
			Severity:    "WORKLOAD",
			Description: "Events (event_scheduler=ON)",
		})
	}

	findings = appendOptimizerTags(findings, req.ServerVariables)
	findings = appendOptimizerProblems(findings, req.ServerStatus, questions)

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func appendFeatureTag(findings []wire.Finding, status map[string]string, statusKey, errKey, desc string) []wire.Finding {
	if getStatus(status, statusKey) > 0 {
		findings = append(findings, wire.Finding{
			ErrKey:      errKey,
			Severity:    "WORKLOAD",
			Description: desc,
		})
	}
	return findings
}

func appendHandlerTag(findings []wire.Finding, status map[string]string) []wire.Finding {
	readKey := getStatus(status, "handler_read_key")
	readRndNext := getStatus(status, "handler_read_rnd_next")
	writeTotal := getStatus(status, "handler_write")

	if readRndNext > 0 && readKey > 0 {
		ratio := float64(readRndNext) / float64(readKey)
		if ratio > 100 {
			findings = append(findings, wire.Finding{
				ErrKey:      "WTAG0020",
				Severity:    "WORKLOAD",
				Description: fmt.Sprintf("Heavy full-scan workload (rnd_next/read_key=%.0f)", ratio),
			})
		}
	}

	if writeTotal > 0 && readKey > 0 {
		writeRatio := float64(writeTotal) / float64(readKey+writeTotal)
		if writeRatio > 0.5 {
			findings = append(findings, wire.Finding{
				ErrKey:      "WTAG0021",
				Severity:    "WORKLOAD",
				Description: fmt.Sprintf("Write-intensive workload (write ratio=%.0f%%)", writeRatio*100),
			})
		}
	}

	return findings
}

func appendOptimizerTags(findings []wire.Finding, vars map[string]string) []wire.Finding {
	optSwitch := getVar(vars, "optimizer_switch")
	if optSwitch == "" {
		return findings
	}

	type optTag struct {
		flag   string
		errKey string
		tag    string
		wantOn bool
	}

	checks := []optTag{
		{"derived_merge", "WTAG0100", "OptDerivedMerge", true},
		{"semijoin", "WTAG0101", "OptSemiJoin", true},
		{"subquery_cache", "WTAG0102", "OptSubqueryCache", true},
		{"index_merge", "WTAG0103", "OptIndexMerge", true},
		{"index_condition_pushdown", "WTAG0104", "OptIcp", true},
		{"mrr", "WTAG0105", "OptMrr", true},
		{"join_cache_hashed", "WTAG0106", "OptHashJoin", true},
		{"rowid_filter", "WTAG0107", "OptRowidFilter", true},
		{"table_elimination", "WTAG0108", "OptTableElimination", true},
		{"condition_pushdown_for_derived", "WTAG0109", "OptConditionPushdown", true},
		{"split_materialized", "WTAG0110", "OptSplitMaterialized", true},
		{"exists_to_in", "WTAG0111", "OptExistsToIn", true},
		{"in_to_exists", "WTAG0112", "OptInToExists", true},
		{"orderby_uses_equalities", "WTAG0113", "OptOrderbyEq", true},
		{"extended_keys", "WTAG0114", "OptExtendedKeys", true},
		{"firstmatch", "WTAG0115", "OptFirstMatch", true},
		{"loosescan", "WTAG0116", "OptLooseScan", true},
		{"materialization", "WTAG0117", "OptMaterialization", true},
		{"duplicateweedout", "WTAG0118", "OptDuplicateWeedout", true},
	}

	flags := parseOptimizerSwitch(optSwitch)
	for _, c := range checks {
		val, exists := flags[c.flag]
		if !exists {
			continue
		}
		if (val == "on") == c.wantOn {
			state := "enabled"
			if !c.wantOn {
				state = "disabled"
			}
			findings = append(findings, wire.Finding{
				ErrKey:      c.errKey,
				Severity:    "WORKLOAD",
				Description: fmt.Sprintf("Optimizer: %s %s (%s)", c.tag, state, c.flag),
			})
		}
	}

	return findings
}

func appendOptimizerProblems(findings []wire.Finding, status map[string]string, questions int64) []wire.Finding {
	selectFullJoin := getStatus(status, "select_full_join")
	if selectFullJoin > 0 {
		findings = append(findings, wire.Finding{
			ErrKey:      "WTAG0150",
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("Joins without indexes (Select_full_join=%d)", selectFullJoin),
			Count:       selectFullJoin,
			Total:       questions,
			Remediations: []wire.Remediation{{
				Type:        "sql",
				Description: "Add indexes to join columns identified by EXPLAIN on slow queries",
				Risk:        "safe",
			}},
		})
	}

	selectRangeCheck := getStatus(status, "select_range_check")
	if selectRangeCheck > 0 {
		findings = append(findings, wire.Finding{
			ErrKey:      "WTAG0151",
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("Joins requiring range check per row (Select_range_check=%d)", selectRangeCheck),
			Count:       selectRangeCheck,
			Total:       questions,
		})
	}

	sortMergePasses := getStatus(status, "sort_merge_passes")
	if sortMergePasses > 0 {
		findings = append(findings, wire.Finding{
			ErrKey:      "WTAG0152",
			Severity:    "WORKLOAD",
			Description: fmt.Sprintf("Multi-pass sorts (Sort_merge_passes=%d)", sortMergePasses),
			Count:       sortMergePasses,
			Total:       questions,
			Remediations: []wire.Remediation{{
				Type:        "my_cnf",
				Description: "Increase sort_buffer_size to reduce merge passes",
				MyCnf:       "sort_buffer_size = 4M",
				Risk:        "safe",
			}},
		})
	}

	tmpDisk := getStatus(status, "created_tmp_disk_tables")
	tmpTotal := getStatus(status, "created_tmp_tables")
	if tmpTotal > 0 && tmpDisk > 0 {
		ratio := float64(tmpDisk) / float64(tmpTotal) * 100
		if ratio > 25 {
			findings = append(findings, wire.Finding{
				ErrKey:      "WTAG0153",
				Severity:    "WORKLOAD",
				Description: fmt.Sprintf("High disk temp table ratio (%.0f%%, %d/%d)", ratio, tmpDisk, tmpTotal),
				Count:       tmpDisk,
				Total:       questions,
				Remediations: []wire.Remediation{{
					Type:        "my_cnf",
					Description: "Increase tmp_table_size and max_heap_table_size",
					MyCnf:       "tmp_table_size = 64M\nmax_heap_table_size = 64M",
					Risk:        "safe",
				}},
			})
		}
	}

	return findings
}

func parseOptimizerSwitch(s string) map[string]string {
	flags := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			flags[kv[0]] = kv[1]
		}
	}
	return flags
}

func getStatus(status map[string]string, key string) int64 {
	if status == nil {
		return 0
	}
	v, ok := status[key]
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func getVar(vars map[string]string, key string) string {
	if vars == nil {
		return ""
	}
	return vars[key]
}
