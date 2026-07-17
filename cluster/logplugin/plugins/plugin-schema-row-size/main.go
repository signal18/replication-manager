// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Ahmad Faruk <ahmad@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

// plugin-schema-row-size flags InnoDB tables whose short, always-inline-stored
// VARCHAR columns already sum past InnoDB's per-row storage budget.
//
// InnoDB inline row budget:
//
//	Documented for a 16K page: slightly less than half a page (~8126 bytes).
//	This plugin generalises that as page_size/2 - 66 (the constant that
//	reproduces 8126 for page_size=16384), special-cases 64K pages (which are
//	NOT simply half-page in practice), and applies a small extra discount for
//	ROW_FORMAT=COMPRESSED tables since KEY_BLOCK_SIZE is not available in the
//	schema snapshot.
//
// VARCHAR selection (per issue #1592):
//
//	Only VARCHAR columns whose declared byte width (declared char length ×
//	charset max-bytes-per-char) is under 256 bytes are summed — those are the
//	columns InnoDB always stores fully inline with a 1-byte length prefix, so
//	their contribution to row pressure is certain regardless of row_format.
//	Wider VARCHAR/TEXT/BLOB columns are candidates for partial/full off-page
//	storage and are deliberately excluded from the sum — including them would
//	make the check less certain, not more accurate.
//
// This is advisory and intentionally conservative, not a byte-exact replica
// of InnoDB's internal record format.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// charsetBytesPerChar maps common MySQL/MariaDB charset names to their
// maximum bytes-per-character. Unknown charsets fall back to 4 (conservative
// — better to overestimate row pressure than miss a real risk).
var charsetBytesPerChar = map[string]int{
	"utf8mb4": 4,
	"utf8mb3": 3,
	"utf8":    3,
	"ucs2":    2,
	"utf16":   4,
	"utf16le": 4,
	"utf32":   4,
	"latin1":  1,
	"latin2":  1,
	"latin5":  1,
	"latin7":  1,
	"ascii":   1,
	"binary":  1,
	"cp850":   1,
	"cp852":   1,
	"cp866":   1,
	"cp1250":  1,
	"cp1251":  1,
	"cp1256":  1,
	"cp1257":  1,
	"koi8r":   1,
	"koi8u":   1,
	"greek":   1,
	"hebrew":  1,
	"gbk":     2,
	"big5":    2,
	"sjis":    2,
	"cp932":   2,
	"euckr":   2,
	"eucjpms": 3,
	"gb18030": 4,
}

const defaultBytesPerChar = 4

// maxColumnsPerTableInDescription caps how many offending columns are listed
// per table in the aggregated finding description, so a table with hundreds
// of short VARCHAR columns doesn't blow up the description size.
const maxColumnsPerTableInDescription = 15

var varcharTypeRe = regexp.MustCompile(`(?i)^varchar\((\d+)\)`)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	findings := evaluateTables(req)
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

// tableOffense is one InnoDB table whose short, always-inline-stored VARCHAR
// columns sum past the estimated InnoDB inline row budget.
type tableOffense struct {
	schema, name, rowFormat, pageSize string
	sum, budget, avgRowLength         int64
	columns                           []string
}

