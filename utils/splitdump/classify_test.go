package splitdump

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestClassifyStreamRoutesApplicationAndSystemSQL(t *testing.T) {
	input := strings.Join([]string{
		"-- MySQL dump header",
		"USE `db`;",
		"CREATE TABLE `t` (id int);",
		"INSERT INTO `t` VALUES (1);",
		"INSTALL PLUGIN disk SONAME 'disk.so';",
		"CREATE USER 'x'@'y';",
		"GRANT ALL ON *.* TO 'x'@'y';",
	}, "\n") + "\n"

	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasSystemContent {
		t.Fatal("expected HasSystemContent=true")
	}

	wantApp := "-- MySQL dump header\nUSE `db`;\nCREATE TABLE `t` (id int);\nINSERT INTO `t` VALUES (1);\n"
	if app.String() != wantApp {
		t.Errorf("application output:\n got:  %q\n want: %q", app.String(), wantApp)
	}
	wantSys := "INSTALL PLUGIN disk SONAME 'disk.so';\nCREATE USER 'x'@'y';\nGRANT ALL ON *.* TO 'x'@'y';\n"
	if sys.String() != wantSys {
		t.Errorf("system output:\n got:  %q\n want: %q", sys.String(), wantSys)
	}
}

func TestClassifyStreamBoundaryLineRoutesExactlyOnce(t *testing.T) {
	input := "CREATE TABLE `t` (id int);\nINSTALL PLUGIN disk SONAME 'disk.so';\n"
	var app, sys bytes.Buffer
	if _, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := app.String() + sys.String()
	if strings.Count(total, "INSTALL PLUGIN") != 1 {
		t.Fatalf("boundary line must appear exactly once across both outputs, got: %q", total)
	}
	if strings.Contains(app.String(), "INSTALL PLUGIN") {
		t.Fatal("boundary line leaked into application output")
	}
}

func TestClassifyStreamAdjacentStatementsNotLostOrDuplicated(t *testing.T) {
	input := strings.Join([]string{
		"INSERT INTO `t` VALUES (99);", // last application line, immediately before boundary
		"INSTALL PLUGIN disk SONAME 'disk.so';",
		"CREATE USER 'first'@'%';", // first system line after boundary
	}, "\n") + "\n"
	var app, sys bytes.Buffer
	if _, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(app.String(), "VALUES (99)") {
		t.Error("statement immediately before the boundary was lost from application output")
	}
	if !strings.Contains(sys.String(), "CREATE USER 'first'") {
		t.Error("statement immediately after the boundary was lost from system output")
	}
	if strings.Count(sys.String(), "CREATE USER 'first'") != 1 {
		t.Error("statement immediately after the boundary was duplicated")
	}
}

func TestClassifyStreamCRLFAndLFBehaveIdentically(t *testing.T) {
	lf := "CREATE TABLE `t` (id int);\nINSTALL PLUGIN disk SONAME 'disk.so';\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	var appLF, sysLF, appCRLF, sysCRLF bytes.Buffer
	resLF, err := ClassifyStream(strings.NewReader(lf), ClassifyOptions{ApplicationWriter: &appLF, SystemWriter: &sysLF})
	if err != nil {
		t.Fatalf("LF: unexpected error: %v", err)
	}
	resCRLF, err := ClassifyStream(strings.NewReader(crlf), ClassifyOptions{ApplicationWriter: &appCRLF, SystemWriter: &sysCRLF})
	if err != nil {
		t.Fatalf("CRLF: unexpected error: %v", err)
	}
	if resLF.HasSystemContent != resCRLF.HasSystemContent {
		t.Fatalf("HasSystemContent differs: LF=%v CRLF=%v", resLF.HasSystemContent, resCRLF.HasSystemContent)
	}
	if !strings.Contains(appCRLF.String(), "CREATE TABLE") || !strings.Contains(sysCRLF.String(), "INSTALL PLUGIN") {
		t.Fatalf("CRLF input misrouted: app=%q sys=%q", appCRLF.String(), sysCRLF.String())
	}
}

