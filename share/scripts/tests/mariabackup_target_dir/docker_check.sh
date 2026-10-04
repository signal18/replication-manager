#!/usr/bin/env bash
# Real Docker check of the mariabackup physical backup command of dbjobs_new.sh (job "mariabackup").
# The command used to run mariabackup in the legacy --innobackupex mode WITHOUT the positional target directory that mode
# requires: it stopped with "innobackupex: Missing argument" and streamed nothing (with the connection options written as
# long options). --innobackupex is also deprecated (mariadb-backup 11.8 says so). The job now runs the native form,
# --defaults-file first, then --backup ... --stream=xbstream --target-dir=<jobs dir>.
# The check takes the command line from the script itself (the line that runs $MARIADB_BACKUP ... --backup ..., up to its pipe
# to socat), runs it in the official mariadb image, with binary logging on, from the job's working directory with the script's
# variables set, and asserts that:
#   1. the command is the native form: --defaults-file comes first, --backup and --target-dir= are present, --innobackupex is not;
#   2. it exits 0, streams data, prints "completed OK!" in the form doneJob greps for, and prints the binlog position line in
#      the form the server parses (filename '..', position '..', GTID of the last change '..');
#   3. the target directory holds nothing but the job's own backup.out afterwards, and the working directory gets no stray entry;
#   4. the backup is restorable the way reseedmariabackup uses it: the stream unpacks with mbstream, "--prepare --export" exits 0,
#      and a fresh server opens that directory with the data intact (an InnoDB and an Aria table, CHECK TABLE OK);
#   5. the previous form (--innobackupex, no positional target directory) fails with "Missing argument" and 0 bytes, so the
#      check can see the bug it guards against.
# Usage: docker_check.sh [mariadb-image ...]   default: mariadb:10.6 mariadb:10.11 mariadb:11.8
set -u
command -v docker >/dev/null || { echo "FAIL: docker is required"; exit 2; }
cd "$(dirname "$0")/../../../.." || exit 2
SCRIPT=share/scripts/dbjobs_new.sh
P=mbt_$$; PW=pw; fail=0; ran=0
IMAGES=("$@"); [ ${#IMAGES[@]} -gt 0 ] || IMAGES=(mariadb:10.6 mariadb:10.11 mariadb:11.8)
ok() { echo "  ok:   $*"; }
bad() { echo "  FAIL: $*"; fail=1; }
trap 'docker rm -f "${P}_db" "${P}_re" >/dev/null 2>&1; docker volume rm -f "${P}_v" >/dev/null 2>&1' EXIT

# the real command: the line of the mariabackup job, without its pipe to socat
LINE=$(grep -E '^\s+\$MARIADB_BACKUP .*--stream=xbstream' "$SCRIPT" | head -1)
[ -n "$LINE" ] || { echo "FAIL: the mariabackup command was not found in $SCRIPT"; exit 2; }
CMD=${LINE%% | socat*}; CMD=${CMD#"${CMD%%[![:space:]]*}"}
# the previous form: --innobackupex first, no target directory
CMD_OLD=${CMD/--backup/}; CMD_OLD=${CMD_OLD/\$MARIADB_BACKUP /\$MARIADB_BACKUP --innobackupex }; CMD_OLD=${CMD_OLD// --target-dir=\"\$LOG_DIR\/\"/}

case "$CMD" in '$MARIADB_BACKUP --defaults-file='*' --backup '*' --stream=xbstream --target-dir='*) ok "the command is the native form: --defaults-file first, --backup, --stream=xbstream, --target-dir=" ;; *) bad "the command is not the native form: $CMD" ;; esac
case "$CMD" in *--innobackupex*) bad "the command still uses the deprecated --innobackupex" ;; *) ok "the command does not use --innobackupex" ;; esac

run_cmd() { # <command text>: runs it in the db container as the job does
  docker exec -e CMD="$1" "${P}_db" sh -c '
    BIN=$(command -v mariadb-backup || command -v mariabackup); MARIADB_BACKUP=$BIN
    MYSQL_CONF=/etc/mysql; USER=root; MYSQL_SERVER=127.0.0.1; PASSWORD='"$PW"'; MYSQL_PORT=3306
    LOG_DIR=/tmp/jobs; rm -rf "$LOG_DIR" /docker-entrypoint-initdb.d/-h* /docker-entrypoint-initdb.d/mariadb_backup_files; mkdir -p "$LOG_DIR"
    cd /docker-entrypoint-initdb.d
    eval "$CMD" >/tmp/o.xb
    echo "exit=$?"; echo "size=$(stat -c %s /tmp/o.xb)"
    echo "completed_ok=$(grep -c -E "([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}|\[Xtrabackup\]) completed OK!" "$LOG_DIR/backup.out")"
    echo "binlog_line=$(grep -c -E "filename '"'"'[^'"'"']+'"'"', position '"'"'[^'"'"']+'"'"', GTID of the last change '"'"'[^'"'"']+'"'"'" "$LOG_DIR/backup.out")"
    echo "missing_arg=$(grep -c "Missing argument" "$LOG_DIR/backup.out")"
    echo "target_leftovers=$(ls -A "$LOG_DIR" | grep -v "^backup.out$" | tr "\n" " ")"
    echo "cwd_stray=$(ls -A /docker-entrypoint-initdb.d | grep -E "^(-|mariadb_backup_files)" | tr "\n" " ")"' 2>&1
}
g() { echo "$out" | sed -n "s/^$1=//p"; }

