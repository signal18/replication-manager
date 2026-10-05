#!/bin/bash
# The whole local matrix in one command: the unit checks (unit_checks.sh), the MariaDB success scenarios
# (matrix_mariadb.sh: 5 versions x 3 scenarios = 15 runs) and the failure
# scenarios (failure_matrix.sh: 3 MariaDB + 2 Percona versions x 7 scenarios
# = 35 runs). Prints both reports, then one total; exit status 0 only when
# all 50 runs pass. About 40 minutes.
# Usage: all_matrix.sh [dbjobs_new.sh]
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$HERE/.work"
ok_report="$HERE/.work/all-success.txt"
fail_report="$HERE/.work/all-failure.txt"
"$HERE/unit_checks.sh" "$@" || { echo "unit checks failed"; exit 1; }
"$HERE/matrix_mariadb.sh" "$@" | tee "$ok_report"
# A success run passes when the restore returned 0, no table mismatched, no
# file was left over, no object was skipped and the scenario's own verdict
# (all its object assertions) is PASS. A run that timed out has no rc.
s_total=$(grep -c ' rc=' "$ok_report")
s_ok=$(grep -c ' rc=0 .* mismatch=0 leftovers=0 .* skipped=0 verdict=PASS$' "$ok_report")
"$HERE/failure_matrix.sh" "$@" | tee "$fail_report"
f_total=$(grep -c ' PASS$\| FAIL' "$fail_report")
f_ok=$(grep -c ' PASS$' "$fail_report")
echo "success runs: $s_ok/$s_total passed; failure runs: $f_ok/$f_total passed; total $((s_ok + f_ok))/$((s_total + f_total))"
[[ $s_total -eq 15 && $f_total -eq 35 && $s_ok -eq $s_total && $f_ok -eq $f_total ]]
