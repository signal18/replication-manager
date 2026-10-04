#!/bin/bash
# Checks of the small decision functions of the restore, run on their own
# (no database, no Docker): the free-space floor between phases, the retry of
# a dead job's report, the object-name guard and the filter applied to the
# backup's server configuration. The functions are cut out of the script.
# Usage: unit_checks.sh [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${1:-$HERE/../../dbjobs_new.sh}"
fails=0
check() { if eval "$2"; then echo "PASS $1"; else echo "FAIL $1"; fails=$((fails + 1)); fi; }
fn() { sed -n "/^$1() {/,/^}/p" "$SCRIPT"; }

# --- pr_disk_ok ---
eval "$(fn pr_disk_ok)"
pr_log() { :; }
RECEIVE_MIN_FREE_PCT=10
DATADIR=/nonexistent
tmp=$(mktemp -d)
BACKUPDIR="$tmp/backup"
df() { printf 'Filesystem 1-blocks Used Available\nx %s 0 %s\n' "$DF_TOTAL" "$DF_AVAIL"; }
mkdir -p "$BACKUPDIR"
DF_TOTAL=1000000000 DF_AVAIL=100000001 PR_STATUS=0
check "free space just above the floor passes" 'pr_disk_ok "test"; [[ $? -eq 0 && $PR_STATUS -eq 0 && -d $BACKUPDIR ]]'
DF_AVAIL=99999999
check "free space just below the floor stops, removes the backup, sets the status" 'pr_disk_ok "test"; [[ $? -eq 1 && $PR_STATUS -eq 1 && ! -d $BACKUPDIR ]]'
unset -f df

# --- receiveReserveBytes ---
eval "$(fn receiveReserveBytes)"
RECEIVE_RESERVE_MIB=512
# A client that is a real executable (the function runs it under timeout).
BINARY_CLIENT="$tmp/fakeclient"
cat >"$BINARY_CLIENT" <<'FAKE'
#!/bin/bash
case "$*" in
*redo_log_capacity*) [[ -n "$CAP" ]] && echo "$CAP" || exit 1 ;;
*log_file_size*) [[ "$LFS" == FAIL ]] && exit 1; echo "$LFS" ;;
esac
FAKE
chmod +x "$BINARY_CLIENT"
export CAP LFS
CAP=4294967296 LFS=
check "reserve is twice the redo capacity when it is large" '[[ $(receiveReserveBytes) -eq 8589934592 ]]'
CAP="" LFS=2147483648
check "reserve falls back to innodb_log_file_size (2 GiB redo gives 4 GiB)" '[[ $(receiveReserveBytes) -eq 4294967296 ]]'
CAP="" LFS=100663296
check "a small redo log keeps the 512 MiB minimum" '[[ $(receiveReserveBytes) -eq 536870912 ]]'
CAP="" LFS=FAIL
check "no readable redo size returns an error and no number" 'out=$(receiveReserveBytes); [[ $? -eq 1 && -z "$out" ]]'
CAP=NULL LFS=NULL
check "a NULL answer returns an error" '! receiveReserveBytes >/dev/null'
CAP="" LFS=""
check "an empty answer returns an error" '! receiveReserveBytes >/dev/null'

# --- recoverDeadJobs ---
eval "$(fn recoverDeadJobs)"
LOG_DIR="$tmp/jobs" LOCK_DIR="$tmp/lock"
JOBS=(reseedmariabackup) JOBS_MODE=api LVL_ERROR=ERROR
mkdir -p "$LOG_DIR/reseedmariabackup.run" "$LOCK_DIR"
echo 999999 >"$LOG_DIR/reseedmariabackup.run/pid"
touch "$LOCK_DIR/reseedmariabackup_lockfile"
send_lines_to_api() { :; }
pr_stop_stale_definition_server() { :; }
REPORT_RC=1
report_job_state() { return $REPORT_RC; }
recoverDeadJobs
recoverDeadJobs
check "a report that fails keeps the marker and the lock" '[[ -f $LOG_DIR/reseedmariabackup.run/pid && -f $LOCK_DIR/reseedmariabackup_lockfile ]]'
check "a report that fails twice writes the message once" '[[ $(grep -c "was interrupted" "$LOG_DIR/reseedmariabackup.out") -eq 1 ]]'
REPORT_RC=0
recoverDeadJobs
check "a report that succeeds removes the marker, the run folder and the lock" '[[ ! -e $LOG_DIR/reseedmariabackup.run && ! -e $LOCK_DIR/reseedmariabackup_lockfile ]]'

# --- pr_unsafe_name ---
eval "$(fn pr_unsafe_name)"
for n in 'a`b' "a'b" 'a"b' 'a\b'; do
    check "unsafe object name: $n" 'pr_unsafe_name "$n"'
done
for n in orders order_items 'T 1' 'a-b'; do
    check "ordinary object name: $n" '! pr_unsafe_name "$n"'
done

# --- pr_filter_backup_cnf (the script's own function) ---
eval "$(fn pr_filter_backup_cnf)"
printf '[mysqld]\ninnodb_page_size=16384\n!include /etc/x.cnf\n  !includedir /y\n!includedir /z\nserver_uuid=abc\n# comment\n[ bad section\ninnodb_undo_directory=/u\n   innodb_x=1\n' >"$tmp/backup-my.cnf"
out=$(pr_filter_backup_cnf "$tmp/backup-my.cnf")
check "the filter drops every include directive, comment and indented line" '! grep -qE "include|comment|innodb_x" <<<"$out"'
check "the filter keeps the sections and prefixes the options" '[[ $out == $'"'"'[mysqld]\nloose-innodb_page_size=16384\nloose-server_uuid=abc\nloose-innodb_undo_directory=/u'"'"' ]]'

rm -rf "$tmp"
echo "failed: $fails"
[[ $fails -eq 0 ]]
