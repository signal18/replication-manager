// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package logplugin

import (
	"fmt"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

// ---- helpers ----------------------------------------------------------------

// auditMsg builds a fake AuditLog entry as AuditLogWatcher would produce it.
// operation: QUERY / QUERY_DML / QUERY_DDL / CONNECT
// sql: the raw SQL text (will be quoted with single-quotes as MariaDB does)
// ts: timestamp string; pass "" for no timestamp (conservatively included)
func auditMsg(ts, operation, sql string) s18log.HttpMessage {
	// Reproduce the AuditLogWatcher output format:
	// .Text = "serverhost, username, host, connid, queryid, OPERATION, database, 'SQL',retcode"
	text := fmt.Sprintf("db1, root, localhost, 1, 1, %s, testdb, '%s',0", operation, sql)
	return s18log.HttpMessage{
		Level:     "INFO",
		Timestamp: ts,
		Text:      text,
	}
}

func recentAuditTS() string {
	return time.Now().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
}

func baselineAuditTS() string {
	// Within the baseline window (between 1h and 24h ago)
	return time.Now().Add(-6 * time.Hour).Format("2006-01-02 15:04:05")
}

func oldAuditTS() string {
	// Outside both windows
	return time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")
}

// ---- parseAuditEntry --------------------------------------------------------

func TestParseAuditEntry_ExtractsOperationAndSQL(t *testing.T) {
	msg := auditMsg("2024-01-02 15:04:05", "QUERY", "SELECT 1")
	ts, op, sql := parseAuditEntry(msg.Timestamp, msg.Text)
	if op != "QUERY" {
		t.Errorf("operation: got %q", op)
	}
	if sql != "SELECT 1" {
		t.Errorf("sql: got %q", sql)
	}
	if ts.IsZero() {
		t.Error("timestamp should be parsed")
	}
}

func TestParseAuditEntry_StripsQuotes(t *testing.T) {
	msg := auditMsg("", "QUERY_DML", "SELECT * FROM t WHERE id=1")
	_, _, sql := parseAuditEntry(msg.Timestamp, msg.Text)
	if sql != "SELECT * FROM t WHERE id=1" {
		t.Errorf("expected unquoted SQL, got %q", sql)
	}
}

func TestParseAuditEntry_TooFewFields(t *testing.T) {
	_, op, sql := parseAuditEntry("", "incomplete text")
	if op != "" || sql != "" {
		t.Error("expected empty op and sql for short text")
	}
}

// ---- parseOpsSet ------------------------------------------------------------

func TestParseOpsSet(t *testing.T) {
	set := parseOpsSet("QUERY,QUERY_DML, QUERY_DDL")
	for _, op := range []string{"QUERY", "QUERY_DML", "QUERY_DDL"} {
		if !set[op] {
			t.Errorf("expected %q in set", op)
		}
	}
	if set["CONNECT"] {
		t.Error("CONNECT should not be in set")
	}
}

// ---- Config helpers ---------------------------------------------------------

func TestConfigInt_Default(t *testing.T) {
	if ConfigInt(nil, "x", 99) != 99 {
		t.Error("nil map should return default")
	}
	if ConfigInt(map[string]string{}, "x", 42) != 42 {
		t.Error("missing key should return default")
	}
	if ConfigInt(map[string]string{"x": "notanint"}, "x", 7) != 7 {
		t.Error("unparseable value should return default")
	}
}

func TestConfigInt_ReadsValue(t *testing.T) {
	if ConfigInt(map[string]string{"timeframe-hours": "8"}, "timeframe-hours", 24) != 8 {
		t.Error("should read 8")
	}
}

func TestConfigStr_Default(t *testing.T) {
	if ConfigStr(nil, "k", "def") != "def" {
		t.Error("nil map should return default")
	}
}

func TestConfigBool(t *testing.T) {
	cases := [][2]string{{"true", "true"}, {"1", "true"}, {"yes", "true"}, {"false", "false"}, {"0", "false"}}
	for _, tc := range cases {
		want := tc[1] == "true"
		got := ConfigBool(map[string]string{"k": tc[0]}, "k", false)
		if got != want {
			t.Errorf("ConfigBool(%q) = %v, want %v", tc[0], got, want)
		}
	}
}

// ---- AuditLogDriftPlugin.Evaluate -------------------------------------------

func TestAuditDrift_NoEntries(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	src := LogSource{ServerURL: "127.0.0.1:3306"}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings for empty log, got %d", len(f))
	}
}

