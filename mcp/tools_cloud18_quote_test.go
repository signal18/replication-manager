package repmanmcp

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// overcommit_pct: unset keeps the infrastructure's cap (nil), an explicit 0 means no borrowing
func TestCloud18SpecFromOvercommitPct(t *testing.T) {
	req := func(args map[string]any) mcp.CallToolRequest {
		var r mcp.CallToolRequest
		r.Params.Arguments = args
		return r
	}
	if s := cloud18SpecFrom(req(map[string]any{"db_count": 2})); s.OvercommitPct != nil {
		t.Fatalf("unset must stay nil: %v", *s.OvercommitPct)
	}
	if s := cloud18SpecFrom(req(map[string]any{"overcommit_pct": 0})); s.OvercommitPct == nil || *s.OvercommitPct != 0 {
		t.Fatalf("an explicit 0 must be kept: %v", s.OvercommitPct)
	}
	if s := cloud18SpecFrom(req(map[string]any{"overcommit_pct": 25.0})); s.OvercommitPct == nil || *s.OvercommitPct != 25 {
		t.Fatalf("25: %v", s.OvercommitPct)
	}
}
