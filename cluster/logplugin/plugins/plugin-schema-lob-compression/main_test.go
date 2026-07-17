package main

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// ---- isLobType ----------------------------------------------------------------

func TestIsLobType(t *testing.T) {
	cases := []struct {
		colType string
		want    bool
	}{
		{"text", true},
		{"TEXT", true},
		{"mediumtext", true},
		{"longblob", true},
		{"blob", true},
		{"varchar(255)", false},
		{"int(11)", false},
	}
	for _, tc := range cases {
		if got := isLobType(tc.colType); got != tc.want {
			t.Errorf("isLobType(%q) = %v, want %v", tc.colType, got, tc.want)
		}
	}
}

// ---- evaluateTables -------------------------------------------------------------

func mariaDBRequest(tables []wire.Table) wire.Request {
	return wire.Request{
		ServerVersion: wire.ServerVersion{Flavor: "MariaDB", Major: 10, Minor: 6},
		Tables:        tables,
	}
}

func TestEvaluateTables_FlagsLargeUncompressedColumn(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "payload", Type: "text", Compressed: false, AvgByteLength: 9000},
		}},
	})
	findings := evaluateTables(req)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].ErrKey != "SCH0002" {
		t.Errorf("wrong ErrKey: %s", findings[0].ErrKey)
	}
	if findings[0].Severity != "SCHEMA" {
		t.Errorf("wrong severity: %s", findings[0].Severity)
	}
}

func TestEvaluateTables_SkipsCompressedColumn(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "payload", Type: "text", Compressed: true, AvgByteLength: 9000},
		}},
	})
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings for a compressed column, got %d", len(findings))
	}
}

func TestEvaluateTables_SkipsUnderThreshold(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "payload", Type: "text", Compressed: false, AvgByteLength: 100},
		}},
	})
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings under threshold, got %d", len(findings))
	}
}

func TestEvaluateTables_SkipsUnsampledColumn(t *testing.T) {
	// AvgByteLength == 0 means "not sampled" (small table, sampling failed, etc.)
	// — must not be treated as "0 bytes, therefore fine" nor flagged.
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "payload", Type: "text", Compressed: false, AvgByteLength: 0},
		}},
	})
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings for an unsampled column, got %d", len(findings))
	}
}

func TestEvaluateTables_SkipsNonLobColumn(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "note", Type: "varchar(255)", Compressed: false, AvgByteLength: 9000},
		}},
	})
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings for a non-LOB column, got %d", len(findings))
	}
}

func TestEvaluateTables_SkipsNonMariaDB(t *testing.T) {
	req := wire.Request{
		ServerVersion: wire.ServerVersion{Flavor: "MySQL", Major: 8, Minor: 0},
		Tables: []wire.Table{
			{Schema: "s", Name: "t", Columns: []wire.TableColumn{
				{Name: "payload", Type: "text", Compressed: false, AvgByteLength: 9000},
			}},
		},
	}
	if findings := evaluateTables(req); len(findings) != 0 {
		t.Errorf("expected 0 findings for non-MariaDB flavor, got %d", len(findings))
	}
}

func TestEvaluateTables_ConfiguredThreshold(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t", Columns: []wire.TableColumn{
			{Name: "payload", Type: "text", Compressed: false, AvgByteLength: 500},
		}},
	})
	req.Config = map[string]string{"avg-length-threshold-bytes": "100"}
	if findings := evaluateTables(req); len(findings) != 1 {
		t.Errorf("expected 1 finding with lowered threshold, got %d", len(findings))
	}
}

// TestEvaluateTables_MultipleOffendingColumnsAggregateIntoOneFinding guards
// against the state-collision bug: repman's state machine keys open states
// as ErrKey@server and only keeps the first write per key for a monitoring
// cycle (state.Map.Add), so multiple SCH0002 findings for the same server
// would silently drop every column after the first. All offenses must be
// folded into a single finding.
func TestEvaluateTables_MultipleOffendingColumnsAggregateIntoOneFinding(t *testing.T) {
	req := mariaDBRequest([]wire.Table{
		{Schema: "s", Name: "t1", Columns: []wire.TableColumn{
			{Name: "body", Type: "longtext", Compressed: false, AvgByteLength: 9000},
		}},
		{Schema: "s", Name: "t2", Columns: []wire.TableColumn{
			{Name: "payload", Type: "blob", Compressed: false, AvgByteLength: 20000},
		}},
	})
	findings := evaluateTables(req)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 aggregated finding for 2 offending columns (state-collision guard), got %d", len(findings))
	}
	if !strings.Contains(findings[0].Description, "t1.body") || !strings.Contains(findings[0].Description, "t2.payload") {
		t.Errorf("expected both offending columns named in the aggregated description, got: %s", findings[0].Description)
	}
	if !strings.Contains(findings[0].Description, "2 BLOB/TEXT column(s)") {
		t.Errorf("expected offense count in description, got: %s", findings[0].Description)
	}
}
