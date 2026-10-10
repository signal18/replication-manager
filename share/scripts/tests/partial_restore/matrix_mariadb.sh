#!/bin/bash
# One summary line per MariaDB version x scenario.
# Usage: [VERSIONS=".."] [SCENARIOS=".."] matrix_mariadb.sh [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$HERE/.work"
for v in ${VERSIONS:-10.5 10.6 10.11 11.4 11.8}; do
  for sc in ${SCENARIOS:-keep-schema no-schema leftovers}; do
    out=$(timeout 900 "$HERE/run_mariadb.sh" "$v" "$sc" "$@" 2>&1)
    echo "$out" >"$HERE/.work/last-mariadb-$v-$sc.log"
    rc=$(echo "$out" | awk -F= '/^partialRestore rc=/{print $2}')
    ok=$(echo "$out" | grep -c '^OK '); bad=$(echo "$out" | grep -c '^MISMATCH ')
    left=$(echo "$out" | sed -n '/--- leftovers/,/--- integrity/p' | grep -vc -- '^---')
    g() { echo "$out" | awk -v k="$1" 'index($0,k)==1{sub(/^[^:]*: */,""); print $1; exit}'; }
    skip=$(echo "$out" | grep -c 'SKIPPED ')
    # The scenario's own verdict (every object assertion of scenarios.sh).
    verdict=$(echo "$out" | awk '/^RESULT /{print ($3=="PASS") ? "PASS" : "FAIL"; exit}')
    printf '%-6s %-12s rc=%s tables=%s/9 mismatch=%s leftovers=%s view=%s trg=%s func=%s event=%s fk=%s seq=%s aria=%s skipped=%s verdict=%s\n' \
      "$v" "$sc" "${rc:-?}" "$ok" "$bad" "$left" "$(g 'view v_plain')" "$(g 'trigger trg_plain')" "$(g 'function f_double')" "$(g 'event ev_tick')" "$(g 'foreign key')" "$(g 'sequence seq1')" "$(g 'aria_t rows')" "$skip" "${verdict:-NONE}"
  done
done
