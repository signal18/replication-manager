#!/bin/bash
# Usage: run_mysql.sh <server tag> <xtrabackup tag> [scenario] [dbjobs_new.sh]
# Server and xtrabackup run in two containers sharing the datadir volume and
# network namespace (xtrabackup is not shipped in the server images).
HERE="$(cd "$(dirname "$0")" && pwd)"
SRV="$1"; PXB="$2"; SCEN="${3:-keep-schema}"; SCRIPT="${4:-$HERE/../../dbjobs_new.sh}"
mkdir -p "$HERE/.work"
WORK="$(mktemp -d "$HERE/.work/mysql.XXXX")"; mkdir -p "$WORK/out"; chmod 777 "$WORK/out"
awk '/^db_owner\(\) \{/{d=1} d{print} d&&/^}/{d=0} /^pr_log\(\) \{/{p=1} /^jobsCheck\(\) \{/{p=0} p' "$SCRIPT" >"$WORK/functions.sh"
cp "$HERE/mysql_inside.sh" "$HERE/scenarios.sh" "$WORK/"
ID="prm$$"; VD="${ID}_data"; VB="${ID}_bk"
cleanup() { docker stop "$ID" >/dev/null 2>&1; docker volume rm "$VD" "$VB" >/dev/null 2>&1; }
trap cleanup EXIT
docker volume create "$VD" >/dev/null; docker volume create "$VB" >/dev/null
docker run -d --rm --name "$ID" -e MYSQL_ROOT_PASSWORD=rootpw -v "$VD:/var/lib/mysql" -v "$VB:/tmp/bk" \
    -v "$WORK:/prtest:ro" -v "$WORK/out:/prtest-out" "percona/percona-server:$SRV" >/dev/null
for i in $(seq 1 90); do docker exec "$ID" mysql -uroot -prootpw -h127.0.0.1 -e "select 1" >/dev/null 2>&1 && break; sleep 1; done
echo "===== percona-server:$SRV ($(docker exec "$ID" mysql -uroot -prootpw -h127.0.0.1 -N -e 'select version()' 2>/dev/null)) + xtrabackup:$PXB scenario=$SCEN"
docker exec -u root "$ID" bash /prtest/mysql_inside.sh setup "$SCEN"
docker exec -u root "$ID" bash -c "rm -rf /tmp/bk/b && mkdir -p /tmp/bk/b && chmod 777 /tmp/bk /tmp/bk/b"
docker run --rm --user root --network "container:$ID" -v "$VD:/var/lib/mysql" -v "$VB:/tmp/bk" "percona/percona-xtrabackup:$PXB" \
    bash -c "xtrabackup --backup --user=root --password=rootpw --host=127.0.0.1 --port=3306 --target-dir=/tmp/bk/b >/tmp/bk/backup.log 2>&1; echo backup_rc=\$?; if [ \"$SCEN\" = prepare-failed ]; then echo \"prepare not run by the test\" >/tmp/bk/prepare.log; else xtrabackup --prepare --export --target-dir=/tmp/bk/b >/tmp/bk/prepare.log 2>&1; echo prepare_rc=\$?; fi; grep -c 'completed OK' /tmp/bk/prepare.log; true" | tr '\n' ' '; echo
docker exec -u root "$ID" bash -c 'chown -R mysql:mysql /tmp/bk/b; tail -2 /tmp/bk/backup.log | grep -v "^$" | tail -1'
if [[ "$SCEN" == "exportcheck" ]]; then
    docker exec -u root "$ID" bash /prtest/mysql_inside.sh exportcheck
else
    # Bounded: a hung restore must not stall the whole run.
    timeout 900 docker exec -u root "$ID" bash /prtest/mysql_inside.sh restore "$SCEN" || echo "RESTORE TIMED OUT OR FAILED (rc=$?)"
fi
echo "(full log: $WORK/out/reseed.out)"
