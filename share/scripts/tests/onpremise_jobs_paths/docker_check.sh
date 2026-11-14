#!/usr/bin/env bash
# Real Docker check of the jobs script (dbjobs_new.sh) on an on-premise host, where the jobs run over SSH as a
# non-root user (#2379). Such a host has none of the container layout: no /bootstrap, no
# /docker-entrypoint-initdb.d, and the jobs user cannot create a directory under /. Its PATH is the distribution's
# default for a regular user, without /usr/sbin, where the packages install mariadbd and mysqld.
#
# Per image, the check runs the script's own print-defaults functions (taken from the script with sed:
# db_server_binary, create_dummy_config_file, send_mariadb_defaults, print_defaults_for_fetch) as a non-root user
# with no passwd entry, PATH=/usr/local/bin:/usr/bin:/bin, and TMP_DIR set to a jobs directory that user owns, as
# replication-manager sets it (<datadir>/.system/jobs). What the API does is stubbed: the config fetch writes one
# conf.d file, the receiver is a file instead of the socat to replication-manager, the API log lines go to stdout.
# It asserts that:
#   1. print-defaults exits 0 and the server binary it ran is the one in /usr/sbin;
#   2. what it sends holds the variable of the fetched configuration, as the server printed it;
#   3. the dummy configuration is under the jobs directory, and nothing was created at /bootstrap;
#   4. the previous code (origin/develop before the fix, or OLD_REF) fails there, so the check can see the bug.
# It also checks in the script itself that the two physical backup jobs work from the jobs directory, not from
# /docker-entrypoint-initdb.d (the mariabackup_target_dir check runs that command for real).
#
# Usage: docker_check.sh [image ...]   default: mariadb:10.11 mariadb:11.8 mysql:8.4
#        OLD_REF=<git ref> docker_check.sh    the code to compare with (default: the parent of the fix)
set -u
command -v docker >/dev/null || { echo "FAIL: docker is required"; exit 2; }
cd "$(dirname "$0")/../../../.." || exit 2
SCRIPT=share/scripts/dbjobs_new.sh
OLD_REF=${OLD_REF:-$(git log -1 --format=%H -S'local extract_dir="/bootstrap/dummy"' -- "$SCRIPT" 2>/dev/null)^}
P=onp_$$; fail=0; ran=0
IMAGES=("$@"); [ ${#IMAGES[@]} -gt 0 ] || IMAGES=(mariadb:10.11 mariadb:11.8 mysql:8.4)
ok() { echo "  ok:   $*"; }
bad() { echo "  FAIL: $*"; fail=1; }
WORK=$(mktemp -d); trap 'docker rm -f "${P}_db" >/dev/null 2>&1; rm -rf "$WORK"' EXIT

FUNCS='/^db_server_binary() {/,/^}/p; /^create_dummy_config_file() {/,/^}/p; /^send_mariadb_defaults() {/,/^}/p; /^print_defaults_for_fetch() {/,/^}/p'
sed -n "$FUNCS" "$SCRIPT" >"$WORK/new.sh"
if git cat-file -e "$OLD_REF:$SCRIPT" 2>/dev/null; then git show "$OLD_REF:$SCRIPT" | sed -n "$FUNCS" >"$WORK/old.sh"; else : >"$WORK/old.sh"; fi
grep -q '^print_defaults_for_fetch() {' "$WORK/new.sh" || { echo "FAIL: print_defaults_for_fetch was not found in $SCRIPT"; exit 2; }

# The stubs of what talks to replication-manager, then the script's functions, then the call print-defaults makes.
cat >"$WORK/run.sh" <<'EOF'
LVL_DEBUG=DEBUG; LVL_INFO=INFO; LVL_WARN=WARN; LVL_ERROR=ERROR
send_lines_to_api() { echo "api[$3]: $1"; }
job_token() { echo token; }
db_owner() { echo "$1"; }
job_stream_signed() { cat; }
socat() { cat >"$TMP_DIR/received"; }
fetch_and_extract_config() {
    rm -rf "$1" && mkdir -p "$1/etc/mysql/conf.d" "$1/etc/mysql/custom.d" || return 1
    printf '[mysqld]\nmax_connections=4242\n' >"$1/etc/mysql/conf.d/10_check.cnf"
}
. "$1"
type db_server_binary >/dev/null 2>&1 && echo "binary=$(db_server_binary)"
print_defaults_for_fetch 127.0.0.1 4444 /etc/mysql "$TMP_DIR/print-defaults.log"
echo "rc=$?"
EOF

chmod -R a+rX "$WORK" # read by the jobs user, mktemp makes it 0700

run_as_jobs_user() { # <functions file>
  docker exec -u 4242:4242 -e PATH=/usr/local/bin:/usr/bin:/bin -e TMP_DIR=/var/lib/onp/.system/jobs "${P}_db" \
    bash -c 'rm -rf "$TMP_DIR"/* 2>/dev/null; cd "$TMP_DIR" && bash /check/run.sh "$1" 2>&1; echo "received_var=$(grep -c -E -- "^--max[-_]connections=4242 *$" "$TMP_DIR/received" 2>/dev/null)"; echo "dummy_in_jobs=$(test -f "$TMP_DIR/dummy/dummy.cnf" && echo 1 || echo 0)"; echo "bootstrap=$(test -e /bootstrap && echo 1 || echo 0)"' _ "$1"
}
g() { echo "$out" | sed -n "s/^$1=//p" | tail -1; }

for img in "${IMAGES[@]}"; do
  ran=$((ran + 1)); echo "=== $img"
  docker rm -f "${P}_db" >/dev/null 2>&1
  docker run -d --name "${P}_db" -v "$WORK:/check:ro" --entrypoint sleep "$img" 600 >/dev/null || { bad "$img did not start"; continue; }
  docker exec "${P}_db" sh -c 'rm -rf /bootstrap; mkdir -p /var/lib/onp/.system/jobs && chown -R 4242:4242 /var/lib/onp'
  if docker exec -u 4242:4242 "${P}_db" mkdir /bootstrap 2>/dev/null; then bad "the jobs user can create /bootstrap: the image does not reproduce the host"; docker exec "${P}_db" rm -rf /bootstrap; fi

  out=$(run_as_jobs_user /check/new.sh)
  case "$(g binary)" in /usr/sbin/mariadbd|/usr/sbin/mysqld) ok "the server binary is found in /usr/sbin out of the PATH: $(g binary)" ;; *) bad "server binary '$(g binary)'"; echo "$out" | sed 's/^/        /' ;; esac
  [ "$(g rc)" = 0 ] && ok "print-defaults exits 0 as a non-root user" || { bad "print-defaults exits '$(g rc)'"; echo "$out" | sed 's/^/        /'; }
  [ "$(g received_var)" = 1 ] && ok "what it sends holds the fetched configuration (--max_connections=4242)" || bad "the fetched variable is not in what it sends"
  [ "$(g dummy_in_jobs)" = 1 ] && [ "$(g bootstrap)" = 0 ] && ok "the dummy configuration is under the jobs directory, nothing at /bootstrap" || bad "dummy in jobs '$(g dummy_in_jobs)', /bootstrap '$(g bootstrap)'"

  if [ -s "$WORK/old.sh" ]; then
    out=$(run_as_jobs_user /check/old.sh)
    [ "$(g rc)" != 0 ] && echo "$out" | grep -q -E "cannot create directory ./bootstrap.|Permission denied|Neither 'mariadbd' nor 'mysqld' found" \
      && ok "the previous code fails there: $(echo "$out" | grep -m1 -o -E "cannot create directory ./bootstrap.|Neither 'mariadbd' nor 'mysqld' found in PATH")" \
      || bad "the previous code does not fail there (rc '$(g rc)'): the check would not see the bug"
  else
    echo "  info: no previous code at '$OLD_REF', comparison skipped"
  fi
  docker rm -f "${P}_db" >/dev/null 2>&1
done

echo "=== the physical backup jobs"
backup_cds=$(sed -n '/^        xtrabackup)$/,/^        mysqldump)$/p' "$SCRIPT" | grep -E '^\s+cd ')
[ "$(echo "$backup_cds" | grep -c 'cd "\$TMP_DIR"')" = 2 ] && ! echo "$backup_cds" | grep -q docker-entrypoint-initdb.d \
  && ok "the xtrabackup and mariabackup jobs work from the jobs directory" || bad "the backup jobs change to: $backup_cds"

[ $ran -gt 0 ] || { echo "FAIL: no image ran"; exit 1; }
[ $fail = 0 ] && echo "PASS" || echo "FAIL"
exit $fail
