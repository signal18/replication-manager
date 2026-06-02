// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_workload_tags detects which MariaDB/MySQL features are actively used
// by the workload — features that cannot be inferred from my.cnf or replication
// topology alone. Detection uses SHOW GLOBAL STATUS counters (Feature_*,
// Handler_*, Select_*, Created_tmp_*) and optimizer_switch variable analysis.
//
// Each detected feature produces a finding with SeverityWorkload and an error
// key in the WTAG0xxx range. These appear in the workload state machine and
// can be matched against MariaDB JIRA (MDEV) components to assess bug impact.
//
// Error key ranges: WTAG0001–WTAG0099 (feature usage from STATUS)
//                   WTAG0100–WTAG0199 (optimizer path detection)
package logplugin

import (
	"fmt"
	"strconv"
	"strings"
)

func init() {
	Register(&WorkloadTagsPlugin{})
}

// WorkloadTagsPlugin implements LogPlugin.
type WorkloadTagsPlugin struct{}

func (p *WorkloadTagsPlugin) Name() string { return "workload-tags" }

func (p *WorkloadTagsPlugin) DefaultSeverity() Severity { return SeverityWorkload }

func (p *WorkloadTagsPlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() {
		return EvaluateResult{}
	}

	var findings []Finding

	// ── Feature detection from SHOW GLOBAL STATUS ──
	// MariaDB Feature_* counters track actual feature usage since server start.
	findings = appendFeatureTag(findings, src, "feature_cte", "WTAG0001", "OptCte", "Common Table Expressions (WITH)")
	findings = appendFeatureTag(findings, src, "feature_window_functions", "WTAG0002", "OptWindowFunctions", "Window functions (OVER)")
	findings = appendFeatureTag(findings, src, "feature_json", "WTAG0003", "Json", "JSON functions")
	findings = appendFeatureTag(findings, src, "feature_dynamic_columns", "WTAG0004", "DynamicColumns", "Dynamic columns")
	findings = appendFeatureTag(findings, src, "feature_fulltext", "WTAG0005", "FullText", "Full-text search")
	findings = appendFeatureTag(findings, src, "feature_gis", "WTAG0006", "Gis", "GIS / spatial functions")
	findings = appendFeatureTag(findings, src, "feature_locale", "WTAG0007", "Locale", "Locale-dependent functions")
	findings = appendFeatureTag(findings, src, "feature_subquery", "WTAG0008", "Subquery", "Subqueries")
	findings = appendFeatureTag(findings, src, "feature_timezone", "WTAG0009", "Timezone", "Timezone conversions")
	findings = appendFeatureTag(findings, src, "feature_trigger", "WTAG0010", "Triggers", "Triggers")
	findings = appendFeatureTag(findings, src, "feature_xml", "WTAG0011", "Xml", "XML functions")
	findings = appendFeatureTag(findings, src, "feature_system_versioning", "WTAG0012", "VersionedTables", "System-versioned tables")
	findings = appendFeatureTag(findings, src, "feature_application_time_periods", "WTAG0013", "ApplicationTimePeriods", "Application-time period tables")
	findings = appendFeatureTag(findings, src, "feature_insert_returning", "WTAG0014", "InsertReturning", "INSERT ... RETURNING")
	findings = appendFeatureTag(findings, src, "feature_into_outfile", "WTAG0015", "IntoOutfile", "SELECT INTO OUTFILE")
	findings = appendFeatureTag(findings, src, "feature_custom_aggregate_functions", "WTAG0016", "CustomAggregate", "Custom aggregate functions")
	findings = appendFeatureTag(findings, src, "feature_invisible_columns", "WTAG0017", "InvisibleColumns", "Invisible columns")

	// Handler-based workload pattern detection
	findings = appendHandlerTag(findings, src)

	// Prepared statements
	findings = appendFeatureTag(findings, src, "prepared_stmt_count", "WTAG0030", "PreparedStmt", "Prepared statements")
	// XA transactions
	findings = appendFeatureTag(findings, src, "com_xa_start", "WTAG0031", "Xa", "XA transactions")
	// Stored routines
	findings = appendFeatureTag(findings, src, "com_call_procedure", "WTAG0032", "StoredProcs", "Stored procedures")
	// Events
	if getVar(src.ServerVariables, "event_scheduler") == "ON" {
		findings = append(findings, Finding{
			ErrKey:      "WTAG0033",
			Severity:    SeverityWorkload,
			Description: fmt.Sprintf("Server %s uses Events (event_scheduler=ON)", src.ServerURL),
		})
	}

	// ── Optimizer path detection from optimizer_switch ──
	findings = appendOptimizerTags(findings, src)

	// ── Optimizer problem indicators from STATUS ──
	findings = appendOptimizerProblems(findings, src)

	return EvaluateResult{
		Findings:     findings,
		CurrentCount: len(findings),
	}
}

