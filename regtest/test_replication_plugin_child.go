// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The CDC core's children (CDC_CORE.md), on a child of this cluster:
//   - testColumnstoreChildCDC: a ColumnStore child (topology columnstore, seeded from a
//     splitdump backup), its tables carried by replplugin-columnstore;
//   - testAnalyticChildCDC: an analytic child of the destination plugin named by the
//     environment REPMAN_REGTEST_ANALYTIC_DESTINATION (skipped without it), the schema's
//     tables made by the plugin at the seed, a table without primary key and a latin1 table
//     included: they must stay InnoDB with their schema state (WARN0340).
// Both: a write load on the parent (inserts, updates, deletes) during which the parent
// switches over (the plugin follows the new master) and the child's database is stopped and
// started (the plugin applies again what it had not recorded); then the child must hold the
// parent's rows exactly (a hash of every row by primary key, per table) once the plugin's
// applied GTID reached the parent's; then replication-plugin-source points to a replica of the
// parent, the plugin restarts on it, a second load must arrive the same; that replica stopped,
// the plugin pauses with ERR00131, never moving to another server. The child is deleted and the
// schema dropped at the end.

const (
	cdcRegtestSchema = "regtest_cdc"
	cdcRegtestRows   = 5000
	cdcRegtestLoad   = 60 * time.Second
	cdcRegtestCatch  = 10 * time.Minute
)

func (regtest *RegTest) TestColumnstoreChildCDC(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return withSplitdumpParentBackup(cl, "child", func() bool {
		return testReplicationPluginChild(cl, "ColumnstoreChildCDC", map[string]any{"topology": config.TopoColumnstore, "dbCount": 1}, "columnstore", false)
	})
}

func (regtest *RegTest) TestAnalyticChildCDC(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	dest := strings.TrimSpace(os.Getenv("REPMAN_REGTEST_ANALYTIC_DESTINATION"))
	if dest == "" {
		tlsLogf(cl, "Skipping AnalyticChildCDC: REPMAN_REGTEST_ANALYTIC_DESTINATION names no destination plugin")
		return true
	}
	return testReplicationPluginChild(cl, "AnalyticChildCDC", map[string]any{"destination": dest, "tables": cdcRegtestSchema + ".*"}, dest, true)
}

