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

// testAnalyticChildTableMode: the CDC core's table mode with a partitioned destination
// (CDC_CORE.md "Modes, init and the GTID start", "Partitioning") on an analytic child of this
// cluster. A log-like table on the parent (a primary key, a NOT NULL time column, 8 weeks of
// rows); an analytic child seeded from the parent, its destination plugin
// REPMAN_REGTEST_ANALYTIC_DESTINATION (innodb when unset); two table-mode indexes through the
// API, the table copied into two schemas of the child partitioned by week: one initialized by a
// SELECT on a consistent snapshot of the source, one from the parent's latest split dump
// (REPMAN_REGTEST_TABLE_INIT_BACKUP=0 skips it). Each destination must give, partition by
// partition, the row count and BIT_XOR(CRC32(CONCAT_WS(...))) of the parent's rows of that
// partition's range:
//   - after the init;
//   - after writes on the parent: new rows now and 40 days ahead (a partition made when a row
//     needs it), updates, rows moved to another week, deletes.
// The child is deleted and the parent's schema dropped at the end.

const (
	logsSchema     = "regtest_cdc_logs"
	logsSelSchema  = "regtest_cdc_logs_sel"
	logsBakSchema  = "regtest_cdc_logs_bak"
	logsRows       = 30000
	logsSelIndex   = "logs-select"
	logsBakIndex   = "logs-backup"
	logsPartPeriod = "week"
)

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
	withBackup := os.Getenv("REPMAN_REGTEST_TABLE_INIT_BACKUP") != "0"
	if err := tlsExecOneConn(master,
		"DROP DATABASE IF EXISTS "+logsSchema,
		"CREATE DATABASE "+logsSchema,
		"USE "+logsSchema,
		"CREATE TABLE access (id BIGINT NOT NULL, created_at DATETIME NOT NULL, level VARCHAR(8) NOT NULL, msg VARCHAR(200), PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		fmt.Sprintf("INSERT INTO access SELECT seq, NOW() - INTERVAL (seq MOD 56) DAY - INTERVAL (seq MOD 3600) SECOND, ELT(1 + seq MOD 3, 'info', 'warn', 'error'), IF(seq MOD 7 = 0, NULL, CONCAT('request ', seq, ' tab\\there')) FROM seq_1_to_%d", logsRows),
	); err != nil {
		return fail("the log table: %s", err)
	}
	defer tlsExecOneConn(cl.GetMaster(), "DROP DATABASE IF EXISTS "+logsSchema)
	// the seed, and the init from a backup, read a fresh logical backup
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("logical backup of %s: %s", cl.Name, err)
	}

	extra := map[string]any{"destination": dest, "tables": logsSchema + ".*", "name": fmt.Sprintf("%s-tm%d", cl.Name, time.Now().Unix()%100000)}
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
	defer tlsExecOneConn(child.GetMaster(), "DROP DATABASE IF EXISTS "+logsSelSchema, "DROP DATABASE IF EXISTS "+logsBakSchema)
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
	indexes := []config.ReplicationPluginIndex{{Name: logsSelIndex, Mode: "table", Tables: []string{logsSchema + ".access"}, SlaveSchema: logsSelSchema, Init: "select", RangeStep: 5000,
		PartitionColumn: "created_at", PartitionPeriod: logsPartPeriod, TargetEngine: "InnoDB"}}
	if withBackup {
		indexes = append(indexes, config.ReplicationPluginIndex{Name: logsBakIndex, Mode: "table", Tables: []string{logsSchema + ".access"}, SlaveSchema: logsBakSchema, Init: "backup",
			PartitionColumn: "created_at", PartitionPeriod: logsPartPeriod})
	}
	var schemas, names []string
	for _, idx := range indexes {
		body, _ := json.Marshal(idx)
		if code, _, resp, err := APIRequestBody(tok, http.MethodPost, base+"/replication-plugin-index-actions/put", body); err != nil || code != 200 {
			return fail("index %s: HTTP %d %v %s", idx.Name, code, err, resp)
		}
		schemas, names = append(schemas, idx.SlaveSchema), append(names, idx.Name)
	}
	if err := tlsWait(10*time.Minute, func() error {
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
		for _, s := range schemas {
			n, err := logsSamePartitions(cl, child, s)
			if err != nil {
				return fmt.Errorf("%s %s: %w", s, when, err)
			}
			tlsLogf(cl, "%s: %s.access equal to the parent in each of its %d partitions %s", name, s, n, when)
		}
		return nil
	}
	if err := check("after the init"); err != nil {
		return fail("%s", err)
	}

	// writes on the parent
	writes := []string{
		"START TRANSACTION",
		fmt.Sprintf("INSERT INTO %s.access SELECT %d + seq, NOW(), 'info', CONCAT('new ', seq) FROM seq_1_to_500", logsSchema, logsRows),
		fmt.Sprintf("INSERT INTO %s.access VALUES (%d, NOW() + INTERVAL 40 DAY, 'warn', 'a row from the future')", logsSchema, logsRows+1000),
		"COMMIT",
		fmt.Sprintf("UPDATE %s.access SET msg = CONCAT(IFNULL(msg, ''), ' updated'), level = 'error' WHERE id BETWEEN 100 AND 299", logsSchema),
		fmt.Sprintf("UPDATE %s.access SET created_at = created_at - INTERVAL 14 DAY WHERE id BETWEEN 1000 AND 1049", logsSchema),
		fmt.Sprintf("DELETE FROM %s.access WHERE id BETWEEN 5000 AND 5299", logsSchema),
		fmt.Sprintf("UPDATE %s.access SET msg = NULL WHERE id = 7", logsSchema),
	}
	if err := tlsExecOneConn(cl.GetMaster(), writes...); err != nil {
		return fail("writes: %s", err)
	}
	if err := check("after the writes on the parent"); err != nil {
		return fail("%s", err)
	}
	tlsLogf(cl, "PASS %s: %s keeps %s.access partitioned by %s in %v, equal partition by partition to the parent after the init (%v) and after inserts now and 40 days ahead, updates, rows moved to another week and deletes", name, run.name, logsSchema, logsPartPeriod, schemas, names)
	return true
}