// appendFeatureTag adds a finding if the STATUS counter is > 0.
func appendFeatureTag(findings []Finding, src LogSource, statusKey, errKey, tag, desc string) []Finding {
	v := getStatus(src.ServerStatus, statusKey)
	if v > 0 {
		findings = append(findings, Finding{
			ErrKey:      errKey,
			Severity:    SeverityWorkload,
			Description: fmt.Sprintf("Server %s uses %s (%s=%d)", src.ServerURL, tag, statusKey, v),
		})
	}
	return findings
}

// appendHandlerTag detects broad workload patterns from handler counters.
func appendHandlerTag(findings []Finding, src LogSource) []Finding {
	readKey := getStatus(src.ServerStatus, "handler_read_key")
	readRndNext := getStatus(src.ServerStatus, "handler_read_rnd_next")
	writeTotal := getStatus(src.ServerStatus, "handler_write")

	// Detect heavy full-scan workload (read_rnd_next >> read_key)
	if readRndNext > 0 && readKey > 0 {
		ratio := float64(readRndNext) / float64(readKey)
		if ratio > 100 {
			findings = append(findings, Finding{
				ErrKey:      "WTAG0020",
				Severity:    SeverityWorkload,
				Description: fmt.Sprintf("Server %s has heavy full-scan workload (handler_read_rnd_next/handler_read_key=%.0f)", src.ServerURL, ratio),
			})
		}
	}

	// Detect write-intensive workload
	if writeTotal > 0 && readKey > 0 {
		writeRatio := float64(writeTotal) / float64(readKey+writeTotal)
		if writeRatio > 0.5 {
			findings = append(findings, Finding{
				ErrKey:      "WTAG0021",
				Severity:    SeverityWorkload,
				Description: fmt.Sprintf("Server %s has write-intensive workload (write ratio=%.0f%%)", src.ServerURL, writeRatio*100),
			})
		}
	}

	return findings
}

// appendOptimizerTags detects which optimizer features are enabled from optimizer_switch.
func appendOptimizerTags(findings []Finding, src LogSource) []Finding {
	optSwitch := getVar(src.ServerVariables, "optimizer_switch")
	if optSwitch == "" {
		return findings
	}

	type optTag struct {
		flag    string
		errKey  string
		tag     string
		wantOn  bool // true = detect when ON, false = detect when OFF (disabled optimization)
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
		isOn := val == "on"
		if isOn == c.wantOn {
			state := "enabled"
			if !c.wantOn {
				state = "disabled"
			}
			findings = append(findings, Finding{
				ErrKey:      c.errKey,
				Severity:    SeverityWorkload,
				Description: fmt.Sprintf("Server %s optimizer: %s %s (%s)", src.ServerURL, c.tag, state, c.flag),
			})
		}
	}

	return findings
}

// appendOptimizerProblems detects optimizer problem indicators from STATUS.
func appendOptimizerProblems(findings []Finding, src LogSource) []Finding {
	selectFullJoin := getStatus(src.ServerStatus, "select_full_join")
	if selectFullJoin > 0 {
		findings = append(findings, Finding{
			ErrKey:      "WTAG0150",
			Severity:    SeverityWorkload,
			Description: fmt.Sprintf("Server %s has joins without indexes (Select_full_join=%d)", src.ServerURL, selectFullJoin),
			Remediations: []Remediation{{
				Type:        "sql",
				Description: "Add indexes to join columns identified by EXPLAIN on slow queries",
				Risk:        "safe",
			}},
		})
	}

	selectRangeCheck := getStatus(src.ServerStatus, "select_range_check")
	if selectRangeCheck > 0 {
		findings = append(findings, Finding{
			ErrKey:      "WTAG0151",
			Severity:    SeverityWorkload,
			Description: fmt.Sprintf("Server %s has joins requiring range check per row (Select_range_check=%d)", src.ServerURL, selectRangeCheck),
		})
	}

	sortMergePasses := getStatus(src.ServerStatus, "sort_merge_passes")
	if sortMergePasses > 0 {
		findings = append(findings, Finding{
			ErrKey:      "WTAG0152",
			Severity:    SeverityWorkload,
			Description: fmt.Sprintf("Server %s has multi-pass sorts (Sort_merge_passes=%d)", src.ServerURL, sortMergePasses),
			Remediations: []Remediation{{
				Type:        "my_cnf",
				Description: "Increase sort_buffer_size to reduce merge passes",
				MyCnf:       "sort_buffer_size = 4M",
				Risk:        "safe",
			}},
		})
	}

	tmpDisk := getStatus(src.ServerStatus, "created_tmp_disk_tables")
	tmpTotal := getStatus(src.ServerStatus, "created_tmp_tables")
	if tmpTotal > 0 && tmpDisk > 0 {
		ratio := float64(tmpDisk) / float64(tmpTotal) * 100
		if ratio > 25 {
			findings = append(findings, Finding{
				ErrKey:      "WTAG0153",
				Severity:    SeverityWorkload,
				Description: fmt.Sprintf("Server %s has high disk temp table ratio (%.0f%%, %d/%d)", src.ServerURL, ratio, tmpDisk, tmpTotal),
				Remediations: []Remediation{{
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

// parseOptimizerSwitch parses the optimizer_switch variable into a flag→value map.
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