func TestAuditDrift_NoDivergence(t *testing.T) {
	// Same template in both current and baseline → no finding
	p := &AuditLogDriftPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			auditMsg(recentAuditTS(), "QUERY", "SELECT * FROM users WHERE id = 1"),
			auditMsg(baselineAuditTS(), "QUERY", "SELECT * FROM users WHERE id = 2"),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("same template in both windows should produce no finding, got %d", len(f))
	}
}

func TestAuditDrift_NewTemplateDetected(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			// Baseline: only SELECT queries
			auditMsg(baselineAuditTS(), "QUERY", "SELECT * FROM users WHERE id = 1"),
			auditMsg(baselineAuditTS(), "QUERY", "SELECT * FROM users WHERE id = 2"),
			// Current: a new DELETE template not seen in baseline
			auditMsg(recentAuditTS(), "QUERY", "SELECT * FROM users WHERE id = 3"),
			auditMsg(recentAuditTS(), "QUERY_DML", "DELETE FROM sessions WHERE expires < NOW()"),
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding for new template, got %d", len(f))
	}
	if f[0].ErrKey != ErrKeyAuditDrift {
		t.Errorf("wrong ErrKey: %s", f[0].ErrKey)
	}
	if f[0].Severity != SeverityWarning {
		t.Errorf("wrong severity: %s", f[0].Severity)
	}
}

func TestAuditDrift_SkipsNonQueryOps(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			auditMsg(baselineAuditTS(), "QUERY", "SELECT 1"),
			// CONNECT ops should be ignored by default
			auditMsg(recentAuditTS(), "CONNECT", ""),
		},
	}
	// Only CONNECT in current window (no QUERY), baseline has QUERY.
	// current_set is empty → no findings (not enough data).
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("CONNECT ops should be skipped, got %d findings", len(f))
	}
}

func TestAuditDrift_CustomWindowViaConfig(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	// Set current-window to 30 minutes — our recent ts (10min ago) is inside
	// but our baseline ts (6h ago) is still in baseline
	cfg := map[string]string{
		"current-window-hours":  "1",
		"baseline-window-hours": "12",
	}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		Config:    cfg,
		AuditLog: []s18log.HttpMessage{
			auditMsg(baselineAuditTS(), "QUERY", "SELECT id FROM orders"),
			auditMsg(recentAuditTS(), "QUERY_DDL", "ALTER TABLE orders ADD COLUMN status INT"),
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding with custom window, got %d", len(f))
	}
}

func TestAuditDrift_OldEntriesIgnored(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			// All entries are older than 24h → outside both windows
			auditMsg(oldAuditTS(), "QUERY", "SELECT 1"),
			auditMsg(oldAuditTS(), "QUERY_DML", "DELETE FROM t WHERE 1=1"),
		},
	}
	// Both windows empty → no findings (not enough data)
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("old entries should be outside both windows, got %d findings", len(f))
	}
}

func TestAuditDrift_NoTimestampConservativelyIncluded(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	// No timestamps → both current and baseline see all templates
	// Same template → no divergence
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			auditMsg("", "QUERY", "SELECT 1"),
			auditMsg("", "QUERY", "SELECT 2"),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("same template (no timestamp) should produce no finding, got %d", len(f))
	}
}

func TestAuditDrift_EmptyBaselineNoFalsePositive(t *testing.T) {
	// Only current-window entries, no baseline entries
	// → not enough data, should not raise
	p := &AuditLogDriftPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		AuditLog: []s18log.HttpMessage{
			auditMsg(recentAuditTS(), "QUERY", "SELECT * FROM t"),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("empty baseline should not raise a false positive, got %d findings", len(f))
	}
}

func TestAuditDrift_MaxNewTemplatesList(t *testing.T) {
	p := &AuditLogDriftPlugin{}
	cfg := map[string]string{"max-new-templates": "2"}

	// Build 5 distinct new templates in current window, 1 known template in baseline
	msgs := []s18log.HttpMessage{
		auditMsg(baselineAuditTS(), "QUERY", "SELECT 1"),
	}
	tables := []string{"a", "b", "c", "d", "e"}
	for _, tbl := range tables {
		msgs = append(msgs, auditMsg(recentAuditTS(), "QUERY_DDL",
			fmt.Sprintf("ALTER TABLE %s ADD COLUMN x INT", tbl)))
	}

	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		Config:    cfg,
		AuditLog:  msgs,
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(f))
	}
	// Description should mention "and N more" since max-new-templates=2 and we have 5
	if !containsStr(f[0].Description, "more") {
		t.Errorf("description should mention truncated templates: %s", f[0].Description)
	}
}
