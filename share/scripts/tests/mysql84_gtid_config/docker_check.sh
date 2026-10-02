#!/usr/bin/env bash
# Real Docker check of the generated with_rep_mysqlgtid.cnf on MySQL and Percona Server.
#
# The template is one version-independent [mysqld] block in which EVERY option has the loose-
# prefix: a server applies the options it knows and ignores the others instead of refusing to
# start. The variables renamed or removed between 5.7 and 8.4 appear under their legacy and
# their modern name; without loose- an unknown name stops the server ("unknown variable
# 'relay_log_info_repository=table'" on 8.4, 'gtid_mode' on a MariaDB that got the tag by
# mistake). A version group the server does not read (the earlier [mysqld-8.0] / [mysqld-5.7]
# sections, with no [mysqld-8.4]) leaves an 8.4 server with gtid_mode=OFF and a replica using
# AUTO_POSITION unable to attach.
#
# For each image this script:
#   1. extracts the template from the embedded OpenSVC moduleset,
#   2. starts a source and a replica from it (cold start, nothing set at runtime),
#   3. checks gtid_mode / enforce_gtid_consistency / relay_log_recovery on both, attaches the
#      replica with AUTO_POSITION and checks that a write replicates and GTID sets match.
# A mariadb:* image is only checked to start with the template (the options are MySQL's and are
# ignored there): that is the case of the tag being applied to a MariaDB server by mistake.
#
# Usage: docker_check.sh [image ...]
#        default: mysql:5.7 mysql:8.0 mysql:8.4 percona/percona-server:8.0 percona/percona-server:8.4 mariadb:10.11
# Control: GTID_CNF=<file> tests that file instead of the template; the check must FAIL for a
#          file that only has [mysqld-8.0] / [mysqld-5.7] groups (the state before the fix) on 8.4,
#          and for a block with plain (non-loose-) options on MariaDB.
set -u
for tool in docker python3; do command -v "$tool" >/dev/null || { echo "FAIL: $tool is required"; exit 2; }; done
cd "$(dirname "$0")/../../../.." || exit 2
MODULESET=share/opensvc/moduleset_mariadb.svc.mrm.db.json
IMAGES=("$@"); [ ${#IMAGES[@]} -eq 0 ] && IMAGES=(mysql:5.7 mysql:8.0 mysql:8.4 percona/percona-server:8.0 percona/percona-server:8.4 mariadb:10.11)
WORK=$(mktemp -d); NET=gtidcnf_net_$$; PW=pw; fail=0
SRC=gtidcnf_src_$$; REP=gtidcnf_rep_$$
drop_servers() { docker rm -f $SRC $REP >/dev/null 2>&1; docker volume rm -f ${SRC}_data ${REP}_data >/dev/null 2>&1; }
cleanup() { drop_servers; docker network rm "$NET" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT

if [ -n "${GTID_CNF:-}" ]; then
  cp "$GTID_CNF" "$WORK/gtid.cnf"
else
  python3 - "$MODULESET" "$WORK/gtid.cnf" <<'PY' || { echo "FAIL: cannot extract the mysqlgtid template"; exit 2; }
import json, sys
d = json.load(open(sys.argv[1])); fmt = None
def walk(o):
    global fmt
    if isinstance(o, dict):
        if o.get("var_name") == "db_cnf_rep_with_mysqlgtid":
            fmt = json.loads(o["var_value"])["fmt"]
        for v in o.values(): walk(v)
    elif isinstance(o, list):
        for v in o: walk(v)
walk(d)
if fmt is None: sys.exit(1)
open(sys.argv[2], "w").write(fmt)
PY
fi
echo "--- template under test:"; sed 's/^/    /' "$WORK/gtid.cnf"

q() { docker exec "$1" sh -c 'c=$(command -v mariadb || command -v mysql); exec "$c" -uroot -p"$0" -N -B -e "$1"' "$PW" "$2" 2>&1 | grep -v -E 'jemalloc|Using a password'; }
wait_up() { for _ in $(seq 1 80); do sleep 3; docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -q true || return 1; q "$1" 'select 1' | grep -q '^1$' && return 0; done; return 1; }
start() { # name image serverid
  # The data directory is a named volume: Docker gives an empty volume the owner of the image's
  # /var/lib/mysql (mysql, 999 or 1001 for Percona), so no chown and no sudo are needed.
  local uid=999; [[ $2 == percona* ]] && uid=1001
  local extra=(); [[ $2 == *:5.* ]] && extra=(--log-slave-updates=ON)   # 5.x needs it for gtid_mode=ON; 8.x logs replica updates by default
  docker run -d --name "$1" --network "$NET" --user "$uid:$uid" -e MYSQL_ROOT_PASSWORD=$PW -e MARIADB_ROOT_PASSWORD=$PW \
    -v "$WORK/gtid.cnf:/etc/mysql/conf.d/gtid.cnf:ro" -v "$1_data:/var/lib/mysql" "$2" \
    --defaults-extra-file=/etc/mysql/conf.d/gtid.cnf --server-id="$3" --log-bin=binlog ${extra[@]+"${extra[@]}"} >/dev/null 2>&1
}

docker network create "$NET" >/dev/null
for img in "${IMAGES[@]}"; do
  echo "=== $img"
  drop_servers
  if [[ $img == mariadb* ]]; then
    start $SRC "$img" 11
    if wait_up $SRC; then echo "  ok:   $(q $SRC 'select @@version') starts with the template (MySQL options ignored: $(docker logs $SRC 2>&1 | grep -c 'unknown variable'))"
    else echo "  FAIL: MariaDB did not start with the template: $(docker logs $SRC 2>&1 | grep -iE 'unknown variable|ERROR' | grep -v jemalloc | head -1 | cut -c1-170)"; fail=1; fi
    continue
  fi
  start $SRC "$img" 11 && start $REP "$img" 12
  if ! wait_up $SRC || ! wait_up $REP; then
    echo "  FAIL: a server did not start: $(docker logs $SRC 2>&1 | grep -iE 'unknown variable|ERROR' | grep -v jemalloc | head -1 | cut -c1-170)"; fail=1; continue
  fi
  ver=$(q $REP 'select @@version')
  read -r maj min pat <<<"$(echo "$ver" | sed -E 's/^([0-9]+)\.([0-9]+)\.([0-9]+).*/\1 \2 \3/')"
  if [ "$maj" -gt 8 ] || { [ "$maj" -eq 8 ] && [ "$min" -ge 4 ]; }; then reset='RESET BINARY LOGS AND GTIDS'; else reset='RESET MASTER'; fi
  for n in $SRC $REP; do
    v=$(q $n 'select concat(@@gtid_mode,"/",@@enforce_gtid_consistency,"/",@@relay_log_recovery)')
    [ "$v" = "ON/ON/1" ] && echo "  ok:   $(echo $n | sed -E 's/gtidcnf_(src|rep)_.*/\1/') $ver cold start gtid_mode/enforce/relay_log_recovery = $v" || { echo "  FAIL: $n $ver gtid_mode/enforce/relay_log_recovery = $v (want ON/ON/1)"; fail=1; }
  done
  echo "  info: ignored/unknown names in the log: $(docker logs $REP 2>&1 | grep -c 'unknown variable')  deprecation notes: $(docker logs $REP 2>&1 | grep -ci 'deprecated')"
  # Both servers ran the image's own initialization SQL with the binary log on, so each holds its
  # own init GTIDs; drop them so the replica only applies what the test writes on the source.
  for n in $SRC $REP; do q $n "$reset" >/dev/null; done
  if [ "$maj" -eq 5 ] || { [ "$maj" -eq 8 ] && { [ "$min" -eq 0 ] && [ "$pat" -lt 23 ]; }; }; then
    q $REP "CHANGE MASTER TO MASTER_HOST='$SRC', MASTER_USER='root', MASTER_PASSWORD='$PW', MASTER_AUTO_POSITION=1; START SLAVE;" >/dev/null
  else
    q $REP "CHANGE REPLICATION SOURCE TO SOURCE_HOST='$SRC', SOURCE_USER='root', SOURCE_PASSWORD='$PW', SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1; START REPLICA;" >/dev/null
  fi
  q $SRC 'create database t; create table t.x(i int primary key); insert into t.x values (1)' >/dev/null
  # Poll (up to 60 s) until the replica is connected, has the row and the GTID sets are equal.
  for _ in $(seq 1 30); do
    st=$(q $REP 'select service_state from performance_schema.replication_connection_status')
    rows=$(q $REP 'select count(*) from t.x' | grep -E '^[0-9]+$' || echo 0)
    sg=$(q $SRC 'select @@global.gtid_executed'); rg=$(q $REP 'select @@global.gtid_executed')
    [ "$st" = "ON" ] && [ "$rows" = "1" ] && [ -n "$sg" ] && [ "$sg" = "$rg" ] && break
    sleep 2
  done
  [ "$st" = "ON" ] && [ "$rows" = "1" ] && [ -n "$sg" ] && [ "$sg" = "$rg" ] \
    && echo "  ok:   replica attached with AUTO_POSITION, write replicated, gtid_executed equal ($sg)" \
    || { echo "  FAIL: io=$st rows=$rows source=$sg replica=$rg"; fail=1; }
done
[ $fail -eq 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"; exit $fail
