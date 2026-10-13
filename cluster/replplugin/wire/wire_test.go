package wire

import (
	"path/filepath"
	"testing"
)

func TestRulesAndState(t *testing.T) {
	c := Config{Rules: []Rule{{Schema: "repl_test"}, {Schema: "shop", Tables: []string{"orders", "items"}}}}
	for _, tt := range []struct {
		schema, table string
		want          bool
	}{
		{"repl_test", "pk_int", true}, {"shop", "orders", true}, {"shop", "users", false}, {"replication_manager_schema", "sbtest1", false},
	} {
		if got := c.Matches(tt.schema, tt.table); got != tt.want {
			t.Errorf("Matches(%s.%s) = %v, want %v", tt.schema, tt.table, got, tt.want)
		}
	}
	p := StatePath(filepath.Join(t.TempDir(), "columnstore.toml"))
	if filepath.Base(p) != "columnstore.state.json" {
		t.Fatalf("state path %s", p)
	}
	if s, err := ReadState(p); err != nil || s.GTID != "" {
		t.Fatalf("no state yet: %v %v", s, err)
	}
	if err := WriteState(p, State{GTID: "4-26814838-3061674"}); err != nil {
		t.Fatal(err)
	}
	if s, err := ReadState(p); err != nil || s.GTID != "4-26814838-3061674" {
		t.Fatalf("read back %v %v", s, err)
	}
}
