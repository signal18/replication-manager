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
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// testAnalyticChildTableMode: the CDC core's table and index modes with partitioned destinations
// (CDC_CORE.md "Modes, init and the GTID start", "Partitioning") on an analytic child of this
// cluster. Two tables on the parent:
//   - access: a log-like table (a BIGINT id spread over 30M, a DATETIME, a TIMESTAMP, ENUM, SET,
//     VARBINARY, a BLOB and a DATETIME holding zero dates, 8 weeks of rows and a few one and two
//     years old);
//   - audit: no primary key, a unique key of NOT NULL columns only.
//
// An analytic child seeded from the parent (REPMAN_REGTEST_ANALYTIC_DESTINATION, innodb when
// unset), and indexes through the API, each into its own schema of the child:
//   - access by week of created_at, by SELECT and from the split dump
//     (REPMAN_REGTEST_TABLE_INIT_BACKUP=0 skips the dump);
//   - access by 1M ids, by year of created_at, by month of the TIMESTAMP seen_at;
//   - access by week, purged after 21 days (the older rows are not written, the old partitions
//     dropped);
//   - audit copied by its unique key;
//   - an index of a query on access, its destination partitioned by month.
//
// Each destination must give, partition by partition, the row count and
// BIT_XOR(CRC32(CONCAT_WS(...))) of the parent's rows of that partition's range (the purged one:
// from its first kept period), after the init and after writes on the parent (rows now and 40
// days ahead, updates, rows moved to another week, deletes, large BLOBs).

const (
	logsSchema = "regtest_cdc_logs"
	logsRows   = 30000
)

// logsIndex is one index of the test and how its destination is compared.
type logsIndex struct {
	idx    config.ReplicationPluginIndex
	expr   string // the partition expression on the parent's rows, "" not partitioned
	from   string // the source rows compared: a table, or a query AS d
	cols   string // the columns of the checksum
	table  string // the destination table
	purged bool   // its first partition is a kept period, not every older row
}

var logsCols = "id, created_at, seen_at, level, flags, HEX(raw), HEX(payload), IFNULL(legacy, 'N'), IFNULL(msg, 'N')"

func logsIndexes(withBackup bool) []logsIndex {
	table := func(name, schema, init, col, period string, purge int) logsIndex {
		return logsIndex{idx: config.ReplicationPluginIndex{Name: name, Mode: "table", Tables: []string{logsSchema + ".access"}, SlaveSchema: schema, Init: init, RangeStep: 5000,
			PartitionColumn: col, PartitionPeriod: period, PartitionPurgeAfterDays: purge, TargetEngine: "InnoDB"}, from: logsSchema + ".access", cols: logsCols, table: "access"}
	}
	out := []logsIndex{
		table("logs-select", "regtest_cdc_logs_sel", "select", "created_at", "week", 0),
		table("logs-id", "regtest_cdc_logs_id", "select", "id", "1M", 0),
		table("logs-year", "regtest_cdc_logs_year", "select", "created_at", "year", 0),
		table("logs-ts", "regtest_cdc_logs_ts", "select", "seen_at", "month", 0),
		table("logs-purge", "regtest_cdc_logs_purge", "select", "created_at", "week", 21),
	}
	if withBackup {
		out = append(out, table("logs-backup", "regtest_cdc_logs_bak", "backup", "created_at", "week", 0))
	}
	out[1].expr, out[2].expr, out[3].expr, out[4].expr = "id", "created_at", "UNIX_TIMESTAMP(seen_at)", "created_at"
	out[0].expr = "created_at"
	out[4].purged = true
	if withBackup {
		out[5].expr = "created_at"
	}
	out = append(out,
		logsIndex{idx: config.ReplicationPluginIndex{Name: "audit", Mode: "table", Tables: []string{logsSchema + ".audit"}, SlaveSchema: "regtest_cdc_logs_audit"},
			from: logsSchema + ".audit", cols: "uid, seq, note", table: "audit"},
		logsIndex{idx: config.ReplicationPluginIndex{Name: "logs-query", SQL: "SELECT a.id, a.created_at, a.level FROM " + logsSchema + ".access a WHERE a.level <> 'info'",
			SlaveSchema: "regtest_cdc_logs_q", SlaveTable: "errors", PartitionColumn: "created_at", PartitionPeriod: "month"},
			expr: "created_at", from: "(SELECT a.id, a.created_at, a.level FROM " + logsSchema + ".access a WHERE a.level <> 'info') AS d", cols: "id, created_at, level", table: "errors"},
	)
	return out
}

