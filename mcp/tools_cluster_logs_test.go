package repmanmcp

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

// list-cluster-logs filters: minimum level (default warning), module tag, limit,
// newest first, empty ring slots skipped, module exposed by name.
func TestFilterLogEntries(t *testing.T) {
	buf := []s18log.HttpMessage{
		{Timestamp: "5", Level: "ERROR", Module: config.ConstLogModTopology, Text: "no master"},
		{Timestamp: "4", Level: "INFO", Module: config.ConstLogModGeneral, Text: "tick"},
		{Timestamp: "3", Level: "WARN", Module: config.ConstLogModOrchestrator, Text: "resize refused"},
		{},
		{Timestamp: "2", Level: "STATE", Module: config.ConstLogModGeneral, Text: "OPENED WARN0148"},
		{Timestamp: "1", Level: "DEBUG", Module: config.ConstLogModTopology, Text: "noise"},
	}
	got := filterLogEntries(buf, "warning", "", 0)
	if len(got) != 3 || got[0].Text != "no master" || got[1].Text != "resize refused" || got[2].Text != "OPENED WARN0148" {
		t.Fatalf("warning and above, newest first, state lines kept: %+v", got)
	}
	if got[0].Module != config.GetTagsForLog(config.ConstLogModTopology) || got[0].Module == "" {
		t.Fatalf("module must be the tag name: %+v", got[0])
	}
	if got := filterLogEntries(buf, "debug", "", 0); len(got) != 5 {
		t.Fatalf("debug must return every non-empty entry: %d", len(got))
	}
	if got := filterLogEntries(buf, "error", "", 0); len(got) != 1 {
		t.Fatalf("error only: %+v", got)
	}
	if got := filterLogEntries(buf, "debug", config.GetTagsForLog(config.ConstLogModTopology), 0); len(got) != 2 {
		t.Fatalf("module filter on topology: %+v", got)
	}
	if got := filterLogEntries(buf, "debug", "", 2); len(got) != 2 || got[0].Timestamp != "5" {
		t.Fatalf("limit keeps the newest: %+v", got)
	}
	if alertsOf(nil)["errors"] == nil {
		t.Fatalf("a nil state machine answers empty lists")
	}
}
