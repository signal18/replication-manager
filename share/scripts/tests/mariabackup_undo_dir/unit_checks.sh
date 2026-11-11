#!/bin/bash
# Checks of pr_stage_undo_dir() and its helpers of dbjobs_new.sh on their own (no database, no Docker): the
# function that points the undo tablespace directory of an extracted mariadb-backup at the undo
# tablespaces the backup holds, before "--prepare --export". The function is cut out of the script.
# Real MariaDB: docker_check.sh next to this file.
# Usage: unit_checks.sh [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${1:-$HERE/../../dbjobs_new.sh}"
fails=0
check() { if eval "$2"; then echo "PASS $1"; else echo "FAIL $1"; fails=$((fails + 1)); fi; }
fn() { sed -n "/^$1() {/,/^}/p" "$SCRIPT"; }
eval "$(fn pr_paths_ok)"
eval "$(fn pr_log)"
eval "$(fn pr_cnf_value)"
eval "$(fn pr_file_problem)"
eval "$(fn pr_resolve_undo_dir)"
eval "$(fn pr_stage_fail)"
eval "$(fn pr_prepare_ok)"
eval "$(fn pr_stage_undo_dir)"
send_lines_to_api() { API_MSG="$1"; }
LVL_ERROR=ERROR job=reseedmariabackup

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
DATADIR="$tmp/data"
BACKUPDIR="$DATADIR/.system/backup"
LOG="$tmp/reseed.out"

# new_backup <undo directory value | UNSET> <innodb_undo_tablespaces | UNSET> <layout: root|sub:<dir>|none>
new_backup() {
    rm -rf "$BACKUPDIR" "$LOG"
    mkdir -p "$BACKUPDIR/mysql" "$BACKUPDIR/d"
    {
        echo "[mysqld]"
        echo "innodb_checksum_algorithm=full_crc32"
        [[ "$1" != UNSET ]] && echo "innodb_undo_directory=$1"
        [[ "$2" != UNSET ]] && echo "innodb_undo_tablespaces=$2"
        echo "innodb_page_size=16384"
    } >"$BACKUPDIR/backup-my.cnf"
    local dir="$BACKUPDIR" i
    case "$3" in
    sub:*) dir="$BACKUPDIR/${3#sub:}"; mkdir -p "$dir" ;;
    esac
    [[ "$3" != none ]] && for i in 001 002 003; do head -c 4096 /dev/zero | tr '\0' 'u' >"$dir/undo$i"; done
    # a mode that is neither the umask's default nor mktemp's 600: the rewrite must keep it, whatever the umask
    chmod 640 "$BACKUPDIR/backup-my.cnf"
    stat -c %a "$BACKUPDIR/backup-my.cnf" >"$tmp/cnf.mode"
    sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-64 >"$tmp/cnf.sha"
}
undo_value() { sed -n 's/^innodb_undo_directory=//p' "$BACKUPDIR/backup-my.cnf"; }
unchanged() { [[ "$(sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-64)" == "$(cat "$tmp/cnf.sha")" ]]; }
stage() { pr_stage_undo_dir "$LOG"; }

# --- the confirmed case: an absolute path of the source server, undo files flat in the backup ---
new_backup /var/lib/mysql/.system/innodb/undo 3 root
check "absolute source path, undo files in the backup root: set to the backup directory" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'
check "  the other options are kept as they were" 'grep -q "^innodb_undo_tablespaces=3$" "$BACKUPDIR/backup-my.cnf" && grep -q "^innodb_page_size=16384$" "$BACKUPDIR/backup-my.cnf" && grep -q "^innodb_checksum_algorithm=full_crc32$" "$BACKUPDIR/backup-my.cnf"'
check "  the change is logged with the old and the new value" 'grep -q "/var/lib/mysql/.system/innodb/undo" "$LOG" && grep -q "set to .$BACKUPDIR." "$LOG"'
check "  the undo tablespaces and the file mode are untouched" '[[ $(ls "$BACKUPDIR" | grep -c "^undo00") -eq 3 && $(stat -c %a "$BACKUPDIR/backup-my.cnf") == "$(cat "$tmp/cnf.mode")" ]]'
check "  no staging file is left behind" '[[ -z "$(ls -A "$BACKUPDIR" | grep "^.backup-my.cnf")" ]]'
sha_after=$(sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-64)
check "  a second run changes nothing (idempotent)" 'stage && [[ "$(sha256sum "$BACKUPDIR/backup-my.cnf" | cut -c1-64)" == "$sha_after" ]]'

