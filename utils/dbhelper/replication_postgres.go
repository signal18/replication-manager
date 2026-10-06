// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jmoiron/sqlx"
)

// PostgreSQL WAL streaming (physical standby) status, in the shape of SHOW SLAVE STATUS.
//
// The existing PostgreSQL status reads the SUBSCRIPTION tables: logical replication only.
// A physical standby has no subscription, so the monitor saw no replication on it and gave
// it no role. A standby tells whom it follows, in what state and how far behind through
// pg_stat_wal_receiver and its replay position; when the receiver is down (primary lost)
// the row is still returned, with the primary taken from primary_conninfo, so the server
// stays a replica that is not streaming rather than a server with no replication.
//
// WAL file names and offsets are computed from the LSN: pg_walfile_name() cannot be
// executed during recovery, and the same names must come out on a primary and a standby.

// PostgresStandbyConnectionName is the connection name of a physical standby's status: what
// tells it from a logical replication status, named after its subscription.
const PostgresStandbyConnectionName = "walreceiver"

// postgresStandbyStatusQuery returns one row on a server in recovery, none on a primary.
const postgresStandbyStatusQuery = `WITH w AS (
  SELECT (SELECT setting::bigint FROM pg_settings WHERE name = 'wal_segment_size') AS seg,
         (SELECT timeline_id FROM pg_control_checkpoint()) AS tli,
         pg_last_wal_receive_lsn() AS recv, pg_last_wal_replay_lsn() AS replay,
         current_setting('primary_conninfo', true) AS ci
), r AS (
  SELECT w.*, s.status, s.sender_host, s.sender_port, s.conninfo, COALESCE(s.flushed_lsn, w.recv, w.replay) AS io_lsn
  FROM w LEFT JOIN pg_stat_wal_receiver s ON true
)
SELECT
  'walreceiver' AS "Connection_name",
  COALESCE(sender_host, substring(ci from 'host=([^ ]+)'), '') AS "Master_Host",
  COALESCE(sender_port::text, substring(ci from 'port=([^ ]+)'), '5432') AS "Master_Port",
  COALESCE(substring(conninfo from 'user=([^ ]+)'), substring(ci from 'user=([^ ]+)'), '') AS "Master_User",
  'master.' || upper(lpad(to_hex(tli), 8, '0') || lpad(to_hex((floor((io_lsn - '0/0'::pg_lsn) / seg)::bigint / (4294967296 / seg))), 8, '0') || lpad(to_hex((floor((io_lsn - '0/0'::pg_lsn) / seg)::bigint % (4294967296 / seg))), 8, '0')) AS "Master_Log_File",
  ((io_lsn - '0/0'::pg_lsn) % seg)::bigint AS "Read_Master_Log_Pos",
  'master.' || upper(lpad(to_hex(tli), 8, '0') || lpad(to_hex((floor((replay - '0/0'::pg_lsn) / seg)::bigint / (4294967296 / seg))), 8, '0') || lpad(to_hex((floor((replay - '0/0'::pg_lsn) / seg)::bigint % (4294967296 / seg))), 8, '0')) AS "Relay_Master_Log_File",
  CASE WHEN status = 'streaming' THEN 'Yes' ELSE 'No' END AS "Slave_IO_Running",
  CASE WHEN pg_is_wal_replay_paused() THEN 'No' ELSE 'Yes' END AS "Slave_SQL_Running",
  ((replay - '0/0'::pg_lsn) % seg)::bigint AS "Exec_Master_Log_Pos",
  CASE WHEN io_lsn = replay THEN 0 ELSE COALESCE(EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp()), 0)::bigint END AS "Seconds_Behind_Master",
  '' AS "Last_IO_Errno", '' AS "Last_SQL_Errno", '' AS "Last_SQL_Error",
  0 AS "Master_Server_Id", 'Slave_Pos' AS "Using_Gtid",
  '0-0-' || (io_lsn - '0/0'::pg_lsn)::bigint AS "Gtid_IO_Pos",
  '0-0-' || (replay - '0/0'::pg_lsn)::bigint AS "Gtid_Slave_Pos",
  1 AS "Slave_Heartbeat_Period", '' AS "Slave_SQL_Running_State"
FROM r WHERE pg_is_in_recovery()`

