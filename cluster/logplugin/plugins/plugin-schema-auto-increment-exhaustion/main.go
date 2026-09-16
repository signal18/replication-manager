// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin-schema-auto-increment-exhaustion flags tables whose AUTO_INCREMENT
// counter has reached a share (default 95 %) of the capacity of the column
// type that holds it (SCH0004), before inserts start failing with
// "Duplicate entry ... for key 'PRIMARY'" (the counter stops at the maximum
// and every insert then collides with the last row).
//
// Data (wire v4): Table.AutoIncrement = information_schema.TABLES.AUTO_INCREMENT,
// the NEXT value to be handed out (exact on MariaDB; on MySQL 8 it can lag until
// the table is opened, the finding says so), and the column whose Extra contains
// "auto_increment" gives the type: TINYINT / SMALLINT / MEDIUMINT / INT / BIGINT,
// signed or unsigned. Everything is compared in uint64 / big.Int, never float,
// so an unsigned BIGINT next to 2^64 is judged exactly.
//
// One aggregated SCHEMA finding per run, sorted by ratio descending, with the
// remaining headroom per table and, per table, the widening ALTER as
// remediation (UNSIGNED first when the column is signed: same storage,
// double the range; then BIGINT UNSIGNED).
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

const (
	defaultThresholdPct = 95
	minThresholdPct     = 50
	maxThresholdPct     = 100
	maxTablesInDesc     = 40
)

var intTypeRe = regexp.MustCompile(`(?i)^\s*(tinyint|smallint|mediumint|int|integer|bigint)\b`)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: evaluateTables(req)})
}

// exhaustion is one table whose counter passed the threshold.
type exhaustion struct {
	schema, table, column, colType string
	unsigned                       bool
	next, capacity                 uint64
	ratioPct                       float64
	rebuildBytes                   int64
}

func evaluateTables(req wire.Request) []wire.Finding {
	threshold := wire.CfgInt(req.Config, "capacity-threshold-pct", defaultThresholdPct)
	if threshold < minThresholdPct {
		threshold = minThresholdPct
	}
	if threshold > maxThresholdPct {
		threshold = maxThresholdPct
	}
	maskIdentifiers := wire.CfgBool(req.Config, "mask-identifiers", false)

	var hits []exhaustion
	for _, t := range req.Tables {
		if t.AutoIncrement == 0 {
			continue
		}
		col, ok := autoIncrementColumn(t)
		if !ok {
			continue
		}
		capacity, unsigned, ok := intCapacity(col.Type)
		if !ok {
			continue
		}
		// information_schema stores AUTO_INCREMENT as BIGINT UNSIGNED; the wire carries
		// int64, so a value past 2^63 arrives negative: reinterpret the bits.
		next := uint64(t.AutoIncrement)
		if !reachedThreshold(next, capacity, threshold) {
			continue
		}
		hits = append(hits, exhaustion{
			schema: t.Schema, table: t.Name, column: col.Name, colType: col.Type, unsigned: unsigned,
			next: next, capacity: capacity,
			ratioPct:     ratioPercent(next, capacity),
			rebuildBytes: t.DataLength + t.IndexLength,
		})
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].ratioPct != hits[j].ratioPct {
			return hits[i].ratioPct > hits[j].ratioPct
		}
		return hits[i].schema+"."+hits[i].table < hits[j].schema+"."+hits[j].table
	})

	parts := make([]string, 0, len(hits))
	var remediations []wire.Remediation
	for i, h := range hits {
		schema, table, column := h.schema, h.table, h.column
		if maskIdentifiers {
			schema, table, column = wire.MaskIdentifier(schema), wire.MaskIdentifier(table), wire.MaskIdentifier(column)
		}
		if i < maxTablesInDesc {
			parts = append(parts, fmt.Sprintf("%s.%s.%s %s at %.2f%% (next=%d, capacity=%d, headroom=%d)",
				schema, table, column, h.colType, h.ratioPct, h.next, h.capacity, h.capacity-h.next))
		} else if i == maxTablesInDesc {
			parts = append(parts, fmt.Sprintf("(+%d more)", len(hits)-maxTablesInDesc))
		}
		if maskIdentifiers {
			continue
		}
		size := humanBytes(h.rebuildBytes)
		if !h.unsigned {
			remediations = append(remediations, wire.Remediation{
				Type:        "sql",
				Description: fmt.Sprintf("%s.%s: make %s UNSIGNED, same storage and double the range (values are never negative in an AUTO_INCREMENT column). Table rebuild of ~%s (ALGORITHM=INPLACE on InnoDB, still a full copy of the table).", h.schema, h.table, h.column, size),
				SQL:         fmt.Sprintf("ALTER TABLE `%s`.`%s` MODIFY `%s` %s UNSIGNED NOT NULL AUTO_INCREMENT", h.schema, h.table, h.column, baseType(h.colType)),
				Risk:        "disruptive",
			})
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.colType)), "bigint") {
			remediations = append(remediations, wire.Remediation{
				Type:        "sql",
				Description: fmt.Sprintf("%s.%s: widen %s to BIGINT UNSIGNED (2^64 values). Table rebuild of ~%s; every foreign key and application column referencing it must be widened too.", h.schema, h.table, h.column, size),
				SQL:         fmt.Sprintf("ALTER TABLE `%s`.`%s` MODIFY `%s` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT", h.schema, h.table, h.column),
				Risk:        "disruptive",
			})
		}
	}

	desc := fmt.Sprintf(
		"%d table(s) have an AUTO_INCREMENT counter at or past %d%% of the capacity of the column type holding it."+
			" When the counter reaches the maximum every INSERT fails with \"Duplicate entry ... for key 'PRIMARY'\"."+
			" next = information_schema.TABLES.AUTO_INCREMENT, the next value to be handed out (exact on MariaDB; on MySQL 8"+
			" it can lag behind until the table is opened). Widen the column before the headroom runs out. Tables: %s",
		len(hits), threshold, strings.Join(parts, "; "))

	return []wire.Finding{{
		ErrKey:       "SCH0004",
		Severity:     "SCHEMA",
		Description:  desc,
		Count:        int64(len(hits)),
		Remediations: remediations,
	}}
}

