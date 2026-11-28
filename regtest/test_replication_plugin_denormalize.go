// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// testAnalyticChildDenormalize: the CDC core's denormalize mode (CDC_CORE.md "Denormalize") on
// an analytic child of this cluster, on the TPC-C schema. Four TPC-C tables on the parent
// (item, customer, orders, order_line, their TPC-C primary keys); an analytic child seeded from
// the parent as any child, its destination plugin REPMAN_REGTEST_ANALYTIC_DESTINATION
// (innodb when unset); one index through the API, its denormalizing query order_line ⋈ orders ⋈
// customer ⋈ item. The denormalized table must give the checksum of the query on the parent,
// COUNT(*) and BIT_XOR(CRC32(CONCAT_WS(...))) of its rows:
//   - after the init (the query on a consistent snapshot of the parent);
//   - after targeted changes on each joined table: a customer renamed, an item renamed, an item
//     deleted (its lines leave the denormalized table) then inserted again (they come back), an
//     order line moved to another item, an order line deleted, a new order with its lines, an
//     order deleted with its lines;
//   - after a write load of new orders, with REPMAN_REGTEST_MERGE_INTERVAL (seconds) through the
//     accumulation when set.
// The child is deleted and the schemas dropped at the end.

const (
	tpccSchema       = "regtest_tpcc"
	tpccDenormSchema = "regtest_tpcc_denormalized"
	tpccDenormTable  = "order_line_denormalized"
	tpccIndexName    = "order-lines"
	tpccItems        = 1000
	tpccDistricts    = 10
	tpccCustomers    = 300 // per district
	tpccLinesMax     = 10  // per order
)

// tpccColumns are the denormalizing query's result columns, in order.
var tpccColumns = []string{
	"ol_w_id", "ol_d_id", "ol_o_id", "ol_number", "ol_quantity", "ol_amount",
	"o_w_id", "o_d_id", "o_id", "o_entry_d", "o_carrier_id",
	"c_w_id", "c_d_id", "c_id", "c_last", "c_credit",
	"i_id", "i_name", "i_price",
}

// tpccDenormalizingQuery is the index's query: every order line with its order, its customer
// and its item, each table's primary key columns returned.
func tpccDenormalizingQuery() string {
	return "SELECT ol.ol_w_id, ol.ol_d_id, ol.ol_o_id, ol.ol_number, ol.ol_quantity, ol.ol_amount, " +
		"o.o_w_id, o.o_d_id, o.o_id, o.o_entry_d, o.o_carrier_id, " +
		"c.c_w_id, c.c_d_id, c.c_id, c.c_last, c.c_credit, " +
		"i.i_id, i.i_name, i.i_price " +
		"FROM " + tpccSchema + ".order_line ol " +
		"JOIN " + tpccSchema + ".orders o ON o.o_w_id = ol.ol_w_id AND o.o_d_id = ol.ol_d_id AND o.o_id = ol.ol_o_id " +
		"JOIN " + tpccSchema + ".customer c ON c.c_w_id = o.o_w_id AND c.c_d_id = o.o_d_id AND c.c_id = o.o_c_id " +
		"JOIN " + tpccSchema + ".item i ON i.i_id = ol.ol_i_id"
}