// postgresMasterStatusQuery is the SHOW MASTER STATUS of PostgreSQL, valid on a primary
// (current write position) and on a standby (replay position).
const postgresMasterStatusQuery = `WITH w AS (
  SELECT (SELECT setting::bigint FROM pg_settings WHERE name = 'wal_segment_size') AS seg,
         (SELECT timeline_id FROM pg_control_checkpoint()) AS tli,
         CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn() ELSE pg_current_wal_lsn() END AS lsn
)
SELECT 'master.' || upper(lpad(to_hex(tli), 8, '0') || lpad(to_hex((floor((lsn - '0/0'::pg_lsn) / seg)::bigint / (4294967296 / seg))), 8, '0') || lpad(to_hex((floor((lsn - '0/0'::pg_lsn) / seg)::bigint % (4294967296 / seg))), 8, '0')) AS "File",
       ((lsn - '0/0'::pg_lsn) % seg)::bigint AS "Position", '' AS "Binlog_Do_DB", '' AS "Binlog_Ignore_DB"
FROM w`

// postgresInRecovery tells whether the server is a standby. A failing check means "not a
// standby": the caller then runs the logical replication status, as before.
func postgresInRecovery(db *sqlx.DB) bool {
	var inRecovery bool
	if err := db.Get(&inRecovery, "SELECT pg_is_in_recovery()"); err != nil {
		return false
	}
	return inRecovery
}

// PostgresReceivedLSN returns the last WAL position a standby RECEIVED from its primary, in
// bytes: what a promotion replays up to, so the position candidates are compared on.
func PostgresReceivedLSN(db *sqlx.DB) (uint64, string, error) {
	query := "SELECT (COALESCE(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn()) - '0/0'::pg_lsn)::bigint"
	var lsn uint64
	err := db.Get(&lsn, query)
	return lsn, query, err
}

// PostgresPromote promotes a standby to primary and waits for the promotion to complete.
// PostgreSQL replays all the WAL it received before it opens to writes.
func PostgresPromote(db *sqlx.DB, waitSeconds int) (string, error) {
	query := fmt.Sprintf("SELECT pg_promote(true, %d)", waitSeconds)
	var promoted bool
	if err := db.Get(&promoted, query); err != nil {
		return query, err
	}
	if !promoted {
		return query, fmt.Errorf("promotion not completed after %d s", waitSeconds)
	}
	return query, nil
}

var (
	postgresConninfoHost = regexp.MustCompile(`(^|\s)host=('[^']*'|\S+)`)
	postgresConninfoPort = regexp.MustCompile(`(^|\s)port=('[^']*'|\S+)`)
	postgresConninfoPass = regexp.MustCompile(`password=('[^']*'|\S+)`)
)

// postgresRepointConninfo returns a primary_conninfo that connects to another primary with
// the same user, password and options.
func postgresRepointConninfo(conninfo string, host string, port string) string {
	if postgresConninfoHost.MatchString(conninfo) {
		conninfo = postgresConninfoHost.ReplaceAllString(conninfo, "${1}host="+host)
	} else {
		conninfo += " host=" + host
	}
	if postgresConninfoPort.MatchString(conninfo) {
		conninfo = postgresConninfoPort.ReplaceAllString(conninfo, "${1}port="+port)
	} else {
		conninfo += " port=" + port
	}
	return strings.TrimSpace(conninfo)
}

// PostgresSetPrimary makes a standby follow another primary: primary_conninfo is rewritten
// with the new host and port and the configuration reloaded (no restart since PostgreSQL 13).
// The statement is returned with the password hidden: it is logged.
func PostgresSetPrimary(db *sqlx.DB, host string, port string) (string, error) {
	var conninfo string
	query := "SELECT current_setting('primary_conninfo', true)"
	if err := db.Get(&conninfo, query); err != nil {
		return query, err
	}
	if conninfo == "" {
		return query, errors.New("no primary_conninfo on this server: not a standby")
	}
	conninfo = postgresRepointConninfo(conninfo, host, port)
	stmt := "ALTER SYSTEM SET primary_conninfo = '" + strings.ReplaceAll(conninfo, "'", "''") + "'"
	logs := postgresConninfoPass.ReplaceAllString(stmt, "password=<hidden>")
	if _, err := db.Exec(stmt); err != nil {
		return logs, errors.New(postgresConninfoPass.ReplaceAllString(err.Error(), "password=<hidden>"))
	}
	reload := "SELECT pg_reload_conf()"
	logs += "\n" + reload
	_, err := db.Exec(reload)
	return logs, err
}

// PostgresEnsurePublication creates the publication of all tables a logical replication
// subscriber follows (CREATE SUBSCRIPTION ... PUBLICATION <name>), unless it exists.
func PostgresEnsurePublication(db *sqlx.DB, name string) (string, error) {
	if name == "" {
		name = "alltables"
	}
	var n int
	query := "SELECT count(*) FROM pg_catalog.pg_publication WHERE pubname = $1"
	if err := db.Get(&n, query, name); err != nil {
		return query, err
	}
	if n > 0 {
		return query, nil
	}
	stmt := "CREATE PUBLICATION " + QuotePostgreSQLIdentifier(name) + " FOR ALL TABLES"
	return stmt, PostgresExecReadWrite(db, stmt)
}

