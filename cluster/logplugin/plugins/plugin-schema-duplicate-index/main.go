// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin-schema-duplicate-index flags redundant indexes (SCH0003): an index whose
// column list is a leftmost prefix of another index of the same table does no
// work the wider index cannot do, and costs writes, buffer pool and disk.
//
// Rule (per table, wire v4 indexes = information_schema.STATISTICS):
//
//   - B is redundant with A when B's columns, in order, are a leftmost prefix
//     of A's columns (or equal) with the same sub-part lengths.
//   - Exception: B is UNIQUE (or PRIMARY) and A is not -- the uniqueness
//     constraint is not redundant. PRIMARY is never the redundant side.
//   - Exact duplicates (same columns, same sub-parts): PRIMARY wins, then a
//     UNIQUE index wins over a non-unique one, then the lexically greater name
//     is the redundant one (so a pair is reported once).
//   - FULLTEXT / SPATIAL indexes are compared only with indexes of the same
//     type; HASH and BTREE are compared together (same order semantics for
//     the prefix rule is a simplification, stated in the finding).
//   - InnoDB secondary indexes implicitly end with the primary key columns, so a
//     NON-unique secondary index that explicitly ends with the PK columns is
//     compared without that suffix: (a, pk) is redundant with (a).
//
// One aggregated SCHEMA finding per run (the state machine keys open states
// as ErrKey@server), one DROP INDEX remediation per redundant index.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// maxIndexesInDescription caps the per-run description length.
const maxIndexesInDescription = 40

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: evaluateTables(req)})
}

// redundancy is one redundant index B covered by A on a table.
type redundancy struct {
	schema, table string
	redundant     wire.TableIndex // B
	coveredBy     wire.TableIndex // A
	exact         bool
	indexLength   int64 // the table's INDEX_LENGTH, shared by all its secondary indexes
	secondaryCnt  int
}

func evaluateTables(req wire.Request) []wire.Finding {
	maskIdentifiers := wire.CfgBool(req.Config, "mask-identifiers", false)

	var found []redundancy
	for _, t := range req.Tables {
		if len(t.Indexes) < 2 {
			continue
		}
		for _, r := range redundantIndexes(t) {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		return nil
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].schema != found[j].schema {
			return found[i].schema < found[j].schema
		}
		if found[i].table != found[j].table {
			return found[i].table < found[j].table
		}
		return found[i].redundant.Name < found[j].redundant.Name
	})

	parts := make([]string, 0, len(found))
	var remediations []wire.Remediation
	for i, r := range found {
		schema, table, b, a := r.schema, r.table, r.redundant.Name, r.coveredBy.Name
		bCols, aCols := indexColsString(r.redundant), indexColsString(r.coveredBy)
		if maskIdentifiers {
			schema, table = wire.MaskIdentifier(schema), wire.MaskIdentifier(table)
			b, a = wire.MaskIdentifier(b), wire.MaskIdentifier(a)
			bCols, aCols = maskCols(r.redundant), maskCols(r.coveredBy)
		}
		if i < maxIndexesInDescription {
			rel := "is a prefix of"
			if r.exact {
				rel = "duplicates"
			}
			part := fmt.Sprintf("%s.%s: index %s (%s) %s %s (%s)", schema, table, b, bCols, rel, a, aCols)
			if r.indexLength > 0 && r.secondaryCnt > 0 {
				part += fmt.Sprintf(" [~%s of %s index bytes]", humanBytes(r.indexLength/int64(r.secondaryCnt)), humanBytes(r.indexLength))
			}
			parts = append(parts, part)
		} else if i == maxIndexesInDescription {
			parts = append(parts, fmt.Sprintf("(+%d more)", len(found)-maxIndexesInDescription))
		}
		if !maskIdentifiers {
			remediations = append(remediations, wire.Remediation{
				Type:        "sql",
				Description: fmt.Sprintf("Drop %s.%s index %s, covered by %s. Verify first that no query uses it through USE/FORCE INDEX or an optimizer hint; the drop is online DDL on InnoDB.", r.schema, r.table, r.redundant.Name, r.coveredBy.Name),
				SQL:         fmt.Sprintf("ALTER TABLE `%s`.`%s` DROP INDEX `%s`", r.schema, r.table, r.redundant.Name),
				Risk:        "moderate",
			})
		}
	}

	desc := fmt.Sprintf(
		"%d redundant index(es): each one's column list is a leftmost prefix of (or equal to) another index of the same table,"+
			" so it serves no lookup the wider index cannot, while every write maintains it and it occupies buffer pool and disk."+
			" Uniqueness is respected (a UNIQUE index is never reported as covered by a non-unique one) and InnoDB's implicit"+
			" primary-key suffix on secondary indexes is taken into account. Size shown = INDEX_LENGTH of the table spread evenly"+
			" over its secondary indexes (an estimate). Indexes: %s",
		len(found), strings.Join(parts, "; "))

	return []wire.Finding{{
		ErrKey:       "SCH0003",
		Severity:     "SCHEMA",
		Description:  desc,
		Count:        int64(len(found)),
		Remediations: remediations,
	}}
}

