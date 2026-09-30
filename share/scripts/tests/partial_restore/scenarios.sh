#!/bin/bash
# Failure-path scenarios shared by mariadb_inside.sh and mysql_inside.sh.
# Sourced inside the test container; needs from the caller: q, CLI, TABLES,
# checksums, DATADIR, BACKUPDIR, LOG_DIR.
#
#   prepare-failed  the backup was never prepared: abort before any change
#   subpart         a subpartitioned table: abort before any change
#   bad-cfg         one table's .cfg is corrupt: that table is rolled back,
#                   the others restored, the job fails
#   pw-changed      root's password changed since the backup: the temporary
#                   server falls back to skip-grants; events restored where
#                   that mode shows them (MySQL 8), else reported (MariaDB)
#   replica         the target is a replica: events come back disabled there
#   interrupted     the job dies while the temporary server runs; a rerun
#                   must clean up and succeed
#
# Every scenario ends with one line "RESULT <scenario> PASS" or
# "RESULT <scenario> FAIL: <reasons>".

# Objects a scenario needs inside the backup.
sc_before_backup() {
    case "$1" in
    subpart)
        $CLI -e "CREATE TABLE prt_a.sp (id INT, d DATE, PRIMARY KEY (id, d)) ENGINE=InnoDB
            PARTITION BY RANGE (YEAR(d)) SUBPARTITION BY HASH (id) SUBPARTITIONS 2
            (PARTITION p0 VALUES LESS THAN (2026), PARTITION p1 VALUES LESS THAN MAXVALUE);
            INSERT INTO prt_a.sp VALUES (1,'2025-01-01'),(2,'2027-01-01');"
        ;;
    esac
}

# Damage done to the prepared backup.
sc_after_prepare() {
    case "$1" in
    bad-cfg)
        [[ -f "$BACKUPDIR/prt_a/gen.cfg" ]] || echo "WARN: no prt_a/gen.cfg in the backup to corrupt"
        head -c 256 /dev/urandom >"$BACKUPDIR/prt_a/gen.cfg"
        ;;
    esac
}

# Changes to the target right before the restore. Sets PASSWORD when the
# credentials change, so run it after the dbjobs globals are set.
sc_before_restore() {
    case "$1" in
    pw-changed)
        $CLI -e "ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY 'newpw'; ALTER USER IF EXISTS 'root'@'localhost' IDENTIFIED BY 'newpw';"
        export MYSQL_PWD=newpw
        PASSWORD=newpw
        DB_CONN_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"
        BINARY_CLIENT="${BINARY_CLIENT%% *} $DB_CONN_PARAMETERS"
        ;;
    replica)
        # A configured but never started channel: pr_master_host only reads
        # the configuration. 192.0.2.1 is TEST-NET, never reachable.
        $CLI -e "CHANGE REPLICATION SOURCE TO SOURCE_HOST='192.0.2.1', SOURCE_USER='repl', SOURCE_PASSWORD='repl'" 2>/dev/null ||
            $CLI -e "CHANGE MASTER TO MASTER_HOST='192.0.2.1', MASTER_USER='repl', MASTER_PASSWORD='repl'"
        ;;
    esac
}

# Everything a restore could change on the target, in a diffable form.
sc_state() {
    checksums
    q "SELECT table_schema, table_name, IFNULL(engine,'VIEW') FROM information_schema.tables WHERE table_schema IN ('prt_a','prt_b','prt_extra') ORDER BY 1,2"
    q "SELECT routine_schema, routine_type, routine_name FROM information_schema.routines WHERE routine_schema IN ('prt_a','prt_b') ORDER BY 1,2,3"
    q "SELECT event_schema, event_name, status FROM information_schema.events WHERE event_schema IN ('prt_a','prt_b') ORDER BY 1,2"
    q "SELECT trigger_schema, trigger_name FROM information_schema.triggers WHERE trigger_schema IN ('prt_a','prt_b') ORDER BY 1,2"
    (cd "$DATADIR" && ls -1 prt_a prt_b prt_extra 2>/dev/null)
}

# Number of temporary definition servers still running.
sc_temp_servers() {
    local p n=0
    for p in /proc/[0-9]*/cmdline; do
        tr '\0' ' ' <"$p" 2>/dev/null | grep -q '[m]rm_defs_ro' && n=$((n + 1))
    done
    echo $n
}

# The job dies (SIGKILL, as a timeout or a crashed dbjobs would) while the
# temporary server is up; the server itself is left running, as it would be.
sc_interrupted_first_run() {
    local bg i seen=0
    (partialRestore) >/dev/null 2>&1 &
    bg=$!
    for i in $(seq 1 1200); do
        [[ -S "$DATADIR/.system/mrm_defs_run/mariadbd.sock" ]] && { seen=1; break; }
        sleep 0.1
    done
    kill -9 "$bg" 2>/dev/null
    wait "$bg" 2>/dev/null
    sleep 1
    echo "interrupted: temp server socket seen=$seen, temp servers alive after the kill=$(sc_temp_servers)"
    [[ $seen -eq 1 ]] || echo "WARN: the job was not caught while the temporary server ran"
}

