#!/bin/bash
# Host-side driver: extract partialRestore + helpers from dbjobs_new.sh and
# run one scenario in a throwaway mariadb:<version> container.
# Usage: run_mariadb.sh <version> [scenario] [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
VER="$1"; SCEN="${2:-keep-schema}"; SCRIPT="${3:-$HERE/../../dbjobs_new.sh}"
mkdir -p "$HERE/.work"
WORK="$(mktemp -d "$HERE/.work/mariadb.XXXX")"; mkdir -p "$WORK/out"; chmod 777 "$WORK/out"
# Every helper partialRestore needs lives between pr_log() and jobsCheck().
awk '/^pr_log\(\) \{/{p=1} /^jobsCheck\(\) \{/{p=0} p' "$SCRIPT" >"$WORK/functions.sh"
cp "$HERE/mariadb_inside.sh" "$HERE/scenarios.sh" "$WORK/"
NAME="prtest-${VER//./}-$$"
trap 'docker stop "$NAME" >/dev/null 2>&1' EXIT
docker run -d --rm --name "$NAME" -e MARIADB_ROOT_PASSWORD=rootpw -v "$WORK:/prtest:ro" -v "$WORK/out:/prtest-out" "mariadb:$VER" >/dev/null
for i in $(seq 1 60); do
    docker exec "$NAME" sh -c 'MYSQL_PWD=rootpw mariadb -uroot -h127.0.0.1 -e "select 1" >/dev/null 2>&1' && break
    sleep 1
done
sleep 2
echo "===== mariadb:$VER ($(docker exec "$NAME" sh -c 'MYSQL_PWD=rootpw mariadb -uroot -h127.0.0.1 -N -e "select version()" 2>/dev/null')) scenario=$SCEN"
docker exec "$NAME" bash /prtest/mariadb_inside.sh "$SCEN"
echo "(full log: $WORK/out/reseed.out)"
