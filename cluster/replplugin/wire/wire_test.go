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

func TestTSVAndCheckpointNames(t *testing.T) {
	row := []interface{}{nil, "a\tb\nc\\d", []byte{0, 'x'}, int64(-5), "", "N"}
	line := EncodeTSV(nil, row)
	if string(line) != "\\N\ta\\tb\\nc\\\\d\t\\0x\t-5\t\tN\n" {
		t.Fatalf("encoded %q", line)
	}
	got := DecodeTSV(line[:len(line)-1])
	want := [][]byte{nil, []byte("a\tb\nc\\d"), {0, 'x'}, []byte("-5"), {}, []byte("N")}
	if len(got) != len(want) {
		t.Fatalf("decoded %q", got)
	}
	for i := range want {
		if (want[i] == nil) != (got[i] == nil) || string(want[i]) != string(got[i]) {
			t.Fatalf("field %d: %q, want %q", i, got[i], want[i])
		}
	}
	if n, ok := ParseCheckpointName(CheckpointName(42)); !ok || n != 42 {
		t.Fatalf("checkpoint name %s", CheckpointName(42))
	}
	if _, ok := ParseCheckpointName("000000000042.tar.gz.tmp"); ok {
		t.Fatal("a temporary file is no checkpoint")
	}
}