for img in "${IMAGES[@]}"; do
  ran=$((ran + 1)); echo "=== $img"
  docker rm -f "${P}_db" >/dev/null 2>&1; docker run -d --name "${P}_db" -e MARIADB_ROOT_PASSWORD=$PW "$img" --log-bin=mysql-bin --server-id=1 >/dev/null
  up=0; for _ in $(seq 1 90); do docker exec "${P}_db" mariadb -uroot -p$PW -h127.0.0.1 -e 'select 1' >/dev/null 2>&1 || docker exec "${P}_db" mysql -uroot -p$PW -h127.0.0.1 -e 'select 1' >/dev/null 2>&1 && { up=1; break; }; sleep 2; done
  [ $up = 1 ] || { bad "$img did not start"; continue; }
  SQL='create database if not exists d; create table d.t(a int primary key, b varchar(20)) engine=innodb; insert into d.t values (1,"one"),(2,"two"),(3,"three"); create table d.m(a int) engine=aria; insert into d.m values (7)'
  docker exec "${P}_db" sh -c "mariadb -uroot -p$PW -e '$SQL' 2>/dev/null || mysql -uroot -p$PW -e '$SQL' 2>/dev/null"
  out=$(run_cmd "$CMD")
  [ "$(g exit)" = 0 ] && [ "$(g size)" -gt 1000000 ] 2>/dev/null && [ "$(g completed_ok)" -ge 1 ] 2>/dev/null && ok "the script's command: exit 0, $(g size) bytes streamed, completed OK!" || bad "the script's command: exit '$(g exit)', size '$(g size)', completed_ok '$(g completed_ok)'"
  [ "$(g binlog_line)" -ge 1 ] 2>/dev/null && ok "the binlog position line the server parses is there" || bad "no binlog position line in the form the server parses"
  [ -z "$(g target_leftovers)" ] && [ -z "$(g cwd_stray)" ] && ok "nothing left in the target directory (but backup.out) and no stray entry in the working directory" || bad "leftovers: target '$(g target_leftovers)', working directory '$(g cwd_stray)'"
  # restorable: take the stream out, unpack and prepare it as reseedmariabackup does, open it with a fresh server
  docker cp "${P}_db:/tmp/o.xb" "/tmp/${P}.xb" >/dev/null 2>&1; docker rm -f "${P}_re" >/dev/null 2>&1; docker volume rm -f "${P}_v" >/dev/null 2>&1; docker volume create "${P}_v" >/dev/null
  prep=$(docker run --rm -v "${P}_v:/data" -v "/tmp/${P}.xb:/in.xb:ro" --entrypoint sh "$img" -c 'B=$(command -v mariadb-backup || command -v mariabackup); S=$(command -v mbstream || command -v xbstream); $S -x -C /data </in.xb; echo "unpack_rc=$?"; $B --prepare --export --target-dir=/data 2>/tmp/p.log; echo "prepare_rc=$?"; chown -R mysql:mysql /data' 2>&1)
  rm -f "/tmp/${P}.xb"
  if [ "$(echo "$prep" | sed -n 's/^unpack_rc=//p')" = 0 ] && [ "$(echo "$prep" | sed -n 's/^prepare_rc=//p')" = 0 ]; then
    docker run -d --name "${P}_re" -e MARIADB_ROOT_PASSWORD=other -v "${P}_v:/var/lib/mysql" "$img" >/dev/null
    rup=0; for _ in $(seq 1 60); do docker exec "${P}_re" sh -c "mariadb -uroot -p$PW -e 'select 1' 2>/dev/null || mysql -uroot -p$PW -e 'select 1' 2>/dev/null" >/dev/null 2>&1 && { rup=1; break; }; sleep 2; done
    if [ $rup = 1 ]; then
      res=$(docker exec "${P}_re" sh -c "(mariadb -uroot -p$PW -N -B -e 'select count(*) from d.t; select count(*) from d.m; check table d.t; check table d.m' 2>/dev/null || mysql -uroot -p$PW -N -B -e 'select count(*) from d.t; select count(*) from d.m; check table d.t; check table d.m' 2>/dev/null) | tr '\t\n' '  '")
      case "$res" in "3 1 d.t check status OK d.m check status OK "*) ok "restorable: unpacked, prepared (--prepare --export) and opened by a fresh server, data intact (InnoDB 3 rows, Aria 1 row, CHECK TABLE OK)" ;; *) bad "the restored server answered: $res" ;; esac
    else bad "a fresh server did not open the prepared backup: $(docker logs "${P}_re" 2>&1 | grep -i -m1 error | cut -c1-140)"; fi
  else bad "unpack/prepare failed: $(echo "$prep" | tr '\n' ' ' | cut -c1-200)"; fi
  docker rm -f "${P}_re" >/dev/null 2>&1; docker volume rm -f "${P}_v" >/dev/null 2>&1
  out=$(run_cmd "$CMD_OLD")
  [ "$(g missing_arg)" -ge 1 ] 2>/dev/null && [ "$(g size)" = 0 ] && ok "the previous form (--innobackupex, no target directory) fails: Missing argument, 0 bytes" || bad "the previous form did not fail as expected: missing_arg '$(g missing_arg)', size '$(g size)'"
  docker rm -f "${P}_db" >/dev/null 2>&1
done
echo "cases: $ran"; [ $fail = 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"; exit $fail
