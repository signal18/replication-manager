#!/bin/sh
# app_job.sh -- stateless Compute (APU) sensor for app deployments and proxies.
#
# Companion to dbjobs_new.sh's collect_dbu (the DBU sensor for databases). It runs in a
# small busybox sidecar that shares the service's netns (container#01, for egress to
# repman) and has the whole-service cgroup bound read-only at /svc-cgroup. It reads the
# cgroup, logs in as the `system` API-key service account, and POSTs the per-window APU
# maxima to repman, which computes the APU (normalise / pivot / binding) so the client
# workload's CPU is never spent on it.
#
# Busybox-friendly by design (ash `sh` + `wget`, no bash/curl): the sidecar is the same
# tiny `busybox` image as the proxy init container, and the script itself ships embedded
# in the repman binary (go:embed share/scripts/app_job.sh), staged into the service
# config tarball as init/app_job and extracted into the shared FS by the init container.
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
CG_NS="${MRM_CLUSTER:-}"
CG_SVC="${SENSOR_NAME:-}"
# resolve_cgroup: this service's cgroup v2 directory. /svc-cgroup when the orchestrator
# bound the exact slice there; else, under the OpenSVC tree bound at /svc-cgroup-root, the
# slice found by NAME: systemd spells a dash as \x2d (pg-logical -> pg\x2dlogical) and a
# backslash cannot travel in a bind mount, so the directory names are decoded before they
# are compared with the cluster (namespace) and service names.
resolve_cgroup() {
    if [ -r /svc-cgroup/memory.current ]; then echo /svc-cgroup; return 0; fi
    [ -n "${CG_NS:-}" ] && [ -n "${CG_SVC:-}" ] && [ -d /svc-cgroup-root ] || return 1
    # the REAL slice is the systemd-escaped spelling (a dash is \x2d, literally in the
    # directory name); the tree may hold EMPTY look-alikes next to it (plain dashed name,
    # backslash-less name: a bind that cannot be spelled still creates the cgroup it
    # names) with no memory controller -- matched by decoded name the phantom came first
    # and the sensor reported nothing (preprod 2026-10-07): escaped path first, then a
    # decoded match that carries a readable memory.current
    ens=$(printf '%s' "$CG_NS" | sed 's/-/\\x2d/g'); esvc=$(printf '%s' "$CG_SVC" | sed 's/-/\\x2d/g')
    s="/svc-cgroup-root/opensvc-ns.$ens.slice/opensvc-ns.$ens-svc.$esvc.slice"
    [ -r "$s/memory.current" ] && { printf '%s\n' "$s"; return 0; }
    for d in /svc-cgroup-root/opensvc-ns.*.slice; do
        [ "$(printf '%s' "${d##*/}" | sed 's/\\x2d/-/g')" = "opensvc-ns.$CG_NS.slice" ] || continue
        for s in "$d"/opensvc-ns.*-svc.*.slice; do
            [ "$(printf '%s' "${s##*/}" | sed 's/\\x2d/-/g')" = "opensvc-ns.$CG_NS-svc.$CG_SVC.slice" ] && [ -r "$s/memory.current" ] && { printf '%s\n' "$s"; return 0; }
        done
    done
    return 1
}
CKPT=/tmp/apu.checkpoint

log() { echo "[app_job] $*" >&2; }

if [ -z "$URL" ] || [ -z "$KEY" ] || [ -z "$CLUSTER" ] || [ -z "$NAME" ]; then
    log "missing env (need REPLICATION_MANAGER_URL, SENSOR_API_KEY, MRM_CLUSTER, SENSOR_NAME); sensor disabled"
    exit 0
fi

# Log in as the system service account with the injected API key; echoes the JWT or "".
# busybox wget: -O- to stdout, --post-data for POST, --header repeatable.
system_login() {
    wget -q --no-check-certificate -O- \
        --header "Content-Type:application/json" \
        --post-data "{\"username\":\"system\",\"password\":\"$KEY\"}" \
        "$URL/api/login" 2>/dev/null \
        | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4
}

# One sample: read the cgroup, compute the cpu rate from the checkpoint, push /apu.
collect_apu() {
    CG=$(resolve_cgroup) || CG=/svc-cgroup
    [ -r "$CG/memory.current" ] || { log "no readable cgroup at $CG; skip"; return 0; }

    now=$(date +%s)
    mem=$(cat "$CG/memory.current" 2>/dev/null || echo 0)
    cpu=$(awk '/^usage_usec/{print $2}' "$CG/cpu.stat" 2>/dev/null || echo 0)
    # disk: df of the cgroup mount is not the unit's data; stateless proxies/apps have
    # little local disk, so report 0 here (the plan carries the reserved disk). A future
    # refinement can statfs the unit's data volume if one is mounted into the sidecar.
    disk=0

    pe=""; pc=""
    [ -s "$CKPT" ] && read -r pe pc < "$CKPT"
    echo "$now $cpu" > "$CKPT"
    [ -z "$pe" ] && return 0                      # first run: seed the checkpoint, no push
    dt=$((now - pe))
    [ "$dt" -le 0 ] && return 0                   # clock skew

    cores=$(awk -v c="$cpu" -v p="$pc" -v dt="$dt" 'BEGIN{printf "%.4f", (c-p)/(dt*1000000)}')

    tok=$(system_login)
    [ -z "$tok" ] && { log "login failed; skip push"; return 0; }

    # busybox date may not support -d @epoch; fall back to current time (window is
    # approximate -- repman uses the values, not the exact boundaries).
    ws=$(date -u -d "@$pe" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
    we=$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

    # Internal network: cumulative rx/tx octets of the pod interface (the sidecar shares
    # the pod netns, so /proc/net/dev IS the unit's eth0). Raw counters -- repman rates them.
    netrx=0; nettx=0
    read -r netrx nettx <<EOF2
$(awk -F'[: ]+' 'NR>2 && $2!="lo" {rx+=$3; tx+=$11} END{printf "%d %d\n", rx+0, tx+0}' /proc/net/dev 2>/dev/null || echo "0 0")
EOF2
    data="{\"windowStart\":\"$ws\",\"windowEnd\":\"$we\",\"memMaxBytes\":$mem,\"cpuMaxCores\":$cores,\"diskMaxBytes\":$disk,\"netRxBytes\":${netrx:-0},\"netTxBytes\":${nettx:-0}}"
    wget -q --no-check-certificate -O- \
        --header "Authorization: Bearer $tok" \
        --header "Content-Type:application/json" \
        --post-data "$data" \
        "$URL/api/clusters/$CLUSTER/apu/$KIND/$NAME" >/dev/null 2>&1 || log "push failed (non-fatal)"
}

log "APU sensor starting: $KIND/$NAME -> $URL (cluster $CLUSTER), every ${INTERVAL}s"
while true; do
    collect_apu
    sleep "$INTERVAL"
done
