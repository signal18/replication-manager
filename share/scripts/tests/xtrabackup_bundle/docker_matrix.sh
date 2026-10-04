#!/bin/bash
# Automated Docker check of the xtrabackup injection (prov-db-docker-xtrabackup-img) on the official images, without
# repman, OpenSVC or Kubernetes: for each case it does what the render does, in Docker.
#   1. runs share/scripts/xtrabackup_bundle.sh in the official xtrabackup image, on an empty volume, with the same
#      XB_IMAGE and XB_SERIES the render passes;
#   2. starts the official database image as the jobs container, with the volume read-only at /opt/xtrabackup and the
#      PATH the render sets (xtrabackupStockPath of cluster/prov_xtrabackup_bundle.go, read from the source);
#   3. runs docker_check.sh in it: socat, xtrabackup and xbstream available, executable, of the server's series.
# MariaDB images are the third product: they ship mariabackup, mbstream and socat, so the setting must be inert on
# them. The matrix checks that the images really ship the three tools (nothing to inject), and runs the render test
# that proves the setting renders nothing for them (needs Go; skipped with a note when there is none).
# Failure cases check the other way: a series mismatch must leave no bundle (status "failed", no "current") and a jobs
# container without the injection must fail docker_check.sh.
# Each case ends with one line, RESULT <case> PASS or RESULT <case> FAIL: <reason>; the exit status is 0 only when
# every case passes. Needs Docker and the network to pull the images.
# Usage: docker_matrix.sh                 # the four official MySQL and Percona images, and the MariaDB images
#        CASES="mysql:8.4|percona/percona-xtrabackup:8.4|8.4" docker_matrix.sh   # <db image>|<xtrabackup image>|<series>
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
BUNDLE_SCRIPT="$ROOT/share/scripts/xtrabackup_bundle.sh"
SRC="$ROOT/cluster/prov_xtrabackup_bundle.go"
STOCK_PATH=$(sed -n 's/^const xtrabackupStockPath = "\(.*\)"$/\1/p' "$SRC")
[ -n "$STOCK_PATH" ] || { echo "cannot read xtrabackupStockPath from $SRC"; exit 2; }
JOBS_PATH="$STOCK_PATH:/opt/xtrabackup/current/bin"