# --- valid configurations are left alone ---
new_backup "./" 3 root
check "./ with the files in the backup: file untouched" 'stage && unchanged'
new_backup "." 3 root
check ". with the files in the backup: file untouched" 'stage && unchanged'
new_backup UNSET 3 root
check "no innodb_undo_directory: file untouched" 'stage && unchanged'
new_backup "./" 0 none
check "no undo tablespaces (innodb_undo_tablespaces=0): file untouched, success" 'stage && unchanged'
new_backup UNSET UNSET root
check "no innodb_undo_tablespaces either: the files found are the ones expected, untouched" 'stage && unchanged'

# --- a relative value other than ./ ---
new_backup undosub 3 root
check "relative value, undo files flat in the root (what mariadb-backup writes): set to the backup directory" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'
new_backup undosub 3 sub:undosub
check "relative value, undo files in that subdirectory of the backup: that subdirectory" 'stage && [[ "$(undo_value)" == "$BACKUPDIR/undosub" ]]'
new_backup "$BACKUPDIR/undosub" 3 sub:undosub
check "a path already under the backup directory is kept (a previous run)" 'stage && [[ "$(undo_value)" == "$BACKUPDIR/undosub" ]] && unchanged'
new_backup ../../../etc 3 root
check "a relative value with .. is never followed: the backup directory" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'
new_backup /var/lib/mysql/.system/innodb/undo 0 none
check "absolute value, no undo tablespaces: still never left pointing outside the backup" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'

# --- the live server's undo files are never used, even when the source path exists here ---
mkdir -p "$tmp/live_undo"; echo live >"$tmp/live_undo/undo001"
new_backup "$tmp/live_undo" 3 root
check "an absolute path that exists on this host (the live server's): not used, the backup's own" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]] && [[ "$(cat "$tmp/live_undo/undo001")" == live ]]'
rm -rf "$tmp/live_undo"

# --- failures: nothing prepared, a reason in the log, the file untouched ---
new_backup /var/lib/mysql/.system/innodb/undo 3 root
rm "$BACKUPDIR/undo002"
check "an undo tablespace missing: fails" '! stage'
check "  the reason names the file and the expectation" 'grep -q "ERROR" "$LOG" && grep -q "undo002 is missing" "$LOG" && grep -q "innodb_undo_directory=" "$LOG"'
check "  the file is left as it was" 'unchanged'
check "  the log carries no completed OK!, so partialRestore (pr_prepare_ok) stops the job with an error status" '( PR_LOG="$LOG"; ! pr_prepare_ok "$LOG" )'
check "  the failure is reported to the job log of the API" '[[ "$API_MSG" == *undo002* ]]'
new_backup /var/lib/mysql/.system/innodb/undo 3 root
: >"$BACKUPDIR/undo003"
check "an empty undo tablespace: fails" '! stage && grep -q "undo003" "$LOG" && unchanged'
new_backup /var/lib/mysql/.system/innodb/undo 3 none
check "no undo file in the backup at all but 3 expected: fails, no empty tablespace is made up" '! stage && [[ -z "$(ls "$BACKUPDIR" | grep "^undo")" ]] && unchanged'
new_backup "./" 3 root
rm "$BACKUPDIR/undo001"
check "the default layout with a missing file: fails with the reason too" '! stage && grep -q "undo001" "$LOG" && unchanged'
new_backup /var/lib/mysql/.system/innodb/undo 3 root
rm "$BACKUPDIR/undo002"; ln -s /etc/hostname "$BACKUPDIR/undo002"
check "an undo tablespace that is a symbolic link: fails" '! stage && grep -q "symbolic link" "$LOG" && unchanged'
new_backup /var/lib/mysql/.system/innodb/undo 3 root
mv "$BACKUPDIR/backup-my.cnf" "$tmp/real.cnf"; ln -s "$tmp/real.cnf" "$BACKUPDIR/backup-my.cnf"
check "a backup-my.cnf that is a symbolic link: fails, the target is not rewritten" '! stage && grep -q "symbolic link" "$LOG" && grep -q "innodb_undo_directory=/var/lib/mysql" "$tmp/real.cnf"'
new_backup undosub 3 sub:undosub
ln -s "$tmp" "$BACKUPDIR/undosub2"; rm -rf "$BACKUPDIR/undosub"; mkdir "$tmp/outside"; for i in 001 002 003; do echo x >"$tmp/outside/undo$i"; done; ln -s "$tmp/outside" "$BACKUPDIR/undosub"
check "a subdirectory that is a symbolic link out of the backup is not followed" 'stage; [[ "$(undo_value)" != "$tmp/outside" && "$(undo_value)" != "$BACKUPDIR/undosub" ]]'
rm -rf "$tmp/outside"