sc_verdict() {
    local s="$1" rc="$2" why=() n ok log="$LOG_DIR/reseed.out" st
    n=$(wc -l </tmp/before.txt)
    ok=$(paste /tmp/before.txt /tmp/after.txt | awk '$2==$4' | wc -l)
    has() { grep -q -- "$1" "$log" || why+=("log lacks '$1'"); }
    obj() { q "SELECT COUNT(*) FROM information_schema.$1"; }
    all_objects() {
        [[ $(obj "views WHERE table_schema='prt_a' AND table_name='v_plain'") == 1 ]] || why+=("view v_plain missing")
        [[ $(obj "triggers WHERE trigger_schema='prt_a' AND trigger_name='trg_plain'") == 1 ]] || why+=("trigger trg_plain missing")
        [[ $(obj "routines WHERE routine_schema='prt_a' AND routine_name='f_double'") == 1 ]] || why+=("function f_double missing")
    }
    case "$s" in
    prepare-failed | subpart)
        [[ $rc -ne 0 ]] || why+=("rc=0")
        has "aborted before any change"
        [[ "$s" == subpart ]] && has "is subpartitioned"
        sc_state >/tmp/post.txt
        diff -q /tmp/pre.txt /tmp/post.txt >/dev/null ||
            why+=("target changed ($(diff /tmp/pre.txt /tmp/post.txt | grep -c '^[<>]') differing lines, see /tmp/pre.txt /tmp/post.txt)")
        ;;
    bad-cfg)
        [[ $rc -ne 0 ]] || why+=("rc=0")
        [[ $ok -eq $((n - 1)) ]] || why+=("identical tables $ok/$n, want $((n - 1))")
        [[ $(obj "tables WHERE table_schema='prt_a' AND table_name='gen'") == 0 ]] || why+=("prt_a.gen still exists")
        compgen -G "$DATADIR/prt_a/gen.*" >/dev/null && why+=("gen files left in the datadir: $(ls "$DATADIR"/prt_a/gen.* | xargs -n1 basename | tr '\n' ' ')")
        [[ -f "$BACKUPDIR/prt_a/gen.ibd" ]] || why+=("gen.ibd not moved back into the backup")
        has "SKIPPED prt_a.gen"
        has "VERIFY FAILED for prt_a"
        all_objects
        ;;
    pw-changed)
        # MySQL 8 shows events with --skip-grant-tables, MariaDB does not:
        # the event must be restored, or reported and the job failed.
        [[ $ok -eq $n ]] || why+=("identical tables $ok/$n")
        has "skip-grant-tables"
        if [[ $(obj "events WHERE event_schema='prt_a' AND event_name='ev_tick'") == 1 ]]; then
            [[ $rc -eq 0 ]] || why+=("rc=$rc although everything was restored")
        else
            grep -q "SKIPPED EVENT prt_a.ev_tick" "$log" || why+=("event ev_tick lost without being reported")
            [[ $rc -ne 0 ]] || why+=("rc=0 although the event was not restored")
        fi
        all_objects
        ;;
    replica)
        [[ $rc -eq 0 ]] || why+=("rc=$rc")
        [[ $ok -eq $n ]] || why+=("identical tables $ok/$n")
        st=$(q "SELECT status FROM information_schema.events WHERE event_schema='prt_a' AND event_name='ev_tick'")
        [[ "$st" == SLAVESIDE_DISABLED || "$st" == REPLICA_SIDE_DISABLED ]] || why+=("event status '${st:-missing}', want disabled on the replica")
        all_objects
        ;;
    *)
        [[ "$s" == interrupted ]] && has "left by an earlier restore"
        [[ $rc -eq 0 ]] || why+=("rc=$rc")
        [[ $ok -eq $n ]] || why+=("identical tables $ok/$n")
        [[ $(obj "events WHERE event_schema='prt_a' AND event_name='ev_tick'") == 1 ]] || why+=("event ev_tick missing")
        all_objects
        ;;
    esac
    # Whatever happened, nothing of the restore machinery may stay behind.
    [[ $(sc_temp_servers) -eq 0 ]] || why+=("$(sc_temp_servers) temporary server(s) still running")
    [[ -e "$DATADIR/.system/mrm_defs_ro" || -e "$DATADIR/.system/mrm_defs_run" ]] && why+=("mrm_defs_* folders left")
    [[ -n $(q "SELECT 1 FROM information_schema.tables WHERE table_name LIKE 'mrm\_pivo%' LIMIT 1") ]] && why+=("staging tables left")
    if [[ ${#why[@]} -eq 0 ]]; then
        echo "RESULT $s PASS"
    else
        echo "RESULT $s FAIL: $(printf '%s; ' "${why[@]}")"
    fi
}
