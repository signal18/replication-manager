#!/bin/bash
# app_job.sh -- stateless Compute (APU) sensor for app deployments and proxies.
#
# Companion to dbjobs_new.sh's collect_dbu (the DBU sensor for databases). It runs in a
# small sidecar that shares the service's netns (container#01, for egress to repman) and
# has the whole-service cgroup bound read-only at /svc-cgroup. It reads the cgroup, logs
# in as the `system` API-key service account, and POSTs the per-window APU maxima to
# repman, which computes the APU (normalise / pivot / binding) so the client workload's
# CPU is never spent on it.
#
# APU (Compute profile) has THREE axes -- mem, cpu, disk -- and NO io (no IOPS lock),
# so unlike collect_dbu this pushes no ioMaxIops. cpu is a rate vs the previous run's
# cumulative usage_usec, persisted in a checkpoint. Fail-soft: any missing piece just
# skips a push, never exits the loop.
#
# Auth: logs in as `system` with SENSOR_API_KEY -- the derived HMAC(SecretKey, cluster)
# credential injected via the OpenSVC SECRET channel (never in svcenv/git). This is why
# apps/proxies, which have no DB password, can authenticate where the DB sensor uses the
# DB-password secret-login.
#
# Env (injected by the OpenSVC sensor container):
#   REPLICATION_MANAGER_URL   e.g. https://mrm-host:10005   (repman API base)
#   SENSOR_API_KEY            the derived system key (from the secret channel)
#   MRM_CLUSTER               cluster name
#   SENSOR_KIND               app | proxy
#   SENSOR_NAME               the app-deployment / proxy name (the /apu {name})
#   SENSOR_INTERVAL           loop seconds (default 60)
set -u

URL="${REPLICATION_MANAGER_URL:-}"
KEY="${SENSOR_API_KEY:-}"
CLUSTER="${MRM_CLUSTER:-}"
KIND="${SENSOR_KIND:-app}"
NAME="${SENSOR_NAME:-}"
INTERVAL="${SENSOR_INTERVAL:-60}"
CG=/svc-cgroup
CKPT=/tmp/apu.checkpoint

log() { echo "[app_job] $*" >&2; }

if [ -z "$URL" ] || [ -z "$KEY" ] || [ -z "$CLUSTER" ] || [ -z "$NAME" ]; then
    log "missing env (need REPLICATION_MANAGER_URL, SENSOR_API_KEY, MRM_CLUSTER, SENSOR_NAME); sensor disabled"
    exit 0
fi

# Log in as the system service account with the injected API key; echoes the JWT or "".
system_login() {
    curl -sk -m 10 -X POST "$URL/api/login" -H 'Content-Type: application/json' \
        -d "{\"username\":\"system\",\"password\":\"$KEY\"}" 2>/dev/null \
        | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4
}

# One sample: read the cgroup, compute the cpu rate from the checkpoint, push /apu.
collect_apu() {
    [ -r "$CG/memory.current" ] || { log "no readable cgroup at $CG; skip"; return 0; }

    local now mem cpu disk
    now=$(date +%s)
    mem=$(cat "$CG/memory.current" 2>/dev/null || echo 0)
    cpu=$(awk '/^usage_usec/{print $2}' "$CG/cpu.stat" 2>/dev/null || echo 0)
    # disk: df of the cgroup mount is not the unit's data; stateless proxies/apps have
    # little local disk, so report 0 here (the plan carries the reserved disk). A future
    # refinement can statfs the unit's data volume if one is mounted into the sidecar.
    disk=0

    local pe pc
    [ -s "$CKPT" ] && read -r pe pc < "$CKPT"
    echo "$now $cpu" > "$CKPT"
    [ -z "${pe:-}" ] && return 0                 # first run: seed the checkpoint, no push
    local dt=$((now - pe))
    [ "$dt" -le 0 ] && return 0                   # clock skew

    local cores
    cores=$(awk -v c="$cpu" -v p="$pc" -v dt="$dt" 'BEGIN{printf "%.4f", (c-p)/(dt*1000000)}')

    local tok
    tok=$(system_login)
    [ -z "$tok" ] && { log "login failed; skip push"; return 0; }

    local ws we
    ws=$(date -u -d "@$pe" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
    we=$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

    local data="{\"windowStart\":\"$ws\",\"windowEnd\":\"$we\",\"memMaxBytes\":$mem,\"cpuMaxCores\":$cores,\"diskMaxBytes\":$disk}"
    curl -sk -m 10 -X POST "$URL/api/clusters/$CLUSTER/apu/$KIND/$NAME" \
        -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' \
        -d "$data" >/dev/null 2>&1 || log "push failed (non-fatal)"
}

log "APU sensor starting: $KIND/$NAME -> $URL (cluster $CLUSTER), every ${INTERVAL}s"
while true; do
    collect_apu
    sleep "$INTERVAL"
done
