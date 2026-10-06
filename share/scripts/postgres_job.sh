#!/bin/bash
# replication-manager jobs sidecar for a PostgreSQL service.
#
# Runs next to the PostgreSQL container, in the same network namespace, from the same
# image (psql, pg_dumpall, pg_basebackup are there). It asks replication-manager through
# its API whether a task is wanted (the API jobs mode, no jobs table), and runs it,
# streaming its output to the receiver replication-manager opens for it:
#
#   pgdump         pg_dumpall of the instance (logical backup)
#   pgstandby      arm the next start as a standby of the primary replication-manager names
#   pgreseed       arm the next start to copy the data again from that primary
#   pgschemasync   create the published tables this subscriber misses (DDL is not replicated)
#   pgrestore      receive the stored physical backup, arm the next start to restore it
#   pgreseedlogical  re-copy this server from the primary at the snapshot of a new slot
# and, every loop, ships the WAL segments archive_command left in /var/lib/postgresql/wal_archive
# to replication-manager (task pgwalarchive, the binlog copy of PostgreSQL), deleting each
# one once received.
#   pgrestorelogical  receive the stored logical backup and replay it on this server
#   pgbasebackup   pg_basebackup as a tar stream with the WAL it needs (physical backup,
#                  online, it blocks neither reads nor writes)
#   optimize       vacuumdb --all --analyze: reclaims dead rows and refreshes the planner
#                  statistics of every database; it blocks neither reads nor writes (never
#                  VACUUM FULL, which locks the tables)
#
# Every call to replication-manager goes through replication-manager-cli, delivered in
# /jobs by the init container: `job needs|receiver|state` for the API, `stream` for the
# data. The image needs neither openssl, curl nor socat.
#
# Environment: REPLICATION_MANAGER_URL, REPLICATION_MANAGER_CLUSTER_NAME,
# REPLICATION_MANAGER_HOST_NAME, REPLICATION_MANAGER_HOST_PORT (the server as the monitor
# knows it), POSTGRES_PASSWORD (also the secret of the API calls).

set -u

export PGHOST="${PGHOST:-127.0.0.1}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${POSTGRES_USER:-postgres}"
export PGPASSWORD="${POSTGRES_PASSWORD:-}"
export PGDATABASE="${POSTGRES_DB:-postgres}"
export PGCONNECT_TIMEOUT=5

INTERVAL="${PG_JOB_INTERVAL:-10}"
CLI="${REPMAN_CLIENT:-/jobs/replication-manager-cli}"
ERR=/tmp/postgres_job.err
TASKS="pgdump pgbasebackup optimize pgstandby pgreseed pgschemasync pgrestore pgrestorelogical pgreseedlogical"
# where the received physical backup waits for the next start (kept by the restore wipe)
RESTORE_DIR="${PGDATA:-/var/lib/postgresql/data}/replication-manager.restore"
# what the next start of PostgreSQL must do, read by the start script (postgres_start.sh)
NEXT_START="${PGDATA:-/var/lib/postgresql/data}/replication-manager.next_start"