func (regtest *RegTest) TestAnalyticChildDenormalize(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const name = "AnalyticChildDenormalize"
	fail := func(format string, args ...any) bool {
		tlsLogf(cl, "FAIL "+name+": "+format, args...)
		return false
	}
	master := cl.GetMaster()
	if master == nil || !master.IsMariaDB() || len(cl.GetSlaves()) == 0 {
		tlsLogf(cl, "Skipping %s: needs a MariaDB master and a replica", name)
		return true
	}
	if APIToken == nil || APIRequest == nil || APIRequestBody == nil {
		tlsLogf(cl, "Skipping %s: no API in this run", name)
		return true
	}
	dest := strings.TrimSpace(os.Getenv("REPMAN_REGTEST_ANALYTIC_DESTINATION"))
	if dest == "" {
		dest = "innodb"
	}
	if err := tlsExecOneConn(master, tpccSchemaStatements()...); err != nil {
		return fail("TPC-C schema: %s", err)
	}
	defer tlsExecOneConn(cl.GetMaster(), "DROP DATABASE IF EXISTS "+tpccSchema)
	// the seed must hold the schema: a fresh logical backup
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("logical backup of %s: %s", cl.Name, err)
	}

	// the child, seeded from the parent as any child, its tables copied by the plugin
	extra := map[string]any{"destination": dest, "tables": tpccSchema + ".*", "dbCount": 1, "name": fmt.Sprintf("%s-dn%d", cl.Name, time.Now().Unix()%100000)}
	run, skipped, ok := createCheckedChildWith(cl, "child", config.GrantClusterReplication, map[string]int{"Master": 1}, extra)
	if run.created {
		defer stagingDelete(cl, run.tok, run.name)
	}
	if skipped {
		return true
	}
	if !ok {
		return fail("the child cluster could not be created")
	}
	var child *cluster.Cluster
	if err := tlsWait(time.Minute, func() (err error) { child, err = cl.GetClusterFromName(run.name); return err }); err != nil || child.GetMaster() == nil {
		return fail("child %s not reachable: %v", run.name, err)
	}
	defer tlsExecOneConn(child.GetMaster(), "DROP DATABASE IF EXISTS "+tpccDenormSchema)
	base := "/api/clusters/" + run.name
	if err := tlsWait(5*time.Minute, func() error {
		if st, ok := child.ReplicationPluginStatuses()[dest]; ok && st.State == "running" {
			return nil
		}
		return fmt.Errorf("plugin %s of %s not running: %v", dest, run.name, child.ReplicationPluginStatuses())
	}); err != nil {
		return fail("%s", err)
	}
	tok, err := APIToken(child, config.GrantClusterSettings)
	if err != nil {
		return fail("%s", err)
	}
	if mi, _ := strconv.Atoi(os.Getenv("REPMAN_REGTEST_MERGE_INTERVAL")); mi > 0 {
		if code, _, resp, err := APIRequest(tok, http.MethodGet, base+"/settings/actions/set/replication-plugin-merge-interval/"+strconv.Itoa(mi)); err != nil || code != 200 {
			return fail("replication-plugin-merge-interval: HTTP %d %v %s", code, err, resp)
		}
		tlsLogf(cl, "%s: the denormalized table fed through the accumulation, merged every %d s", name, mi)
	}

	// the index, through the API: the plugin restarts with it and runs its init
	index := config.ReplicationPluginIndex{Name: tpccIndexName, SQL: tpccDenormalizingQuery(), SlaveSchema: tpccDenormSchema, SlaveTable: tpccDenormTable}
	if p := os.Getenv("REPMAN_REGTEST_DENORM_PARTITION"); p != "" {
		// the denormalized table partitioned by the order's entry date (day, week or month)
		index.PartitionColumn, index.PartitionPeriod = "o_entry_d", p
		tlsLogf(cl, "%s: the denormalized table partitioned by %s of o_entry_d", name, p)
	}
	body, _ := json.Marshal(index)
	if code, _, resp, err := APIRequestBody(tok, http.MethodPost, base+"/replication-plugin-index-actions/put", body); err != nil || code != 200 {
		return fail("index: HTTP %d %v %s", code, err, resp)
	}
	if err := tlsWait(10*time.Minute, func() error {
		st, ok := child.ReplicationPluginStatuses()[dest]
		if ok && st.State == "running" && slices.Contains(st.IndexesReady, tpccIndexName) {
			return nil
		}
		return fmt.Errorf("index %s not ready: %+v", tpccIndexName, st)
	}); err != nil {
		return fail("init: %s", err)
	}
	if err := tpccSameDenormalized(cl, child, dest); err != nil {
		return fail("after the init: %s", err)
	}
	tlsLogf(cl, "%s: init done, the denormalized table matches the query on %s", name, cl.Name)

	// targeted changes on each joined table
	changes := []string{
		"START TRANSACTION",
		fmt.Sprintf("UPDATE %s.customer SET c_last = 'RENAMED' WHERE c_w_id = 1 AND c_d_id = 1 AND c_id = 1", tpccSchema),
		fmt.Sprintf("UPDATE %s.item SET i_name = 'renamed item', i_price = i_price + 1 WHERE i_id = 2", tpccSchema),
		fmt.Sprintf("DELETE FROM %s.item WHERE i_id = 3", tpccSchema),
		fmt.Sprintf("UPDATE %s.order_line SET ol_i_id = 5 WHERE ol_w_id = 1 AND ol_d_id = 2 AND ol_o_id = 2 AND ol_number = 1", tpccSchema),
		fmt.Sprintf("DELETE FROM %s.order_line WHERE ol_w_id = 1 AND ol_d_id = 3 AND ol_o_id = 3 AND ol_number = 2", tpccSchema),
		fmt.Sprintf("UPDATE %s.orders SET o_carrier_id = 7 WHERE o_w_id = 1 AND o_d_id = 4 AND o_id = 4", tpccSchema),
		fmt.Sprintf("DELETE FROM %s.order_line WHERE ol_w_id = 1 AND ol_d_id = 5 AND ol_o_id = 5", tpccSchema),
		fmt.Sprintf("DELETE FROM %s.orders WHERE o_w_id = 1 AND o_d_id = 5 AND o_id = 5", tpccSchema),
		"COMMIT",
		// an order with its lines, its customer the renamed one
		fmt.Sprintf("INSERT INTO %s.orders VALUES (1, 1, 100001, 1, NOW(), NULL, 3)", tpccSchema),
		fmt.Sprintf("INSERT INTO %s.order_line VALUES (1, 1, 100001, 1, 10, 4, 9.50), (1, 1, 100001, 2, 11, 1, 3.25), (1, 1, 100001, 3, 3, 2, 1.00)", tpccSchema),
	}
	if err := tlsExecOneConn(cl.GetMaster(), changes...); err != nil {
		return fail("changes: %s", err)
	}
	if err := tpccSameDenormalized(cl, child, dest); err != nil {
		return fail("after the changes on each joined table: %s", err)
	}
	// the deleted item inserted again: its lines come back
	if err := tlsExecOneConn(cl.GetMaster(), fmt.Sprintf("INSERT INTO %s.item VALUES (3, 3, 'item back', 4.00, 'data')", tpccSchema)); err != nil {
		return fail("item back: %s", err)
	}
	if err := tpccSameDenormalized(cl, child, dest); err != nil {
		return fail("after the deleted item came back: %s", err)
	}

	// a load of new orders
	for o := 200001; o < 200001+500; o++ {
		d, c := 1+o%tpccDistricts, 1+o%tpccCustomers
		stmts := []string{"START TRANSACTION", fmt.Sprintf("INSERT INTO %s.orders VALUES (1, %d, %d, %d, NOW(), NULL, 2)", tpccSchema, d, o, c)}
		for n := 1; n <= 2; n++ {
			stmts = append(stmts, fmt.Sprintf("INSERT INTO %s.order_line VALUES (1, %d, %d, %d, %d, %d, %d.10)", tpccSchema, d, o, n, 1+(o*n)%tpccItems, n, n))
		}
		if o%5 == 0 {
			stmts = append(stmts, fmt.Sprintf("UPDATE %s.customer SET c_balance = c_balance + 1, c_last = CONCAT('L', %d) WHERE c_w_id = 1 AND c_d_id = %d AND c_id = %d", tpccSchema, o, d, c))
		}
		stmts = append(stmts, "COMMIT")
		if err := tlsExecOneConn(cl.GetMaster(), stmts...); err != nil {
			return fail("load: %s", err)
		}
	}
	if err := tpccSameDenormalized(cl, child, dest); err != nil {
		return fail("after the load: %s", err)
	}
	tlsLogf(cl, "PASS %s: %s keeps %s.%s equal to the denormalizing query on %s after the init, a change on each joined table, a deleted item inserted again and a load", name, run.name, tpccDenormSchema, tpccDenormTable, cl.Name)
	return true
}

