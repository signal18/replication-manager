#!/usr/bin/env bash
# Checks, without Docker, xtrabackup_undo_args of dbjobs_new.sh: --innodb-undo-directory is passed as ONE argument and as the
# server reports it, for MySQL 8.0 and 8.4 only (the series replication-manager's default_path.cnf has a version group for);
# when the server cannot be asked the array stays empty and a warning without secret is posted to the job log.
set -u
cd "$(dirname "$0")/../../../.." || exit 2
SCRIPT=share/scripts/dbjobs_new.sh
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail=0; ok() { echo "  ok:   $*"; }; bad() { echo "  FAIL: $*"; fail=1; }
LVL_WARN=WARN
API_SENT=""; API_JOB=""; API_LVL=""
send_lines_to_api() { API_SENT="$1"; API_JOB="$2"; API_LVL="$3"; }
eval "$(sed -n '/^xtrabackup_undo_args() {/,/^}/p' "$SCRIPT")"
cat >"$T/client" <<'CL'
#!/bin/sh
# fake mysql client: $FAKE_VERSION, $FAKE_UNDO; FAKE_FAIL=1 fails every query, FAKE_FAIL=undo only the undo query
[ "${FAKE_FAIL:-0}" = 1 ] && exit 1
case "$*" in
*"@@version"*) echo "$FAKE_VERSION" ;;
*"@@innodb_undo_directory"*) [ "${FAKE_FAIL:-0}" = undo ] && exit 1; echo "$FAKE_UNDO" ;;
esac
CL
chmod +x "$T/client"; BINARY_CLIENT=$T/client
check() { # <version> <undo> <expected: one argument, or empty for none> [fail: 1 | undo]
  export FAKE_VERSION=$1 FAKE_UNDO=$2 FAKE_FAIL=${4:-0}; API_SENT=""; API_JOB=""; API_LVL=""
  xtrabackup_undo_args
  if [ -z "$3" ]; then want_n=0; else want_n=1; fi
  [ "${#XB_UNDO_ARGS[@]}" = "$want_n" ] && [ "${XB_UNDO_ARGS[0]:-}" = "$3" ] \
    && ok "version=$1 undo='$2'${4:+ (query failure: $4)} -> ${#XB_UNDO_ARGS[@]} argument: '${XB_UNDO_ARGS[0]:-}'" \
    || bad "version=$1 undo='$2': got ${#XB_UNDO_ARGS[@]} argument(s) '${XB_UNDO_ARGS[*]}', want '$3'"
}
check 8.0.35 ./ "--innodb-undo-directory=./"
check 8.0.35-27 ./ "--innodb-undo-directory=./"
check 8.4.11-11 ./ "--innodb-undo-directory=./"
check 8.4.11-11 /var/lib/mysql/.system/innodb/undo "--innodb-undo-directory=/var/lib/mysql/.system/innodb/undo"
check 8.0.35 "/data/my undo" "--innodb-undo-directory=/data/my undo"
check 8.0.35 "/data/*" "--innodb-undo-directory=/data/*"
check 5.7.44 ./ ""
check 8.1.0 ./ ""
check 8.3.0 ./ ""
check 9.1.0 ./ ""
check 8.04 ./ ""
check 8.0.35 "" ""
# a leaked argument from a previous call must not survive
XB_UNDO_ARGS=("--innodb-undo-directory=stale"); check 5.7.44 ./ ""

echo "=== when the server cannot be asked: no argument, and a warning with no secret"
BINARY_CLIENT="$T/client --password=SECRETPW"
for mode in 1 undo; do
  check 8.0.35 ./ "" $mode
  [ "$API_LVL" = WARN ] && [ "$API_JOB" = xtrabackup ] && ok "query failure ($mode): a WARN is posted to the xtrabackup job log" || bad "query failure ($mode): lvl='$API_LVL' job='$API_JOB'"
  case "$API_SENT" in *SECRETPW*|*--password*) bad "the warning leaks the client command: $API_SENT" ;; *) ok "query failure ($mode): the warning holds no credential" ;; esac
done
BINARY_CLIENT=$T/client
echo "=== a client that exits 0 but answers nothing: no argument, and a warning"
check "" ./ ""
[ "$API_LVL" = WARN ] && ok "empty version: a WARN is posted" || bad "empty version: lvl='$API_LVL' msg='$API_SENT'"
check 8.0.35 "" ""
[ "$API_LVL" = WARN ] && ok "empty innodb_undo_directory on 8.0: a WARN is posted" || bad "empty undo directory: lvl='$API_LVL' msg='$API_SENT'"
check 5.7.44 "" ""
[ -z "$API_SENT" ] && ok "an empty undo directory on a series that is not covered stays silent" || bad "unexpected warning on 5.7: $API_SENT"
check 8.0.35 ./ "--innodb-undo-directory=./"
[ -z "$API_SENT" ] && ok "no warning when the server answers" || bad "unexpected warning: $API_SENT"
check 5.7.44 ./ ""
[ -z "$API_SENT" ] && ok "no warning for a series that is not covered" || bad "unexpected warning: $API_SENT"
[ $fail = 0 ] && echo "RESULT: PASS" || echo "RESULT: FAIL"; exit $fail
