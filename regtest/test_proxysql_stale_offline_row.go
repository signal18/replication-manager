// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/misc"
)

// TestProxySQLStaleOfflineRowRecovery (#2295): a master recovered from a failure
// can find, in every ProxySQL, its writer row still in the writer hostgroup but
// OFFLINE_SOFT, next to a stale row of its own in the offline hostgroup (666) that
// an earlier bootstrap left. Reintroducing it then moved the 666 row onto the
// writer row (UNIQUE constraint failed) and nothing retried: writes stayed off
// until fixed by hand.
//
// The test puts every ProxySQL of the cluster in that state for the current
// master, as the bootstrap and the standalone drain did, and expects
// replication-manager within 60s to bring the writer row back ONLINE and drop
// the 666 row. The master's reader row, drained too, must be settled (#2298):
// ONLINE when the configuration wants the master to serve reads
// (proxy-servers-read-on-master), otherwise ONLINE or dropped, never left
// OFFLINE_SOFT.
func (regtest *RegTest) TestProxySQLStaleOfflineRowRecovery(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModProxySQL, level, "TEST proxysql-stale-offline-row: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}

	master := cl.GetMaster()
	if master == nil {
		return fail("cluster has no master")
	}
	host, port := misc.Unbracket(master.Host), master.Port

	var proxies []*cluster.ProxySQLProxy
	for _, prx := range cl.Proxies {
		if p, ok := prx.(*cluster.ProxySQLProxy); ok && !p.IsIgnored() {
			proxies = append(proxies, p)
		}
	}
	if len(proxies) == 0 {
		return fail("no ProxySQL attached to the cluster")
	}

	// connect is called per check: a connection kept across the wait would see
	// nothing wrong if ProxySQL restarted underneath.
	withConn := func(p *cluster.ProxySQLProxy, f func(psql *proxysql.ProxySQL) bool) bool {
		psql, err := p.Connect()
		if err != nil {
			logf(config.LvlErr, "could not connect to ProxySQL %s: %s", p.Name, err)
			return false
		}
		defer psql.Connection.Close()
		return f(&psql)
	}

	// --- seed: stale 666 row + writer row OFFLINE_SOFT, as in the incident ---
	for _, p := range proxies {
		ok := withConn(p, func(psql *proxysql.ProxySQL) bool {
			if !psql.HasRowInHostgroup(host, port, psql.WriterHG) {
				logf(config.LvlErr, "ProxySQL %s has no writer row for master %s", p.Name, master.URL)
				return false
			}
			if err := psql.AddOfflineServer(host, port, p.UseSSL()); err != nil {
				logf(config.LvlErr, "ProxySQL %s: could not add the stale offline row: %s", p.Name, err)
				return false
			}
			if err := psql.SetOfflineSoft(host, port); err != nil {
				logf(config.LvlErr, "ProxySQL %s: could not drain the writer row: %s", p.Name, err)
				return false
			}
			if err := psql.LoadServersToRuntime(); err != nil {
				logf(config.LvlErr, "ProxySQL %s: could not load servers to runtime: %s", p.Name, err)
				return false
			}
			return true
		})
		if !ok {
			return fail("could not seed ProxySQL %s", p.Name)
		}
	}
	logf("TEST", "seeded a stale offline row and an OFFLINE_SOFT writer row for %s on %d ProxySQL", master.URL, len(proxies))

	// --- expect: writer ONLINE, no 666 row, on every ProxySQL ---
	recovered := proxyReadBackendWaitFor(60*time.Second, func() bool {
		for _, p := range proxies {
			if !withConn(p, func(psql *proxysql.ProxySQL) bool {
				if psql.HasRowInHostgroup(host, port, proxysql.OfflineHG) {
					return false
				}
				_, status, _, _, _, _, err := psql.GetStatsForHostWrite(host, port)
				if err != nil || status != "ONLINE" {
					return false
				}
				// the reader row: dropped (gone from the configuration), or ONLINE
				if !psql.HasRowInHostgroup(host, port, psql.ReaderHG) {
					return !cl.Configurator.HasProxyReadLeader()
				}
				_, rstatus, _, _, _, _, err := psql.GetStatsForHostRead(host, port)
				return err == nil && rstatus == "ONLINE"
			}) {
				return false
			}
		}
		return true
	})

	// --- cleanup: on a failed run, rows the seed drained may still be OFFLINE_SOFT ---
	for _, p := range proxies {
		withConn(p, func(psql *proxysql.ProxySQL) bool {
			if err := psql.SetOnlineSoft(host, port); err != nil {
				logf(config.LvlErr, "ProxySQL %s: cleanup could not set %s ONLINE: %s", p.Name, master.URL, err)
			}
			_ = psql.LoadServersToRuntime()
			return true
		})
	}

	if !recovered {
		return fail("master %s was not back ONLINE as writer, without an offline row and with its reader row ONLINE or dropped, on every ProxySQL within 60s", master.URL)
	}
	logf("TEST", "master %s back ONLINE as writer on every ProxySQL, stale offline row dropped, reader row settled — OK", master.URL)
	return true
}