log() { echo "[postgres_job] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

job() { "$CLI" job --secret-env POSTGRES_PASSWORD "$@"; }

run_task() {
    local task="$1" addr rc_cmd=0 rc_stream=0
    job state "$task" processing || log "$task: cannot report processing"
    case "$task" in
    optimize)
        # nothing to stream: run, report
        log "$task: vacuumdb --all --analyze"
        if vacuumdb --all --analyze >"$ERR" 2>&1; then
            log "$task: done"
            job state "$task" done || log "$task: cannot report done"
        else
            log "$task: failed: $(tail -c 300 "$ERR")"
            job state "$task" error || log "$task: cannot report error"
        fi
        return
        ;;
    esac
    if ! addr=$(job receiver "$task") || [ -z "$addr" ]; then
        log "$task: no receiver"
        job state "$task" error
        return
    fi
    case "$task" in
    pgschemasync)
        # Logical replication subscriber: PostgreSQL does not replicate DDL. The tables of the
        # publication this server has not are created here from the primary's definition
        # (pg_dump --schema-only), then the subscription is refreshed so they join it with
        # their rows copied; the apply worker, dead on "relation does not exist", passes.
        local phost="${addr%:*}" pport="${addr##*:}" sub missing t args
        sub=$(psql -X -At -c "SELECT subname FROM pg_subscription ORDER BY oid LIMIT 1")
        missing=$(PGHOST="$phost" PGPORT="$pport" psql -X -At -c "SELECT schemaname || '.' || tablename FROM pg_publication_tables WHERE pubname = '${sub:-alltables}'" 2>"$ERR" \
            | while read -r t; do [ -n "$t" ] && [ "$(psql -X -At -c "SELECT to_regclass('$t') IS NOT NULL")" = "f" ] && echo "$t"; done)
        if [ -z "$missing" ]; then
            log "$task: no published table missing"
            job state "$task" done
            return
        fi
        args=""; for t in $missing; do args="$args -t $t"; done
        log "$task: creating from $addr:$(echo $missing | tr '\n' ' ')"
        # shellcheck disable=SC2086
        # the subscriber keeps a read-only default: this session is switched to read-write first
        if { echo "SET default_transaction_read_only = off;"; PGHOST="$phost" PGPORT="$pport" pg_dump --schema-only --no-owner --no-privileges $args 2>"$ERR"; } | psql -X -v ON_ERROR_STOP=0 -q >>"$ERR" 2>&1 \
            && psql -X -At -c "SET default_transaction_read_only = off" -c "ALTER SUBSCRIPTION ${sub:-alltables} REFRESH PUBLICATION" >>"$ERR" 2>&1; then
            log "$task: done, subscription refreshed"
            job state "$task" done || log "$task: cannot report done"
        else
            log "$task: failed: $(tail -c 300 "$ERR")"
            job state "$task" error || log "$task: cannot report error"
        fi
        return
        ;;
    pgstandby|pgreseed)
        # Role change of this server, decided by replication-manager: PostgreSQL cannot
        # become a standby while it runs, so the change is armed here, on the data
        # volume, and applied by the start script at the next start of the service.
        #   pgstandby  follow $addr as a standby, data kept (clean switchover)
        #   pgreseed   copy the data again from $addr (former primary after a failover)
        if printf '%s %s %s\n' "${task#pg}" "${addr%:*}" "${addr##*:}" > "$NEXT_START.tmp" && mv "$NEXT_START.tmp" "$NEXT_START"; then
            log "$task: next start armed: ${task#pg} of $addr"
            job state "$task" done || log "$task: cannot report done"
        else
            log "$task: cannot write $NEXT_START"
            job state "$task" error || log "$task: cannot report error"
        fi
        return
        ;;
    pgreseedlogical)
        # Re-copy of a logical replication subscriber (a former primary after a failover):
        # logical replication has no position to resume from, so the slot the subscription
        # will use is created on the primary FIRST, with an exported snapshot, the primary is
        # dumped at that snapshot while the slot's connection stays open, and the dump is
        # replayed here; replication-manager then subscribes on that slot (create_slot=false)
        # and streaming resumes exactly where the dump stopped. "TARGET=host:port SLOT=name".
        local target slot phost pport db snap line dumprc
        target="${addr%% *}"; slot=$(printf '%s' "$addr" | sed -n 's/.*SLOT=\([^ ]*\).*/\1/p'); slot="${slot:-alltables}"
        phost="${target%:*}"; pport="${target##*:}"; db="${PGDATABASE:-postgres}"
        : > "$ERR"
        # a slot of that name left by an earlier attempt, never consumed, is in the way
        PGHOST="$phost" PGPORT="$pport" psql -X -At -d "$db" -c "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '$slot' AND NOT active" >>"$ERR" 2>&1
        coproc REPL { PGHOST="$phost" PGPORT="$pport" psql -X -At -F '|' "dbname=$db replication=database" 2>>"$ERR"; }
        echo "CREATE_REPLICATION_SLOT $slot LOGICAL pgoutput EXPORT_SNAPSHOT;" >&"${REPL[1]}"
        line=""; read -r -t 60 line <&"${REPL[0]}" || true
        snap=$(printf '%s' "$line" | cut -d'|' -f3)
        if [ -z "$snap" ]; then
            log "$task: no slot/snapshot from $target: $(tail -c 300 "$ERR")"
            exec {REPL[1]}>&-; wait "$REPL_PID" 2>/dev/null
            job state "$task" error || log "$task: cannot report error"
            return
        fi
        log "$task: slot $slot created on $target at snapshot $snap, copying database $db"
        # the local replay: writes allowed, the DDL log trigger silent (this server was a
        # publisher: its event trigger would log the replay), its own publication dropped
        { printf "SET default_transaction_read_only = off;\nSET replication_manager.applying_ddl = on;\nDROP PUBLICATION IF EXISTS %s;\n" "$slot"
          PGHOST="$phost" PGPORT="$pport" pg_dump -d "$db" --snapshot="$snap" --clean --if-exists --no-owner --no-privileges --no-publications --no-subscriptions 2>>"$ERR"
          echo "-- dump rc ${PIPESTATUS[0]:-?}" >&2
        } | psql -X -v ON_ERROR_STOP=0 -q -d "$db" >>"$ERR" 2>&1
        dumprc=$?
        exec {REPL[1]}>&-; wait "$REPL_PID" 2>/dev/null
        if [ "$dumprc" -eq 0 ] && ! grep -q '^pg_dump: error' "$ERR"; then
            log "$task: database $db replayed at snapshot $snap ($(grep -c '^ERROR' "$ERR" 2>/dev/null || echo 0) statements refused, see $ERR)"
            job state "$task" done || log "$task: cannot report done"
        else
            log "$task: failed: $(grep -m1 '^pg_dump: error' "$ERR" || tail -c 300 "$ERR")"
            PGHOST="$phost" PGPORT="$pport" psql -X -At -d "$db" -c "SELECT pg_drop_replication_slot('$slot')" >>"$ERR" 2>&1
            job state "$task" error || log "$task: cannot report error"
        fi
        return
        ;;
    pgrestore|pgrestorelogical)
        # Restore from the backup replication-manager keeps, the way a MariaDB server is
        # reseeded from one: this side listens on the server's SST port, reports waiting, and
        # replication-manager streams the file (gzip or not, as stored).
        #   pgrestore         the pg_basebackup tar goes to $RESTORE_DIR and the next start
        #                     replaces the data directory with it (then follows TARGET as a
        #                     standby when there is one); replication-manager restarts the service
        #   pgrestorelogical  the pg_dumpall is replayed on this running server
        local port target file pid rc_sql
        port=$(printf '%s' "$addr" | sed -n 's/^LISTEN=\([0-9]*\).*/\1/p')
        target=$(printf '%s' "$addr" | sed -n 's/.*TARGET=\(.*\)$/\1/p')
        if [ -z "$port" ]; then
            log "$task: no port to listen on in '$addr'"
            job state "$task" error
            return
        fi
        if [ "$task" = pgrestore ]; then
            mkdir -p "$RESTORE_DIR" && file="$RESTORE_DIR/pgbasebackup.tar"
        else
            file="/tmp/pgrestorelogical.dump"
        fi
        rm -f "$file"
        log "$task: listening on :$port"
        "$CLI" stream --listen ":$port" --accept-timeout 900 > "$file" 2>"$ERR" &
        pid=$!
        sleep 1
        job state "$task" waiting || log "$task: cannot report waiting"
        if ! wait "$pid" || [ ! -s "$file" ]; then
            log "$task: receive failed: $(tail -c 300 "$ERR")"
            rm -f "$file"
            job state "$task" error || log "$task: cannot report error"
            return
        fi
        log "$task: received $(stat -c %s "$file") bytes"
        if [ "$task" = pgrestore ]; then
            if printf 'restore %s %s\n' "${target%:*}" "${target##*:}" > "$NEXT_START.tmp" && mv "$NEXT_START.tmp" "$NEXT_START"; then
                log "$task: next start armed: restore${target:+, then standby of $target}"
                job state "$task" done || log "$task: cannot report done"
            else
                log "$task: cannot write $NEXT_START"
                job state "$task" error || log "$task: cannot report error"
            fi
            return
        fi
        # logical: a gzip stream or plain SQL; the dump drops and recreates what it holds, the
        # roles and databases in use cannot be dropped (errors expected, the run goes on)
        if [ "$(head -c 2 "$file" | od -An -tx1 | tr -d ' ')" = "1f8b" ]; then
            { echo "SET default_transaction_read_only = off;"; gunzip -c "$file"; } | psql -X -v ON_ERROR_STOP=0 -q -d postgres >"$ERR" 2>&1
        else
            { echo "SET default_transaction_read_only = off;"; cat "$file"; } | psql -X -v ON_ERROR_STOP=0 -q -d postgres >"$ERR" 2>&1
        fi
        rc_sql=$?
        rm -f "$file"
        if [ "$rc_sql" -eq 0 ]; then
            log "$task: replayed ($(grep -c '^ERROR' "$ERR" 2>/dev/null || echo 0) statements refused, see $ERR)"
            job state "$task" done || log "$task: cannot report done"
        else
            log "$task: psql failed: $(tail -c 300 "$ERR")"
            job state "$task" error || log "$task: cannot report error"
        fi
        return
        ;;
    esac
    log "$task: streaming to $addr"
    : > "$ERR"
    case "$task" in
    pgdump)
        pg_dumpall --clean --if-exists 2>"$ERR" | "$CLI" stream --to "$addr"
        rc_cmd=${PIPESTATUS[0]} rc_stream=${PIPESTATUS[1]}
        ;;
    pgbasebackup)
        pg_basebackup -D - -Ft -X fetch -c fast 2>"$ERR" | "$CLI" stream --to "$addr"
        rc_cmd=${PIPESTATUS[0]} rc_stream=${PIPESTATUS[1]}
        ;;
    esac
    if [ "$rc_cmd" -eq 0 ] && [ "$rc_stream" -eq 0 ]; then
        log "$task: done"
        job state "$task" done || log "$task: cannot report done"
    else
        log "$task: failed (tool $rc_cmd, stream $rc_stream): $(head -c 300 "$ERR")"
        job state "$task" error || log "$task: cannot report error"
    fi
}