// tpccSchemaStatements create the four TPC-C tables (TPC-C's primary keys, a subset of its
// columns) and load one warehouse.
func tpccSchemaStatements() []string {
	s := tpccSchema
	return []string{
		"DROP DATABASE IF EXISTS " + s,
		"CREATE DATABASE " + s,
		"USE " + s, // the seq_1_to_N tables of the sequence engine need a schema
		"CREATE TABLE item (i_id INT NOT NULL, i_im_id INT, i_name VARCHAR(24), i_price DECIMAL(5,2), i_data VARCHAR(50), PRIMARY KEY (i_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE customer (c_id INT NOT NULL, c_d_id TINYINT NOT NULL, c_w_id SMALLINT NOT NULL, c_first VARCHAR(16), c_last VARCHAR(16), c_credit CHAR(2), c_balance DECIMAL(12,2), PRIMARY KEY (c_w_id, c_d_id, c_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE orders (o_w_id SMALLINT NOT NULL, o_d_id TINYINT NOT NULL, o_id INT NOT NULL, o_c_id INT, o_entry_d DATETIME, o_carrier_id TINYINT, o_ol_cnt TINYINT, PRIMARY KEY (o_w_id, o_d_id, o_id), KEY idx_orders (o_w_id, o_d_id, o_c_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE order_line (ol_w_id SMALLINT NOT NULL, ol_d_id TINYINT NOT NULL, ol_o_id INT NOT NULL, ol_number TINYINT NOT NULL, ol_i_id INT, ol_quantity TINYINT, ol_amount DECIMAL(6,2), PRIMARY KEY (ol_w_id, ol_d_id, ol_o_id, ol_number), KEY idx_ol_item (ol_i_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		fmt.Sprintf("INSERT INTO item SELECT seq, seq MOD 100, CONCAT('item ', seq), 1 + (seq MOD 100), 'data' FROM seq_1_to_%d", tpccItems),
		fmt.Sprintf("INSERT INTO customer SELECT 1 + (seq - 1) MOD %d, 1 + (seq - 1) DIV %d, 1, CONCAT('F', seq), CONCAT('LAST', seq MOD 1000), IF(seq MOD 10 = 0, 'BC', 'GC'), 0 FROM seq_1_to_%d", tpccCustomers, tpccCustomers, tpccDistricts*tpccCustomers),
		// one order per customer, its id the customer's
		fmt.Sprintf("INSERT INTO orders SELECT 1, 1 + (seq - 1) DIV %d, 1 + (seq - 1) MOD %d, 1 + (seq - 1) MOD %d, '2026-01-01' + INTERVAL seq MINUTE, IF(seq MOD 3 = 0, NULL, seq MOD 10), 1 + seq MOD %d FROM seq_1_to_%d", tpccCustomers, tpccCustomers, tpccCustomers, tpccLinesMax, tpccDistricts*tpccCustomers),
		fmt.Sprintf("INSERT INTO order_line SELECT o.o_w_id, o.o_d_id, o.o_id, n.seq, 1 + (o.o_id * 7 + n.seq * 13 + o.o_d_id) MOD %d, n.seq, o.o_id MOD 100 + n.seq / 4 FROM orders o JOIN seq_1_to_%d n ON n.seq <= o.o_ol_cnt", tpccItems, tpccLinesMax),
	}
}

