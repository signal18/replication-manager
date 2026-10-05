#!/usr/bin/env bash
# Real Docker check of the database runtime UID/GID (prov-db-run-as-uid and prov-db-volume-uid).
#
# What replication-manager renders for a database (OpenSVC: `--user UID:GID` on the container from
# prov-db-run-as-uid, the data volume and /run/mysqld owned by prov-db-volume-uid through the bootstrap;
# Kubernetes: the same as securityContext and init chown) only works if the official images really
# run and restart under that identity. The two settings are independent. This script checks exactly
# that, on real images:
#
#   per image and identity it creates the data and run volumes owned by the chown pair, starts the
#   server with the run-as user, checks the mysqld process identity, that every datadir file
#   belongs to exactly the expected UID:GID pair and that the PID file in /run/mysqld belongs to the
#   process, writes a row, stops and starts the same container again (a second boot on the same
#   datadir, as a restart does) and checks that the row and all of this survive. With root only the
#   UID of the datadir files is checked: the MySQL entrypoint chowns the owner to mysql and leaves
#   the group of part of the tree at 0.
#
# Identities (the empty-setting behavior is the legacy one; Percona Server images are recognized by
# name and own the volume 1001:1001 when prov-db-volume-uid is empty):
#   default   both settings empty. MariaDB and MySQL images: volumes owned by 999:999, `--user mysql`
#             for MySQL images, no `--user` for MariaDB images (their entrypoint starts as root and
#             drops to 999:999 itself). Percona Server images: volumes owned by 1001:1001 and no
#             `--user` (the image's own 1001).
#   custom    run-as 1234 and chown 1234 (GID defaults to the UID), ids with no passwd entry
#   mixed     run-as and chown both the default UID with another GID (the UID:GID form)
#   root      run-as 0 and chown 0: root, with a root-owned volume. MariaDB and MySQL entrypoints
#             start as root and drop to 999:999 themselves; Percona Server's mysqld refuses to run
#             as root (MY-010123), which is the expected result for it.
#   rootkeep  run-as 0 and chown left empty: root, with the legacy volume owner (the case where only
#             the way the container is started changes). Same expected results as root.
#
# Percona Server images are built for 1001: under another UID their entrypoint cannot start the
# telemetry agent (`Permission denied`) and the server logs it; this is reported as info, the
# server itself runs.
#
# Default images: every LTS line of cluster/logplugin/plugins/plugin-score-lts/lts-versions.json
# (MariaDB, MySQL, Percona Server). The run is sequential on purpose (one server at a time) and
# takes a while: about a minute per image and identity.
#
# Usage: docker_check.sh [image ...]          IDS="default custom" docker_check.sh mysql:8.4
# Control: CONTROL_VOLUME_OWNER=999:999 owns the volumes by that pair whatever `--user` says, which
#          is what OpenSVC rendered for Percona Server before the image's own 1001 was the
#          default (a 1001 process on a 999 datadir): the check must FAIL for percona with the
#          default identity.
set -u
for tool in docker python3; do command -v "$tool" >/dev/null || { echo "FAIL: $tool is required"; exit 2; }; done
cd "$(dirname "$0")/../../../.." || exit 2
LTS=cluster/logplugin/plugins/plugin-score-lts/lts-versions.json
IDS=${IDS:-"default custom mixed root rootkeep"}
VOLUME_OWNER=${CONTROL_VOLUME_OWNER:-}
PW=pw; fail=0; ran=0; P=uidgid_$$

