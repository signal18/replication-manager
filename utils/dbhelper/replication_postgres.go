// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

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

// DDL replication for logical replication, trigger based, no extension (PostgreSQL
// replicates rows, never DDL): an event trigger logs every DDL statement of the publisher
// into replication_manager_schema.ddl_log, a published table like any other, so the
// statement travels to the subscribers as a row; there a trigger that fires on replicated
// rows too (ENABLE ALWAYS) executes it with the publisher's search_path.
//
// Guards: a statement executed by the apply trigger is not logged again
// (replication_manager.applying_ddl); logical replication objects, event triggers, the log
// table itself and TEMP objects are never replicated; a statement that fails on the
// subscriber is a WARNING in its log, never a stopped apply. A table a statement creates
// joins the subscription at the next refresh (the monitor runs it, PostgresRefreshSubscription).
const postgresDDLReplicationInstall = `
-- the install is DDL itself: not logged (an earlier version of the event trigger may be live)
SET replication_manager.applying_ddl = on;
CREATE SCHEMA IF NOT EXISTS replication_manager_schema;
CREATE TABLE IF NOT EXISTS replication_manager_schema.ddl_log (
    id          bigserial PRIMARY KEY,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    issued_by   text NOT NULL,
    tag         text NOT NULL,
    search_path text NOT NULL,
    command     text NOT NULL
);
CREATE OR REPLACE FUNCTION replication_manager_schema.log_ddl() RETURNS event_trigger
LANGUAGE plpgsql SECURITY DEFINER AS $f$
DECLARE q text;
BEGIN
    IF current_setting('replication_manager.applying_ddl', true) = 'on' THEN RETURN; END IF;
    -- a subscriber never logs: its DDL is local administration, and its log table is the
    -- replicated copy of the publisher's (its own sequence would collide with the ids)
    IF EXISTS (SELECT 1 FROM pg_catalog.pg_subscription) THEN RETURN; END IF;
    IF tg_tag IN ('CREATE SUBSCRIPTION', 'ALTER SUBSCRIPTION', 'DROP SUBSCRIPTION', 'CREATE PUBLICATION', 'ALTER PUBLICATION', 'DROP PUBLICATION', 'CREATE EVENT TRIGGER', 'ALTER EVENT TRIGGER', 'DROP EVENT TRIGGER') THEN RETURN; END IF;
    q := current_query();
    IF q IS NULL OR q ~* 'replication_manager_schema\.ddl_log' OR q ~* '^\s*(create|alter|drop)\s+(temp|temporary)\s' THEN RETURN; END IF;
    INSERT INTO replication_manager_schema.ddl_log (issued_by, tag, search_path, command)
    VALUES (session_user, tg_tag, current_setting('search_path'), q);
END $f$;
DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl;
CREATE EVENT TRIGGER replication_manager_log_ddl ON ddl_command_end EXECUTE FUNCTION replication_manager_schema.log_ddl();
DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl_drop;
CREATE EVENT TRIGGER replication_manager_log_ddl_drop ON sql_drop EXECUTE FUNCTION replication_manager_schema.log_ddl();
CREATE OR REPLACE FUNCTION replication_manager_schema.apply_ddl() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER AS $f$
BEGIN
    IF current_setting('session_replication_role') <> 'replica' THEN RETURN NEW; END IF;
    PERFORM set_config('replication_manager.applying_ddl', 'on', true);
    PERFORM set_config('search_path', NEW.search_path, true);
    BEGIN
        EXECUTE NEW.command;
    EXCEPTION WHEN OTHERS THEN
        RAISE WARNING 'replication-manager: replicated DDL #% failed: % -- %', NEW.id, SQLERRM, left(NEW.command, 200);
    END;
    PERFORM set_config('replication_manager.applying_ddl', 'off', true);
    RETURN NEW;
END $f$;
DROP TRIGGER IF EXISTS replication_manager_apply_ddl ON replication_manager_schema.ddl_log;
CREATE TRIGGER replication_manager_apply_ddl AFTER INSERT ON replication_manager_schema.ddl_log
    FOR EACH ROW EXECUTE FUNCTION replication_manager_schema.apply_ddl();
ALTER TABLE replication_manager_schema.ddl_log ENABLE ALWAYS TRIGGER replication_manager_apply_ddl;
-- The apply worker runs as the subscription's owner and a replicated DDL cannot run in a
-- read-only transaction ("cannot set transaction read-write mode inside a read-only
-- transaction"): the subscriptions are owned by a dedicated role, the only one exempt from
-- the read-only default, as a SUPER user is from read_only on MySQL (PostgresOwnSubscription).
-- The monitor's own role and the application roles keep the default. The role has the LOGIN
-- attribute (the worker is refused otherwise: "role is not permitted to log in") but no
-- password, so no password authentication can ever succeed for it.
DO $d$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'replication_manager') THEN
        CREATE ROLE replication_manager SUPERUSER LOGIN PASSWORD NULL;
    END IF;
    ALTER ROLE replication_manager LOGIN PASSWORD NULL;
    EXECUTE format('ALTER ROLE %I RESET default_transaction_read_only', session_user);
END $d$;
ALTER ROLE replication_manager SET default_transaction_read_only = off;
RESET replication_manager.applying_ddl;
`