# --- innodb_undo_tablespaces: present but not usable, and its bounds ---
for bad in "" abc "-1" "3x" "3 4" "0x3" "1.5" "128" "1000" "99999999999999999999" 'completed OK!'; do
    new_backup /var/lib/mysql/.system/innodb/undo "$bad" root
    check "innodb_undo_tablespaces='${bad}': refused, file untouched" '! stage && grep -q "not a number from 0 to 127" "$LOG" && unchanged'
done
new_backup /var/lib/mysql/.system/innodb/undo 'completed OK!' root
stage
check "  the untrusted value is not copied into the log with the banner of the prepare" '! grep -q "completed OK!" "$LOG" && ( PR_LOG="$LOG"; ! pr_prepare_ok "$LOG" )'
new_backup /var/lib/mysql/.system/innodb/undo 003 root
check "innodb_undo_tablespaces=003 (leading zeros) is 3" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'
new_backup /var/lib/mysql/.system/innodb/undo 127 none
for ((i = 1; i <= 127; i++)); do printf -v f 'undo%03d' "$i"; echo u >"$BACKUPDIR/$f"; done
check "127 undo tablespaces (MariaDB's maximum), all present: accepted" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'
new_backup UNSET UNSET none
echo u >"$BACKUPDIR/undo001"; echo u >"$BACKUPDIR/undo128"
check "no recorded count, an undo file numbered 128: refused" '! stage && grep -q "numbered above 127" "$LOG"'
# the banner text in what the staging logs on success, from the other untrusted value
new_backup "/var/lib/mysql completed OK! x" 3 root
check "innodb_undo_directory with the banner text, valid files: staged, and the log has no banner" 'stage && ! grep -q "completed OK!" "$LOG" && ( PR_LOG="$LOG"; ! pr_prepare_ok "$LOG" )'

# --- inconsistent undo files, the number of tablespaces not recorded ---
new_backup UNSET UNSET root
rm "$BACKUPDIR/undo002"
check "undo001 and undo003 without a recorded count (a gap): fails, nothing is guessed" '! stage && grep -q "undo002 is missing" "$LOG" && unchanged'
new_backup /var/lib/mysql/.system/innodb/undo 5 root
check "5 tablespaces expected, 3 in the backup: fails" '! stage && grep -q "undo004 is missing" "$LOG" && unchanged'

