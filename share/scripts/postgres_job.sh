#!/bin/bash
# replication-manager jobs sidecar for a PostgreSQL service.
#
# Runs next to the PostgreSQL container, in the same network namespace, from the same
# image (psql, pg_dumpall, pg_basebackup are there). It polls the jobs table that
# replication-manager fills (replication_manager_schema.jobs) and runs one task at a
# time, streaming its output to the receiver replication-manager opened for it:
#
#   pgdump         pg_dumpall of the instance (logical backup)
#   pgbasebackup   pg_basebackup as a tar stream with the WAL it needs (physical backup,
#                  online, non blocking)
#
# The stream goes through `replication-manager-cli stream`, delivered in /jobs by the
# init container; without it, through bash's own TCP redirection (no TLS).
#
# Job states, the ones the MariaDB jobs use: 0 queued, 1 processing, 3 finished, 5 error.

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

log() { echo "[postgres_job] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

sql() { psql -X -q -At -v ON_ERROR_STOP=1 -c "$1"; }

# stream_to HOST PORT: stdin to the receiver, the `socat -u STDIN TCP:host:port` of the
# MariaDB jobs.
stream_to() {
    local host="$1" port="$2"
    if [ -x "$CLI" ]; then
        case "$host" in *:*) host="[$host]" ;; esac
        "$CLI" stream --to "$host:$port"
        return $?
    fi
    exec 3<>"/dev/tcp/$1/$port" || return 1
    cat >&3
    local rc=$?
    exec 3>&-
    return $rc
}

# set_state ID STATE DONE MESSAGE
set_state() {
    local msg="${4//\'/\'\'}"
    sql "UPDATE replication_manager_schema.jobs SET state=$2, done=$3, result='${msg:0:2000}', \"end\"=CASE WHEN $3=1 OR $2=5 THEN now() ELSE \"end\" END WHERE id=$1" \
        || log "cannot report state $2 of job $1"
}

run_task() {
    local id="$1" task="$2" host="$3" port="$4"
    local rc_cmd=0 rc_stream=0
    : > "$ERR"
    case "$task" in
    pgdump)
        pg_dumpall --clean --if-exists 2>"$ERR" | stream_to "$host" "$port"
        rc_cmd=${PIPESTATUS[0]} rc_stream=${PIPESTATUS[1]}
        ;;
    pgbasebackup)
        pg_basebackup -D - -Ft -X fetch -c fast 2>"$ERR" | stream_to "$host" "$port"
        rc_cmd=${PIPESTATUS[0]} rc_stream=${PIPESTATUS[1]}
        ;;
    *)
        set_state "$id" 5 0 "unknown task $task"
        log "job $id: unknown task $task"
        return
        ;;
    esac
    if [ "$rc_cmd" -eq 0 ] && [ "$rc_stream" -eq 0 ]; then
        set_state "$id" 3 1 "$task streamed to $host:$port"
        log "job $id: $task streamed to $host:$port"
    else
        set_state "$id" 5 0 "$task failed (tool $rc_cmd, stream $rc_stream): $(tr '\n' ' ' < "$ERR" | cut -c1-1500)"
        log "job $id: $task failed (tool $rc_cmd, stream $rc_stream): $(head -c 300 "$ERR")"
    fi
}

log "starting: PostgreSQL $PGHOST:$PGPORT as $PGUSER, every ${INTERVAL}s, client $([ -x "$CLI" ] && echo "$CLI" || echo 'absent, bash TCP')"
while true; do
    row=$(sql "SELECT id||'|'||task||'|'||server||'|'||port FROM replication_manager_schema.jobs WHERE done=0 AND state=0 ORDER BY id LIMIT 1" 2>/dev/null)
    if [ -n "$row" ]; then
        IFS='|' read -r id task host port <<<"$row"
        log "job $id: $task to $host:$port"
        set_state "$id" 1 0 "processing"
        run_task "$id" "$task" "$host" "$port"
    fi
    sleep "$INTERVAL"
done