// PostgresInstallDDLReplication installs the DDL replication objects on a server, idempotent;
// on every server of a logical replication cluster, whatever its role (a switchover changes
// nothing).
func PostgresInstallDDLReplication(db *sqlx.DB) (string, error) {
	return "DDL replication objects (replication_manager_schema.ddl_log, event trigger, apply trigger)", PostgresExecReadWrite(db, postgresDDLReplicationInstall)
}

// PostgresDropDDLReplication removes the publisher-side logging (the off-switch); the log
// table and the apply trigger stay, harmless without new rows.
func PostgresDropDDLReplication(db *sqlx.DB) (string, error) {
	stmt := "DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl; DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl_drop"
	return stmt, PostgresExecReadWrite(db, "DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl", "DROP EVENT TRIGGER IF EXISTS replication_manager_log_ddl_drop")
}

// PostgresDDLLogMaxID is the id of the last DDL logged (replicated) on a server, 0 when none
// or when the log does not exist: an index lookup the monitor can afford every tick.
func PostgresDDLLogMaxID(db *sqlx.DB) (int64, string, error) {
	query := "SELECT COALESCE((SELECT max(id) FROM replication_manager_schema.ddl_log), 0) WHERE to_regclass('replication_manager_schema.ddl_log') IS NOT NULL"
	var id int64
	err := db.Get(&id, query)
	if err == sql.ErrNoRows {
		return 0, query, nil
	}
	return id, query, err
}

// PostgresSubscriptionNeedsRefresh tells, from the PUBLISHER's table count and the
// subscriber's subscribed count, whether a REFRESH PUBLICATION would add tables.
func PostgresSubscriptionNeedsRefresh(publisher, subscriber *sqlx.DB, publication string) (bool, string, error) {
	if publication == "" {
		publication = "alltables"
	}
	var published, subscribed int
	q1 := "SELECT count(*) FROM pg_catalog.pg_publication_tables WHERE pubname = $1"
	if err := publisher.Get(&published, q1, publication); err != nil {
		return false, q1, err
	}
	q2 := "SELECT count(*) FROM pg_catalog.pg_subscription_rel"
	if err := subscriber.Get(&subscribed, q2); err != nil {
		return false, q2, err
	}
	return published != subscribed, q1 + "; " + q2, nil
}

// PostgresOwnSubscription hands the subscription to the replication_manager role created by
// the DDL replication install: its apply worker then runs exempt from the read-only default.
func PostgresOwnSubscription(db *sqlx.DB, name string) (string, error) {
	stmt := "ALTER SUBSCRIPTION " + QuotePostgreSQLIdentifier(name) + " OWNER TO replication_manager"
	return stmt, PostgresExecReadWrite(db, stmt)
}

// PostgresArchiverStatus is pg_stat_archiver with the count of segments waiting to be
// archived (the .ready files of pg_wal/archive_status).
type PostgresArchiver struct {
	Ready        int
	FailedCount  int
	LastArchived time.Time
	LastFailed   time.Time
}

// PostgresArchiverStatus reads the archiver's state on a server with archive_mode on.
func PostgresArchiverStatus(db *sqlx.DB) (PostgresArchiver, string, error) {
	var st PostgresArchiver
	query := "SELECT (SELECT count(*) FROM pg_ls_archive_statusdir() WHERE name LIKE '%.ready'), failed_count, coalesce(last_archived_time, 'epoch'::timestamptz), coalesce(last_failed_time, 'epoch'::timestamptz) FROM pg_stat_archiver"
	err := db.QueryRow(query).Scan(&st.Ready, &st.FailedCount, &st.LastArchived, &st.LastFailed)
	return st, query, err
}