func testReplicationPluginChild(cl *cluster.Cluster, name string, extra map[string]any, plugin string, refusals bool) bool {
	fail := func(format string, args ...any) bool {
		tlsLogf(cl, "FAIL "+name+": "+format, args...)
		return false
	}
	master := cl.GetMaster()
	if master == nil || !master.IsMariaDB() || len(cl.GetSlaves()) == 0 {
		tlsLogf(cl, "Skipping %s: needs a MariaDB master and a replica", name)
		return true
	}
	// the schema, before the backup the child is seeded from
	stmts := []string{
		"DROP DATABASE IF EXISTS " + cdcRegtestSchema,
		"CREATE DATABASE " + cdcRegtestSchema,
		"CREATE TABLE " + cdcRegtestSchema + ".orders (id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, customer INT NOT NULL, status VARCHAR(16) NOT NULL, amount DECIMAL(12,2), note VARCHAR(120), created DATETIME NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE " + cdcRegtestSchema + ".lines (id BIGINT NOT NULL PRIMARY KEY, order_id BIGINT NOT NULL, qty INT NOT NULL, label VARCHAR(60)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
	}
	// the tables the destination must refuse: a latin1 one, and one without primary key unless
	// the parent forces primary keys (innodb_force_primary_key, set by the configurator: such a
	// table cannot exist there)
	refused := []string{}
	if refusals {
		if !strings.EqualFold(tlsLiveVariable(master, "INNODB_FORCE_PRIMARY_KEY"), "ON") {
			refused = append(refused, "nopk")
			stmts = append(stmts,
				"CREATE TABLE "+cdcRegtestSchema+".nopk (a INT, b VARCHAR(10)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
				"INSERT INTO "+cdcRegtestSchema+".nopk VALUES (1,'a'),(2,'b')")
		}
		refused = append(refused, "latin")
		stmts = append(stmts,
			"CREATE TABLE "+cdcRegtestSchema+".latin (id INT PRIMARY KEY, name VARCHAR(40)) ENGINE=InnoDB DEFAULT CHARSET=latin1",
			"INSERT INTO "+cdcRegtestSchema+".latin VALUES (1,'a'),(2,'b')")
	}
	stmts = append(stmts,
		"USE "+cdcRegtestSchema, // the seq_1_to_N tables of the sequence engine need a schema
		fmt.Sprintf("INSERT INTO %s.orders (customer,status,amount,note,created) SELECT seq MOD 1000, 'new', (seq MOD 10000)/100, CONCAT('row ', seq), '2026-01-01' + INTERVAL seq SECOND FROM seq_1_to_%d", cdcRegtestSchema, cdcRegtestRows),
		fmt.Sprintf("INSERT INTO %s.lines SELECT seq, 1 + seq MOD %d, seq MOD 7, CONCAT('line ', seq) FROM seq_1_to_%d", cdcRegtestSchema, cdcRegtestRows, 2*cdcRegtestRows))
	if err := tlsExecOneConn(master, stmts...); err != nil {
		return fail("schema: %s", err)
	}
	defer tlsExecOneConn(cl.GetMaster(), "DROP DATABASE IF EXISTS "+cdcRegtestSchema)
	// the seed must hold the schema: a fresh logical backup
	if err := master.JobBackupLogical(context.Background()); err != nil {
		return fail("logical backup of %s: %s", cl.Name, err)
	}

	// a name per run: OpenSVC may still be unprovisioning the previous run's child
	extra["name"] = fmt.Sprintf("%s-cdc%d", cl.Name, time.Now().Unix()%100000)
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
	base := "/api/clusters/" + run.name
	// the plugin runs
	if err := tlsWait(5*time.Minute, func() error {
		if st, ok := child.ReplicationPluginStatuses()[plugin]; ok && st.State == "running" {
			return nil
		}
		if d := stagingAlert(run.tok, base, "ERR00132"); d != "" {
			return stagingStop{d}
		}
		return fmt.Errorf("plugin %s of %s not running: %v", plugin, run.name, child.ReplicationPluginStatuses())
	}); err != nil {
		return fail("%s", err)
	}
	if refusals {
		// the two tables the destination cannot hold, each with its schema state
		for _, tbl := range refused {
			found := false
			for _, s := range child.SchemaStateMachine.GetOpenStates() {
				if strings.Contains(s.ErrDesc, cdcRegtestSchema+"."+tbl) && strings.HasPrefix(s.ErrKey, "WARN0340") {
					found = true
				}
			}
			if !found {
				return fail("no WARN0340 for %s.%s: %v", cdcRegtestSchema, tbl, child.SchemaStateMachine.GetOpenStates())
			}
		}
	}

	// the feed is a replication of its plugin (repl-<source>-<target>-cdc) in the child
	// master's server.Replications, beside its channel from the parent, and the analytic
	// child's status reads it through that object (REPLICATION_INTERFACE.md, step 4)
	want := cluster.CanonicalReplicationPluginName(plugin)
	if err := tlsWait(time.Minute, func() error {
		cm := child.GetMaster()
		if cm == nil {
			return fmt.Errorf("no child master")
		}
		var seen []string
		for _, r := range cm.Replications {
			seen = append(seen, r.Name()+"="+r.Plugin().Name+"/"+string(cluster.KindOf(r))+"/"+r.Row().SlaveIORunning.String)
			if cluster.KindOf(r) == cluster.ReplicationKindCDC && r.Plugin().Name == want && r.Row().SlaveIORunning.String == "Yes" && !cluster.CanFailover(r) && r.TargetServer() == cm {
				return nil
			}
		}
		return fmt.Errorf("no running replication of %s in %s's replications: %v", want, cm.URL, seen)
	}); err != nil {
		return fail("%s", err)
	}
	if refusals {
		if f, ok := child.AnalyticFeedStatus(); !ok || !f.Running || f.Replication != want || f.Status == nil {
			return fail("the analytic feed through its replication: %+v", f)
		}
		tlsLogf(cl, "%s: the feed is a replication of %s in %s's replications", name, want, child.GetMaster().URL)
	}

	// REPMAN_REGTEST_MERGE_INTERVAL (seconds): the copies fed through the InnoDB staging,
	// merged at that period; the rows are then compared once a merge holds the parent's GTID
	if mi, _ := strconv.Atoi(os.Getenv("REPMAN_REGTEST_MERGE_INTERVAL")); mi > 0 {
		stok, err := APIToken(child, config.GrantClusterSettings)
		if err != nil {
			return fail("%s", err)
		}
		if code, _, resp, err := APIRequest(stok, "GET", base+"/settings/actions/set/replication-plugin-merge-interval/"+strconv.Itoa(mi)); err != nil || code != 200 {
			return fail("replication-plugin-merge-interval: HTTP %d %v %s", code, err, resp)
		}
		if err := tlsWait(3*time.Minute, func() error {
			if st, ok := child.ReplicationPluginStatuses()[plugin]; ok && st.State == "running" && st.NextMergeAt != "" {
				return nil
			}
			return fmt.Errorf("plugin %s not running with its staging yet", plugin)
		}); err != nil {
			return fail("%s", err)
		}
		tlsLogf(cl, "%s: the copies fed through the staging, merged every %d s", name, mi)
	}

	// the load, with a switchover of the parent and a stop/start of the child's database
	var wg sync.WaitGroup
	stop := make(chan struct{})
	loadErr := make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); loadErr <- cdcRegtestLoadParent(cl, stop) }()
	time.Sleep(cdcRegtestLoad / 3)
	tlsLogf(cl, "%s: switchover of %s during the feed", name, cl.Name)
	cl.SwitchoverWaitTest()
	time.Sleep(cdcRegtestLoad / 6)
	cm := child.GetMaster()
	tlsLogf(cl, "%s: stop and start of %s during the feed", name, cm.URL)
	url := cm.URL
	child.StopDatabaseService(cm)
	// the stop's orchestration done before the start (a start during it is refused, 409)
	if err := tlsWait(5*time.Minute, func() error {
		if sv := child.GetServerFromURL(url); sv != nil && !sv.IsDown() {
			return fmt.Errorf("%s still up", url)
		}
		return nil
	}); err != nil {
		close(stop)
		wg.Wait()
		return fail("the child's database did not stop: %s", err)
	}
	time.Sleep(20 * time.Second)
	var started time.Time
	if err := tlsWait(10*time.Minute, func() error {
		sv := child.GetServerFromURL(url)
		if sv == nil || sv.IsDown() {
			// once, then again each 2 minutes while it stays down (a start refused while
			// another orchestration ran)
			if sv != nil && time.Since(started) > 2*time.Minute {
				started = time.Now()
				child.StartDatabaseService(sv)
			}
			return fmt.Errorf("%s not back", url)
		}
		return nil
	}); err != nil {
		close(stop)
		wg.Wait()
		return fail("the child's database did not start again: %s", err)
	}
	time.Sleep(cdcRegtestLoad / 3)
	close(stop)
	wg.Wait()
	if err := <-loadErr; err != nil {
		return fail("load: %s", err)
	}
	if err := cdcRegtestSame(cl, child, plugin, []string{"orders", "lines"}); err != nil {
		return fail("after the switchover and the child's restart: %s", err)
	}

	// a dedicated replica as the source
	replica := cl.GetSlaves()[0]
	tok, err := APIToken(child, config.GrantClusterSettings)
	if err != nil {
		return fail("%s", err)
	}
	if code, _, resp, err := APIRequest(tok, "GET", base+"/settings/actions/set/replication-plugin-source/"+replica.Host+":"+replica.Port); err != nil || code != 200 {
		return fail("replication-plugin-source: HTTP %d %v %s", code, err, resp)
	}
	if err := tlsWait(3*time.Minute, func() error {
		if st, ok := child.ReplicationPluginStatuses()[plugin]; ok && st.State == "running" && cdcPluginSource(child, plugin) == replica.Host {
			return nil
		}
		return fmt.Errorf("plugin not running on %s yet (source %s)", replica.URL, cdcPluginSource(child, plugin))
	}); err != nil {
		return fail("%s", err)
	}
	stop2 := make(chan struct{})
	go func() { time.Sleep(cdcRegtestLoad / 2); close(stop2) }()
	if err := cdcRegtestLoadParent(cl, stop2); err != nil {
		return fail("load with the replica source: %s", err)
	}
	if err := cdcRegtestSame(cl, child, plugin, []string{"orders", "lines"}); err != nil {
		return fail("with the replica source %s: %s", replica.URL, err)
	}
	// the replica lost: the plugin pauses with ERR00131, it never moves
	cl.StopDatabaseService(replica)
	if err := tlsWait(5*time.Minute, func() error {
		if d := stagingAlert(run.tok, base, "ERR00131"); d != "" {
			return nil
		}
		return fmt.Errorf("no ERR00131 on %s with %s stopped", run.name, replica.URL)
	}); err != nil {
		cl.StartDatabaseService(replica)
		return fail("%s", err)
	}
	// paused: no runner, or one still on the lost replica, never on another server
	src := cdcPluginSource(child, plugin)
	moved := src != "" && src != replica.Host
	cl.StartDatabaseService(replica)
	if moved {
		return fail("the plugin moved away from its lost replica source %s", replica.URL)
	}
	tlsLogf(cl, "PASS %s: %s fed by %s through a switchover and a restart of the child, then from the replica %s, rows identical to %s; the replica lost: paused with ERR00131%s", name, run.name, plugin, replica.URL, cl.Name, map[bool]string{true: "; " + strings.Join(refused, " and ") + " left InnoDB with WARN0340", false: ""}[refusals])
	return true
}