func (regtest *RegTest) TestAnalyticChildTableMode(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const name = "AnalyticChildTableMode"
	fail := func(format string, args ...any) bool {
		tlsLogf(cl, "FAIL "+name+": "+format, args...)
		return false
	}
	master := cl.GetMaster()
	if master == nil || !master.IsMariaDB() {
		tlsLogf(cl, "Skipping %s: needs a MariaDB master", name)
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
	indexes := logsIndexes(os.Getenv("REPMAN_REGTEST_TABLE_INIT_BACKUP") != "0")
	if err := tlsExecOneConn(master,
		"DROP DATABASE IF EXISTS "+logsSchema,
		"CREATE DATABASE "+logsSchema,
		"USE "+logsSchema,
		"SET SESSION sql_mode = 'NO_ENGINE_SUBSTITUTION'", // zero dates
		"CREATE TABLE access (id BIGINT NOT NULL, created_at DATETIME NOT NULL, seen_at TIMESTAMP NOT NULL DEFAULT '2000-01-01 00:00:00', level ENUM('info','warn','error') NOT NULL, flags SET('a','b','c'), raw VARBINARY(16), payload BLOB, legacy DATETIME NULL, msg VARCHAR(200), PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		// ids spread over 30M (1M ranges); 8 weeks of rows, every 1000th one or two years old
		fmt.Sprintf("INSERT INTO access SELECT seq * 1000, NOW() - INTERVAL (seq MOD 56) DAY - INTERVAL (seq MOD 3600) SECOND - INTERVAL IF(seq MOD 1000 = 0, 1 + seq MOD 2, 0) YEAR, NOW() - INTERVAL (seq MOD 50) DAY, ELT(1 + seq MOD 3, 'info', 'warn', 'error'), ELT(1 + seq MOD 4, '', 'a', 'a,b', 'c'), UNHEX(MD5(seq)), IF(seq MOD 5 = 0, NULL, REPEAT(CHAR(seq MOD 256), seq MOD 300)), IF(seq MOD 11 = 0, '0000-00-00 00:00:00', IF(seq MOD 13 = 0, NULL, '2020-01-01 10:00:00')), IF(seq MOD 7 = 0, NULL, CONCAT('request ', seq, ' tab\\there')) FROM seq_1_to_%d", logsRows),
	); err != nil {
		return fail("the source tables: %s", err)
	}
	// a table keyed by a unique key of NOT NULL columns only; a parent that forces primary keys
	// (innodb_force_primary_key) refuses it, and that case is left out
	if err := tlsExecOneConn(master, "USE "+logsSchema,
		"CREATE TABLE audit (uid INT NOT NULL, seq INT NOT NULL, note VARCHAR(40), UNIQUE KEY uk_audit (uid, seq)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"INSERT INTO audit SELECT seq MOD 100, seq, CONCAT('n', seq) FROM seq_1_to_2000"); err != nil {
		tlsLogf(cl, "%s: a table without a primary key is refused by %s (%s): the unique-key case is left out", name, cl.Name, err)
		indexes = slices.DeleteFunc(indexes, func(ix logsIndex) bool { return ix.idx.Name == "audit" })
		tlsExecOneConn(master, "DROP TABLE IF EXISTS "+logsSchema+".audit")
	}
	defer tlsExecOneConn(cl.GetMaster(), "DROP DATABASE IF EXISTS "+logsSchema)
	// the seed, and the init from a backup, read a fresh logical backup
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("logical backup of %s: %s", cl.Name, err)
	}

	// one database: one is enough here (disk)
	extra := map[string]any{"destination": dest, "tables": logsSchema + ".*", "dbCount": 1, "name": fmt.Sprintf("%s-tm%d", cl.Name, time.Now().Unix()%100000)}
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
	var drops []string
	for _, ix := range indexes {
		drops = append(drops, "DROP DATABASE IF EXISTS "+ix.idx.SlaveSchema)
	}
	defer tlsExecOneConn(child.GetMaster(), drops...)
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
	var names []string
	for _, ix := range indexes {
		body, _ := json.Marshal(ix.idx)
		if code, _, resp, err := APIRequestBody(tok, http.MethodPost, base+"/replication-plugin-index-actions/put", body); err != nil || code != 200 {
			return fail("index %s: HTTP %d %v %s", ix.idx.Name, code, err, resp)
		}
		names = append(names, ix.idx.Name)
	}
	if err := tlsWait(15*time.Minute, func() error {
		st, ok := child.ReplicationPluginStatuses()[dest]
		if ok && st.State == "running" && !slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(st.IndexesReady, n) }) {
			return nil
		}
		return fmt.Errorf("indexes %v not ready: %+v", names, st)
	}); err != nil {
		return fail("init: %s", err)
	}
	check := func(when string) error {
		if err := cdcWaitApplied(cl, child, dest); err != nil {
			return err
		}
		for _, ix := range indexes {
			n, err := logsSame(cl, child, ix)
			if err != nil {
				return fmt.Errorf("index %s (%s.%s) %s: %w", ix.idx.Name, ix.idx.SlaveSchema, ix.table, when, err)
			}
			tlsLogf(cl, "%s: index %s, %s.%s equal to the parent in each of its %d partitions %s", name, ix.idx.Name, ix.idx.SlaveSchema, ix.table, n, when)
		}
		return nil
	}
	if err := check("after the init"); err != nil {
		return fail("%s", err)
	}

	writes := []string{
		"USE " + logsSchema, // the seq_1_to_N tables of the sequence engine need a schema
		"SET SESSION sql_mode = 'NO_ENGINE_SUBSTITUTION'",
		"START TRANSACTION",
		fmt.Sprintf("INSERT INTO %s.access (id, created_at, seen_at, level, flags, raw, payload, legacy, msg) SELECT %d + seq, NOW(), NOW(), 'warn', 'b', UNHEX('00FF'), REPEAT('x', 200000), '0000-00-00 00:00:00', CONCAT('new ', seq) FROM seq_1_to_40", logsSchema, logsRows*1000),
		fmt.Sprintf("INSERT INTO %s.access (id, created_at, seen_at, level, msg) VALUES (%d, NOW() + INTERVAL 40 DAY, NOW() + INTERVAL 40 DAY, 'error', 'a row from the future')", logsSchema, logsRows*1000+500),
		"COMMIT",
		fmt.Sprintf("UPDATE %s.access SET msg = CONCAT(IFNULL(msg, ''), ' updated'), level = 'error', flags = 'a,c' WHERE id BETWEEN 100000 AND 299000", logsSchema),
		fmt.Sprintf("UPDATE %s.access SET created_at = created_at - INTERVAL 14 DAY WHERE id BETWEEN 1000000 AND 1049000", logsSchema),
		fmt.Sprintf("DELETE FROM %s.access WHERE id BETWEEN 5000000 AND 5299000", logsSchema),
	}
	if slices.ContainsFunc(indexes, func(ix logsIndex) bool { return ix.idx.Name == "audit" }) {
		writes = append(writes,
			fmt.Sprintf("UPDATE %s.audit SET note = 'changed' WHERE uid = 7", logsSchema),
			fmt.Sprintf("DELETE FROM %s.audit WHERE uid = 8", logsSchema),
			fmt.Sprintf("INSERT INTO %s.audit VALUES (500, 1, 'new')", logsSchema))
	}
	if err := tlsExecOneConn(cl.GetMaster(), writes...); err != nil {
		return fail("writes: %s", err)
	}
	if err := check("after the writes on the parent"); err != nil {
		return fail("%s", err)
	}
	if st, ok := child.ReplicationPluginStatuses()[dest]; ok {
		tlsLogf(cl, "%s: purge: %d partitions dropped, %d rows older than its horizon not written", name, st.PartitionsDropped, st.PurgedRowsSkipped)
		if st.PartitionsDropped == 0 && st.PurgedRowsSkipped == 0 {
			return fail("the purge index dropped no partition and skipped no row")
		}
	}
	tlsLogf(cl, "PASS %s: %s keeps %d destinations (%v) equal partition by partition to the parent after the init and after inserts now and 40 days ahead, updates, rows moved to another week, deletes and large BLOBs", name, run.name, len(indexes), names)
	return true
}