// tpccSameDenormalized waits until the plugin applied (and merged, with an accumulation) the
// parent's GTID, then compares the checksum of the denormalizing query on the parent with the
// denormalized table's on the child.
func tpccSameDenormalized(cl, child *cluster.Cluster, plugin string) error {
	if err := cdcWaitApplied(cl, child, plugin); err != nil {
		return err
	}
	a, na, err := tpccChecksum(cl.GetMaster().Conn.DB, tpccDenormalizingQuery())
	if err != nil {
		return fmt.Errorf("parent: %w", err)
	}
	b, nb, err := tpccChecksum(child.GetMaster().Conn.DB, "SELECT * FROM "+tpccDenormSchema+"."+tpccDenormTable)
	if err != nil {
		return fmt.Errorf("child: %w", err)
	}
	if a != b || na != nb {
		return fmt.Errorf("parent %d rows checksum %s, child %d rows checksum %s", na, a, nb, b)
	}
	return nil
}

// tpccChecksum is COUNT(*) and BIT_XOR(CRC32(CONCAT_WS('#', columns))) of the rows of query.
func tpccChecksum(db *sql.DB, query string) (string, int, error) {
	cols := make([]string, len(tpccColumns))
	for i, c := range tpccColumns {
		cols[i] = "IFNULL(d." + c + ", '\\\\N')"
	}
	var n int
	var x sql.NullString
	err := db.QueryRow("SELECT COUNT(*), BIT_XOR(CRC32(CONCAT_WS('#', "+strings.Join(cols, ", ")+"))) FROM ("+query+") AS d").Scan(&n, &x)
	return x.String, n, err
}
