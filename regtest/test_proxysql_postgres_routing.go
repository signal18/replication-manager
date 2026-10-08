// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestProxySQLPostgresRouting (#1893): a ProxySQL in front of a PostgreSQL
// cluster is driven through its pgsql_* objects. It checks, through the first
// proxy of the cluster (a ProxySQL):
//
//  1. placement: the writer hostgroup holds the master only, a replica is an
//     ONLINE reader;
//  2. routing: a write and a SELECT ... FOR UPDATE reach the master, a plain
//     SELECT reaches a replica with the pgsqlrwsplit proxy tag (the master
//     without it);
//  3. after a switchover: the writer hostgroup holds the new master only, the
//     old master is a reader, and the routing of 2 follows;
//  4. a switchover back to the original master.
//
// Not in the ALL list: it switches the master over twice. Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testProxySQLPostgresRouting
func (regtest *RegTest) TestProxySQLPostgresRouting(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModProxySQL, level, "TEST proxysql-postgres-routing: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}

	master := cl.GetMaster()
	if master == nil || !master.IsPostgreSQLHost() {
		return fail("needs a PostgreSQL master")
	}
	if len(cl.GetSlaves()) == 0 {
		return fail("needs a PostgreSQL replica")
	}
	if len(cl.Proxies) == 0 || cl.Proxies[0] == nil || cl.Proxies[0].GetType() != config.ConstProxySqlproxy {
		return fail("needs a ProxySQL as the first proxy of the cluster")
	}
	prx := cl.Proxies[0]
	readSplit := cl.Configurator.IsFilterInProxyTags("proxy.route.pgsqlrwsplit")
	original := master

	// --- 1. placement ---
	if !proxysqlPgWaitFor(60*time.Second, func() bool { return proxysqlPgPlacementOK(prx, cl.GetMaster()) }) {
		return fail("placement: writer hostgroup is not the master %s alone, or no replica is an ONLINE reader (%s)", cl.GetMaster().URL, proxysqlPgBackends(prx))
	}
	logf("TEST", "placement: writer %s, readers ONLINE — OK", cl.GetMaster().URL)

	// --- 2. routing ---
	if err := proxysqlPgProbeTable(cl); err != nil {
		return fail("routing: %s", err)
	}
	if err := proxysqlPgRouting(cl, readSplit); err != nil {
		return fail("routing: %s", err)
	}
	logf("TEST", "routing: writes and SELECT ... FOR UPDATE to the master, reads to %s — OK", map[bool]string{true: "a replica", false: "the master"}[readSplit])

	// --- 3. switchover ---
	cl.SwitchoverWaitTest()
	newMaster := cl.GetMaster()
	if newMaster == nil || newMaster.URL == original.URL {
		return fail("switchover did not move the master off %s", original.URL)
	}
	ok := proxysqlPgWaitFor(90*time.Second, func() bool {
		return proxysqlPgPlacementOK(prx, newMaster) && proxysqlPgIsOnlineReader(prx, original)
	})
	if !ok {
		return fail("after switchover: writer hostgroup is not the new master %s alone, or the old master %s is not an ONLINE reader (%s)", newMaster.URL, original.URL, proxysqlPgBackends(prx))
	}
	logf("TEST", "switchover: writer %s, old master %s an ONLINE reader — OK", newMaster.URL, original.URL)
	if err := proxysqlPgRouting(cl, readSplit); err != nil {
		return fail("routing after switchover: %s", err)
	}
	logf("TEST", "routing after switchover — OK")

	// --- 4. back to the original master ---
	cl.SwitchoverWaitTest()
	if !proxysqlPgWaitFor(90*time.Second, func() bool {
		m := cl.GetMaster()
		return m != nil && m.URL == original.URL && proxysqlPgPlacementOK(prx, m)
	}) {
		return fail("switchover back: master is not %s again with the proxy following (%s)", original.URL, proxysqlPgBackends(prx))
	}
	logf("TEST", "switchover back to %s — OK", original.URL)
	return true
}

type proxysqlPgView struct {
	BackendsWrite []cluster.Backend `json:"backendsWrite"`
	BackendsRead  []cluster.Backend `json:"backendsRead"`
}

func proxysqlPgBackendsView(prx cluster.DatabaseProxy) proxysqlPgView {
	var v proxysqlPgView
	if raw, err := json.Marshal(prx); err == nil {
		json.Unmarshal(raw, &v)
	}
	return v
}

