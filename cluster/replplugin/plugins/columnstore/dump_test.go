package main

import (
	"strings"
	"testing"
)

// mysqldump's INSERT lines, as a splitdump data file holds them.
func TestForEachInsert(t *testing.T) {
	in := "/*!40101 SET NAMES utf8mb4 */;\n" +
		"INSERT INTO `pk_int` VALUES (1,'row one','{\\\"k\\\":1}'),(2,'it''s\\na\\\\b',NULL);\n" +
		"INSERT INTO `pk_uuid_binary` VALUES (0x0102FF,b'101',_binary 'raw',-3.5e2);\n"
	var rows [][]dumpValue
	if err := forEachInsert(strings.NewReader(in), func(r []dumpValue) error { rows = append(rows, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	if string(rows[0][1].Text) != "row one" || string(rows[0][2].Text) != `{"k":1}` {
		t.Fatalf("row 1 %+v", rows[0])
	}
	if string(rows[1][1].Text) != "it's\na\\b" || !rows[1][2].Null {
		t.Fatalf("row 2 %+v", rows[1])
	}
	r := rows[2]
	if !r[0].Hex || string(r[0].Text) != "\x01\x02\xff" || !r[1].Bits || bitsToDecimal(r[1]) != "5" || string(r[2].Text) != "raw" || string(r[3].Text) != "-3.5e2" {
		t.Fatalf("row 3 %+v", r)
	}
	if bitsToDecimal(dumpValue{Text: []byte{0x01, 0x00}, Hex: true}) != "256" {
		t.Fatal("hex bits")
	}
}