// autoIncrementColumn returns the column whose EXTRA carries auto_increment.
func autoIncrementColumn(t wire.Table) (wire.TableColumn, bool) {
	for _, c := range t.Columns {
		if strings.Contains(strings.ToLower(c.Extra), "auto_increment") {
			return c, true
		}
	}
	return wire.TableColumn{}, false
}

// intCapacity returns the maximum value an integer column type can hold, and
// whether it is unsigned. Display widths (int(11)) and ZEROFILL are ignored.
func intCapacity(colType string) (capacity uint64, unsigned bool, ok bool) {
	m := intTypeRe.FindStringSubmatch(colType)
	if m == nil {
		return 0, false, false
	}
	unsigned = strings.Contains(strings.ToLower(colType), "unsigned")
	switch strings.ToLower(m[1]) {
	case "tinyint":
		capacity = 127
	case "smallint":
		capacity = 32767
	case "mediumint":
		capacity = 8388607
	case "int", "integer":
		capacity = 2147483647
	case "bigint":
		capacity = math.MaxInt64
	}
	if unsigned {
		capacity = capacity*2 + 1
	}
	return capacity, unsigned, true
}

// reachedThreshold is next*100 >= capacity*pct, in big integers so an unsigned
// BIGINT near 2^64 never overflows or rounds.
func reachedThreshold(next, capacity uint64, pct int) bool {
	lhs := new(big.Int).Mul(new(big.Int).SetUint64(next), big.NewInt(100))
	rhs := new(big.Int).Mul(new(big.Int).SetUint64(capacity), big.NewInt(int64(pct)))
	return lhs.Cmp(rhs) >= 0
}

// ratioPercent is next/capacity*100 with two decimals, computed in big.Int.
func ratioPercent(next, capacity uint64) float64 {
	if capacity == 0 {
		return 0
	}
	scaled := new(big.Int).Mul(new(big.Int).SetUint64(next), big.NewInt(10000))
	scaled.Quo(scaled, new(big.Int).SetUint64(capacity))
	return float64(scaled.Int64()) / 100
}

// baseType keeps the integer keyword and its display width, dropping UNSIGNED /
// ZEROFILL so the remediation can re-add UNSIGNED cleanly.
func baseType(colType string) string {
	t := strings.TrimSpace(colType)
	lower := strings.ToLower(t)
	for _, suffix := range []string{" unsigned zerofill", " zerofill", " unsigned"} {
		if i := strings.Index(lower, suffix); i > 0 {
			t = t[:i]
			lower = lower[:i]
		}
	}
	return t
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
