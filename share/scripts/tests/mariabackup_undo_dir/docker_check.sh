#!/usr/bin/env bash
# Real MariaDB check of pr_stage_undo_dir() of dbjobs_new.sh: a mariadb-backup taken from a server whose
# innodb_undo_directory is not where the backup is extracted must be prepared ("--prepare --export", the command
# of reseedmariabackup/flashbackmariabackup), must yield its .cfg files, and must leave the original directory alone.
#
# For each MariaDB version and each layout of the source server (default, absolute undo directory, relative
# undo directory, absolute directory with no undo tablespace) a backup is streamed and unpacked in an ISOLATED
# restore container that runs no database. Where the layout names an absolute directory, that directory EXISTS in
# the restore container and holds a dummy copy of the backup's own undo tablespaces (a writable path of the
# restore host, as the live server's undo directory is). It sits on a tmpfs mounted strictatime, so its files'
# checksums AND access times show whether anything opened it. Each layout runs twice:
#   plain  the way the job ran before: "--prepare --export" on the unpacked backup, nothing else;
#   fixed  the way it runs now: pr_stage_undo_dir (extracted from the script), then the same prepare.
# Asserts for the fixed run: the prepare exits 0 and prints "completed OK!"; the .cfg files of the tables exist;
# the original directory is byte-identical AND was never read (access times); the temporary definition server
# (pr_export_definitions, from the script) starts on the prepared backup and reads the tables' definitions.
# Asserts for the plain run, so the check can see the bug it guards against: with the original directory reachable the
# prepare "succeeds" (exit 0) and rewrites the dummy undo files (critical; MariaDB 10.6 only sometimes opens them); with it absent, or relative, the prepare fails the
# way lab reseeds did (exit 1, no .cfg, no "completed OK!"). Also: a backup with an undo tablespace missing, or fewer
# files than innodb_undo_tablespaces, is refused by the staging, no prepare is run and the directory is untouched.
# Usage: docker_check.sh [mariadb-version ...]   default: 10.6 10.11 11.4 11.8
set -u
command -v docker >/dev/null || { echo "FAIL: docker is required"; exit 2; }
cd "$(dirname "$0")/../../../.." || exit 2
SCRIPT=share/scripts/dbjobs_new.sh
P=mud_$$; PW=pw; fail=0; ran=0
VERSIONS=("$@"); [ ${#VERSIONS[@]} -gt 0 ] || VERSIONS=(10.6 10.11 11.4 11.8)
WORK=$(mktemp -d); chmod 755 "$WORK"
ok() { echo "  ok:   $*"; }
bad() { echo "  FAIL: $*"; fail=1; }
note() { echo "  note: $*"; }
cleanup() { docker rm -f $(docker ps -aq --filter "name=${P}_") >/dev/null 2>&1; docker volume rm -f $(docker volume ls -q --filter "name=${P}_") >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT
# the functions of the restore, cut out of the script (as the partial restore harness does)
awk '/^db_owner\(\) \{/{d=1} d{print} d&&/^}/{d=0} /^pr_log\(\) \{/{p=1} /^jobsCheck\(\) \{/{p=0} p' "$SCRIPT" >"$WORK/functions.sh"
grep -q '^pr_stage_undo_dir() {' "$WORK/functions.sh" || { echo "FAIL: pr_stage_undo_dir is not in $SCRIPT"; exit 2; }

# ---- inside the restore container -------------------------------------------------------------------------------
cat >"$WORK/inside.sh" <<'INSIDE'
#!/usr/bin/env bash
# env: XB (the backup stream) MODE (plain|fixed) CASE ORIG (the original undo directory, "" when none exists on this host) TAMPER (""|missing|count5)
LVL_ERROR=ERROR LVL_WARN=WARN LVL_INFO=INFO LVL_DEBUG=DEBUG
BIN=$(command -v mariadb-backup || command -v mariabackup); MBS=$(command -v mbstream || command -v mariadb-bstream)
DATADIR=/var/lib/mysql; BACKUPDIR=$DATADIR/.system/backup
mkdir -p "$DATADIR/.system"; rm -rf "$BACKUPDIR"; mkdir -p "$BACKUPDIR"
$MBS -x -C "$BACKUPDIR" <"$XB" || { echo "extract_failed=1"; exit 0; }
chmod -R a+rX "$BACKUPDIR"
sum() { (cd "$1" 2>/dev/null && sha256sum undo* 2>/dev/null | cut -c1-16 | tr '\n' ' '); }
atm() { (cd "$1" 2>/dev/null && stat -c '%X' undo* 2>/dev/null | tr '\n' ' '); }
if [[ -n "$ORIG" ]]; then
    # a writable original directory on this host, holding valid undo tablespaces (copies of the backup's own)
    rm -rf "${ORIG:?}"/*; cp -p "$BACKUPDIR"/undo[0-9]* "$ORIG"/ 2>/dev/null
    sleep 2
    S0=$(sum "$ORIG"); A0=$(atm "$ORIG")   # checksum first: reading the files for it changes their access times
fi
echo "orig_files=$( [[ -n "$ORIG" ]] && ls "$ORIG" | tr '\n' ' ')"
echo "cnf_undo=$(sed -n 's/^innodb_undo_directory=//p' "$BACKUPDIR/backup-my.cnf") tablespaces=$(sed -n 's/^innodb_undo_tablespaces=//p' "$BACKUPDIR/backup-my.cnf")"
case "$TAMPER" in
missing) rm -f "$BACKUPDIR/undo002" ;;
count5) sed -i 's/^innodb_undo_tablespaces=.*/innodb_undo_tablespaces=5/' "$BACKUPDIR/backup-my.cnf" ;;
esac
sha_cnf0=$(sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-16)
job=reseedmariabackup LOG_DIR=/tmp/jobs; rm -rf "$LOG_DIR"; mkdir -p "$LOG_DIR"; PR_LOG="$LOG_DIR/reseed.out"; : >"$PR_LOG"
USER=root PASSWORD=pw MYSQL_SERVER=127.0.0.1 MYSQL_PORT=3306
DB_CONN_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"
BINARY_CLIENT="mariadb $DB_CONN_PARAMETERS"
JOBS_MODE=api ID="" PR_STATUS=0
send_lines_to_api() { echo "api: $1" >>/tmp/api.log; }
source /work/functions.sh
stage_rc=-
if [[ "$MODE" == fixed ]]; then pr_stage_undo_dir "$PR_LOG"; stage_rc=$?; fi
echo "stage_rc=$stage_rc"
echo "cnf_changed=$([[ "$(sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-16)" == "$sha_cnf0" ]] && echo 0 || echo 1) cnf_undo_after=$(sed -n 's/^innodb_undo_directory=//p' "$BACKUPDIR/backup-my.cnf")"
if [[ "$MODE" == plain || "$stage_rc" == 0 ]]; then
    $BIN --prepare --export --target-dir=$BACKUPDIR >>"$PR_LOG" 2>&1; echo "prepare_rc=$?"
else
    echo "prepare_rc=not_run"
fi
echo "banner=$(grep -c 'completed OK!' "$PR_LOG")"
echo "backup_type=$(sed -n 's/^backup_type = //p' $BACKUPDIR/mariadb_backup_checkpoints 2>/dev/null)"
echo "cfg_t=$([ -f $BACKUPDIR/d/t.cfg ] && echo 1 || echo 0) cfg_u=$([ -f $BACKUPDIR/d/u.cfg ] && echo 1 || echo 0) ibd_count=$(find $BACKUPDIR -name '*.ibd' | wc -l) cfg_count=$(find $BACKUPDIR -name '*.cfg' | wc -l)"
if [[ -n "$ORIG" ]]; then
    A1=$(atm "$ORIG"); S1=$(sum "$ORIG")   # access times first at the end: the checksum reads the files
    echo "orig_checksums_unchanged=$([[ "$S1" == "$S0" ]] && echo 1 || echo 0) orig_never_read=$([[ "$A1" == "$A0" ]] && echo 1 || echo 0)"
    A0=$(atm "$ORIG")   # the checksum above read the files: the access times are taken again for the check that follows
fi
grep -E 'ERROR|Failed to open|Unknown/unsupported' "$PR_LOG" | head -4 | sed 's/^/log: /' | cut -c1-200
if [[ "$MODE" == fixed && "$stage_rc" == 0 && -z "$TAMPER" ]]; then
    isr=0; [[ "$(id -u)" == 0 ]] && isr=1   # set by partialRestore for pr_export_definitions
    chown -R mysql:mysql "$BACKUPDIR"          # partialRestore does the same before it (the temporary server runs as mysql)
    pr_export_definitions; echo "defs_rc=$?"
    echo "defs_t=$([ -s "$PR_DEFS/d/t.sql" ] && grep -c 'CREATE TABLE' "$PR_DEFS/d/t.sql" || echo 0) defs_u=$([ -s "$PR_DEFS/d/u.sql" ] && grep -c 'CREATE TABLE' "$PR_DEFS/d/u.sql" || echo 0)"
    if [[ -n "$ORIG" ]]; then
        A2=$(atm "$ORIG"); S2=$(sum "$ORIG")
        echo "orig_after_defs_unchanged=$([[ "$S2" == "$S0" ]] && echo 1 || echo 0) orig_after_defs_never_read=$([[ "$A2" == "$A0" ]] && echo 1 || echo 0)"
    fi
    grep -E 'ERROR' "$PR_LOG" | tail -2 | sed 's/^/log2: /' | cut -c1-200
fi
echo "refusal=$(grep -oE 'undo[0-9]{3} is (missing|empty|a symbolic link)' "$PR_LOG" | head -1 | tr ' ' '_')"
echo "reason_log=$(grep -E 'Backup configuration|ERROR: the backup expects|ERROR: .* symbolic' "$PR_LOG" | head -2 | cut -c1-230 | tr '\n' '|')"
INSIDE
chmod 755 "$WORK/inside.sh"

g() { echo "$out" | sed -n "s/.*\b$1=\([^ ]*\).*/\1/p" | head -1; }

# ---- the source server of one layout -> the backup stream in $WORK/<case>.xb ------------------------------------
# <image> <case> <server args> [tmpfs dir]
make_backup() {
    local img=$1 c=$2 args=$3 tdir=${4:-} src="${P}_src" ts="" vol=""
    docker rm -f "$src" >/dev/null 2>&1
    [ -n "$tdir" ] && ts="--tmpfs $tdir:rw,uid=999,gid=999,mode=0755"
    if [ "$c" = rel ]; then
        vol="${P}_relvol"; docker volume rm -f "$vol" >/dev/null 2>&1; docker volume create "$vol" >/dev/null
        docker run --rm -v "$vol:/var/lib/mysql" --entrypoint sh "$img" -c 'mkdir -p /var/lib/mysql/undosub && chown -R mysql:mysql /var/lib/mysql' >/dev/null
        ts="-v $vol:/var/lib/mysql"
    fi
    docker run -d --name "$src" $ts -e MARIADB_ROOT_PASSWORD=$PW "$img" --log-bin=mysql-bin --server-id=1 $args >/dev/null
    local up=0 i
    for i in $(seq 1 90); do
        docker exec "$src" mariadb -uroot -p$PW -h127.0.0.1 -e 'select 1' >/dev/null 2>&1 && { up=1; break; }
        [ "$(docker inspect -f '{{.State.Running}}' "$src")" = true ] || break
        sleep 2
    done
    [ $up = 1 ] || { docker rm -f "$src" >/dev/null 2>&1; return 1; }
    docker exec "$src" mariadb -uroot -p$PW -h127.0.0.1 -e 'create database d; create table d.t(a int primary key, b varchar(40)) engine=innodb; insert into d.t values (1,"one"),(2,"two"),(3,"three"); create table d.u(a int primary key) engine=innodb; insert into d.u values (9)' >/dev/null 2>&1
    docker exec "$src" sh -c "B=\$(command -v mariadb-backup || command -v mariabackup); \$B --backup --user=root --password=$PW --host=127.0.0.1 --stream=xbstream --target-dir=/tmp/t 2>/tmp/be >/tmp/b.xb; grep -c 'completed OK' /tmp/be" >"$WORK/backup_ok" 2>&1
    docker exec "$src" mariadb -uroot -p$PW -h127.0.0.1 -N -e 'select concat(@@innodb_undo_directory," / ",@@innodb_undo_tablespaces)' 2>/dev/null >"$WORK/server_undo"
    docker cp "$src:/tmp/b.xb" "$WORK/$c.xb" >/dev/null 2>&1
    docker rm -f "$src" >/dev/null 2>&1; [ -n "$vol" ] && docker volume rm -f "$vol" >/dev/null 2>&1
    [ "$(tr -d '\n ' <"$WORK/backup_ok")" = 1 ] && [ -s "$WORK/$c.xb" ]
}

# ---- one restore: a fresh isolated container, no database -----------------------------------------------------------
# <image> <case> <mode> <orig dir or ""> [tamper]  -> $out
restore() {
    local img=$1 c=$2 mode=$3 orig=$4 tamper=${5:-} h="${P}_host" ts=""
    docker rm -f "$h" >/dev/null 2>&1
    [ -n "$orig" ] && ts="--tmpfs $orig:rw,strictatime,mode=0777"
    docker run -d --name "$h" $ts -v "$WORK:/work:ro" --entrypoint sleep "$img" 3600 >/dev/null
    out=$(docker exec -e XB="/work/$c.xb" -e MODE="$mode" -e CASE="$c" -e ORIG="$orig" -e TAMPER="$tamper" "$h" bash /work/inside.sh 2>&1)
    docker rm -f "$h" >/dev/null 2>&1
}

CASES=("default||" "abs|--innodb-undo-directory=/undo_ext --innodb-undo-tablespaces=3|/undo_ext" "abs_absent|--innodb-undo-directory=/undo_ext --innodb-undo-tablespaces=3|/undo_ext" "rel|--innodb-undo-directory=undosub --innodb-undo-tablespaces=3|" "zero_abs|--innodb-undo-directory=/undo_ext --innodb-undo-tablespaces=0|/undo_ext")

for ver in "${VERSIONS[@]}"; do
    img="mariadb:$ver"; ran=$((ran + 1)); echo "=== $img"
    docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >/dev/null 2>&1 || { bad "$img cannot be pulled"; continue; }
    for spec in "${CASES[@]}"; do
        IFS='|' read -r c args tdir <<<"$spec"
        base=$c; [ "$c" = abs_absent ] && base=abs
        # the backup of "abs_absent" is the one of "abs"; the restore host has no such directory
        if [ "$c" = abs_absent ] && [ -s "$WORK/abs.xb" ]; then cp "$WORK/abs.xb" "$WORK/abs_absent.xb"; else
            echo " -- layout $c [$args]"
            if ! make_backup "$img" "$c" "$args" "$tdir"; then
                if [ "$c" = zero_abs ] || [ "$c" = rel ]; then note "$c: this server does not start or back up with this layout; skipped"; else bad "$c: the source server or its backup failed"; fi
                continue
            fi
            echo "     source server: innodb_undo_directory / innodb_undo_tablespaces = $(cat "$WORK/server_undo")"
        fi
        [ "$c" = abs_absent ] && echo " -- layout abs_absent: the backup of abs, restored where /undo_ext does not exist"
        orig=""; case "$c" in abs | zero_abs) orig="/undo_ext" ;; esac

        # ---------------- plain: what the job did before ----------------
        restore "$img" "$c" plain "$orig"
        pr=$(g prepare_rc); cfg=$(g cfg_t)
        case "$c" in
        default)
            [ "$pr" = 0 ] && [ "$cfg" = 1 ] && [ "$(g banner)" = 1 ] && ok "$c plain: prepare exit 0, completed OK!, .cfg written (the default layout never was affected)" || bad "$c plain: prepare=$pr cfg_t=$cfg"
            ;;
        abs)
            if [ "$(g orig_checksums_unchanged)" = 0 ]; then
                ok "$c plain: the prepare exited $pr and REWROTE the undo files of the reachable original directory (the critical case, seen)"
            elif [ "$(g orig_never_read)" = 0 ]; then
                ok "$c plain: the prepare exited $pr and OPENED the undo files of the reachable original directory (read, not rewritten on this version: the bug, seen)"
            elif [ "$ver" = 10.6 ]; then
                # MariaDB 10.6's unfixed prepare opens the reachable original directory in some runs and not in others
                note "$c plain: this run of the unfixed prepare (exit $pr) neither opened nor rewrote the original directory (it varies on 10.6)"
            else
                bad "$c plain: the bug was not seen: prepare=$pr, original directory unchanged=$(g orig_checksums_unchanged) never_read=$(g orig_never_read)"
            fi
            ;;
        abs_absent | rel)
            if [ "$pr" != 0 ] && [ "$cfg" = 0 ] && [ "$(g banner)" = 0 ]; then
                ok "$c plain: prepare exit $pr, no .cfg, no completed OK! (the failure of the lab reseed)"
            else
                bad "$c plain: the failure was not seen: prepare=$pr cfg_t=$cfg banner=$(g banner)"
            fi
            ;;
        zero_abs)
            [ "$pr" = 0 ] && [ "$cfg" = 1 ] && ok "$c plain: no undo tablespace, the prepare does not need the directory" || note "$c plain: prepare=$pr cfg_t=$cfg"
            ;;
        esac

        # ---------------- fixed: what the job does now ----------------
        restore "$img" "$c" fixed "$orig"
        sr=$(g stage_rc); pr=$(g prepare_rc)
        [ "$sr" = 0 ] && ok "$c fixed: pr_stage_undo_dir succeeded (undo $(g cnf_undo) -> $(g cnf_undo_after))" || { bad "$c fixed: pr_stage_undo_dir returned $sr: $(echo "$out" | grep -E 'reason_log' | head -1)"; continue; }
        [ "$pr" = 0 ] && [ "$(g banner)" = 1 ] && ok "$c fixed: --prepare --export exit 0, completed OK!, backup_type=$(g backup_type)" || bad "$c fixed: prepare=$pr banner=$(g banner) $(echo "$out" | grep '^log:' | head -2 | tr '\n' ' ')"
        [ "$(g cfg_t)" = 1 ] && [ "$(g cfg_u)" = 1 ] && [ "$(g cfg_count)" = "$(g ibd_count)" ] && ok "$c fixed: .cfg generated for every InnoDB tablespace ($(g cfg_count) .cfg, $(g ibd_count) .ibd; d.t, d.u included)" || bad "$c fixed: cfg_t=$(g cfg_t) cfg_u=$(g cfg_u) cfg=$(g cfg_count) ibd=$(g ibd_count)"
        case "$c" in
        default) [ "$(g cnf_changed)" = 0 ] && ok "$c fixed: a backup without a custom undo directory: backup-my.cnf left byte-identical" || bad "$c fixed: backup-my.cnf was changed" ;;
        *) [ "$(g cnf_changed)" = 1 ] && [ "$(g cnf_undo_after)" = "/var/lib/mysql/.system/backup" ] && ok "$c fixed: the staged backup-my.cnf names the backup directory" || bad "$c fixed: backup-my.cnf undo directory is '$(g cnf_undo_after)'" ;;
        esac
        if [ -n "$orig" ]; then
            [ "$(g orig_checksums_unchanged)" = 1 ] && [ "$(g orig_never_read)" = 1 ] && ok "$c fixed: the original directory $orig is byte-identical and was never read (access times)" || bad "$c fixed: the original directory was touched: unchanged=$(g orig_checksums_unchanged) never_read=$(g orig_never_read)"
        fi
        [ "$(g defs_rc)" = 0 ] && [ "$(g defs_t)" -ge 1 ] 2>/dev/null && [ "$(g defs_u)" -ge 1 ] 2>/dev/null && ok "$c fixed: the temporary definition server started on the prepared backup and read d.t, d.u" || bad "$c fixed: definition server rc=$(g defs_rc) d.t=$(g defs_t) d.u=$(g defs_u) $(echo "$out" | grep '^log2:' | head -2 | tr '\n' ' ')"
        if [ -n "$orig" ]; then
            [ "$(g orig_after_defs_unchanged)" = 1 ] && [ "$(g orig_after_defs_never_read)" = 1 ] && ok "$c fixed: the definition server did not touch $orig either" || bad "$c fixed: the definition server touched $orig"
        fi

        # ---------------- unsafe backups are refused ----------------
        if [ "$c" = abs ]; then
            restore "$img" "$c" fixed "$orig" missing
            [ "$(g stage_rc)" = 1 ] && [ "$(g prepare_rc)" = not_run ] && [ "$(g backup_type)" != log-applied ] && [ "$(g refusal)" = undo002_is_missing ] && [ "$(g orig_checksums_unchanged)" = 1 ] && [ "$(g orig_never_read)" = 1 ] && ok "$c: undo002 missing: refused with the reason, no prepare run, $orig untouched" || bad "$c: missing undo002 was not refused cleanly: stage=$(g stage_rc) prepare=$(g prepare_rc) type=$(g backup_type) $(echo "$out" | grep reason_log)"
            restore "$img" "$c" fixed "$orig" count5
            [ "$(g stage_rc)" = 1 ] && [ "$(g prepare_rc)" = not_run ] && [ "$(g refusal)" = undo004_is_missing ] && [ "$(g orig_never_read)" = 1 ] && ok "$c: innodb_undo_tablespaces=5 with 3 files: refused, no prepare run, $orig never read" || bad "$c: inconsistent undo files were not refused: stage=$(g stage_rc) prepare=$(g prepare_rc) $(echo "$out" | grep reason_log)"
        fi
    done
done
echo
[ $ran -gt 0 ] || { echo "FAIL: nothing ran"; exit 2; }
[ $fail -eq 0 ] && echo "ALL PASS ($ran version(s))" || echo "FAILED"
exit $fail