// evaluateTables inspects every InnoDB table in req.Tables and returns at
// most one SCH0001 finding aggregating every offending table found.
//
// All findings from one plugin run share the fixed ErrKey "SCH0001", and the
// repman state machine keys open states as ErrKey@server — only the first
// state written for a given key survives a monitoring cycle (see
// state.Map.Add). Emitting one finding per table would silently drop every
// table after the first, so every offense is folded into a single finding's
// description instead, matching the aggregation pattern used by
// plugin-security-hardening's SEC0107/SEC0108.
func evaluateTables(req wire.Request) []wire.Finding {
	inlineMaxBytes := wire.CfgInt(req.Config, "inline-varchar-max-bytes", wire.EnvInt("REPMAN_SCHEMA_ROW_SIZE_INLINE_MAX_BYTES", 256))

	var offenses []tableOffense
	for _, t := range req.Tables {
		if !strings.EqualFold(t.Engine, "InnoDB") {
			continue
		}

		budget := innodbRowBudget(req.ServerVariables["innodb_page_size"], t.RowFormat)

		type contribution struct {
			name  string
			bytes int
		}
		var contributions []contribution
		sum := 0
		for _, c := range t.Columns {
			byteWidth, ok := varcharByteWidth(c.Type, c.Charset)
			if !ok || byteWidth >= inlineMaxBytes {
				continue
			}
			prefix := 1
			if byteWidth > 255 {
				prefix = 2
			}
			width := byteWidth + prefix
			sum += width
			contributions = append(contributions, contribution{name: c.Name, bytes: width})
		}

		if int64(sum) <= budget || len(contributions) == 0 {
			continue
		}

		sort.Slice(contributions, func(i, j int) bool { return contributions[i].bytes > contributions[j].bytes })
		colDesc := make([]string, 0, len(contributions))
		for i, c := range contributions {
			if i >= maxColumnsPerTableInDescription {
				colDesc = append(colDesc, fmt.Sprintf("(+%d more)", len(contributions)-maxColumnsPerTableInDescription))
				break
			}
			colDesc = append(colDesc, fmt.Sprintf("%s(%dB)", c.name, c.bytes))
		}

		offenses = append(offenses, tableOffense{
			schema:       t.Schema,
			name:         t.Name,
			rowFormat:    orDefault(t.RowFormat, "(default)"),
			pageSize:     orDefault(req.ServerVariables["innodb_page_size"], "16384"),
			sum:          int64(sum),
			budget:       budget,
			avgRowLength: t.AvgRowLength,
			columns:      colDesc,
		})
	}

	if len(offenses) == 0 {
		return nil
	}

	sort.Slice(offenses, func(i, j int) bool {
		if offenses[i].schema != offenses[j].schema {
			return offenses[i].schema < offenses[j].schema
		}
		return offenses[i].name < offenses[j].name
	})

	parts := make([]string, 0, len(offenses))
	for _, o := range offenses {
		part := fmt.Sprintf("%s.%s (sum=%dB, limit=%dB, row_format=%s, page_size=%s, columns: %s)",
			o.schema, o.name, o.sum, o.budget, o.rowFormat, o.pageSize, strings.Join(o.columns, ", "))
		if o.avgRowLength > 0 {
			part += fmt.Sprintf(" [observed avg_row_length=%dB]", o.avgRowLength)
		}
		parts = append(parts, part)
	}

	desc := fmt.Sprintf(
		"%d InnoDB table(s) have short inline-stored VARCHAR columns summing past the estimated InnoDB inline"+
			" row budget. Rows past this budget either fail with \"Row size too large\" or force additional columns"+
			" off-page, hurting scan performance. Consider narrowing these columns, moving less-hot data to a"+
			" companion table, or switching to ROW_FORMAT=DYNAMIC/COMPRESSED if not already in use. Tables: %s",
		len(offenses), strings.Join(parts, "; "),
	)

	return []wire.Finding{{
		ErrKey:      "SCH0001",
		Severity:    "SCHEMA",
		Description: desc,
	}}
}

// varcharByteWidth returns the declared byte width of a VARCHAR column
// (declared char length × charset max-bytes-per-char) and whether colType is
// a VARCHAR at all.
func varcharByteWidth(colType, charset string) (int, bool) {
	m := varcharTypeRe.FindStringSubmatch(strings.TrimSpace(colType))
	if m == nil {
		return 0, false
	}
	declaredChars, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	bpc, ok := charsetBytesPerChar[strings.ToLower(charset)]
	if !ok {
		bpc = defaultBytesPerChar
	}
	return declaredChars * bpc, true
}

// innodbRowBudget estimates the maximum bytes InnoDB can store inline for a
// single row, given innodb_page_size and row_format. See package doc for the
// derivation — this deliberately reproduces the documented ~8126-byte limit
// for the default 16K page size.
func innodbRowBudget(pageSizeStr, rowFormat string) int64 {
	pageSize, err := strconv.ParseInt(strings.TrimSpace(pageSizeStr), 10, 64)
	if err != nil || pageSize <= 0 {
		pageSize = 16384 // InnoDB default
	}

	var budget int64
	if pageSize >= 65536 {
		// 64K pages are not simply half-page in practice; MySQL/MariaDB
		// restrict effective inline row storage well below that. Use a
		// quarter-page estimate with the same fixed overhead as the general
		// formula, which is conservative relative to commonly cited ~16K limits.
		budget = pageSize/4 - 66
	} else {
		budget = pageSize/2 - 66
	}

	if strings.EqualFold(rowFormat, "Compressed") {
		// KEY_BLOCK_SIZE is not available in the schema snapshot, so apply a
		// conservative discount — compressed tables generally have a smaller
		// effective inline budget than their uncompressed innodb_page_size implies.
		budget = budget * 9 / 10
	}
	return budget
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
