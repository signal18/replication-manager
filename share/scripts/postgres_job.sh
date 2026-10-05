#!/bin/bash
# replication-manager jobs sidecar for a PostgreSQL service.
#
# Runs next to the PostgreSQL container, in the same network namespace, from the same
# image (psql, pg_dumpall, pg_basebackup are there). It asks replication-manager through
# its API whether a task is wanted (the API jobs mode, no jobs table), and runs it,
# streaming its output to the receiver replication-manager opens for it:
#
#   pgdump         pg_dumpall of the instance (logical backup)
#   pgbasebackup   pg_basebackup as a tar stream with the WAL it needs (physical backup,
#                  online, it blocks neither reads nor writes)
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
TASKS="pgdump pgbasebackup"

log() { echo "[postgres_job] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

job() { "$CLI" job --secret-env POSTGRES_PASSWORD "$@"; }

run_task() {
    local task="$1" addr rc_cmd=0 rc_stream=0
    job state "$task" processing || log "$task: cannot report processing"
    if ! addr=$(job receiver "$task") || [ -z "$addr" ]; then
        log "$task: no receiver"
        job state "$task" error
        return
    fi
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

log "starting: PostgreSQL $PGHOST:$PGPORT as $PGUSER, server ${REPLICATION_MANAGER_HOST_NAME:-?}:${REPLICATION_MANAGER_HOST_PORT:-?} of ${REPLICATION_MANAGER_CLUSTER_NAME:-?}, every ${INTERVAL}s"
while [ ! -x "$CLI" ]; do
    log "waiting for $CLI"
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
    sleep "$INTERVAL"
done
