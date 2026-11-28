// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

func orderLineTable() *schema.Table {
	return &schema.Table{Schema: "tpcc", Name: "order_line", PKColumns: []int{0, 1},
		Columns: []schema.TableColumn{{Name: "ol_o_id", Type: schema.TYPE_NUMBER}, {Name: "ol_number", Type: schema.TYPE_NUMBER}, {Name: "ol_i_id", Type: schema.TYPE_NUMBER}}}
}

// The micro_checkpoint_pack keeps per source table the primary keys to replace and to delete:
// a deleted key is kept as deleted, inserted again it is back to replace; an update that
// changes the primary key deletes the old key.
func TestPackKeys(t *testing.T) {
	b := newPackBuf(orderLineTable())
	b.pack("insert", [][]interface{}{{int64(1), int64(1), int64(10)}, {int64(1), int64(2), int64(11)}})
	b.pack("delete", [][]interface{}{{int64(1), int64(2), int64(11)}})
	b.pack("update", [][]interface{}{{int64(1), int64(1), int64(10)}, {int64(1), int64(1), int64(12)}})
	b.pack("update", [][]interface{}{{int64(2), int64(1), int64(5)}, {int64(3), int64(1), int64(5)}})
	b.pack("delete", [][]interface{}{{int64(4), int64(1), int64(5)}})
	b.pack("insert", [][]interface{}{{int64(4), int64(1), int64(6)}})
	packs, files := renderPacks(map[string]*packBuf{"tpcc.order_line": b})
	if len(packs) != 1 || packs[0].Keys != 5 || strings.Join(packs[0].KeyColumns, ",") != "ol_o_id,ol_number" {
		t.Fatalf("%+v", packs)
	}
	if got := string(files[packs[0].Replace]); got != "1\t1\n3\t1\n4\t1\n" {
		t.Fatalf("replace %q", got)
	}
	if got := string(files[packs[0].Delete]); got != "1\t2\n2\t1\n" {
		t.Fatalf("delete %q", got)
	}
	if k := decodeKeys(files[packs[0].Delete]); len(k) != 2 || string(k[1][0].([]byte)) != "2" {
		t.Fatalf("decoded %v", k)
	}
}

func tpccFields() []field {
	return []field{
		{name: "ol_w_id", schema: "tpcc", orgTable: "order_line", orgName: "ol_w_id"}, {name: "ol_o_id", schema: "tpcc", orgTable: "order_line", orgName: "ol_o_id"}, {name: "ol_number", schema: "tpcc", orgTable: "order_line", orgName: "ol_number"},
		{name: "amount", schema: "", orgTable: "", orgName: ""},
		{name: "i_id", schema: "tpcc", orgTable: "item", orgName: "i_id"}, {name: "i_name", schema: "tpcc", orgTable: "item", orgName: "i_name"},
	}
}

// The source tables and their primary keys come from the query's result: each one's primary
// key columns must be returned, under one name each.
func TestIndexFromFields(t *testing.T) {
	saved := primaryKeyOf
	defer func() { primaryKeyOf = saved }()
	primaryKeyOf = func(_ context.Context, _ *sqlx.DB, schema, table string) ([]string, error) {
		return map[string][]string{"order_line": {"ol_w_id", "ol_o_id", "ol_number"}, "item": {"i_id"}, "customer": {"c_id"}}[table], nil
	}
	idx := wire.Index{Name: "ol", SQL: "q", Schema: "dn", Table: "ol"}
	m, err := indexFromFields(context.Background(), nil, idx, tpccFields())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.key, ",") != "ol_w_id,ol_o_id,ol_number,i_id" || fmt.Sprint(m.keyPos) != "[0 1 2 4]" || len(m.tables) != 2 || m.tables[1].cols[0] != "i_id" {
		t.Fatalf("%+v", m)
	}
	// a primary key column missing from the result
	missing := append(tpccFields()[:1], tpccFields()[2:]...)
	if _, err := indexFromFields(context.Background(), nil, idx, missing); err == nil || !strings.Contains(err.Error(), "ol_o_id") {
		t.Fatalf("missing pk column: %v", err)
	}
	// a table joined twice
	twice := append(tpccFields(), field{name: "i2_id", schema: "tpcc", orgTable: "item", orgName: "i_id"})
	if _, err := indexFromFields(context.Background(), nil, idx, twice); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("table twice: %v", err)
	}
	// two columns of one name
	dup := append(tpccFields(), field{name: "amount", schema: "", orgTable: "", orgName: ""})
	if _, err := indexFromFields(context.Background(), nil, idx, dup); err == nil {
		t.Fatal("two columns of one name must be refused")
	}
}

