#!/usr/bin/env bash
# Real Docker check of the --innodb-undo-directory handling of the xtrabackup physical backup (dbjobs_new.sh,
# xtrabackup_undo_args). The server is a stock MySQL or Percona Server image started with the undo layout of
# replication-manager's default_path.cnf: undo tablespaces outside ./.system through the version group [mysqld-8.0] /
# [mysqld-8.4], and ./.system/innodb/undo in [mysqld], the only group xtrabackup reads from my.cnf.
# The backup is run the way the jobs script runs it: the argument array of the real xtrabackup_undo_args (extracted from
# dbjobs_new.sh and asked to the live server) is handed to xtrabackup as separate argv entries (never through a shell
# string), from /docker-entrypoint-initdb.d, streamed with --stream=xbstream. For each case it shows that
#   1. WITHOUT the option xtrabackup exits non-zero on the undo path: "Cannot create .../undo_NNN because ... already" when
#      the undo files are in the datadir, "no existing undo tablespaces found" when they are in another directory,
#   2. WITH the option it exits 0 and prints "completed OK", the stream unpacks with xbstream, backup-my.cnf carries the
#      option, and xtrabackup --prepare on that target exits 0.
# Cases: stock mysql:8.0 and mysql:8.4, percona/percona-server:8.0 and :8.4 (undo in the datadir, "./"), mysql:8.4 with an
# undo directory whose path holds a space (the single-argument guarantee, end to end), and mysql:5.7, which gets no argument.
# Usage: docker_check.sh [server-image:xtrabackup-image ...]   default: the four above (the space case always runs).
set -u
command -v docker >/dev/null || { echo "FAIL: docker is required"; exit 2; }
cd "$(dirname "$0")/../../../.." || exit 2
SCRIPT=share/scripts/dbjobs_new.sh
P=xbu_$$; PW=pw; fail=0; ran=0
PAIRS=("$@"); [ ${#PAIRS[@]} -gt 0 ] || PAIRS=(mysql:8.0:percona/percona-xtrabackup:8.0 mysql:8.4:percona/percona-xtrabackup:8.4 percona/percona-server:8.0:percona/percona-xtrabackup:8.0 percona/percona-server:8.4:percona/percona-xtrabackup:8.4)
ok() { echo "  ok:   $*"; }
bad() { echo "  FAIL: $*"; fail=1; }
drop() { docker rm -f "${P}_db" >/dev/null 2>&1; docker volume rm -f "${P}_data" "${P}_conf" >/dev/null 2>&1; }
drop_all() { drop; docker volume rm -f "${P}_undo" >/dev/null 2>&1; }
LVL_WARN=WARN; send_lines_to_api() { echo "  (warning posted: $1)"; }
eval "$(sed -n '/^xtrabackup_undo_args() {/,/^}/p' "$SCRIPT")"
CLIENT=$(mktemp); trap 'drop_all; rm -f "$CLIENT"' EXIT
printf '#!/bin/sh\nexec docker exec -i "%s_db" mysql -uroot -p%s "$@" 2>/dev/null\n' "$P" "$PW" >"$CLIENT"; chmod +x "$CLIENT"; BINARY_CLIENT=$CLIENT
wait_up() { local up=0; for _ in $(seq 1 120); do docker exec "${P}_db" mysql -uroot -p$PW -h127.0.0.1 -e 'select 1' >/dev/null 2>&1 && { up=1; break; }; sleep 2; done; [ $up = 1 ]; }

# xb_run <xtrabackup-image> [argument...]: the job's backup, then unpack and prepare; prints rc / log tail / prepare result
xb_run() {
  local img=$1; shift
  docker run --rm --user 0:0 --network "container:${P}_db" --volumes-from "${P}_db" -v "${P}_conf:/xbconf:ro" -w /docker-entrypoint-initdb.d \
    --entrypoint sh "$img" -c '
      rm -rf /tmp/x /tmp/s.xb /tmp/b.log
      xtrabackup --defaults-file=/xbconf/undo.cnf --backup "$@" -uroot -p'"$PW"' -H127.0.0.1 -P3306 --stream=xbstream --target-dir=/tmp/ 2>/tmp/b.log >/tmp/s.xb
      rc=$?; echo "backup_rc=$rc"; echo "completed_ok=$(grep -c "completed OK" /tmp/b.log)"; echo "log_tail_begin"; tail -6 /tmp/b.log; echo "log_tail_end"
      if [ $rc = 0 ]; then
        mkdir /tmp/x && xbstream -x -C /tmp/x </tmp/s.xb; echo "unpack_rc=$?"
        echo "backup_my_undo=$(grep -c "^innodb[-_]undo[-_]directory" /tmp/x/backup-my.cnf)"
        echo "backup_my_undo_values=$(sed -n "s/^innodb[-_]undo[-_]directory[[:space:]]*=[[:space:]]*//p" /tmp/x/backup-my.cnf | tr "\n" "|")"
        xtrabackup --prepare --target-dir=/tmp/x >/tmp/p.log 2>&1; echo "prepare_rc=$?"
      fi' _ "$@" 2>&1
}

# run_case <label> <server-image> <xtrabackup-image> <conf-dir-in-server> <version-group> <undo-value-in-conf> [extra docker run args for the server...]
run_case() {
  local label=$1 srv=$2 xbimg=$3 confdir=$4 grp=$5 undoval=$6; shift 6
  ran=$((ran + 1)); echo "=== $label: $srv with $xbimg (version group [$grp], undo = $undoval)"
  drop; docker volume create "${P}_data" >/dev/null; docker volume create "${P}_conf" >/dev/null
  docker run --rm -v "${P}_conf:/c" --entrypoint sh alpine -c "cat >/c/undo.cnf <<'CNF'
[mysqld]
loose_innodb_undo_directory = ./.system/innodb/undo
[$grp]
loose_innodb_undo_directory = $undoval
CNF"
  docker run -d --name "${P}_db" -e MYSQL_ROOT_PASSWORD=$PW -v "${P}_data:/var/lib/mysql" -v "${P}_conf:$confdir:ro" "$@" "$srv" >/dev/null
  wait_up || { bad "$srv did not start: $(docker logs "${P}_db" 2>&1 | tail -2 | tr '\n' ' ' | cut -c1-160)"; return; }
  docker exec "${P}_db" mysql -uroot -p$PW -h127.0.0.1 -e 'create database d; create table d.t(a int primary key, b varchar(20)); insert into d.t values (1,"x"),(2,"y")' 2>&1 | grep -v Warning
  local undo; undo=$(docker exec "${P}_db" mysql -uroot -p$PW -N -B -e 'select @@innodb_undo_directory' 2>/dev/null)
  [ "$undo" = "${undoval//\"/}" ] && ok "the server reports innodb_undo_directory = '$undo'" || { bad "innodb_undo_directory is '$undo', expected '${undoval//\"/}'"; return; }

  out=$(xb_run "$xbimg")
  rc=$(echo "$out" | sed -n 's/^backup_rc=//p')
  if [ "$rc" != 0 ] && echo "$out" | grep -q -E 'Cannot create .*/undo_[0-9]+ because .*already|no existing undo tablespaces found'; then ok "without the option: exit $rc on the undo path ($(echo "$out" | grep -E 'Cannot create|no existing undo' | head -1 | sed 's/^[^ ]* [0-9] //' | cut -c1-100))"
  else bad "without the option: exit '$rc', and no undo path error was found: $(echo "$out" | sed -n '/log_tail_begin/,/log_tail_end/p' | tail -3 | tr '\n' ' ' | cut -c1-200)"; fi

  xtrabackup_undo_args
  expected="--innodb-undo-directory=${undoval//\"/}"
  [ "${#XB_UNDO_ARGS[@]}" = 1 ] && [ "${XB_UNDO_ARGS[0]}" = "$expected" ] && ok "xtrabackup_undo_args asked to the live server: one argument '${XB_UNDO_ARGS[0]}'" || { bad "xtrabackup_undo_args gave ${#XB_UNDO_ARGS[@]} argument(s): '${XB_UNDO_ARGS[*]}', want one '$expected'"; return; }

  out=$(xb_run "$xbimg" "${XB_UNDO_ARGS[@]}")
  g() { echo "$out" | sed -n "s/^$1=//p"; }
  [ "$(g backup_rc)" = 0 ] && [ "$(g completed_ok)" -ge 1 ] 2>/dev/null && ok "with the argument: exit 0 and 'completed OK'" || bad "with the argument: exit '$(g backup_rc)', completed_ok '$(g completed_ok)': $(echo "$out" | sed -n '/log_tail_begin/,/log_tail_end/p' | tail -3 | tr '\n' ' ' | cut -c1-200)"
  [ "$(g unpack_rc)" = 0 ] && ok "the stream unpacks with xbstream" || bad "unpack_rc '$(g unpack_rc)'"
  # the value the server reported must be the one kept in the backup metadata, as it is (relative stays relative), and
  # every entry must be that value: a stale ./.system/... entry from [mysqld] would be a different configuration
  want="${expected#--innodb-undo-directory=}"; vals=$(g backup_my_undo_values)
  [ -n "$vals" ] && [ "$vals" = "$want|" ] && ok "backup-my.cnf keeps exactly the reported value: innodb_undo_directory=$want" || bad "backup-my.cnf undo entries are '$vals', expected only '$want'"
  [ "$(g prepare_rc)" = 0 ] && ok "xtrabackup --prepare on that backup exits 0" || bad "xtrabackup --prepare exit '$(g prepare_rc)'"
  docker rm -f "${P}_db" >/dev/null 2>&1
}

confdir_of() { case "$1" in percona/percona-server*) echo /etc/my.cnf.d ;; *) echo /etc/mysql/conf.d ;; esac; }
for pair in "${PAIRS[@]}"; do
  XB="percona/percona-xtrabackup:${pair##*percona-xtrabackup:}"; SRV="${pair%:percona/percona-xtrabackup:*}"
  case "$SRV" in *:8.0*) grp=mysqld-8.0 ;; *:8.4*) grp=mysqld-8.4 ;; *) grp=mysqld-8.0 ;; esac
  run_case "stock layout" "$SRV" "$XB" "$(confdir_of "$SRV")" "$grp" "./"
done

# an undo directory whose path holds a space, on a volume: xtrabackup must receive it as ONE argument
drop_all; docker volume create "${P}_undo" >/dev/null
docker run --rm -v "${P}_undo:/u" --entrypoint sh alpine -c 'chown 999:999 /u'
run_case "undo path with a space" mysql:8.4 percona/percona-xtrabackup:8.4 /etc/mysql/conf.d mysqld-8.4 '"/undo dir"' -v "${P}_undo:/undo dir"

echo "=== a series that xtrabackup_undo_args does not cover gets no argument (mysql:5.7)"
ran=$((ran + 1)); drop
docker run -d --name "${P}_db" -e MYSQL_ROOT_PASSWORD=$PW mysql:5.7 >/dev/null
if wait_up; then xtrabackup_undo_args; [ "${#XB_UNDO_ARGS[@]}" = 0 ] && ok "no argument for $(docker exec "${P}_db" mysql -uroot -p$PW -N -B -e 'select @@version' 2>/dev/null)" || bad "5.7 got '${XB_UNDO_ARGS[*]}'"; else bad "mysql:5.7 did not start"; fi
echo "cases: $ran"; [ $fail = 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"; exit $fail
