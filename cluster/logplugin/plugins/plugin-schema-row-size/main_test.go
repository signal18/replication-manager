package main

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// ---- varcharByteWidth --------------------------------------------------------

func TestVarcharByteWidth(t *testing.T) {
	cases := []struct {
		colType   string
		charset   string
		wantBytes int
		wantOK    bool
	}{
		{"varchar(255)", "utf8mb4", 1020, true},
		{"VARCHAR(50)", "latin1", 50, true},
		{"varchar(100)", "", 400, true}, // unknown charset falls back to conservative 4 bytes/char
		{"varchar(100)", "utf8mb3", 300, true},
		{"text", "utf8mb4", 0, false},
		{"blob", "", 0, false},
		{"int(11)", "", 0, false},
		{"char(10)", "latin1", 0, false}, // CHAR is not VARCHAR — intentionally excluded
	}
	for _, tc := range cases {
		gotBytes, gotOK := varcharByteWidth(tc.colType, tc.charset)
		if gotOK != tc.wantOK {
			t.Errorf("varcharByteWidth(%q, %q) ok = %v, want %v", tc.colType, tc.charset, gotOK, tc.wantOK)
			continue
		}
		if gotOK && gotBytes != tc.wantBytes {
			t.Errorf("varcharByteWidth(%q, %q) = %d bytes, want %d", tc.colType, tc.charset, gotBytes, tc.wantBytes)
		}
	}
}

// ---- innodbRowBudget ---------------------------------------------------------

func TestInnodbRowBudget_16KPage(t *testing.T) {
	// Documented ~8126-byte limit for the default 16K page must be reproduced exactly.
	got := innodbRowBudget("16384", "")
	if got != 8126 {
		t.Errorf("innodbRowBudget(16384, \"\") = %d, want 8126", got)
	}
}

func TestInnodbRowBudget_DefaultsWhenMissing(t *testing.T) {
	got := innodbRowBudget("", "")
	if got != 8126 {
		t.Errorf("innodbRowBudget(\"\", \"\") = %d, want 8126 (16K default)", got)
	}
	got = innodbRowBudget("not-a-number", "")
	if got != 8126 {
		t.Errorf("innodbRowBudget(garbage, \"\") = %d, want 8126 (16K default)", got)
	}
}

func TestInnodbRowBudget_SmallerPages(t *testing.T) {
	if got := innodbRowBudget("8192", ""); got != 8192/2-66 {
		t.Errorf("innodbRowBudget(8192, \"\") = %d, want %d", got, 8192/2-66)
	}
	if got := innodbRowBudget("4096", ""); got != 4096/2-66 {
		t.Errorf("innodbRowBudget(4096, \"\") = %d, want %d", got, 4096/2-66)
	}
}

func TestInnodbRowBudget_64KPageIsSpecialCased(t *testing.T) {
	got := innodbRowBudget("65536", "")
	halfPage := int64(65536 / 2)
	if got >= halfPage {
		t.Errorf("innodbRowBudget(65536, \"\") = %d, must be well under half-page (%d)", got, halfPage)
	}
}

func TestInnodbRowBudget_CompressedRowFormatDiscount(t *testing.T) {
	uncompressed := innodbRowBudget("16384", "Dynamic")
	compressed := innodbRowBudget("16384", "Compressed")
	if compressed >= uncompressed {
		t.Errorf("compressed budget (%d) should be lower than uncompressed budget (%d)", compressed, uncompressed)
	}
}

// ---- end-to-end finding logic (via main()'s table loop, exercised through a
// small helper since main() itself talks to stdin/stdout) -------------------

func TestEvaluateTables_FlagsWideRow(t *testing.T) {
	// 40 VARCHAR(63) utf8mb4 columns: 63*4=252 bytes (under the 256-byte inline
	// threshold) + 1-byte prefix = 253 each. 40 * 253 = 10120, past the
	// ~8126-byte 16K-page budget.
	cols := make([]wire.TableColumn, 0, 40)
	for i := 0; i < 40; i++ {
		cols = append(cols, wire.TableColumn{Name: "c", Type: "varchar(63)", Charset: "utf8mb4"})
	}
	req := wire.Request{
		ServerVariables: map[string]string{"innodb_page_size": "16384"},
		Tables: []wire.Table{
			{Schema: "s", Name: "wide", Engine: "InnoDB", RowFormat: "Dynamic", Columns: cols},
		},
	}
	findings := evaluateTables(req)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].ErrKey != "SCH0001" {
		t.Errorf("wrong ErrKey: %s", findings[0].ErrKey)
	}
	if findings[0].Severity != "SCHEMA" {
		t.Errorf("wrong severity: %s", findings[0].Severity)
	}
}

