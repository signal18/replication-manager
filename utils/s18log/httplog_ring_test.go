package s18log

import (
	"fmt"
	"testing"
)

func msg(i int) HttpMessage {
	return HttpMessage{Group: GroupNone, Level: "INFO", Timestamp: fmt.Sprintf("2026/11/20 10:00:%02d", i), Text: fmt.Sprintf("m%d", i)}
}

func TestHttpLogAddReportDropBoundsAndCounts(t *testing.T) {
	tl := NewHttpLog(3)
	drops := 0
	for i := 0; i < 10; i++ {
		if tl.AddReportDrop(msg(i)) {
			drops++
		}
		if len(tl.Buffer) > tl.Len+1 {
			t.Fatalf("buffer grew to %d past its bound %d", len(tl.Buffer), tl.Len+1)
		}
	}
	entries := tl.Entries()
	// Shift keeps Len previous entries plus the new one: Len+1 real entries at most
	if len(entries) != 4 {
		t.Fatalf("want 4 entries kept, got %d", len(entries))
	}
	if entries[0].Text != "m9" || entries[3].Text != "m6" {
		t.Fatalf("want newest first m9..m6, got %s..%s", entries[0].Text, entries[3].Text)
	}
	if drops != 6 {
		t.Fatalf("want 6 drops (10 added, 4 kept), got %d", drops)
	}
}

func TestHttpLogEntriesSkipsEmptySlots(t *testing.T) {
	tl := NewHttpLog(5)
	if n := len(tl.Entries()); n != 0 {
		t.Fatalf("a new ring holds no entry, got %d", n)
	}
	tl.Add(msg(1))
	if n := len(tl.Entries()); n != 1 {
		t.Fatalf("want 1 entry, got %d", n)
	}
}

func TestHttpLogResizeKeepsNewest(t *testing.T) {
	tl := NewHttpLog(5)
	for i := 0; i < 5; i++ {
		tl.Add(msg(i))
	}
	tl.Resize(2)
	entries := tl.Entries()
	if tl.Len != 2 || len(entries) != 2 || entries[0].Text != "m4" || entries[1].Text != "m3" {
		t.Fatalf("resize to 2 must keep m4,m3; got len=%d entries=%v", tl.Len, entries)
	}
	tl.Resize(0)
	if tl.Len != 2 {
		t.Fatalf("resize(0) must change nothing, got len %d", tl.Len)
	}
	tl.Resize(4)
	tl.Add(msg(9))
	if got := len(tl.Entries()); got != 3 {
		t.Fatalf("after growing to 4 and one add, want 3 entries, got %d", got)
	}
}
