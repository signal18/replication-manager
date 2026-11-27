// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// tableChecksumDB is the throwaway schema testTableChecksum creates and drops.
const tableChecksumDB = "regtest_checksum"

// TestTableChecksum exercises the table checksum through the API on a real MariaDB cluster with
// replicas (#2465), in a throwaway schema with three tables (t_big of 2 M rows, t_one and t_two
// of 5000):
//   - the checksum of one table ends OK, the checksum of the schema ends OK on the three;
//   - a row changed on one replica only (sql_log_bin=0) makes that table ER, and only that one;
//   - the repair of that table puts the replica's row back and the table ends OK;
//   - a cancel sent while the schema checksum runs t_big ends it NA and the tables it did not
//     reach NA, nothing left running;
//   - a table dropped, the schema refreshed, then recreated answers 404 to its checksum until
//     the next schema refresh, then ends OK;
//   - no WARN0342 (a checksum that failed) is open for the schema at the end.
//
// It runs on a cluster with innodb_force_primary_key as well: the checksum's work tables carry
// their primary key. The schema is dropped at the end, whatever happens, and every replica must
// still replicate. MariaDB only (the checksum's SQL, the sequence engine), and SKIP otherwise.
//
// Not part of "ALL" (it writes on a replica on purpose). Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testTableChecksum
func (regtest *RegTest) TestTableChecksum(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testTableChecksum: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testTableChecksum: FAIL "+format, args...)
		return false
	}
	if APIToken == nil || APIRequest == nil {
		return fail("the API hooks are not wired (run the test through the server)")
	}
	master := cl.GetMaster()
	var replica *clusterpkg.ServerMonitor
	for _, s := range cl.GetSlaves() {
		if s != nil && s.Conn != nil {
			replica = s
			break
		}
	}
	if master == nil || master.Conn == nil || replica == nil {
		return fail("needs a master and a replica with a connection")
	}
	if !master.DBVersion.IsMariaDB() {
		step("SKIP: MariaDB only (the checksum's SQL and the sequence engine), master is %s", master.DBVersion.Flavor)
		return true
	}
	token, err := APIToken(cl, config.GrantClusterChecksum, config.GrantClusterChecksumRepair, config.GrantClusterSharding)
	if err != nil {
		return fail("no token: %s", err)
	}
	base := "/api/clusters/" + url.PathEscape(cl.Name)
	call := func(path string) (int, string) {
		code, _, body, err := APIRequest(token, http.MethodPost, base+path)
		if err != nil {
			return 0, err.Error()
		}
		return code, string(body)
	}
	table := func(name string) *dbhelper.Table {
		m := cl.GetMaster()
		if m == nil || m.DictTables == nil {
			return nil
		}
		return m.DictTables.Get(tableChecksumDB + "." + name)
	}
	syncOf := func(name string) string {
		if t := table(name); t != nil {
			return t.TableSync
		}
		return "absent"
	}
	// refresh asks for a schema scan until the table is (or is no longer) in the dictionary
	refresh := func(name string, present bool) error {
		return tlsWait(5*time.Minute, func() error {
			if (table(name) != nil) == present {
				return nil
			}
			cl.SetWaitMonitorSchema()
			return fmt.Errorf("%s.%s present=%t not reached after the schema refreshes", tableChecksumDB, name, !present)
		})
	}
	// waitDone waits until none of the tables is running (PR) or waiting (WA)
	waitDone := func(names ...string) error {
		return tlsWait(5*time.Minute, func() error {
			for _, n := range names {
				if s := syncOf(n); s == "PR" || s == "WA" || s == "" {
					return fmt.Errorf("%s still %q", n, s)
				}
			}
			return nil
		})
	}
	expect := func(want map[string]string) error {
		for n, w := range want {
			if got := syncOf(n); got != w {
				return fmt.Errorf("%s.%s is %q, want %q", tableChecksumDB, n, got, w)
			}
		}
		return nil
	}
	resetSync := func(names ...string) {
		for _, n := range names {
			if t := table(n); t != nil {
				t.TableSync = ""
			}
		}
	}
	all := []string{"t_big", "t_one", "t_two"}

	defer func() {
		if err := tlsExecOneConn(master, "DROP DATABASE IF EXISTS "+tableChecksumDB); err != nil {
			step("cleanup: %s", err)
		}
		cl.SetWaitMonitorSchema()
	}()
	if err := tlsExecOneConn(master,
		"DROP DATABASE IF EXISTS "+tableChecksumDB,
		"CREATE DATABASE "+tableChecksumDB,
		"USE "+tableChecksumDB,
		"CREATE TABLE t_big (id INT PRIMARY KEY, v VARCHAR(40)) ENGINE=InnoDB",
		"INSERT INTO t_big SELECT seq, CONCAT('v', seq) FROM seq_1_to_2000000",
		"CREATE TABLE t_one (id INT PRIMARY KEY, v VARCHAR(40)) ENGINE=InnoDB",
		"INSERT INTO t_one SELECT seq, CONCAT('v', seq) FROM seq_1_to_5000",
		"CREATE TABLE t_two (id INT PRIMARY KEY, v VARCHAR(40)) ENGINE=InnoDB",
		"INSERT INTO t_two SELECT seq, CONCAT('v', seq) FROM seq_1_to_5000"); err != nil {
		return fail("test schema: %s", err)
	}
	for _, n := range all {
		if err := refresh(n, true); err != nil {
			return fail("%s", err)
		}
	}
	step("schema %s in the dictionary", tableChecksumDB)

	// 1. one table
	if code, body := call("/schema/" + tableChecksumDB + "/t_one/actions/checksum-table"); code != http.StatusOK {
		return fail("checksum-table t_one: HTTP %d %s", code, body)
	}
	if err := waitDone("t_one"); err != nil {
		return fail("checksum of t_one: %s", err)
	}
	if err := expect(map[string]string{"t_one": "OK"}); err != nil {
		return fail("checksum of one table: %s", err)
	}
	step("PASS one table OK")

	// 2. the schema
	resetSync(all...)
	if code, body := call("/schema/" + tableChecksumDB + "/all/actions/checksum-schema"); code != http.StatusOK {
		return fail("checksum-schema: HTTP %d %s", code, body)
	}
	if err := waitDone(all...); err != nil {
		return fail("checksum of the schema: %s", err)
	}
	if err := expect(map[string]string{"t_big": "OK", "t_one": "OK", "t_two": "OK"}); err != nil {
		return fail("checksum of the schema: %s", err)
	}
	step("PASS schema OK")

	// 3. a row changed on one replica only: that table ER, the others OK
	if err := tlsExecOneConn(replica, "SET SESSION sql_log_bin=0",
		"UPDATE "+tableChecksumDB+".t_two SET v='diverged' WHERE id=42"); err != nil {
		return fail("divergence on %s: %s", replica.URL, err)
	}
	resetSync(all...)
	if code, body := call("/schema/" + tableChecksumDB + "/all/actions/checksum-schema"); code != http.StatusOK {
		return fail("checksum-schema: HTTP %d %s", code, body)
	}
	if err := waitDone(all...); err != nil {
		return fail("checksum of the schema with a divergence: %s", err)
	}
	if err := expect(map[string]string{"t_big": "OK", "t_one": "OK", "t_two": "ER"}); err != nil {
		return fail("divergence on %s: %s", replica.URL, err)
	}
	step("PASS divergence on %s found on t_two only", replica.URL)

	// 4. the repair of t_two puts the replica's row back
	if code, body := call("/schema/" + tableChecksumDB + "/t_two/actions/checksum-repair-table"); code != http.StatusOK {
		return fail("checksum-repair-table t_two: HTTP %d %s", code, body)
	}
	if err := tlsWait(5*time.Minute, func() error {
		if s := syncOf("t_two"); s != "OK" {
			return fmt.Errorf("t_two is %q after the repair", s)
		}
		return nil
	}); err != nil {
		return fail("repair: %s", err)
	}
	var v string
	if err := replica.Conn.Get(&v, "SELECT v FROM "+tableChecksumDB+".t_two WHERE id=42"); err != nil || v != "v42" {
		return fail("repair: row 42 on %s is %q (%v), want v42", replica.URL, v, err)
	}
	step("PASS repair of t_two: OK, the row is back on %s", replica.URL)

	// 5. a cancel while the schema checksum runs t_big
	resetSync(all...)
	if code, body := call("/schema/" + tableChecksumDB + "/all/actions/checksum-schema"); code != http.StatusOK {
		return fail("checksum-schema: HTTP %d %s", code, body)
	}
	deadline := time.Now().Add(time.Minute)
	for syncOf("t_big") != "PR" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if s := syncOf("t_big"); s != "PR" {
		return fail("cancel: t_big never seen running (%q)", s)
	}
	if code, body := call("/actions/checksum-cancel"); code != http.StatusOK {
		return fail("checksum-cancel: HTTP %d %s", code, body)
	}
	if err := waitDone(all...); err != nil {
		return fail("cancel: %s", err)
	}
	if err := expect(map[string]string{"t_big": "NA", "t_one": "NA", "t_two": "NA"}); err != nil {
		return fail("cancel: %s", err)
	}
	if cl.ChecksumIsRunning() {
		return fail("cancel: a checksum is still marked running")
	}
	step("PASS cancel: t_big NA, the tables after it NA, nothing running")

	// 6. a table dropped, the schema refreshed, then recreated: 404 until the next refresh, then OK
	if err := tlsExecOneConn(master, "DROP TABLE "+tableChecksumDB+".t_one"); err != nil {
		return fail("drop t_one: %s", err)
	}
	if err := refresh("t_one", false); err != nil {
		return fail("%s", err)
	}
	if err := tlsExecOneConn(master, "USE "+tableChecksumDB,
		"CREATE TABLE t_one (id INT PRIMARY KEY, v VARCHAR(40)) ENGINE=InnoDB",
		"INSERT INTO t_one SELECT seq, CONCAT('r', seq) FROM seq_1_to_5000"); err != nil {
		return fail("recreate t_one: %s", err)
	}
	if table("t_one") == nil {
		code, body := call("/schema/" + tableChecksumDB + "/t_one/actions/checksum-table")
		if code != http.StatusNotFound || !strings.Contains(body, "retry after the next schema refresh") {
			return fail("recreated t_one before the refresh: HTTP %d %s, want 404", code, body)
		}
		step("PASS recreated t_one before the refresh: 404 %s", strings.TrimSpace(body))
	} else {
		step("recreated t_one was already in the dictionary again: the 404 was not exercised")
	}
	if err := refresh("t_one", true); err != nil {
		return fail("%s", err)
	}
	if code, body := call("/schema/" + tableChecksumDB + "/t_one/actions/checksum-table"); code != http.StatusOK {
		return fail("checksum-table t_one after the refresh: HTTP %d %s", code, body)
	}
	if err := waitDone("t_one"); err != nil {
		return fail("checksum of the recreated t_one: %s", err)
	}
	if err := expect(map[string]string{"t_one": "OK"}); err != nil {
		return fail("recreated table: %s", err)
	}
	step("PASS recreated t_one OK after the refresh")

	// 7. no checksum of the schema left failed (WARN0342)
	if cl.SchemaStateMachine != nil {
		for _, w := range cl.SchemaStateMachine.GetOpenWarnings() {
			if strings.HasPrefix(w.ErrNumber, "WARN0342@"+tableChecksumDB+".") {
				return fail("%s open: %s", w.ErrNumber, w.ErrDesc)
			}
		}
	}

	// every replica still replicates
	if err := tlsWait(time.Minute, func() error {
		for _, s := range cl.GetSlaves() {
			if s != nil && s.IsReplicationBroken() {
				return fmt.Errorf("replica %s does not replicate", s.URL)
			}
		}
		return nil
	}); err != nil {
		return fail("%s", err)
	}
	step("PASS")
	return true
}
