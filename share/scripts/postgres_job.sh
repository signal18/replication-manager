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
TASKS="pgdump pgbasebackup optimize pgstandby pgreseed"
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
# resolve_cgroup: the cgroup v2 directory of this PostgreSQL server, as dbjobs_new.sh's
# resolve_dbu_cgroup: the service slice the orchestrator binds at /svc-cgroup when it is
# there; on premise (this script runs on the host through ssh) the postmaster's own cgroup
# from /proc/<pid>/cgroup under the host cgroup tree. Nothing when neither is readable.
resolve_cgroup() {
    if [ -r /svc-cgroup/memory.current ]; then
        echo /svc-cgroup
        return 0
    fi
    local pid sub base
    pid=$(pgrep -x postgres 2>/dev/null | head -1)
    [ -n "$pid" ] || return 0
    sub=$(awk -F: '$1=="0"{print $3; exit}' "/proc/$pid/cgroup" 2>/dev/null)
    [ -n "$sub" ] || return 0
    for base in "/proc/$pid/root/sys/fs/cgroup" "/sys/fs/cgroup"; do
        [ -r "${base}${sub}/memory.current" ] && { echo "${base}${sub}"; return 0; }
    done
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
    report_usage
    sleep "$INTERVAL"
done
