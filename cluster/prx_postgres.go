// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"net"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// What the proxies carry for a PostgreSQL cluster: the traffic marker, the master check and
// the connection through the proxy speak PostgreSQL there, MySQL everywhere else.

// isPostgresMaster: the cluster's master is a PostgreSQL server.
func (cluster *Cluster) isPostgresMaster() bool {
	m := cluster.GetMaster()
	return m != nil && m.IsPostgreSQLHost()
}

// postgresProxyConnection connects through a proxy's write port with the credential and
// database the monitor uses on the master (lib/pq form).
func (cluster *Cluster) postgresProxyConnection(host string, port int) (*sqlx.DB, error) {
	m := cluster.GetMaster()
	if m == nil {
		return nil, fmt.Errorf("no master")
	}
	sslmode := "disable"
	if cluster.HaveDBTLSCert {
		sslmode = "require"
	}
	user, pass := cluster.GetDbUser(), cluster.GetDbPass()
	if cluster.Conf.MonitorWriteHeartbeatCredential != "" {
		user, pass = splitCredential(cluster.Conf.GetDecryptedValue("monitoring-write-heartbeat-credential"))
	}
	dsn := fmt.Sprintf("sslmode=%s host=%s port=%d user=%s dbname=%s connect_timeout=%d password=%s", sslmode, host, port, user, m.PostgressDB, cluster.Conf.Timeout, pass)
	return sqlx.Open("postgres", dsn)
}

func splitCredential(credential string) (string, string) {
	if i := strings.IndexByte(credential, ':'); i >= 0 {
		return credential[:i], credential[i+1:]
	}
	return credential, ""
}

// postgresInjectTrafficMarker writes the traffic marker through the proxy: one row updated
// at every tick, the PostgreSQL form of the DML marker (there is no CREATE OR REPLACE VIEW
// marker: PostgreSQL has no binary log position to mark, the marker is the write heartbeat
// and the proof that writes reach the primary through the proxy).
//
// The table is created ONCE on every server of the cluster, not only through the proxy:
// DDL is not carried by logical replication, and a subscriber missing the table stops
// applying (relation does not exist) -- so each subscriber gets the table and refreshes its
// subscription before the first marker is written.
func (cluster *Cluster) postgresInjectTrafficMarker(db *sqlx.DB, uuid string, readyKey string) error {
	if cluster.injectTrafficTableReady == nil {
		cluster.injectTrafficTableReady = make(map[string]bool)
	}
	if !cluster.injectTrafficTableReady[readyKey] {
		if err := cluster.postgresEnsureTrafficTable(); err != nil {
			return err
		}
		cluster.injectTrafficTableReady[readyKey] = true
	}
	_, err := db.Exec("INSERT INTO replication_manager_schema.pseudo_gtid_hist (id, uuid, ts) VALUES (1, $1, now()) ON CONFLICT (id) DO UPDATE SET uuid = EXCLUDED.uuid, ts = EXCLUDED.ts", uuid)
	return err
}

// postgresEnsureTrafficTable creates the marker table on every reachable server of the
// cluster (a standby in recovery refuses DDL and is skipped: it has the primary's copy), and
// makes every logical replication subscriber pick the table up.
func (cluster *Cluster) postgresEnsureTrafficTable() error {
	ddl := []string{"CREATE SCHEMA IF NOT EXISTS replication_manager_schema", "CREATE TABLE IF NOT EXISTS replication_manager_schema.pseudo_gtid_hist (id INT PRIMARY KEY, uuid VARCHAR(64), ts TIMESTAMPTZ)"}
	for _, s := range cluster.Servers {
		if s == nil || s.Conn == nil || s.IsDown() || !s.IsPostgreSQLHost() {
			continue
		}
		var inRecovery bool
		if err := s.Conn.Get(&inRecovery, "SELECT pg_is_in_recovery()"); err != nil || inRecovery {
			continue
		}
		// a subscriber keeps a read-only default: the monitor's session is switched
		if err := dbhelper.PostgresExecReadWrite(s.Conn, ddl...); err != nil {
			return fmt.Errorf("traffic marker table on %s: %w", s.URL, err)
		}
		for _, r := range s.Replications {
			if name := r.ConnectionName.String; name != "" && name != dbhelper.PostgresStandbyConnectionName {
				// a subscriber: tables created after the subscription need a refresh
				if _, err := dbhelper.PostgresRefreshSubscription(s.Conn, name); err != nil {
					return fmt.Errorf("subscription refresh on %s: %w", s.URL, err)
				}
			}
		}
	}
	return nil
}

// postgresProxyServesMaster tells whether what answers on the proxy's write port is the
// cluster's master: a primary (not in recovery) at the master's address. PostgreSQL has no
// server_id, and a standby seeded by pg_basebackup shares the primary's system identifier.
func (cluster *Cluster) postgresProxyServesMaster(db *sqlx.DB) (bool, error) {
	m := cluster.GetMaster()
	if m == nil {
		return false, nil
	}
	var inRecovery bool
	var addr string
	if err := db.QueryRow("SELECT pg_is_in_recovery(), COALESCE(host(inet_server_addr()), '')").Scan(&inRecovery, &addr); err != nil {
		return false, err
	}
	if inRecovery {
		return false, nil
	}
	if m.IP != "" && m.IP == addr {
		return true, nil
	}
	if ips, err := net.LookupHost(m.Host); err == nil {
		for _, ip := range ips {
			if ip == addr {
				return true, nil
			}
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Proxy compare master: proxy serves %s, master is %s", addr, m.Host)
	return false, nil
}
