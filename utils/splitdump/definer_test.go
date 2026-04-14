package splitdump

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// TestNewDefinerStrippingReaderPlainSQL verifies that DEFINER clauses are removed and all
// other content is passed through unchanged on a plain (non-compressed) reader.
func TestNewDefinerStrippingReaderPlainSQL(t *testing.T) {
	input := strings.Join([]string{
		"CREATE DEFINER=`root`@`localhost` VIEW `v` AS SELECT 1;",
		"-- plain comment",
		"CREATE TABLE `t` (`id` INT);",
	}, "\n") + "\n"

	r, done := NewDefinerStrippingReader(strings.NewReader(input))
	got, err := io.ReadAll(r)
	done(err)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := string(got)
	if strings.Contains(strings.ToUpper(result), "DEFINER=") {
		t.Fatalf("DEFINER clause still present:\n%s", result)
	}
	if !strings.Contains(result, "SELECT 1") {
		t.Fatalf("expected SELECT 1 to be preserved:\n%s", result)
	}
	if !strings.Contains(result, "plain comment") {
		t.Fatalf("expected comment to be preserved:\n%s", result)
	}
	if !strings.Contains(result, "CREATE TABLE") {
		t.Fatalf("expected CREATE TABLE to be preserved:\n%s", result)
	}
}

// TestNewDefinerStrippingReaderGzip verifies that DEFINER stripping works transparently on
// a gzip-compressed reader (the caller decompresses before passing to the helper).
func TestNewDefinerStrippingReaderGzip(t *testing.T) {
	input := "CREATE DEFINER=`u`@`h` PROCEDURE `p`() BEGIN SELECT 42; END;\n"
	expected := "CREATE  PROCEDURE `p`() BEGIN SELECT 42; END;\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(input)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	gzr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gzr.Close()

	r, done := NewDefinerStrippingReader(gzr)
	got, readErr := io.ReadAll(r)
	done(readErr)
	if readErr != nil {
		t.Fatalf("unexpected error: %v", readErr)
	}

	if string(got) != expected {
		t.Fatalf("got %q, want %q", string(got), expected)
	}
}

// TestNewDefinerStrippingReaderLargeLine verifies that a line larger than bufio.Scanner's
// default token limit (64 KiB) and the old 4 MiB limit does not cause an error.
// This is the core regression test for the ErrTooLong failure that bufio.Scanner would produce.
func TestNewDefinerStrippingReaderLargeLine(t *testing.T) {
	// Build a line that exceeds 4 MiB — the limit used by the old bufio.Scanner approach.
	bigPayload := strings.Repeat("x", 5*1024*1024) // 5 MiB of 'x'
	input := "DEFINER=`admin`@`%` " + bigPayload + "\n"
	// After stripping: the DEFINER prefix is removed, leaving " " + bigPayload + "\n"
	wantLen := 1 + len(bigPayload) + 1 // space + payload + newline

	r, done := NewDefinerStrippingReader(strings.NewReader(input))
	got, err := io.ReadAll(r)
	done(err)
	if err != nil {
		t.Fatalf("unexpected error reading large line: %v", err)
	}
	if len(got) != wantLen {
		t.Fatalf("unexpected output length: got %d, want %d", len(got), wantLen)
	}
	if strings.Contains(strings.ToUpper(string(got)), "DEFINER=") {
		t.Fatalf("DEFINER clause still present in large-line output")
	}
}

// TestNewDefinerStrippingReaderMultipleDefiners verifies that multiple DEFINER clauses on
// separate lines are each stripped independently.
func TestNewDefinerStrippingReaderMultipleDefiners(t *testing.T) {
	input := "DEFINER=`a`@`b` VIEW v1 AS SELECT 1;\nDEFINER=`c`@`d` VIEW v2 AS SELECT 2;\n"
	r, done := NewDefinerStrippingReader(strings.NewReader(input))
	got, err := io.ReadAll(r)
	done(err)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := string(got)
	if strings.Contains(strings.ToUpper(result), "DEFINER=") {
		t.Fatalf("DEFINER clause still present:\n%s", result)
	}
	if !strings.Contains(result, "SELECT 1") || !strings.Contains(result, "SELECT 2") {
		t.Fatalf("expected both SELECT statements preserved:\n%s", result)
	}
}

// TestNewDefinerStrippingReaderEmptyInput verifies correct behavior on an empty reader.
func TestNewDefinerStrippingReaderEmptyInput(t *testing.T) {
	r, done := NewDefinerStrippingReader(strings.NewReader(""))
	got, err := io.ReadAll(r)
	done(err)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty output, got %q", string(got))
	}
}

// TestIsMysqlSystemAllBaseNameRequired verifies that IsMysqlSystemAll only matches
// the base filename, not a full path. Callers must pass filepath.Base(path).
func TestIsMysqlSystemAllBaseNameRequired(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"mysql.system-all.sql.gz", true},
		{"mysql.system-all.sql", true},
		{"mysql.system-all", true},
		{"MYSQL.SYSTEM-ALL.SQL.GZ", true}, // case-insensitive
		{"/some/dir/mysql.system-all.sql.gz", false}, // full path must not match
		{"other.system-all.sql.gz", false},
		{"mysql.system-all.sql.gz.bak", false},
	}
	for _, tc := range cases {
		got := IsMysqlSystemAll(tc.input)
		if got != tc.want {
			t.Errorf("IsMysqlSystemAll(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