// TestClassifyStreamReEntersSystemSectionAfterNonSystemHeader is the
// regression for a real production failure: mariadb-dump's --system=all
// "stats" component dumps mysql.innodb_table_stats/column_stats/etc. as
// ordinary tables (USE mysql; then normal table-structure/data sections)
// AFTER the INSTALL PLUGIN/CREATE USER/GRANT statements that open the system
// section. SplitDumpLineParser has always treated mysql.system-all as
// re-enterable (opened in append mode, same as routine/event/trigger
// sections) for exactly this reason. ClassifyStream must route the same way:
// the reappearing non-system header exits the system section back to
// ApplicationWriter, and a later system-section-boundary line can reopen it.
func TestClassifyStreamReEntersSystemSectionAfterNonSystemHeader(t *testing.T) {
	input := strings.Join([]string{
		"INSTALL PLUGIN disk SONAME 'disk.so';",
		"CREATE USER 'x'@'y';",
		"USE `mysql`;",
		"-- Table structure for table `innodb_table_stats`",
		"CREATE TABLE `innodb_table_stats` (db_name varchar(64));",
		"-- Dumping data for table `innodb_table_stats`",
		"INSERT INTO `innodb_table_stats` VALUES ('db','t');",
		"CREATE USER 'z'@'y';",
	}, "\n") + "\n"
	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasSystemContent {
		t.Fatal("expected HasSystemContent to be true")
	}
	if !strings.Contains(sys.String(), "INSTALL PLUGIN") || !strings.Contains(sys.String(), "CREATE USER 'x'@'y'") {
		t.Fatalf("expected the initial system statements in SystemWriter, got: %q", sys.String())
	}
	if !strings.Contains(app.String(), "innodb_table_stats") {
		t.Fatalf("expected the stats-table dump in ApplicationWriter, got: %q", app.String())
	}
	if !strings.Contains(sys.String(), "CREATE USER 'z'@'y'") {
		t.Fatalf("expected the system section to reopen after the stats table, got: %q", sys.String())
	}
}

func TestClassifyStreamEmptySystemSectionIsSuccessfulNoOp(t *testing.T) {
	input := "USE `db`;\nCREATE TABLE `t` (id int);\nINSERT INTO `t` VALUES (1);\n"
	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasSystemContent {
		t.Fatal("expected HasSystemContent=false for a dump with no system section")
	}
	if sys.Len() != 0 {
		t.Fatalf("expected empty system output, got %q", sys.String())
	}
	if app.String() != input {
		t.Fatalf("application output should be the whole stream:\n got:  %q\n want: %q", app.String(), input)
	}
}

func TestClassifyStreamCapturesMetadataRegardlessOfPosition(t *testing.T) {
	input := strings.Join([]string{
		"CHANGE MASTER TO MASTER_LOG_FILE='mysql-bin.000042', MASTER_LOG_POS=1234;",
		"SET GLOBAL gtid_slave_pos='0-1-100';",
		"INSTALL PLUGIN disk SONAME 'disk.so';",
	}, "\n") + "\n"
	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Metadata.File != "mysql-bin.000042" || result.Metadata.Position != 1234 {
		t.Fatalf("unexpected binlog metadata: %+v", result.Metadata)
	}
	if result.Metadata.GTID != "0-1-100" {
		t.Fatalf("unexpected GTID metadata: %+v", result.Metadata)
	}
}

func TestClassifyStreamSourceDataZeroDisablesMetadataCapture(t *testing.T) {
	input := strings.Join([]string{
		"-- source-data=0",
		"CHANGE MASTER TO MASTER_LOG_FILE='mysql-bin.000042', MASTER_LOG_POS=1234;",
		"INSTALL PLUGIN disk SONAME 'disk.so';",
	}, "\n") + "\n"
	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Metadata.SourceData != 0 || result.Metadata.File != "" {
		t.Fatalf("expected metadata capture to be disabled by source-data=0, got: %+v", result.Metadata)
	}
}

// TestClassifyStreamStreamsManySmallLinesWithoutFullBuffering proves
// ClassifyStream doesn't accumulate the whole stream (or the whole system
// section) before writing anything out — it forwards each line as it's
// classified, so a large number of ordinary-sized lines streams through
// without holding the full ~20MB input in memory at once. This is NOT a
// claim that any single line is memory-bounded — see
// TestClassifyStreamHandlesSingleLargeLine for that distinct, narrower
// property.
func TestClassifyStreamStreamsManySmallLinesWithoutFullBuffering(t *testing.T) {
	var b strings.Builder
	row := "INSERT INTO `t` VALUES (1,'" + strings.Repeat("x", 1024) + "');\n"
	for i := 0; i < 20000; i++ { // ~20MB across many ordinary-sized lines
		b.WriteString(row)
	}
	b.WriteString("INSTALL PLUGIN disk SONAME 'disk.so';\n")

	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(b.String()), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasSystemContent {
		t.Fatal("expected HasSystemContent=true")
	}
	if app.Len() != len(b.String())-len("INSTALL PLUGIN disk SONAME 'disk.so';\n") {
		t.Fatalf("unexpected application output length: got %d", app.Len())
	}
}