// Logical replication (publication / subscription) helpers of a master change.

// PostgresExecReadWrite runs statements the monitor must execute on a server whose default is
// read-only (a frozen primary, a subscriber): one dedicated connection, switched to
// read-write first. Not a multi-statement string: that would be an implicit transaction
// block, which CREATE/DROP/REFRESH SUBSCRIPTION and ALTER SYSTEM refuse.
func PostgresExecReadWrite(db *sqlx.DB, stmts ...string) error {
	conn, err := db.Connx(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "SET default_transaction_read_only = off"); err != nil {
		return err
	}
	// the session goes back to the server's default before the connection returns to the
	// pool: a session-level SET would hide the server value from the monitor's reads
	// (pg_settings shows the session's value) and the read-only enforcement looped
	defer conn.ExecContext(context.Background(), "RESET default_transaction_read_only")
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return err
		}
	}
	return nil
}

// postgresLiteral quotes a string literal.
func postgresLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// PostgresSubscriptionName returns the subscription of a subscriber, "" when none.
func PostgresSubscriptionName(db *sqlx.DB) (string, string, error) {
	query := "SELECT COALESCE((SELECT subname FROM pg_catalog.pg_subscription ORDER BY oid LIMIT 1), '')"
	var name string
	err := db.Get(&name, query)
	return name, query, err
}

// PostgresDropSubscription removes a subscriber's subscription. When the publisher is gone
// the replication slot there cannot be dropped: the subscription is detached from it first.
func PostgresDropSubscription(db *sqlx.DB, name string, publisherAlive bool) (string, error) {
	id := QuotePostgreSQLIdentifier(name)
	logs := ""
	if !publisherAlive {
		for _, stmt := range []string{"ALTER SUBSCRIPTION " + id + " DISABLE", "ALTER SUBSCRIPTION " + id + " SET (slot_name = NONE)"} {
			logs += stmt + "\n"
			if err := PostgresExecReadWrite(db, stmt); err != nil {
				return logs, err
			}
		}
	}
	stmt := "DROP SUBSCRIPTION " + id
	logs += stmt
	return logs, PostgresExecReadWrite(db, stmt)
}

// PostgresRefreshSubscription makes a subscription cover the tables its publication has now
// (tables added after the subscription are not followed until then), without copying data.
// It cannot run inside a transaction block nor a read-only session.
func PostgresRefreshSubscription(db *sqlx.DB, name string) (string, error) {
	stmt := "ALTER SUBSCRIPTION " + QuotePostgreSQLIdentifier(name) + " REFRESH PUBLICATION WITH (copy_data = false)"
	return stmt, PostgresExecReadWrite(db, stmt)
}

// PostgresSetDefaultReadOnly is the PostgreSQL freeze: new transactions of every session are
// read-only (default_transaction_read_only), applied live. The logical replication apply
// worker is not affected (verified on PostgreSQL 17), superusers can lift it per session.
func PostgresSetDefaultReadOnly(db *sqlx.DB, readOnly bool) (string, error) {
	stmt := "ALTER SYSTEM RESET default_transaction_read_only"
	if readOnly {
		stmt = "ALTER SYSTEM SET default_transaction_read_only = on"
	}
	reload := "SELECT pg_reload_conf()"
	return stmt + "\n" + reload, PostgresExecReadWrite(db, stmt, reload)
}

// PostgresTerminateClientBackends ends the client sessions other than this one: with the
// read-only default in place, what they held open cannot write any more.
func PostgresTerminateClientBackends(db *sqlx.DB) (int, string, error) {
	query := "SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()"
	var n int
	err := db.Get(&n, query)
	return n, query, err
}

// PostgresSubscriberCaughtUp tells, from the PUBLISHER, whether the subscriber's slot has
// confirmed everything the publisher wrote.
func PostgresSubscriberCaughtUp(db *sqlx.DB, slot string) (bool, string, error) {
	query := "SELECT COALESCE((SELECT confirmed_flush_lsn >= pg_current_wal_lsn() FROM pg_catalog.pg_replication_slots WHERE slot_name = $1), false)"
	var ok bool
	err := db.Get(&ok, query, slot)
	return ok, query, err
}

// PostgresDropReplicationSlot drops a leftover slot (a former subscriber's) on a publisher.
func PostgresDropReplicationSlot(db *sqlx.DB, slot string) (string, error) {
	query := "SELECT pg_drop_replication_slot(slot_name) FROM pg_catalog.pg_replication_slots WHERE slot_name = " + postgresLiteral(slot) + " AND NOT active"
	return query, PostgresExecReadWrite(db, query)
}