func TestEvaluateTables_SkipsNonInnoDB(t *testing.T) {
	cols := make([]wire.TableColumn, 0, 40)
	for i := 0; i < 40; i++ {
		cols = append(cols, wire.TableColumn{Name: "c", Type: "varchar(63)", Charset: "utf8mb4"})
	}
	req := wire.Request{
		ServerVariables: map[string]string{"innodb_page_size": "16384"},
		Tables: []wire.Table{
			{Schema: "s", Name: "wide", Engine: "MyISAM", RowFormat: "Dynamic", Columns: cols},
		},
	}
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings for non-InnoDB table, got %d", len(findings))
	}
}

func TestEvaluateTables_SkipsWideVarcharsPast256Bytes(t *testing.T) {
	// VARCHAR(1000) latin1 = 1000 bytes, at/above the 256-byte inline threshold —
	// deliberately excluded from the sum regardless of count.
	cols := make([]wire.TableColumn, 0, 20)
	for i := 0; i < 20; i++ {
		cols = append(cols, wire.TableColumn{Name: "c", Type: "varchar(1000)", Charset: "latin1"})
	}
	req := wire.Request{
		ServerVariables: map[string]string{"innodb_page_size": "16384"},
		Tables: []wire.Table{
			{Schema: "s", Name: "wide", Engine: "InnoDB", RowFormat: "Dynamic", Columns: cols},
		},
	}
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings when all VARCHARs are >= inline threshold, got %d", len(findings))
	}
}

func TestEvaluateTables_UnderBudgetNotFlagged(t *testing.T) {
	req := wire.Request{
		ServerVariables: map[string]string{"innodb_page_size": "16384"},
		Tables: []wire.Table{
			{Schema: "s", Name: "small", Engine: "InnoDB", RowFormat: "Dynamic", Columns: []wire.TableColumn{
				{Name: "a", Type: "varchar(50)", Charset: "latin1"},
				{Name: "b", Type: "varchar(50)", Charset: "latin1"},
			}},
		},
	}
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings under budget, got %d", len(findings))
	}
}

// TestEvaluateTables_MultipleOffendingTablesAggregateIntoOneFinding guards
// against the state-collision bug: repman's state machine keys open states
// as ErrKey@server and only keeps the first write per key for a monitoring
// cycle (state.Map.Add), so multiple SCH0001 findings for the same server
// would silently drop every table after the first. All offenses must be
// folded into a single finding.
func TestEvaluateTables_MultipleOffendingTablesAggregateIntoOneFinding(t *testing.T) {
	wideCols := make([]wire.TableColumn, 0, 40)
	for i := 0; i < 40; i++ {
		wideCols = append(wideCols, wire.TableColumn{Name: "c", Type: "varchar(63)", Charset: "utf8mb4"})
	}
	req := wire.Request{
		ServerVariables: map[string]string{"innodb_page_size": "16384"},
		Tables: []wire.Table{
			{Schema: "s", Name: "wide_a", Engine: "InnoDB", RowFormat: "Dynamic", Columns: wideCols},
			{Schema: "s", Name: "wide_b", Engine: "InnoDB", RowFormat: "Dynamic", Columns: wideCols},
		},
	}
	findings := evaluateTables(req)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 aggregated finding for 2 offending tables (state-collision guard), got %d", len(findings))
	}
	if !strings.Contains(findings[0].Description, "wide_a") || !strings.Contains(findings[0].Description, "wide_b") {
		t.Errorf("expected both offending tables named in the aggregated description, got: %s", findings[0].Description)
	}
	if !strings.Contains(findings[0].Description, "2 InnoDB table(s)") {
		t.Errorf("expected offense count in description, got: %s", findings[0].Description)
	}
}