// logsSamePartitions compares, partition by partition, the child's destination schema.access
// with the parent's rows of the same range: COUNT(*) and BIT_XOR(CRC32(CONCAT_WS(...))). It
// returns the partitions compared.
func logsSamePartitions(cl, child *cluster.Cluster, schema string) (int, error) {
	cdb := child.GetMaster().Conn.DB
	type part struct {
		name, bound string
	}
	rows, err := cdb.Query("SELECT PARTITION_NAME, PARTITION_DESCRIPTION FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'access' AND PARTITION_NAME IS NOT NULL ORDER BY PARTITION_ORDINAL_POSITION", schema)
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
		p.bound = strings.Trim(p.bound, "'")
		parts = append(parts, p)
	}
	rows.Close()
	if len(parts) < 2 {
		return 0, fmt.Errorf("%d partitions: the destination is not partitioned by week", len(parts))
	}
	sum := func(db *sql.DB, from string) (int, string, error) {
		var n int
		var x sql.NullString
		err := db.QueryRow("SELECT COUNT(*), BIT_XOR(CRC32(CONCAT_WS('#', id, created_at, level, IFNULL(msg, '\\\\N')))) FROM "+from).Scan(&n, &x)
		return n, x.String, err
	}
	pdb := cl.GetMaster().Conn.DB
	total := 0
	for i, p := range parts {
		where := fmt.Sprintf("created_at < '%s'", p.bound)
		if i > 0 {
			where = fmt.Sprintf("created_at >= '%s' AND %s", parts[i-1].bound, where)
		}
		a, ha, err := sum(pdb, logsSchema+".access WHERE "+where)
		if err != nil {
			return 0, fmt.Errorf("parent: %w", err)
		}
		b, hb, err := sum(cdb, schema+".access PARTITION (`"+p.name+"`)")
		if err != nil {
			return 0, fmt.Errorf("child: %w", err)
		}
		if a != b || ha != hb {
			return 0, fmt.Errorf("partition %s (< %s): parent %d rows checksum %s, child %d rows checksum %s", p.name, p.bound, a, ha, b, hb)
		}
		tlsLogf(cl, "AnalyticChildTableMode: %s partition %s (< %s): %d rows, checksum %s on both", schema, p.name, p.bound, b, hb)
		total += b
	}
	// every row of the parent is in a partition: none past the last bound
	n, _, err := sum(pdb, logsSchema+".access")
	if err != nil {
		return 0, err
	}
	if n != total {
		return 0, fmt.Errorf("the parent has %d rows, the partitions %d", n, total)
	}
	return len(parts), nil
}