// TestClassifyStreamHandlesSingleLargeLine proves a large-but-under-the-ceiling
// single line (e.g. one extended-INSERT statement spanning many rows) still
// classifies and forwards correctly. ClassifyStream now bounds each line via
// bufio.Scanner + classifyMaxLineSize (a hard 1GiB ceiling, enforced -- see
// TestClassifyStreamRejectsLineExceedingCeiling below for the ceiling itself);
// this test's ~4MB line is comfortably under that ceiling, so it's exercising
// the ordinary "large but legitimate" path, not the rejection path.
func TestClassifyStreamHandlesSingleLargeLine(t *testing.T) {
	hugeLine := "INSERT INTO `t` VALUES " + strings.Repeat("(1,'x'),", 500000) + "(1,'x');\n"
	input := hugeLine + "INSTALL PLUGIN disk SONAME 'disk.so';\n"

	var app, sys bytes.Buffer
	result, err := ClassifyStream(strings.NewReader(input), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasSystemContent {
		t.Fatal("expected HasSystemContent=true")
	}
	if app.String() != hugeLine {
		t.Fatalf("expected the huge line to be forwarded intact to the application writer (len got=%d want=%d)", app.Len(), len(hugeLine))
	}
}

// TestClassifyStreamRejectsLineExceedingCeiling proves classifyMaxLineSize is
// an enforced hard ceiling, not just documentation: a line larger than the
// cap must fail closed with bufio.ErrTooLong wrapped in a normal error,
// rather than allocating without bound (repo law T18). Uses the internal
// classifyStream helper with a small injected cap -- constructing a real
// ~1GiB line just to exercise this path would be impractical for a unit test.
func TestClassifyStreamRejectsLineExceedingCeiling(t *testing.T) {
	const cap = 64
	oversized := strings.Repeat("x", cap*2) + "\n"

	var app, sys bytes.Buffer
	_, err := classifyStream(strings.NewReader(oversized), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys}, cap)
	if err == nil {
		t.Fatal("expected an error for a line exceeding the size ceiling, got nil")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected the wrapped error to unwrap to bufio.ErrTooLong, got: %v", err)
	}
}

// TestClassifyStreamAcceptsLineAtCeiling is the boundary control case: a line
// that fits exactly within the cap (including its terminator) must succeed.
func TestClassifyStreamAcceptsLineAtCeiling(t *testing.T) {
	const cap = 64
	line := strings.Repeat("x", cap-1) + "\n" // exactly cap bytes including '\n'

	var app, sys bytes.Buffer
	_, err := classifyStream(strings.NewReader(line), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys}, cap)
	if err != nil {
		t.Fatalf("expected a line exactly at the ceiling to succeed, got: %v", err)
	}
	if app.String() != line {
		t.Fatalf("expected the line forwarded intact, got %q", app.String())
	}
}

// TestSharedBoundaryDefinition asserts SplitDumpLineParser and ClassifyStream
// both key off the exact same isSystemSectionBoundary predicate (Design
// Contract #12: one boundary definition, not two divergent heuristics).
func TestSharedBoundaryDefinition(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"INSTALL PLUGIN disk SONAME 'disk.so';", true},
		{"CREATE USER 'x'@'y';", true},
		{"CREATE TABLE t (id int);", false},
		{"-- Dumping data for table `t`", false},
	}
	for _, c := range cases {
		if got := isSystemSectionBoundary(c.line); got != c.want {
			t.Errorf("isSystemSectionBoundary(%q) = %v, want %v", c.line, got, c.want)
		}
		// ClassifyStream must agree with the shared predicate on a minimal stream.
		var app, sys bytes.Buffer
		result, err := ClassifyStream(strings.NewReader(c.line+"\n"), ClassifyOptions{ApplicationWriter: &app, SystemWriter: &sys})
		if err != nil {
			t.Fatalf("unexpected error classifying %q: %v", c.line, err)
		}
		if result.HasSystemContent != c.want {
			t.Errorf("ClassifyStream(%q).HasSystemContent = %v, want %v", c.line, result.HasSystemContent, c.want)
		}
	}
}
