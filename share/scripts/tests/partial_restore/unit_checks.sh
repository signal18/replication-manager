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

# The removal helpers every removal of the backup folder goes through.
eval "$(fn pr_paths_ok)"
eval "$(fn pr_rm_backupdir)"
eval "$(fn reset_backupdir)"
eval "$(fn receiveBackupAbort)"

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
DATADIR="$tmp/data"; BACKUPDIR="$DATADIR/.system/backup"
mkdir -p "$DATADIR/.system/mrm_defs_ro" "$DATADIR/.system/mrm_defs_run"
touch "$DATADIR/.system/mrm_defs_ro/ibdata1"
recoverDeadJobs
check "a dead restore's temporary server folders are removed with it" '[[ ! -e $DATADIR/.system/mrm_defs_ro && ! -e $DATADIR/.system/mrm_defs_run && -d $DATADIR/.system ]]'
check "a report that succeeds removes the marker, the run folder and the lock" '[[ ! -e $LOG_DIR/reseedmariabackup.run && ! -e $LOCK_DIR/reseedmariabackup_lockfile ]]'

# --- a reused PID (proc_start_ticks) ---
eval "$(fn proc_start_ticks)"
bash -c 'exec -a dbjobs_fake sleep 60' &
fpid=$!
sleep 0.3
ticks=$(proc_start_ticks "$fpid")
check "the start time of a running process is a number" '[[ "$ticks" =~ ^[0-9]+$ ]]'
check "the start time of a missing process is an error" '! proc_start_ticks 999999 >/dev/null'
REPORT_RC=0
newrun() { mkdir -p "$LOG_DIR/reseedmariabackup.run"; echo "$fpid" >"$LOG_DIR/reseedmariabackup.run/pid"; [[ -n "$1" ]] && echo "$1" >"$LOG_DIR/reseedmariabackup.run/start"; true; }
rm -rf "$LOG_DIR/reseedmariabackup.run"
newrun "$ticks"; recoverDeadJobs
check "a live dbjobs run with the recorded start time is left alone" '[[ -f $LOG_DIR/reseedmariabackup.run/pid ]]'
rm -rf "$LOG_DIR/reseedmariabackup.run"
newrun ""; recoverDeadJobs
check "a live dbjobs run of an older version (no start time) is left alone" '[[ -f $LOG_DIR/reseedmariabackup.run/pid ]]'
rm -rf "$LOG_DIR/reseedmariabackup.run"
newrun 1; recoverDeadJobs
check "a PID reused by another dbjobs run (other start time) ends the job" '[[ ! -e $LOG_DIR/reseedmariabackup.run ]]'
kill "$fpid" 2>/dev/null; wait "$fpid" 2>/dev/null

# --- pr_paths_ok ---
eval "$(fn pr_paths_ok)"
check "set data and backup directories pass" 'DATADIR=/var/lib/mysql BACKUPDIR=/var/lib/mysql/.system/backup pr_paths_ok'
check "an empty data directory is refused" '! DATADIR="" BACKUPDIR=/.system/backup pr_paths_ok'
check "the root as data directory is refused" '! DATADIR=/ BACKUPDIR=/.system/backup pr_paths_ok'
check "an empty backup directory is refused" '! DATADIR=/var/lib/mysql BACKUPDIR="" pr_paths_ok'
check "a relative data directory is refused" '! DATADIR=var/lib/mysql BACKUPDIR=/var/lib/mysql/.system/backup pr_paths_ok'
check "a relative backup directory is refused" '! DATADIR=/var/lib/mysql BACKUPDIR=.system/backup pr_paths_ok'

# --- no removal with unset directories (rm and mkdir are mocked, nothing is removed) ---
RMLOG="$tmp/rm.log"
rm() { echo "rm $*" >>"$RMLOG"; }
mkdir() { echo "mkdir $*" >>"$RMLOG"; }
: >"$RMLOG"
DATADIR="" BACKUPDIR=/.system/backup
check "pr_rm_backupdir with an empty data directory fails without calling rm" '! pr_rm_backupdir; [[ ! -s $RMLOG ]]'
check "reset_backupdir with an empty data directory fails without calling rm or mkdir" '! reset_backupdir; [[ ! -s $RMLOG ]]'
DATADIR=/ BACKUPDIR=/
check "pr_rm_backupdir with the root as backup folder fails without calling rm" '! pr_rm_backupdir; [[ ! -s $RMLOG ]]'
job=reseedmariabackup LOG_DIR="$tmp/jobs" PR_STATUS=0
send_lines_to_api() { :; }
DATADIR="" BACKUPDIR=/.system/backup
receiveBackupAbort "test message"
check "receiveBackupAbort with unset directories reports the error without calling rm" '[[ $PR_STATUS -eq 1 && ! -s $RMLOG ]]'
DF_TOTAL=1000000000 DF_AVAIL=1 PR_STATUS=0
df() { printf 'Filesystem 1-blocks Used Available\nx %s 0 %s\n' "$DF_TOTAL" "$DF_AVAIL"; }
DATADIR="" BACKUPDIR=/.system/backup
pr_disk_ok "test"
check "pr_disk_ok with unset directories stops without calling rm" '[[ $PR_STATUS -eq 1 && ! -s $RMLOG ]]'
unset -f df
DATADIR=/var/lib/mysql BACKUPDIR=/var/lib/mysql/.system/backup
: >"$RMLOG"
reset_backupdir
check "reset_backupdir with set directories removes then recreates the backup folder" '[[ $(cat "$RMLOG") == $'"'"'rm -rf -- /var/lib/mysql/.system/backup\nmkdir -p -- /var/lib/mysql/.system/backup'"'"' ]]'
unset -f rm mkdir

# --- the job log exists before the backup stream arrives ---
# (the log follower waits 60 s for it; the stream can take much longer)
check "each reseed/flashback branch creates its log before receiving" '[[ $(grep -c "echo \"Waiting for the backup stream.\" >>\"\$LOG_DIR/\(reseed\|flash\).out\"" "$SCRIPT") -eq 4 ]]'
check "the prepare output is appended to that log, never truncating it" '[[ $(grep -c -- "--prepare --export --target-dir=\$BACKUPDIR 2>>\"\$LOG_DIR/\(reseed\|flash\).out\"" "$SCRIPT") -eq 4 ]] && ! grep -q -- "--target-dir=\$BACKUPDIR 2>\"" "$SCRIPT"'

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