// cdcRegtestLoadParent writes on the parent's current master until stop: per transaction an
// order inserted, one updated, one line updated, some deleted.
func cdcRegtestLoadParent(cl *cluster.Cluster, stop <-chan struct{}) error {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	errs := 0
	for i := 0; ; i++ {
		select {
		case <-stop:
			return nil
		default:
		}
		m := cl.GetMaster()
		if m == nil || m.Conn == nil {
			time.Sleep(time.Second)
			continue
		}
		k, l := 1+r.Intn(cdcRegtestRows), 1+r.Intn(2*cdcRegtestRows)
		stmts := []string{
			"START TRANSACTION",
			fmt.Sprintf("INSERT INTO %s.orders (customer,status,amount,note,created) VALUES (%d,'new',%d.25,'load %d',NOW())", cdcRegtestSchema, r.Intn(1000), r.Intn(9999), i),
			fmt.Sprintf("UPDATE %s.orders SET status='paid', amount=amount+1, note='upd %d' WHERE id=%d", cdcRegtestSchema, i, k),
			fmt.Sprintf("UPDATE %s.lines SET qty=qty+1 WHERE id=%d", cdcRegtestSchema, l),
		}
		if i%3 == 0 {
			stmts = append(stmts, fmt.Sprintf("DELETE FROM %s.orders WHERE id=%d", cdcRegtestSchema, 1+r.Intn(cdcRegtestRows)))
		}
		if i%7 == 0 {
			stmts = append(stmts, fmt.Sprintf("DELETE FROM %s.lines WHERE id=%d", cdcRegtestSchema, 1+r.Intn(2*cdcRegtestRows)))
		}
		stmts = append(stmts, "COMMIT")
		if err := tlsExecOneConn(m, stmts...); err != nil {
			// a switchover refuses writes for a moment: retried on the new master
			if errs++; errs > 200 {
				return err
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// cdcPluginSource is the source host of the running plugin, "" when none runs (paused).
func cdcPluginSource(child *cluster.Cluster, plugin string) string {
	return child.ReplicationPluginSourceHost(plugin)
}

// cdcRegtestSame waits until the plugin applied the parent's GTID, then compares the rows of
// each table, parent master against child master.
func cdcRegtestSame(cl, child *cluster.Cluster, plugin string, tables []string) error {
	if err := cdcWaitApplied(cl, child, plugin); err != nil {
		return err
	}
	m, cm := cl.GetMaster(), child.GetMaster()
	for _, t := range tables {
		a, na, err := cdcTableHash(m.Conn.DB, t)
		if err != nil {
			return err
		}
		b, nb, err := cdcTableHash(cm.Conn.DB, t)
		if err != nil {
			return err
		}
		if a != b || na != nb {
			return fmt.Errorf("%s.%s: parent %d rows %s, child %d rows %s", cdcRegtestSchema, t, na, a[:12], nb, b[:12])
		}
	}
	return nil
}

// cdcWaitApplied waits until the plugin applied the parent's GTID, and merged it when it
// feeds an accumulation.
func cdcWaitApplied(cl, child *cluster.Cluster, plugin string) error {
	m := cl.GetMaster()
	if m == nil {
		return fmt.Errorf("no master on %s", cl.Name)
	}
	var target string
	if err := m.Conn.Get(&target, "SELECT @@gtid_binlog_pos"); err != nil {
		return err
	}
	return tlsWait(cdcRegtestCatch, func() error {
		st, ok := child.ReplicationPluginStatuses()[plugin]
		if !ok {
			return fmt.Errorf("plugin %s not running", plugin)
		}
		if !gtidCovers(st.AppliedGTID, target) || (st.NextMergeAt != "" && !gtidCovers(st.MergedGTID, target)) {
			return fmt.Errorf("applied %q merged %q, parent at %q", st.AppliedGTID, st.MergedGTID, target)
		}
		return nil
	})
}

// gtidCovers: every domain of want is at least at its sequence in have.
func gtidCovers(have, want string) bool {
	seqs := map[string]uint64{}
	for _, g := range strings.Split(have, ",") {
		p := strings.Split(strings.TrimSpace(g), "-")
		if len(p) == 3 {
			n, _ := strconv.ParseUint(p[2], 10, 64)
			if n > seqs[p[0]] {
				seqs[p[0]] = n
			}
		}
	}
	for _, g := range strings.Split(want, ",") {
		p := strings.Split(strings.TrimSpace(g), "-")
		if len(p) != 3 {
			continue
		}
		n, _ := strconv.ParseUint(p[2], 10, 64)
		if seqs[p[0]] < n {
			return false
		}
	}
	return true
}

// cdcTableHash hashes every row of the table in primary key order, as text.
func cdcTableHash(db *sql.DB, table string) (string, int, error) {
	rows, err := db.Query("SELECT * FROM " + cdcRegtestSchema + "." + table + " ORDER BY id")
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	h := sha256.New()
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", n, err
		}
		for _, v := range vals {
			if v == nil {
				h.Write([]byte("\\N"))
			} else {
				h.Write(v)
			}
			h.Write([]byte{0})
		}
		n++
	}
	return hex.EncodeToString(h.Sum(nil)), n, rows.Err()
}
