package main

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func idx(name string, unique bool, cols ...string) wire.TableIndex {
	ix := wire.TableIndex{Name: name, Unique: unique, Primary: name == "PRIMARY", Type: "BTREE"}
	for _, c := range cols {
		ix.Columns = append(ix.Columns, wire.IndexColumn{Name: c})
	}
	return ix
}

func table(indexes ...wire.TableIndex) wire.Table {
	return wire.Table{Schema: "shop", Name: "orders", Engine: "InnoDB", IndexLength: 4 << 20, Indexes: indexes}
}

func names(rs []redundancy) map[string]string {
	m := map[string]string{}
	for _, r := range rs {
		m[r.redundant.Name] = r.coveredBy.Name
	}
	return m
}

func TestPrefixIsRedundant(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("k_ab", false, "a", "b"))))
	if got["k_a"] != "k_ab" || len(got) != 1 {
		t.Fatalf("want k_a covered by k_ab only, got %v", got)
	}
}

func TestExactDuplicateReportsLexicallyGreaterName(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("k_b", false, "a", "b"), idx("k_a", false, "a", "b"))))
	if got["k_b"] != "k_a" || len(got) != 1 {
		t.Fatalf("want k_b (greater name) covered by k_a, got %v", got)
	}
}

func TestUniqueNotCoveredByNonUnique(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("u_a", true, "a"), idx("k_ab", false, "a", "b"))))
	if len(got) != 0 {
		t.Fatalf("UNIQUE(a) must not be reported as covered by KEY(a,b), got %v", got)
	}
}

func TestUniquePrefixNotCoveredByWiderUnique(t *testing.T) {
	// UNIQUE(a) forbids two rows sharing a; UNIQUE(a,b) does not: dropping u_a changes semantics.
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("u_a", true, "a"), idx("u_ab", true, "a", "b"))))
	if len(got) != 0 {
		t.Fatalf("UNIQUE(a) must NOT be reported as covered by UNIQUE(a,b), got %v", got)
	}
}

func TestUniqueExactDuplicateIsRedundant(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("u_b", true, "a", "b"), idx("u_a", true, "a", "b"))))
	if got["u_b"] != "u_a" || len(got) != 1 {
		t.Fatalf("two identical UNIQUE indexes: the greater name is redundant, got %v", got)
	}
}

func TestNonUniqueCoveredByUnique(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("u_ab", true, "a", "b"))))
	if got["k_a"] != "u_ab" {
		t.Fatalf("KEY(a) is covered by UNIQUE(a,b), got %v", got)
	}
}

func TestPrimaryIsNeverRedundant(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("u_id_x", true, "id", "x"))))
	if _, bad := got["PRIMARY"]; bad {
		t.Fatalf("PRIMARY reported as redundant: %v", got)
	}
}

func TestDuplicateOfPrimaryIsRedundant(t *testing.T) {
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("k_id", false, "id"), idx("u_id", true, "id"))))
	if got["k_id"] != "PRIMARY" || got["u_id"] != "PRIMARY" {
		t.Fatalf("KEY(id) and UNIQUE(id) duplicate PRIMARY(id), got %v", got)
	}
}

func TestImplicitPrimaryKeySuffix(t *testing.T) {
	// InnoDB appends the PK to every secondary index: (a, id) adds nothing over (a).
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("k_a_id", false, "a", "id"))))
	if got["k_a_id"] != "k_a" || len(got) != 1 {
		t.Fatalf("want k_a_id covered by k_a (implicit PK suffix), got %v", got)
	}
	// Not for MyISAM: no implicit suffix there.
	tb := table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("k_a_id", false, "a", "id"))
	tb.Engine = "MyISAM"
	got = names(redundantIndexes(tb))
	if got["k_a"] != "k_a_id" || len(got) != 1 {
		t.Fatalf("MyISAM: want k_a covered by k_a_id, got %v", got)
	}
}

func TestFulltextIsolation(t *testing.T) {
	ft := idx("ft_a", false, "a")
	ft.Type = "FULLTEXT"
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), ft, idx("k_ab", false, "a", "b"))))
	if len(got) != 0 {
		t.Fatalf("FULLTEXT(a) must not compare with BTREE(a,b), got %v", got)
	}
}

func TestFulltextOnlyExactDuplicate(t *testing.T) {
	ftA := idx("ft_a", false, "a")
	ftA.Type = "FULLTEXT"
	ftAB := idx("ft_ab", false, "a", "b")
	ftAB.Type = "FULLTEXT"
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), ftA, ftAB)))
	if len(got) != 0 {
		t.Fatalf("FULLTEXT(a) is not covered by FULLTEXT(a,b): MATCH() needs the exact column list, got %v", got)
	}
	ftA2 := idx("ft_a2", false, "a")
	ftA2.Type = "FULLTEXT"
	got = names(redundantIndexes(table(idx("PRIMARY", true, "id"), ftA, ftA2)))
	if got["ft_a2"] != "ft_a" || len(got) != 1 {
		t.Fatalf("two identical FULLTEXT(a): the greater name is redundant, got %v", got)
	}
}

func TestSubPartMustMatch(t *testing.T) {
	short := idx("k_a10", false, "a")
	short.Columns[0].SubPart = 10
	got := names(redundantIndexes(table(idx("PRIMARY", true, "id"), short, idx("k_ab", false, "a", "b"))))
	if len(got) != 0 {
		t.Fatalf("KEY(a(10)) is not a prefix of KEY(a,b), got %v", got)
	}
}

func TestFindingAggregatesAndRemediates(t *testing.T) {
	req := wire.Request{Tables: []wire.Table{
		table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("k_ab", false, "a", "b")),
		{Schema: "shop", Name: "items", Engine: "InnoDB", Indexes: []wire.TableIndex{idx("PRIMARY", true, "id"), idx("k_x", false, "x"), idx("k_xy", false, "x", "y")}},
	}}
	f := evaluateTables(req)
	if len(f) != 1 || f[0].ErrKey != "SCH0003" || f[0].Severity != "SCHEMA" || f[0].Count != 2 {
		t.Fatalf("want one SCH0003 SCHEMA finding with count 2, got %+v", f)
	}
	if len(f[0].Remediations) != 2 || !strings.Contains(f[0].Remediations[0].SQL, "DROP INDEX `k_x`") {
		t.Fatalf("want two DROP INDEX remediations sorted by table (items first), got %+v", f[0].Remediations)
	}
	if !strings.Contains(f[0].Description, "shop.orders: index k_a (a) is a prefix of k_ab (a,b)") {
		t.Fatalf("description missing the orders line: %s", f[0].Description)
	}
}

func TestMaskIdentifiersDropsSQL(t *testing.T) {
	req := wire.Request{Config: map[string]string{"mask-identifiers": "true"}, Tables: []wire.Table{
		table(idx("PRIMARY", true, "id"), idx("k_a", false, "a"), idx("k_ab", false, "a", "b")),
	}}
	f := evaluateTables(req)
	if len(f) != 1 || len(f[0].Remediations) != 0 {
		t.Fatalf("masked run must carry no SQL, got %+v", f)
	}
	if strings.Contains(f[0].Description, "orders") || strings.Contains(f[0].Description, "k_ab") {
		t.Fatalf("names leaked in masked description: %s", f[0].Description)
	}
}

func TestNoIndexesNoFinding(t *testing.T) {
	if f := evaluateTables(wire.Request{Tables: []wire.Table{{Schema: "s", Name: "t", Engine: "InnoDB"}}}); f != nil {
		t.Fatalf("want nil, got %+v", f)
	}
}