IMAGES=("$@")
if [ ${#IMAGES[@]} -eq 0 ]; then
  while IFS= read -r img; do IMAGES+=("$img"); done < <(python3 - "$LTS" <<'PY'
import json, sys
lts = json.load(open(sys.argv[1]))["lts"]
prefix = {"mariadb": "mariadb", "mysql": "mysql", "percona": "percona/percona-server"}
for flavor, versions in lts.items():
    for v in versions:
        print("%s:%s" % (prefix[flavor], v))
PY
)
  [ ${#IMAGES[@]} -gt 0 ] || { echo "FAIL: no image found in $LTS"; exit 2; }
fi

C=${P}_db
drop_case() { docker rm -f "$C" >/dev/null 2>&1; docker volume rm -f "${P}_data" "${P}_run" >/dev/null 2>&1; }
trap drop_case EXIT

# The queries only probe the identity, so the MariaDB client does not use TLS: MariaDB 11.x generates a
# self-signed server certificate at every start, and a client that verifies it a moment too early
# fails with "TLS/SSL error: certificate is not yet valid" (seen after a restart, on a plain query).
q() { docker exec "$C" sh -c 'if command -v mariadb >/dev/null; then c=mariadb; o=--skip-ssl; else c=mysql; o=; fi; exec "$c" $o -uroot -p"$0" -N -B -e "$1"' "$PW" "$1" 2>&1 | grep -v -E 'jemalloc|Using a password'; }
# Ready = the final server answers. The image entrypoints first run a temporary server with
# --skip-networking to initialize the datadir; it already answers `select 1` and is shut down a few
# seconds later, taking any statement sent to it along, so wait for skip_networking = 0.
wait_up() { for _ in $(seq 1 90); do sleep 2; docker inspect -f '{{.State.Running}}' "$C" 2>/dev/null | grep -q true || return 1; q 'select @@skip_networking' | grep -q '^0$' && return 0; done; return 1; }
# uid:gid of the mysqld/mariadbd process, read from /proc inside the container
proc_pair() { docker exec "$C" sh -c 'for d in /proc/[0-9]*; do n=$(cat $d/comm 2>/dev/null); case "$n" in mysqld|mariadbd) awk "/^Uid:/{u=\$2}/^Gid:/{g=\$2}END{print u\":\"g}" $d/status; break;; esac; done' 2>/dev/null | grep -E '^[0-9]+:[0-9]+$' | head -1; }
# distinct owner UIDs, and distinct UID:GID pairs, of the datadir contents
data_uids() { docker exec "$C" sh -c 'find /var/lib/mysql -xdev -printf "%U\n" | sort -u | tr "\n" " "' 2>/dev/null | sed 's/ *$//'; }
data_pairs() { docker exec "$C" sh -c 'find /var/lib/mysql -xdev -printf "%U:%G\n" | sort -u | tr "\n" " "' 2>/dev/null | sed 's/ *$//'; }
# owner UID of the PID file: the server creates it in /run/mysqld, the volume the identity must be able to write
pid_uid() { docker exec "$C" sh -c 'stat -c %u /run/mysqld/mysqld.pid' 2>/dev/null | grep -E '^[0-9]+$' | head -1; }
# check_owners <label-suffix>: sets ok/msg
check_owners() {
  local du dp pu
  du=$(data_uids); [ "$du" = "$exp_u" ] || { ok=0; msg="$msg datadir owner uids$1=[$du] want [$exp_u];"; }
  if [ "$id" != root ] && [ "$id" != rootkeep ]; then
    dp=$(data_pairs); [ "$dp" = "$exp_u:$exp_g" ] || { ok=0; msg="$msg datadir owner pairs$1=[$dp] want [$exp_u:$exp_g];"; }
  fi
  pu=$(pid_uid); [ "$pu" = "$exp_u" ] || { ok=0; msg="$msg pid file owner$1=[$pu] want [$exp_u];"; }
}
# Fresh volumes owned by the pair, as the OpenSVC bootstrap / Kubernetes init container do, then the
# server. A hidden marker keeps each volume non-empty: Docker copies the image directory's owner into
# a volume that is empty when it is mounted, which would silently undo the chown (and hide the very
# bug this script looks for). mysqld ignores hidden entries in its datadir.
launch_case() { # image volume-owner label [user]   (user: the --user value; none when absent)
  drop_case
  docker volume create "${P}_data" >/dev/null; docker volume create "${P}_run" >/dev/null
  docker run --rm --user 0:0 --entrypoint sh -v "${P}_data:/var/lib/mysql" -v "${P}_run:/run/mysqld" "$1" \
    -c 'touch /var/lib/mysql/.uidgid /run/mysqld/.uidgid && chown -R "$0" /var/lib/mysql /run/mysqld' "${VOLUME_OWNER:-$2}" >/dev/null 2>&1 \
    || { echo "  FAIL: [$3] cannot prepare the volumes"; return 1; }
  docker run -d --name "$C" ${4+--user "$4"} -e MYSQL_ROOT_PASSWORD=$PW -e MARIADB_ROOT_PASSWORD=$PW \
    -v "${P}_data:/var/lib/mysql" -v "${P}_run:/run/mysqld" "$1" >/dev/null 2>&1
}

for img in "${IMAGES[@]}"; do
  echo "=== $img"
  if [[ $img == percona* ]]; then def=1001; alt=999; else def=999; alt=1001; fi
  for id in $IDS; do
    # u:g = what the settings ask for; vol = the owner of the volumes (prov-db-volume-uid, or the legacy
    # one); runuser = the --user value (prov-db-run-as-uid, or the legacy rule: the "mysql" account by
    # name for MySQL images, none otherwise)
    case $id in
      default)  u=$def; g=$def; vol=$def:$def ;;
      custom)   u=1234; g=1234; vol=1234:1234 ;;
      mixed)    u=$def; g=$alt; vol=$u:$g ;;
      root)     u=0;    g=0;    vol=0:0 ;;
      rootkeep) u=0;    g=0;    vol=$def:$def ;;
      *) echo "FAIL: unknown identity $id"; exit 2 ;;
    esac
    runuser="$u:$g"
    if [ "$id" = default ]; then
      if [[ $img == *mysql* && $img != percona* ]]; then runuser=mysql; else unset runuser; fi
    fi
    # expected process identity: the pair, except that MariaDB/MySQL drop root to 999:999
    if [ "$id" = root ] || [ "$id" = rootkeep ]; then exp_u=999; exp_g=999; else exp_u=$u; exp_g=$g; fi
    ran=$((ran + 1)); label="$id $u:$g"
    launch_case "$img" "$vol" "$label" ${runuser+"$runuser"} || { fail=1; continue; }
    up=0; wait_up && up=1
    # The MariaDB 11.x entrypoint sometimes aborts while it initializes ("TLS/SSL error: certificate
    # is not yet valid", on its own self-signed certificate, with its own client, which this script
    # cannot configure): a timing problem of the image, seen in 5 of 36 MariaDB 11.4 / 11.8 / 12.3
    # cases over three runs, whatever the identity and in bursts (the same image and identity pass
    # on the next try). Retry such a start once, and say so.
    if [ $up = 0 ] && docker logs "$C" 2>&1 | grep -q 'certificate is not yet valid'; then
      echo "  note: [$label] retrying once: the entrypoint hit 'TLS/SSL error: certificate is not yet valid' while initializing (the image's own timing race, not the identity)"
      launch_case "$img" "$vol" "$label" ${runuser+"$runuser"} || { fail=1; continue; }
      up=0; wait_up && up=1
    fi
    if [ $up = 0 ]; then
      reason=$(docker logs "$C" 2>&1 | grep -iE 'MY-010123|as root|ERROR' | grep -v jemalloc | head -1 | cut -c1-150)
      if { [ "$id" = root ] || [ "$id" = rootkeep ]; } && [[ $img == percona* ]] && docker logs "$C" 2>&1 | grep -q 'MY-010123'; then
        echo "  ok:   [$label] refused to run as root, as expected (MY-010123)"
      else
        echo "  FAIL: [$label] did not start: $reason"; fail=1
      fi
      continue
    fi
    if { [ "$id" = root ] || [ "$id" = rootkeep ]; } && [[ $img == percona* ]]; then
      echo "  FAIL: [$label] Percona Server ran as root; it must refuse (MY-010123)"; fail=1; continue
    fi
    ok=1; msg=""
    pp=$(proc_pair); [ "$pp" = "$exp_u:$exp_g" ] || { ok=0; msg="$msg process=$pp want $exp_u:$exp_g;"; }
    check_owners ""
    q 'create database t; create table t.x(i int primary key); insert into t.x values (7)' >/dev/null
    [ "$(q 'select i from t.x')" = "7" ] || { ok=0; msg="$msg the row could not be written;"; }
    docker stop -t 30 "$C" >/dev/null 2>&1; docker start "$C" >/dev/null 2>&1
    if wait_up; then
      rows=$(q 'select i from t.x'); [ "$rows" = "7" ] || { ok=0; msg="$msg row after restart=[$rows];"; }
      pp2=$(proc_pair); [ "$pp2" = "$exp_u:$exp_g" ] || { ok=0; msg="$msg process after restart=$pp2;"; }
      check_owners " after restart"
    else
      ok=0; msg="$msg did not restart: $(docker logs "$C" 2>&1 | grep -iE 'ERROR' | grep -v jemalloc | tail -1 | cut -c1-120);"
    fi
    if [ $ok = 1 ]; then
      note=""; { [ "$id" = root ] || [ "$id" = rootkeep ]; } && note=" (entrypoint dropped root to $exp_u:$exp_g)"
      echo "  ok:   [$label] process $pp, datadir pairs $(data_pairs), pid file uid $(pid_uid), row kept across a restart$note"
    else
      echo "  FAIL: [$label]$msg"; fail=1
    fi
    if [[ $img == percona* ]] && [ "$u" != 1001 ]; then
      n=$(docker logs "$C" 2>&1 | grep -c 'telemetry-agent-supervisor.sh: Permission denied')
      [ "$n" -gt 0 ] && echo "  info: [$label] Percona Server image, UID $u is not its own 1001: telemetry agent denied $n time(s) in the entrypoint (the server runs)"
    fi
  done
done
drop_case
echo "cases: $ran"
[ $fail -eq 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"; exit $fail