// Keys are split across restricted queries, never dropped: max_keys_per_batch per query.
func TestKeyBatches(t *testing.T) {
	meta := &indexMeta{idx: wire.Index{MaxKeysPerBatch: 3}, tables: []sourceTable{
		{schema: "tpcc", table: "order_line", cols: []string{"ol_o_id", "ol_number"}},
		{schema: "tpcc", table: "item", cols: []string{"i_id"}},
	}}
	m := wire.Manifest{Packs: []wire.PackTable{
		{Schema: "tpcc", Table: "item", Replace: "i.r"},
		{Schema: "tpcc", Table: "order_line", Replace: "o.r", Delete: "o.d"},
	}}
	files := map[string][]byte{"i.r": []byte("1\n2\n"), "o.r": []byte("1\t1\n1\t2\n"), "o.d": []byte("2\t1\n3\t1\n")}
	b := keyBatches(meta, m, files)
	total := 0
	for _, conds := range b {
		n := 0
		for _, c := range conds {
			n += len(c.Keys)
		}
		if n > 3 {
			t.Fatalf("a batch of %d keys", n)
		}
		total += n
	}
	if len(b) != 2 || total != 6 {
		t.Fatalf("%d batches, %d keys", len(b), total)
	}
}

// The unpack: the denormalized rows the packed keys touch are deleted, the query restricted to
// the same keys gives the rows to write, whose keys are deleted too (once).
func TestUnpack(t *testing.T) {
	sdb, smock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	tdb, tmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer tdb.Close()
	ctx := context.Background()
	tconn, err := sqlx.NewDb(tdb, "mysql").Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tconn.Close()
	meta := &indexMeta{idx: wire.Index{Name: "ol", SQL: "SELECT ol.ol_o_id, i.i_id, i.i_name FROM tpcc.order_line ol JOIN tpcc.item i ON i.i_id = ol.ol_i_id", Schema: "dn", Table: "ol"},
		tables:  []sourceTable{{schema: "tpcc", table: "order_line", cols: []string{"ol_o_id"}}, {schema: "tpcc", table: "item", cols: []string{"i_id"}}},
		key:     []string{"ol_o_id", "i_id"},
		columns: []string{"ol_o_id", "i_id", "i_name"},
		keyPos:  []int{0, 1}}
	m := wire.Manifest{Seq: 7, Packs: []wire.PackTable{{Schema: "tpcc", Table: "item", KeyColumns: []string{"i_id"}, Replace: "p0.replace.tsv", Delete: "p0.delete.tsv"}}}
	files := map[string][]byte{"p0.replace.tsv": []byte("5\n"), "p0.delete.tsv": []byte("6\n")}
	// item 5 renamed (two order lines), item 6 deleted (one order line)
	tmock.ExpectQuery(regexp.QuoteMeta("SELECT `ol_o_id`, `i_id` FROM `dn`.`ol` AS t WHERE (t.`i_id`) IN ((?), (?))")).
		WillReturnRows(sqlmock.NewRows([]string{"ol_o_id", "i_id"}).AddRow("1", "5").AddRow("2", "5").AddRow("3", "6"))
	smock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM (" + meta.idx.SQL + ") AS d WHERE (d.`i_id`) IN ((?), (?))")).
		WillReturnRows(sqlmock.NewRows([]string{"ol_o_id", "i_id", "i_name"}).AddRow("1", "5", "new").AddRow("2", "5", "new"))
	tables, out, err := unpack(ctx, sqlx.NewDb(sdb, "mysql"), tconn, meta, m, files, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].DeleteRows != 3 || tables[0].InsertRows != 2 {
		t.Fatalf("%+v", tables)
	}
	if got := string(out[tables[0].Deletes]); got != "1\t5\n2\t5\n3\t6\n" {
		t.Fatalf("deletes %q", got)
	}
	if got := string(out[tables[0].Inserts]); got != "1\t5\tnew\n2\t5\tnew\n" {
		t.Fatalf("inserts %q", got)
	}
	if err := tmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A changed query or denormalized table is a new index: its init runs again.
func TestIndexID(t *testing.T) {
	a := wire.Index{Name: "ol", SQL: "SELECT 1", Schema: "dn", Table: "ol"}
	b := a
	b.SQL = "SELECT 2"
	if indexID(a) == indexID(b) || !strings.HasPrefix(indexID(a), "ol@") || indexID(a) != indexID(a) {
		t.Fatalf("%s %s", indexID(a), indexID(b))
	}
}