// redundantIndexes returns every redundant index of one table with the index that
// covers it. Each index is reported at most once (with the first covering index
// found, preferring the longest one).
func redundantIndexes(t wire.Table) []redundancy {
	pk := primaryColumns(t.Indexes)
	secondary := 0
	for _, ix := range t.Indexes {
		if !ix.Primary {
			secondary++
		}
	}
	var out []redundancy
	for _, b := range t.Indexes {
		if b.Primary || len(b.Columns) == 0 {
			continue
		}
		bCols := comparableColumns(b, pk, t.Engine)
		var best *wire.TableIndex
		exact := false
		for i := range t.Indexes {
			a := t.Indexes[i]
			if a.Name == b.Name || len(a.Columns) == 0 {
				continue
			}
			if !sameFamily(a.Type, b.Type) {
				continue
			}
			aCols := comparableColumns(a, pk, t.Engine)
			if !isPrefix(bCols, aCols) {
				continue
			}
			isExact := len(bCols) == len(aCols)
			if b.Unique && !a.Unique {
				// the uniqueness constraint is not redundant
				continue
			}
			if isExact && !a.Primary && a.Unique == b.Unique && a.Name > b.Name {
				// exact duplicate of equal rank: the lexically greater name is the
				// redundant one, so a (greater) will be reported against b, not b against a
				continue
			}
			if best == nil || len(aCols) > len(comparableColumns(*best, pk, t.Engine)) || (a.Primary && !best.Primary) {
				cp := a
				best = &cp
				exact = isExact
			}
		}
		if best != nil {
			out = append(out, redundancy{
				schema: t.Schema, table: t.Name,
				redundant: b, coveredBy: *best, exact: exact,
				indexLength: t.IndexLength, secondaryCnt: secondary,
			})
		}
	}
	return out
}

// primaryColumns returns the PRIMARY key column names in order, nil when none.
func primaryColumns(indexes []wire.TableIndex) []string {
	for _, ix := range indexes {
		if ix.Primary {
			cols := make([]string, 0, len(ix.Columns))
			for _, c := range ix.Columns {
				cols = append(cols, c.Name)
			}
			return cols
		}
	}
	return nil
}

// comparableColumns is the index column list used for the prefix comparison:
// "name(subpart)" tokens, lower-cased; for a NON-unique InnoDB secondary index a
// trailing run equal to the primary key columns is dropped (InnoDB appends the
// PK to every secondary index anyway, so the explicit suffix adds nothing).
func comparableColumns(ix wire.TableIndex, pk []string, engine string) []string {
	cols := make([]string, 0, len(ix.Columns))
	for _, c := range ix.Columns {
		tok := strings.ToLower(c.Name)
		if c.SubPart > 0 {
			tok = fmt.Sprintf("%s(%d)", tok, c.SubPart)
		}
		cols = append(cols, tok)
	}
	if !ix.Unique && !ix.Primary && strings.EqualFold(engine, "InnoDB") && len(pk) > 0 && len(cols) > len(pk) {
		match := true
		for i, p := range pk {
			if cols[len(cols)-len(pk)+i] != strings.ToLower(p) {
				match = false
				break
			}
		}
		if match {
			cols = cols[:len(cols)-len(pk)]
		}
	}
	return cols
}

// isPrefix reports whether b is a leftmost prefix of (or equal to) a.
func isPrefix(b, a []string) bool {
	if len(b) > len(a) {
		return false
	}
	for i := range b {
		if b[i] != a[i] {
			return false
		}
	}
	return true
}

// sameFamily: FULLTEXT and SPATIAL only compare with their own kind; BTREE/HASH
// (and the empty type) compare together.
func sameFamily(a, b string) bool {
	fa, fb := strings.ToUpper(a), strings.ToUpper(b)
	special := func(s string) bool { return s == "FULLTEXT" || s == "SPATIAL" }
	if special(fa) || special(fb) {
		return fa == fb
	}
	return true
}

func indexColsString(ix wire.TableIndex) string {
	parts := make([]string, 0, len(ix.Columns))
	for _, c := range ix.Columns {
		if c.SubPart > 0 {
			parts = append(parts, fmt.Sprintf("%s(%d)", c.Name, c.SubPart))
		} else {
			parts = append(parts, c.Name)
		}
	}
	return strings.Join(parts, ",")
}

func maskCols(ix wire.TableIndex) string {
	parts := make([]string, 0, len(ix.Columns))
	for _, c := range ix.Columns {
		parts = append(parts, wire.MaskIdentifier(c.Name))
	}
	return strings.Join(parts, ",")
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