# report_usage: the thin resource sensor of the MariaDB jobs (collect_dbu), for PostgreSQL.
# Reads this service's cgroup (bound read-only at /svc-cgroup), the data directory size and
# the network counters of the shared namespace, and posts the window since the last report.
USAGE_INTERVAL="${PG_JOB_USAGE_INTERVAL:-60}"
USAGE_CKPT=/tmp/postgres_job.usage
last_usage=0
CG_NS="${REPLICATION_MANAGER_CLUSTER_NAME:-}"
CG_SVC="${REPLICATION_MANAGER_HOST_NAME%%.*}"
# resolve_cgroup: this service's cgroup v2 directory. /svc-cgroup when the orchestrator
# bound the exact slice there; else, under the OpenSVC tree bound at /svc-cgroup-root, the
# slice found by NAME: systemd spells a dash as \x2d (pg-logical -> pg\x2dlogical) and a
# backslash cannot travel in a bind mount, so the directory names are decoded before they
# are compared with the cluster (namespace) and service names.
resolve_cgroup() {
    if [ -r /svc-cgroup/memory.current ]; then echo /svc-cgroup; return 0; fi
    # on premise (run on the host through ssh): the postmaster's own cgroup
    pid=$(pgrep -x postgres 2>/dev/null | head -1)
    if [ -n "$pid" ]; then
        sub=$(awk -F: '$1=="0"{print $3; exit}' "/proc/$pid/cgroup" 2>/dev/null)
        for base in "/proc/$pid/root/sys/fs/cgroup" "/sys/fs/cgroup"; do
            [ -n "$sub" ] && [ -r "${base}${sub}/memory.current" ] && { echo "${base}${sub}"; return 0; }
        done
    fi
    [ -n "${CG_NS:-}" ] && [ -n "${CG_SVC:-}" ] && [ -d /svc-cgroup-root ] || return 1
    for d in /svc-cgroup-root/opensvc-ns.*.slice; do
        [ "$(printf '%s' "${d##*/}" | sed 's/\\x2d/-/g')" = "opensvc-ns.$CG_NS.slice" ] || continue
        for s in "$d"/opensvc-ns.*-svc.*.slice; do
            [ "$(printf '%s' "${s##*/}" | sed 's/\\x2d/-/g')" = "opensvc-ns.$CG_NS-svc.$CG_SVC.slice" ] && { echo "$s"; return 0; }
        done
    done
    return 1
}
report_usage() {
    local cg now mem cpu io disk rx tx
    cg=$(resolve_cgroup)
    [ -n "$cg" ] && [ -r "$cg/memory.current" ] || return 0
    now=$(date +%s)
    [ $((now - last_usage)) -ge "$USAGE_INTERVAL" ] || return 0
    last_usage=$now
    mem=$(cat "$cg/memory.current" 2>/dev/null || echo 0)
    cpu=$(awk '/^usage_usec/{print $2}' "$cg/cpu.stat" 2>/dev/null)
    io=$(awk '{for(i=1;i<=NF;i++){if($i ~ /^rios=/){sub("rios=","",$i);r+=$i} if($i ~ /^wios=/){sub("wios=","",$i);w+=$i}}} END{printf "%d", r+w+0}' "$cg/io.stat" 2>/dev/null)
    disk=$(du -sb "${PGDATA:-/var/lib/postgresql/data}" 2>/dev/null | awk '{print $1}')
    read -r rx tx < <(awk -F'[: ]+' 'NR>2 && $2!="lo" {rx+=$3; tx+=$11} END{printf "%d %d\n", rx+0, tx+0}' /proc/net/dev 2>/dev/null)
    local p_epoch="" p_cpu="" p_io=""
    [ -s "$USAGE_CKPT" ] && read -r p_epoch p_cpu p_io < "$USAGE_CKPT"
    echo "$now ${cpu:-0} ${io:-0}" > "$USAGE_CKPT"
    [ -n "$p_epoch" ] || return 0
    local dt=$((now - p_epoch))
    [ "$dt" -gt 0 ] || return 0
    local cores iops
    cores=$(awk -v c="${cpu:-0}" -v p="$p_cpu" -v dt="$dt" 'BEGIN{v=(c-p)/(dt*1000000); if(v<0)v=0; printf "%.4f", v}')
    iops=$(awk -v c="${io:-0}" -v p="$p_io" -v dt="$dt" 'BEGIN{v=(c-p)/dt; if(v<0)v=0; printf "%.4f", v}')
    printf '{"windowStart":"%s","windowEnd":"%s","memMaxBytes":%s,"cpuMaxCores":%s,"ioMaxIops":%s,"diskMaxBytes":%s,"netRxBytes":%s,"netTxBytes":%s}' \
        "$(date -u -d "@$p_epoch" +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ)" \
        "${mem:-0}" "$cores" "$iops" "${disk:-0}" "${rx:-0}" "${tx:-0}" \
        | job usage 2>"$ERR" || log "usage report: $(head -c 200 "$ERR")"
}

# ship_wal_archive: each file archive_command completed (oldest first, a .partial is still
# being written by cp) is streamed to a receiver replication-manager opens for it and
# removed once sent; a failure leaves it for the next loop (PostgreSQL keeps producing,
# replication-manager watches the backlog). The archive directory exists only when the
# service mounts it.
WAL_ARCHIVE_DIR=/var/lib/postgresql/wal_archive
wal_ship_failed=""
ship_wal_archive() {
    [ -d "$WAL_ARCHIVE_DIR" ] || return 0
    local f name addr n=0
    for f in $(ls -1 "$WAL_ARCHIVE_DIR" 2>/dev/null | grep -E '^[0-9A-F]{24}(\.[0-9A-F]{8}\.backup)?$|^[0-9A-F]{8}\.history$' | sort); do
        name="$f"; f="$WAL_ARCHIVE_DIR/$f"
        if ! addr=$(job receiver pgwalarchive "$name" 2>"$ERR") || [ -z "$addr" ]; then
            [ "$wal_ship_failed" = "$name" ] || log "wal archive: no receiver for $name: $(head -c 200 "$ERR")"
            wal_ship_failed="$name"
            return
        fi
        if "$CLI" stream --to "$addr" < "$f" 2>"$ERR"; then
            rm -f "$f"; n=$((n + 1)); wal_ship_failed=""
        else
            [ "$wal_ship_failed" = "$name" ] || log "wal archive: $name not sent: $(head -c 200 "$ERR")"
            wal_ship_failed="$name"
            return
        fi
    done
    [ "$n" -eq 0 ] || log "wal archive: $n file(s) shipped"
}

log "starting: PostgreSQL $PGHOST:$PGPORT as $PGUSER, server ${REPLICATION_MANAGER_HOST_NAME:-?}:${REPLICATION_MANAGER_HOST_PORT:-?} of ${REPLICATION_MANAGER_CLUSTER_NAME:-?}, every ${INTERVAL}s"
while [ ! -x "$CLI" ]; do
    log "waiting for $CLI"
    sleep "$INTERVAL"
done
until psql -X -At -c "SELECT 1" >/dev/null 2>&1; do
    log "waiting for PostgreSQL"
    sleep "$INTERVAL"
done
while true; do
    for task in $TASKS; do
        job needs "$task" 2>"$ERR"
        case $? in
        0) run_task "$task" ;;
        1) ;;
        *) log "needs $task: $(head -c 200 "$ERR")" ;;
        esac
    done
    report_usage
    ship_wal_archive
    sleep "$INTERVAL"
done