CASES=${CASES:-"mysql:8.0.35|percona/percona-xtrabackup:8.0|8.0
mysql:8.4|percona/percona-xtrabackup:8.4|8.4
percona/percona-server:8.0|percona/percona-xtrabackup:8.0|8.0
percona/percona-server:8.4|percona/percona-xtrabackup:8.4|8.4"}

TMP=$(mktemp -d)
sed 's#@MOUNT@#/opt/xtrabackup#g' "$BUNDLE_SCRIPT" >"$TMP/init.sh"
RUN="xbm$$"
cleanup() { docker rm -f "$RUN-jobs" >/dev/null 2>&1; docker volume ls -q --filter "name=$RUN" | xargs -r docker volume rm >/dev/null 2>&1; rm -rf "$TMP"; }
trap cleanup EXIT

total=0
bad=0
result() { # result <case> <reason, empty when it passed>
    total=$((total + 1))
    if [ -z "$2" ]; then echo "RESULT $1 PASS"; else echo "RESULT $1 FAIL: $2"; bad=$((bad + 1)); fi
}

# init <volume> <xtrabackup image> <series>: the init container of the render
init() {
    docker run --rm --user 0:0 -v "$1:/bundle" -v "$TMP/init.sh:/init.sh:ro" -e "XB_IMAGE=$2" -e "XB_SERIES=$3" \
        --entrypoint /bin/sh "$2" /init.sh >"$TMP/init.out" 2>&1
}
status() { docker run --rm -v "$1:/bundle" --entrypoint /bin/sh "$2" -c 'cat /bundle/status 2>/dev/null; [ -d /bundle/current ] && echo "current=yes" || echo "current=no"' 2>&1 | tr '\n' ' '; }
# jobs <volume or empty> <db image>: the jobs container, the database image with the render's mount and PATH
jobs() {
    docker rm -f "$RUN-jobs" >/dev/null 2>&1
    if [ -n "$1" ]; then
        docker run -d --name "$RUN-jobs" -v "$1:/opt/xtrabackup:ro" -e "PATH=$JOBS_PATH" --entrypoint sleep "$2" 600 >/dev/null
    else
        docker run -d --name "$RUN-jobs" --entrypoint sleep "$2" 600 >/dev/null
    fi
}
server_version() { docker run --rm --entrypoint mysqld "$1" --version 2>/dev/null | sed -n 's/.*Ver \([0-9][0-9.]*\).*/\1/p' | head -1; }

while IFS='|' read -r dbimg xbimg series; do
    [ -n "$dbimg" ] || continue
    name="$dbimg"
    vol="$RUN-$total"
    docker volume create "$vol" >/dev/null
    version=$(server_version "$dbimg")
    if [ -z "$version" ]; then result "$name" "cannot read the server version of $dbimg"; continue; fi

    # success: the bundle is built, the jobs container sees the three tools
    if ! init "$vol" "$xbimg" "$series"; then result "$name" "the init container failed: $(tail -2 "$TMP/init.out" | tr '\n' ' ')"; continue; fi
    st=$(status "$vol" "$xbimg")
    case "$st" in "ok $xbimg"*"current=yes"*) ;; *) result "$name" "bundle status: $st"; continue ;; esac
    jobs "$vol" "$dbimg"
    if out=$(bash "$HERE/docker_check.sh" "$version" -- docker exec "$RUN-jobs" 2>&1); then result "$name (injection)" ""; else result "$name (injection)" "$(printf '%s' "$out" | grep -E 'FAIL' | head -3 | tr '\n' ' ')"; fi

    # no injection: the same container without the volume must fail the check (the check can tell)
    jobs "" "$dbimg"
    if bash "$HERE/docker_check.sh" "$version" -- docker exec "$RUN-jobs" >/dev/null 2>&1; then result "$name (no injection must fail the check)" "the check passed without the tools"; else result "$name (no injection must fail the check)" ""; fi

    # wrong series: the init container refuses, leaves no bundle, and the check fails
    wrong="8.0"; [ "$series" = "8.0" ] && wrong="8.4"
    init "$vol" "$xbimg" "$wrong"
    st=$(status "$vol" "$xbimg")
    case "$st" in "failed xtrabackup is for the MySQL series $series but the database image is $wrong"*"current=no"*) result "$name (series mismatch is refused, no bundle left)" "" ;; *) result "$name (series mismatch is refused, no bundle left)" "status: $st" ;; esac
    jobs "$vol" "$dbimg"
    if bash "$HERE/docker_check.sh" "$version" -- docker exec "$RUN-jobs" >/dev/null 2>&1; then result "$name (refused bundle must fail the check)" "the check passed with no bundle"; else result "$name (refused bundle must fail the check)" ""; fi
    docker rm -f "$RUN-jobs" >/dev/null 2>&1
    docker volume rm "$vol" >/dev/null 2>&1
done <<<"$CASES"

# MariaDB: the image ships its own tools, so the injection has nothing to do
for dbimg in ${MARIADB:-mariadb:10.11 mariadb:11.8}; do
    jobs "" "$dbimg"
    if out=$(docker exec "$RUN-jobs" sh -c 'command -v socat && { command -v mariabackup || command -v mariadb-backup; } && command -v mbstream' 2>&1); then
        result "$dbimg (ships socat, mariabackup and mbstream itself)" ""
    else
        result "$dbimg (ships socat, mariabackup and mbstream itself)" "$(printf '%s' "$out" | tr '\n' ' ')"
    fi
done
if command -v go >/dev/null 2>&1; then
    if (cd "$ROOT" && go test ./cluster/ -run 'TestXtrabackupInertOnMariaDB' -count=1 >"$TMP/inert.out" 2>&1); then
        result "mariadb (the setting renders nothing, OpenSVC and Kubernetes)" ""
    else
        result "mariadb (the setting renders nothing, OpenSVC and Kubernetes)" "$(tail -3 "$TMP/inert.out" | tr '\n' ' ')"
    fi
else
    echo "SKIP mariadb render test: go is not installed"
fi

echo "TOTAL $total checks, $bad failed"
[ "$bad" -eq 0 ]
