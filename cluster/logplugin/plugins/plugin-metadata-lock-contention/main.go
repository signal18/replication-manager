// plugin-metadata-lock-contention detects metadata lock (MDL) waits that
// indicate DDL blocking DML or long-running transactions blocking ALTER TABLE.
//
// WARN0305 — raised when any MDL wait exceeds lock-wait-ms-threshold OR
// the total number of active MDL waits exceeds lock-count-threshold.
//
// Requires the MariaDB METADATA_LOCK_INFO plugin to be installed.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	lock-wait-ms-threshold  int  default: 5000  — single wait duration in ms to trigger  (env: REPMAN_METADATA_LOCK_CONTENTION_LOCK_WAIT_MS_THRESHOLD)
//	lock-count-threshold    int  default: 3     — concurrent MDL waits to trigger         (env: REPMAN_METADATA_LOCK_CONTENTION_LOCK_COUNT_THRESHOLD)
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

	waitThresholdMs := wire.CfgInt(req.Config, "lock-wait-ms-threshold", wire.EnvInt("REPMAN_METADATA_LOCK_CONTENTION_LOCK_WAIT_MS_THRESHOLD", 5000))
	countThreshold := wire.CfgInt(req.Config, "lock-count-threshold", wire.EnvInt("REPMAN_METADATA_LOCK_CONTENTION_LOCK_COUNT_THRESHOLD", 3))

	if len(req.MetaDataLocks) == 0 {
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	type lockInfo struct {
		table  string
		mode   string
		waitMs int64
	}

	var longWaits []lockInfo
	var allLocks []lockInfo

	for _, m := range req.MetaDataLocks {
		table := m.Schema + "." + m.Table
		allLocks = append(allLocks, lockInfo{table: table, mode: m.LockMode, waitMs: m.LockTimeMs})
		if m.LockTimeMs >= int64(waitThresholdMs) {
			longWaits = append(longWaits, lockInfo{table: table, mode: m.LockMode, waitMs: m.LockTimeMs})
		}
	}

	var findings []wire.Finding

	if len(longWaits) > 0 {
		sort.Slice(longWaits, func(i, j int) bool {
			return longWaits[i].waitMs > longWaits[j].waitMs
		})
		var parts []string
		for i, lw := range longWaits {
			if i >= 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s [%s] %.1fs", lw.table, lw.mode, float64(lw.waitMs)/1000))
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0305",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: %d MDL wait(s) exceeding %dms: %s",
				req.ServerURL, len(longWaits), waitThresholdMs,
				strings.Join(parts, " | ")),
		})
	} else if len(allLocks) >= countThreshold {
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0305",
			Severity: "WORKLOAD",
			Description: fmt.Sprintf(
				"Server %s: %d concurrent metadata lock waits (threshold: %d)",
				req.ServerURL, len(allLocks), countThreshold),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}
