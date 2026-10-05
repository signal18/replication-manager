#!/bin/bash
# Host-side driver: extract partialRestore + helpers from dbjobs_new.sh and
# run one scenario in a throwaway mariadb:<version> container.
# Usage: run_mariadb.sh <version> [scenario] [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
VER="$1"; SCEN="${2:-keep-schema}"; SCRIPT="${3:-$HERE/../../dbjobs_new.sh}"
mkdir -p "$HERE/.work"
WORK="$(mktemp -d "$HERE/.work/mariadb.XXXX")"; mkdir -p "$WORK/out"; chmod 777 "$WORK/out"
# Every helper partialRestore needs lives between pr_log() and jobsCheck(), plus db_owner().
awk '/^db_owner\(\) \{/{d=1} d{print} d&&/^}/{d=0} /^pr_log\(\) \{/{p=1} /^jobsCheck\(\) \{/{p=0} p' "$SCRIPT" >"$WORK/functions.sh"
cp "$HERE/mariadb_inside.sh" "$HERE/scenarios.sh" "$WORK/"
NAME="prtest-${VER//./}-$$"
# No --rm: a container that dies at startup keeps its logs for the message below.
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
docker run -d --name "$NAME" -e MARIADB_ROOT_PASSWORD=rootpw -v "$WORK:/prtest:ro" -v "$WORK/out:/prtest-out" "mariadb:$VER" >/dev/null
for i in $(seq 1 60); do
    if [[ "$(docker inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null)" != "true" ]]; then
        echo "ERROR: the mariadb:$VER container exited at startup (exit $(docker inspect -f '{{.State.ExitCode}} oom={{.State.OOMKilled}}' "$NAME" 2>&1)); last log lines:"
        docker logs "$NAME" 2>&1 | tail -25
        exit 1
    fi
    docker exec "$NAME" sh -c 'MYSQL_PWD=rootpw mariadb -uroot -h127.0.0.1 -e "select 1" >/dev/null 2>&1' && break
    sleep 1
done
sleep 2
echo "===== mariadb:$VER ($(docker exec "$NAME" sh -c 'MYSQL_PWD=rootpw mariadb -uroot -h127.0.0.1 -N -e "select version()" 2>/dev/null')) scenario=$SCEN"
docker exec "$NAME" bash /prtest/mariadb_inside.sh "$SCEN"
echo "(full log: $WORK/out/reseed.out)"
