#!/bin/bash
# Phase 1 failure-path matrix: one RESULT line per server version x scenario.
# Usage: [MARIADB=".."] [MYSQL=".."] [SCENARIOS=".."] failure_matrix.sh [dbjobs_new.sh]
#   MARIADB    mariadb image tags              (default: 10.5 10.11 11.8)
#   MYSQL      percona server:xtrabackup tags  (default: 8.0.35:8.0.35 8.4:8.4)
#   SCENARIOS  see scenarios.sh                (default: all seven)
# Full output of each run: .work/fail-<server>-<scenario>.log
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$HERE/.work"
SCENARIOS="${SCENARIOS:-prepare-failed subpart unsafe-name bad-cfg pw-changed replica interrupted}"
fails=0
report() {
    local name="$1" sc="$2" log="$3" res
    res=$(grep -m1 '^RESULT ' "$log")
    [[ -z "$res" ]] && res="RESULT $sc FAIL: no verdict (see the log)"
    [[ "$res" == *PASS ]] || fails=$((fails + 1))
    printf '%-16s %s\n' "$name" "${res#RESULT }"
}
for v in ${MARIADB-10.5 10.11 11.8}; do
    for sc in $SCENARIOS; do
        log="$HERE/.work/fail-mariadb-$v-$sc.log"
        timeout 900 "$HERE/run_mariadb.sh" "$v" "$sc" "$@" >"$log" 2>&1
        report "mariadb:$v" "$sc" "$log"
    done
done
for pair in ${MYSQL-8.0.35:8.0.35 8.4:8.4}; do
    srv="${pair%%:*}" pxb="${pair#*:}"
    for sc in $SCENARIOS; do
        log="$HERE/.work/fail-percona-$srv-$sc.log"
        timeout 1200 "$HERE/run_mysql.sh" "$srv" "$pxb" "$sc" "$@" >"$log" 2>&1
        report "percona:$srv" "$sc" "$log"
    done
done
echo "failed: $fails"
[[ $fails -eq 0 ]]