func proxysqlPgBackends(prx cluster.DatabaseProxy) string {
	v := proxysqlPgBackendsView(prx)
	s := "writers:"
	for _, b := range v.BackendsWrite {
		s += fmt.Sprintf(" %s:%s/%s", b.Host, b.Port, b.PrxStatus)
	}
	s += " readers:"
	for _, b := range v.BackendsRead {
		s += fmt.Sprintf(" %s:%s/%s", b.Host, b.Port, b.PrxStatus)
	}
	return s
}

// proxysqlPgPlacementOK: the writer hostgroup holds the master alone and
// ONLINE, and a server other than the master is an ONLINE reader.
func proxysqlPgPlacementOK(prx cluster.DatabaseProxy, master *cluster.ServerMonitor) bool {
	if master == nil {
		return false
	}
	v := proxysqlPgBackendsView(prx)
	if len(v.BackendsWrite) != 1 || v.BackendsWrite[0].Host != master.Host || v.BackendsWrite[0].Port != master.Port || v.BackendsWrite[0].PrxStatus != "ONLINE" {
		return false
	}
	for _, b := range v.BackendsRead {
		if (b.Host != master.Host || b.Port != master.Port) && b.PrxStatus == "ONLINE" {
			return true
		}
	}
	return false
}

func proxysqlPgIsOnlineReader(prx cluster.DatabaseProxy, s *cluster.ServerMonitor) bool {
	for _, b := range proxysqlPgBackendsView(prx).BackendsRead {
		if b.Host == s.Host && b.Port == s.Port && b.PrxStatus == "ONLINE" {
			return true
		}
	}
	return false
}

func proxysqlPgWaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return cond()
}

// proxysqlPgAddrs: the addresses a server answers inet_server_addr() with.
func proxysqlPgAddrs(s *cluster.ServerMonitor) map[string]bool {
	addrs := map[string]bool{s.Host: true}
	if ips, err := net.LookupHost(s.Host); err == nil {
		for _, ip := range ips {
			addrs[ip] = true
		}
	}
	return addrs
}

// proxysqlPgProbeTable creates the table the routing checks write to, once,
// before the switchover: after it the checks only run DML, a DDL on a
// promoted logical publisher being #1921, not a proxy matter. Its key is a
// uuid: a serial key would depend on the sequence of a promoted logical
// publisher (#1922), not on the routing.
func proxysqlPgProbeTable(cl *cluster.Cluster) error {
	db, err := cl.GetClusterProxyConn()
	if err != nil {
		return fmt.Errorf("no connection through the proxy: %s", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS regtest_proxysql_probe (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), src inet DEFAULT inet_server_addr())"); err != nil {
		return fmt.Errorf("create table through the proxy: %s", err)
	}
	// let the table reach the replicas (logical replication: DDL replicated
	// as a row) before rows are written to it
	time.Sleep(5 * time.Second)
	return nil
}

// proxysqlPgRouting sends a write, a SELECT ... FOR UPDATE and a plain SELECT
// through the proxy and checks which server answered each.
func proxysqlPgRouting(cl *cluster.Cluster, readSplit bool) error {
	master := cl.GetMaster()
	db, err := cl.GetClusterProxyConn()
	if err != nil {
		return fmt.Errorf("no connection through the proxy: %s", err)
	}
	defer db.Close()
	masterAddrs := proxysqlPgAddrs(master)
	var row struct {
		ID   string `db:"id"`
		Addr string `db:"addr"`
	}
	if err := db.Get(&row, "INSERT INTO regtest_proxysql_probe DEFAULT VALUES RETURNING id::text AS id, host(src) AS addr"); err != nil {
		return fmt.Errorf("write through the proxy: %s", err)
	}
	addr := row.Addr
	if !masterAddrs[addr] {
		return fmt.Errorf("write answered by %s, not by the master %s", addr, master.URL)
	}
	if err := db.Get(&addr, "SELECT host(inet_server_addr()) FROM regtest_proxysql_probe WHERE id = $1 FOR UPDATE", row.ID); err != nil {
		return fmt.Errorf("SELECT ... FOR UPDATE through the proxy: %s", err)
	}
	if !masterAddrs[addr] {
		return fmt.Errorf("SELECT ... FOR UPDATE answered by %s, not by the master %s", addr, master.URL)
	}
	if err := db.Get(&addr, "SELECT host(inet_server_addr())"); err != nil {
		return fmt.Errorf("read through the proxy: %s", err)
	}
	if readSplit && masterAddrs[addr] {
		return fmt.Errorf("read answered by the master %s with the pgsqlrwsplit tag", master.URL)
	}
	if !readSplit && !masterAddrs[addr] {
		return fmt.Errorf("read answered by %s, not by the master %s, without the pgsqlrwsplit tag", addr, master.URL)
	}
	return nil
}
