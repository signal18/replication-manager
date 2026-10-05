#!/bin/bash
# Checks, from outside, what the xtrabackup injection (prov-db-docker-xtrabackup-img) must give the jobs container of a
# database server once it is started or provisioned: socat, xtrabackup and xbstream are available on its PATH, can be
# executed, and xtrabackup is the build for the server's series. It runs at Docker level: nothing here depends on
# dbjobs_new.sh, which cannot run without socat. It does not run a backup; whatever a backup then does is not judged.
#
# Usage: docker_check.sh <server version> -- <command prefix that runs a command in the jobs container>
#   OpenSVC:    docker_check.sh 8.4.11-11 -- sudo docker exec repmy84..my1.container.jobs
#   Kubernetes: docker_check.sh 8.0.35    -- kubectl exec -n <ns> <pod> -c <name>-dbjobs --
# <server version> is the server's @@version ("8.4.11-11", "8.0.35"): its major.minor is the series xtrabackup must be for.
# Exit status 0 when every check passes.
if [ "$#" -lt 3 ] || [ "$2" != "--" ]; then
    sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//' | head -12
    exit 2
fi
VERSION="$1"
shift 2
SERIES=$(printf '%s' "$VERSION" | sed -n 's/^\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')
[ -n "$SERIES" ] || { echo "cannot read the series from '$VERSION'"; exit 2; }
fails=0
check() { # check <name> <command run in the container> <expected pattern, "" for success only>
    local out rc
    out=$("${PREFIX[@]}" sh -c "$2" 2>&1)
    rc=$?
    if [ "$rc" -eq 0 ] && { [ -z "$3" ] || [[ "$out" == $3 ]]; }; then
        echo "PASS $1"
    else
        echo "FAIL $1: rc=$rc ${out:0:200}"
        fails=$((fails + 1))
    fi
}
PREFIX=("$@")

# socat first: the jobs script cannot run, reach repman or receive its upgrade without it
check "socat is on the PATH" 'command -v socat' '*/socat'
check "socat runs" 'socat -V' '*socat version*'
check "xtrabackup is on the PATH" 'command -v xtrabackup' '*/xtrabackup'
check "xtrabackup runs" 'xtrabackup --version' '*xtrabackup version*'
check "xbstream is on the PATH" 'command -v xbstream' '*/xbstream'
check "xbstream runs" 'xbstream --version' '*xbstream*'
check "xtrabackup is for the server series $SERIES" "xtrabackup --version 2>&1 | grep -q 'based on MySQL server $SERIES\\.'" ''

if [ "$fails" -eq 0 ]; then echo "RESULT PASS"; else echo "RESULT FAIL: $fails check(s)"; fi
[ "$fails" -eq 0 ]
