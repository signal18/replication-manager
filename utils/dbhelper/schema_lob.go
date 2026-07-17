// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file enriches the schema dictionary with BLOB/TEXT LOB metadata
// (MariaDB per-column COMPRESSED detection and bounded-sample observed
// average byte length) consumed by the plugin-schema-lob-compression schema
// advisor plugin (see issue #1592).

package dbhelper

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// Bounded sampling / gating constants. Average-length collection must never
// become a full-table scan on the exact largest-LOB tables it is trying to
// flag, so every query here is capped: a fixed row LIMIT with no ORDER BY
// (keeps the read close to a clustered-index prefix scan) and a short
// per-query timeout, with silent skip-on-failure.
const (
	lobSampleLimit           = 1024
	lobSampleTimeout         = 2 * time.Second
	lobCandidateMinDataBytes = 1 << 20 // 1MB — skip trivially small tables
)

var (
	showCreateColumnRe = regexp.MustCompile("(?m)^\\s*`([^`]+)`\\s")
	compressedAttrRe   = regexp.MustCompile(`(?i)\bCOMPRESSED\b`)
)

// IsLobColumnType reports whether colType (as stored by information_schema,
// already lowercased) is a BLOB or TEXT family type (including the
// tiny/medium/long variants, which all contain "blob" or "text").
func IsLobColumnType(colType string) bool {
	return strings.Contains(colType, "blob") || strings.Contains(colType, "text")
}

// EnrichLobColumns populates Compressed and AvgByteLength on BLOB/TEXT
// candidate columns of MariaDB tables. No-op for non-MariaDB servers and for
// tables under lobCandidateMinDataBytes.
//
// Per table with LOB candidates this issues at most one SHOW CREATE TABLE
// (shared across all candidate columns, for compression detection) plus one
// bounded sampled AVG(LENGTH()) query per column not already detected as
// COMPRESSED. Every query is capped — see the constants above. Failures are
// skipped silently: this must never break schema monitoring.
func EnrichLobColumns(db *sqlx.DB, myver *version.Version, tables map[string]*Table) string {
	if myver == nil || !myver.IsMariaDB() {
		return ""
	}

	var logBuilder strings.Builder
	for _, t := range tables {
		if t.DataLength < lobCandidateMinDataBytes {
			continue
		}
		var candidates []*Column
		for i := range t.TableColumns {
			c := &t.TableColumns[i]
			if IsLobColumnType(c.Type) {
				candidates = append(candidates, c)
			}
		}
		if len(candidates) == 0 {
			continue
		}

		appendLog(&logBuilder, applyCompressedFlags(db, t, candidates))

		for _, c := range candidates {
			if c.Compressed {
				continue // already flagged compressed — no need to size it
			}
			avg, qlog, err := sampleAvgColumnLength(db, t.TableSchema, t.TableName, c.Name)
			appendLog(&logBuilder, qlog)
			if err != nil {
				continue // skip silently — sampling failures must not break schema monitoring
			}
			c.AvgByteLength = avg
		}
	}
	return logBuilder.String()
}

// applyCompressedFlags runs SHOW CREATE TABLE once and sets Compressed on any
// candidate whose column definition line declares COMPRESSED. MariaDB's
// per-column compression attribute is not exposed via information_schema —
// SHOW CREATE TABLE parsing is the only way to detect it.
func applyCompressedFlags(db *sqlx.DB, t *Table, candidates []*Column) string {
	qtable, err := QuoteMySQLTableIdentifier(t.TableSchema + "." + t.TableName)
	if err != nil {
		return ""
	}
	query := "SHOW CREATE TABLE " + qtable
	ctx, cancel := context.WithTimeout(context.Background(), lobSampleTimeout)
	defer cancel()

	var name, ddl string
	if err := db.QueryRowxContext(ctx, query).Scan(&name, &ddl); err != nil {
		return fmt.Sprintf("%s -- failed: %v", query, err)
	}

	byName := make(map[string]*Column, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = c
	}
	for _, line := range strings.Split(ddl, "\n") {
		m := showCreateColumnRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		col, ok := byName[m[1]]
		if !ok {
			continue
		}
		col.Compressed = compressedAttrRe.MatchString(line)
	}
	return query
}

// sampleAvgColumnLength computes a bounded-sample average byte length for one
// column: at most lobSampleLimit non-NULL rows, no ORDER BY, short timeout.
// Never scans the full table.
func sampleAvgColumnLength(db *sqlx.DB, schema, table, column string) (int64, string, error) {
	qtable, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return 0, "", err
	}
	if verr := ValidateIdentifier(column); verr != nil {
		return 0, "", verr
	}
	qcol := QuoteMySQLIdentifier(column)

	query := fmt.Sprintf(
		"SELECT AVG(LENGTH(%s)) FROM (SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT %d) lob_sample",
		qcol, qcol, qtable, qcol, lobSampleLimit,
	)
	ctx, cancel := context.WithTimeout(context.Background(), lobSampleTimeout)
	defer cancel()

	var avg sql.NullFloat64
	if err := db.QueryRowxContext(ctx, query).Scan(&avg); err != nil {
		return 0, query, err
	}
	if !avg.Valid {
		return 0, query, nil // all-NULL sample or empty table — nothing to report
	}
	return int64(avg.Float64), query, nil
}
