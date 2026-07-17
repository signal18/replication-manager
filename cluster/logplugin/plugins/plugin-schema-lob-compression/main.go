// plugin-schema-lob-compression flags MariaDB BLOB/TEXT columns with a large
// observed average size that are not using MariaDB's per-column COMPRESSED
// storage attribute.
//
// Observed average size and compression status are not computed here — they
// are gathered during schema monitoring under bounded guardrails (a fixed
// row-sample LIMIT, no full-table AVG(LENGTH()) scan, short timeout, silent
// skip-on-failure — see utils/dbhelper/schema_lob.go) and handed to this
// plugin via the wire Tables snapshot. This plugin only applies the
// threshold/compression check; it never talks to the database directly.
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

	findings := evaluateTables(req)
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

// evaluateTables inspects every BLOB/TEXT column in req.Tables and returns at
// most one SCH0002 finding aggregating every column whose sampled average
// byte length exceeds the threshold and that is not already COMPRESSED.
// MariaDB-only — MySQL and other flavors do not support per-column compression.
//
// All findings from one plugin run share the fixed ErrKey "SCH0002", and the
// repman state machine keys open states as ErrKey@server — only the first
// state written for a given key survives a monitoring cycle (see
// state.Map.Add). Emitting one finding per column would silently drop every
// column after the first, so every offense is folded into a single finding's
// description instead, matching the aggregation pattern used by
// plugin-security-hardening's SEC0107/SEC0108.
func evaluateTables(req wire.Request) []wire.Finding {
	if !strings.EqualFold(req.ServerVersion.Flavor, "MariaDB") {
		return nil
	}

	threshold := wire.CfgInt(req.Config, "avg-length-threshold-bytes", wire.EnvInt("REPMAN_SCHEMA_LOB_COMPRESSION_THRESHOLD_BYTES", 8192))

	var offenses []string
	for _, t := range req.Tables {
		for _, c := range t.Columns {
			if !isLobType(c.Type) {
				continue
			}
			if c.Compressed {
				continue
			}
			if c.AvgByteLength <= 0 || int64(c.AvgByteLength) <= int64(threshold) {
				continue
			}

			offenses = append(offenses, fmt.Sprintf(
				"%s.%s.%s (%s, avg=%dB, suggest: ALTER TABLE %s.%s MODIFY %s %s COMPRESSED;)",
				t.Schema, t.Name, c.Name, c.Type, c.AvgByteLength, t.Schema, t.Name, c.Name, c.Type,
			))
		}
	}

	if len(offenses) == 0 {
		return nil
	}
	sort.Strings(offenses)

	desc := fmt.Sprintf(
		"%d BLOB/TEXT column(s) exceed the %d-byte average-size threshold and are not using MariaDB per-column"+
			" compression. Large uncompressed BLOB/TEXT columns waste storage and I/O; per-column COMPRESSED is a"+
			" cheap win for exactly this case. Columns: %s",
		len(offenses), threshold, strings.Join(offenses, "; "),
	)

	return []wire.Finding{{
		ErrKey:      "SCH0002",
		Severity:    "SCHEMA",
		Description: desc,
	}}
}

// isLobType reports whether colType (e.g. "text", "mediumblob") is a
// BLOB/TEXT family type. Mirrors dbhelper.IsLobColumnType — kept independent
// since plugins carry no replication-manager dependency at runtime.
func isLobType(colType string) bool {
	lower := strings.ToLower(colType)
	return strings.Contains(lower, "blob") || strings.Contains(lower, "text")
}