# --- the job branches themselves (the real code of the case, stubs around it) ---
# A staging failure ends the job with an error status without running the prepare or the restore (partialRestore),
# whatever the log says; a success prepares and then restores.
fake_mb="$tmp/fake_mariabackup"; printf '#!/bin/bash\necho "$@" >>"%s/mb.calls"\n' "$tmp" >"$fake_mb"; chmod +x "$fake_mb"
branch() { # <job name> -> prints the state the branch left
    local blk
    blk=$(sed -n "/^        $1)/,/^            ;;/p" "$SCRIPT")
    (
        job=$1 LOG_DIR="$tmp/jobs" MARIADB_BACKUP="$fake_mb" PR_STATUS=0 PARTIAL_RESTORE_JSON='{"stale":"yes"}'
        rm -rf "$LOG_DIR" "$tmp/mb.calls" "$tmp/pr.calls"; mkdir -p "$LOG_DIR"
        reset_backupdir() { :; }; socatCleaner() { :; }; pauseJob() { :; }; receiveBackup() { return 0; }
        partialRestore() { echo called >>"$tmp/pr.calls"; PR_STATUS=0; }
        eval "case \"\$job\" in $blk esac"
        echo "status=$PR_STATUS json=[$PARTIAL_RESTORE_JSON] prepare_calls=$(cat "$tmp/mb.calls" 2>/dev/null | wc -l) restore_calls=$(cat "$tmp/pr.calls" 2>/dev/null | wc -l)"
    )
}
for spec in "reseedmariabackup reseed.out" "flashbackmariabackup flash.out"; do
    set -- $spec; jn=$1 lg=$2
    new_backup /var/lib/mysql/.system/innodb/undo 3 root
    out=$(branch "$jn")
    check "$jn, valid backup: staged, prepared with the job's own command, then restored, status 0" '[[ "$out" == "status=0 json=[{\"stale\":\"yes\"}] prepare_calls=1 restore_calls=1" && "$(cat "$tmp/mb.calls")" == "--prepare --export --target-dir=$BACKUPDIR" && "$(undo_value)" == "$BACKUPDIR" ]]'
    new_backup /var/lib/mysql/.system/innodb/undo 3 root
    rm "$BACKUPDIR/undo002"
    out=$(branch "$jn")
    check "$jn, undo file missing: no prepare, no restore, status 1, no restore metadata" '[[ "$out" == "status=1 json=[] prepare_calls=0 restore_calls=0" ]]'
    check "  the job output says so and the log has the reason" 'grep -q "Backup not prepared" "$tmp/jobs/$jn.out" && grep -q "undo002 is missing" "$tmp/jobs/$lg"'
    new_backup "/x completed OK!" 3 root
    rm "$BACKUPDIR/undo002"
    out=$(branch "$jn")
    check "$jn, the refusal logs a value with the banner text in it: still aborted, and the log has no banner" '[[ "$out" == "status=1 json=[] prepare_calls=0 restore_calls=0" ]] && ! grep -q "completed OK!" "$tmp/jobs/$lg"'
    rm -f "$BACKUP_CNF_DUMMY"
    new_backup UNSET 3 root; rm "$BACKUPDIR/backup-my.cnf"
    out=$(branch "$jn")
    check "$jn, no backup-my.cnf: aborted, status 1" '[[ "$out" == "status=1 json=[] prepare_calls=0 restore_calls=0" ]]'
done
for jn in reseedxtrabackup flashbackxtrabackup; do
    blk=$(sed -n "/^        $jn)/,/^            ;;/p" "$SCRIPT")
    check "$jn (MySQL, not mariadb-backup): left as it was" '[[ "$blk" != *pr_stage_undo_dir* ]]'
done
check "the API result and doneJob() of a reseed/flashback read PR_STATUS" '[[ "$(sed -n "/^doneJob() {/,/^}/p" "$SCRIPT")" == *"[ \"\$PR_STATUS\" -ne 0 ]"* ]] && grep -q "if \[ \"\$PR_STATUS\" -ne 0 \]; then" "$SCRIPT"'

# --- no file, or unset directories ---
rm -rf "$BACKUPDIR"; mkdir -p "$BACKUPDIR"
check "no backup-my.cnf: fails, nothing is prepared" '! stage && grep -q "backup-my.cnf is missing" "$LOG"'
mkdir -p "$BACKUPDIR/backup-my.cnf"
check "backup-my.cnf that is a directory, not a file: fails" '! stage'
rm -rf "$BACKUPDIR"; mkdir -p "$BACKUPDIR"
BACKUPDIR_SAVED="$BACKUPDIR"; BACKUPDIR=""
check "an unset backup directory: fails" '! stage'
BACKUPDIR="$BACKUPDIR_SAVED"

# --- the other undo options spelled with dashes, spaces and a quoted value ---
new_backup UNSET 3 root
printf '[mysqld]\ninnodb-undo-directory = /var/lib/mysql/.system/innodb/undo\ninnodb-undo-tablespaces = 3\n' >"$BACKUPDIR/backup-my.cnf"
check "dash spelling with spaces around =: handled" 'stage && [[ "$(sed -n "s/^innodb_undo_directory=//p" "$BACKUPDIR/backup-my.cnf")" == "$BACKUPDIR" ]]'
printf '[mysqld]\ninnodb_undo_directory="/var/lib/mysql/.system/innodb/undo"\ninnodb_undo_tablespaces=3\n' >"$BACKUPDIR/backup-my.cnf"
check "a quoted value: handled" 'stage && [[ "$(undo_value)" == "$BACKUPDIR" ]]'

echo
[[ $fails -eq 0 ]] && echo "ALL PASS" || echo "$fails FAILED"
exit $fails