// logsSame compares, partition by partition, the child's destination of ix with the parent's
// rows of the same range: COUNT(*) and BIT_XOR(CRC32(CONCAT_WS(...))). An unpartitioned one is
// compared whole. It returns the partitions compared.
func logsSame(cl, child *cluster.Cluster, ix logsIndex) (int, error) {
	cdb, pdb := child.GetMaster().Conn.DB, cl.GetMaster().Conn.DB
	sum := func(db *sql.DB, from string) (int, string, error) {
		var n int
		var x sql.NullString
		err := db.QueryRow("SELECT COUNT(*), BIT_XOR(CRC32(CONCAT_WS('#', "+ix.cols+"))) FROM "+from).Scan(&n, &x)
		return n, x.String, err
	}
	dest := ix.idx.SlaveSchema + "." + ix.table
	if ix.expr == "" {
		a, ha, err := sum(pdb, ix.from)
		if err != nil {
			return 0, fmt.Errorf("parent: %w", err)
		}
		b, hb, err := sum(cdb, dest)
		if err != nil {
			return 0, fmt.Errorf("child: %w", err)
		}
		if a != b || ha != hb {
			return 0, fmt.Errorf("parent %d rows checksum %s, child %d rows checksum %s", a, ha, b, hb)
		}
		return 1, nil
	}
	type part struct{ name, bound string }
	rows, err := cdb.Query("SELECT PARTITION_NAME, PARTITION_DESCRIPTION FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND PARTITION_NAME IS NOT NULL ORDER BY PARTITION_ORDINAL_POSITION", ix.idx.SlaveSchema, ix.table)
	if err != nil {
		return 0, err
	}
	var parts []part
	for rows.Next() {
		var p part
		if err := rows.Scan(&p.name, &p.bound); err != nil {
			rows.Close()
			return 0, err
		}
		parts = append(parts, p)
	}
	rows.Close()
	if len(parts) < 2 {
		return 0, fmt.Errorf("%d partitions: the destination is not partitioned", len(parts))
	}
	where := func(from string) string {
		if strings.HasSuffix(from, ") AS d") {
			return "SELECT * FROM " + from + " WHERE "
		}
		return ""
	}
	// a purged destination keeps the periods from the one holding now - purge days (weeks from
	// Monday): the parent's older rows are not in it
	lower := ""
	if ix.purged {
		k := time.Now().UTC().AddDate(0, 0, -ix.idx.PartitionPurgeAfterDays)
		k = k.AddDate(0, 0, -((int(k.Weekday()) + 6) % 7))
		lower = fmt.Sprintf("%s >= '%s' AND ", ix.expr, k.Format("2006-01-02"))
	}
	total := 0
	for i, p := range parts {
		cond := lower + ix.expr + " < " + p.bound
		if i > 0 {
			cond = lower + ix.expr + " >= " + parts[i-1].bound + " AND " + ix.expr + " < " + p.bound
		}
		src := ix.from + " WHERE " + cond
		if w := where(ix.from); w != "" {
			src = "(" + w + cond + ") AS r"
		}
		a, ha, err := sum(pdb, src)
		if err != nil {
			return 0, fmt.Errorf("parent: %w", err)
		}
		b, hb, err := sum(cdb, dest+" PARTITION (`"+p.name+"`)")
		if err != nil {
			return 0, fmt.Errorf("child: %w", err)
		}
		if a != b || ha != hb {
			return 0, fmt.Errorf("partition %s (< %s): parent %d rows checksum %s, child %d rows checksum %s", p.name, p.bound, a, ha, b, hb)
		}
		tlsLogf(cl, "AnalyticChildTableMode: %s partition %s (< %s): %d rows, checksum %s on both", dest, p.name, p.bound, b, hb)
		total += b
	}
	// every row of the parent is in a partition: none past the last bound (below the first
	// kept period for a purged one)
	all := ix.from
	if ix.purged {
		all = ix.from + " WHERE " + strings.TrimSuffix(lower, " AND ")
	}
	n, _, err := sum(pdb, all)
	if err != nil {
		return 0, err
	}
	if n != total {
		return 0, fmt.Errorf("the parent has %d rows, the partitions %d", n, total)
	}
	return len(parts), nil
}
