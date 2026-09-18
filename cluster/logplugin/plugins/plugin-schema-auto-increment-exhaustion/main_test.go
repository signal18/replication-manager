package main

import (
	"math"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func aiTable(name, colType string, next int64) wire.Table {
	return wire.Table{
		Schema: "shop", Name: name, Engine: "InnoDB", AutoIncrement: next, DataLength: 10 << 20, IndexLength: 2 << 20,
		Columns: []wire.TableColumn{{Name: "id", Type: colType, Extra: "auto_increment"}, {Name: "v", Type: "varchar(10)"}},
	}
}

func TestIntCapacity(t *testing.T) {
	cases := []struct {
		colType  string
		capacity uint64
		unsigned bool
		ok       bool
	}{
		{"tinyint(4)", 127, false, true},
		{"tinyint(3) unsigned", 255, true, true},
		{"smallint(6)", 32767, false, true},
		{"smallint(5) unsigned", 65535, true, true},
		{"mediumint(9)", 8388607, false, true},
		{"mediumint(8) unsigned", 16777215, true, true},
		{"int(11)", 2147483647, false, true},
		{"int(10) unsigned", 4294967295, true, true},
		{"INT UNSIGNED ZEROFILL", 4294967295, true, true},
		{"integer", 2147483647, false, true},
		{"bigint(20)", math.MaxInt64, false, true},
		{"bigint(20) unsigned", math.MaxUint64, true, true},
		{"varchar(10)", 0, false, false},
		{"decimal(10,0)", 0, false, false},
	}
	for _, c := range cases {
		cap, uns, ok := intCapacity(c.colType)
		if ok != c.ok || cap != c.capacity || uns != c.unsigned {
			t.Errorf("intCapacity(%q) = (%d, %v, %v), want (%d, %v, %v)", c.colType, cap, uns, ok, c.capacity, c.unsigned, c.ok)
		}
	}
}

func TestThresholdEdges(t *testing.T) {
	// int signed capacity 2147483647; 95% = 2040109464.65 -> next 2040109465 hits, 2040109464 does not
	if !reachedThreshold(2040109465, 2147483647, 95) {
		t.Fatal("2040109465 should reach 95% of int")
	}
	if reachedThreshold(2040109464, 2147483647, 95) {
		t.Fatal("2040109464 should not reach 95% of int")
	}
	// unsigned bigint near 2^64: exact, no float rounding
	if reachedThreshold(math.MaxUint64-1000, math.MaxUint64, 100) {
		t.Fatal("MaxUint64-1000 is below 100%")
	}
	if !reachedThreshold(math.MaxUint64, math.MaxUint64, 100) {
		t.Fatal("MaxUint64 is exactly 100%")
	}
	if !reachedThreshold(uint64(float64(math.MaxUint64)*0.96), math.MaxUint64, 95) {
		t.Fatal("96% of MaxUint64 must reach 95%")
	}
}

func TestFindingSortedByRatioWithRemediations(t *testing.T) {
	req := wire.Request{Tables: []wire.Table{
		aiTable("a_int", "int(11)", 2100000000),           // 97.79%, signed -> UNSIGNED + BIGINT remediations
		aiTable("b_uint", "int(10) unsigned", 4290000000), // 99.88%, unsigned -> BIGINT only
		aiTable("c_fine", "int(11)", 1000),                // far below
		aiTable("d_big", "bigint(20) unsigned", -1),       // int64 -1 = 2^64-1 = 100%, nothing wider to offer
		{Schema: "shop", Name: "noai", Engine: "InnoDB", AutoIncrement: 5, Columns: []wire.TableColumn{{Name: "id", Type: "int(11)"}}},
	}}
	f := evaluateTables(req)
	if len(f) != 1 || f[0].ErrKey != "SCH0004" || f[0].Severity != "SCHEMA" || f[0].Count != 3 {
		t.Fatalf("want one SCH0004 finding with count 3, got %+v", f)
	}
	d := f[0].Description
	if strings.Index(d, "shop.d_big") > strings.Index(d, "shop.b_uint") || strings.Index(d, "shop.b_uint") > strings.Index(d, "shop.a_int") {
		t.Fatalf("tables must be sorted by ratio desc (d_big, b_uint, a_int): %s", d)
	}
	if !strings.Contains(d, "shop.d_big.id bigint(20) unsigned at 100.00% (next=18446744073709551615") {
		t.Fatalf("unsigned bigint at max must read exactly: %s", d)
	}
	if strings.Contains(d, "c_fine") || strings.Contains(d, "noai") {
		t.Fatalf("tables under threshold or without auto_increment leaked: %s", d)
	}
	var sqls []string
	for _, r := range f[0].Remediations {
		sqls = append(sqls, r.SQL)
	}
	joined := strings.Join(sqls, "\n")
	if !strings.Contains(joined, "ALTER TABLE `shop`.`a_int` MODIFY `id` int(11) UNSIGNED NOT NULL AUTO_INCREMENT") {
		t.Fatalf("signed int must get the UNSIGNED remediation: %s", joined)
	}
	if !strings.Contains(joined, "ALTER TABLE `shop`.`b_uint` MODIFY `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT") {
		t.Fatalf("unsigned int must get the BIGINT remediation: %s", joined)
	}
	if strings.Contains(joined, "`b_uint` MODIFY `id` int(10) UNSIGNED") || strings.Contains(joined, "`d_big`") {
		t.Fatalf("no UNSIGNED step for an unsigned column, nothing for bigint unsigned: %s", joined)
	}
}

func TestThresholdConfigClamped(t *testing.T) {
	req := wire.Request{Config: map[string]string{"capacity-threshold-pct": "10"}, Tables: []wire.Table{aiTable("t", "int(11)", 1100000000)}} // 51%
	if f := evaluateTables(req); len(f) != 1 {
		t.Fatalf("threshold 10 clamps to 50, 51%% must be reported, got %+v", f)
	}
	req.Config["capacity-threshold-pct"] = "60"
	if f := evaluateTables(req); f != nil {
		t.Fatalf("51%% must not be reported at threshold 60, got %+v", f)
	}
}

func TestMaskIdentifiersDropsSQL(t *testing.T) {
	req := wire.Request{Config: map[string]string{"mask-identifiers": "true"}, Tables: []wire.Table{aiTable("orders", "int(11)", 2100000000)}}
	f := evaluateTables(req)
	if len(f) != 1 || len(f[0].Remediations) != 0 || strings.Contains(f[0].Description, "orders") {
		t.Fatalf("masked run must carry no SQL and no names, got %+v", f)
	}
}
