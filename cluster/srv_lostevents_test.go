package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lost-events viewer paginates by BINARY POSITION: every page must be
// line-aligned, NextPos must chain pages without loss or overlap, and EOF
// must fire exactly at the end whatever the byte budget.
func TestReadLostEventsPage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delta.sql")
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("### INSERT INTO test.t VALUES (")
		b.WriteString(strings.Repeat("x", i%50))
		b.WriteString(")\n")
	}
	content := b.String()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Walk the whole file with a small byte budget and rebuild it.
	var rebuilt strings.Builder
	pos := int64(0)
	pages := 0
	for {
		page, err := ReadLostEventsPage(path, pos, 4096)
		if err != nil {
			t.Fatalf("page at %d: %v", pos, err)
		}
		for _, l := range page.Lines {
			rebuilt.WriteString(l)
			rebuilt.WriteString("\n")
		}
		if page.NextPos <= pos && !page.EOF {
			t.Fatalf("cursor did not advance at %d", pos)
		}
		pos = page.NextPos
		pages++
		if page.EOF {
			break
		}
		if pages > 10000 {
			t.Fatal("pagination did not terminate")
		}
	}
	if rebuilt.String() != content {
		t.Fatalf("pagination lost or duplicated content: got %d bytes, want %d", rebuilt.Len(), len(content))
	}
	if pages < 2 {
		t.Fatalf("expected multiple pages, got %d", pages)
	}

	// Out-of-range position answers EOF, not an error.
	page, err := ReadLostEventsPage(path, int64(len(content))+100, 4096)
	if err != nil || !page.EOF {
		t.Fatalf("expected clean EOF past the end, got page=%+v err=%v", page, err)
	}
}
