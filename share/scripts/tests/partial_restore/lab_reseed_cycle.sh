#!/bin/bash
# OpenSVC lab only: a cluster with db1 (master) on one node and db2 (replica)
# on another. Full physical reseed cycle through repman, then an exact
# comparison of db2 with db1. db2's state before the reseed does not matter:
# the reseed replaces it entirely from db1's backup.
# Needs on the first node: /tmp/rmapi.sh (repman API helper: copy lab_rmapi.sh)
# and /tmp/dbstate.sh (per-table content hashes + object counts: copy
# lab_dbstate.sh). Configure with the environment (no default: nothing of a
# real lab is written in this repository):
#   NODE1, NODE2   ssh targets of the node with db1 and of the node with db2
#   CLUSTER        cluster name in repman (OpenSVC namespace/service names follow it)
#   SVC_DOMAIN     DNS domain of the OpenSVC services (db1.<cluster>.svc.<domain>)
#   DB1_ID, DB2_ID repman server ids of db1 and db2 (API: /api/clusters/<cluster>/topology/servers)
set -u
: "${NODE1:?set NODE1}" "${NODE2:?set NODE2}" "${CLUSTER:?set CLUSTER}" "${SVC_DOMAIN:?set SVC_DOMAIN}" "${DB1_ID:?set DB1_ID}" "${DB2_ID:?set DB2_ID}"
N1="ssh -o BatchMode=yes -o ConnectTimeout=10 $NODE1"
N2="ssh -o BatchMode=yes -o ConnectTimeout=10 $NODE2"
ENVR="CLUSTER=$CLUSTER SVC_DOMAIN=$SVC_DOMAIN"
BKD=/var/lib/replication-manager/backups/$CLUSTER/db1.$CLUSTER.svc.${SVC_DOMAIN}_3306
J=/var/lib/mysql/.system/jobs
say() { echo "[$(date -u +%H:%M:%S)] $*"; }

say "1. physical backup of db1"
T0=$($N1 'date -u +%s')
$N1 "/tmp/rmapi.sh GET /api/clusters/$CLUSTER/servers/$DB1_ID/actions/backup-physical" >/dev/null
for i in $(seq 1 90); do
    sleep 10
    done_ok=$($N1 "sudo docker exec ${CLUSTER}..db1.container.db sh -c 'stat -c %Y $J/backup.out; tail -1 $J/backup.out'" 2>/dev/null)
    ts=$(echo "$done_ok" | head -1)
    if [[ "${ts:-0}" -gt "$T0" && "$done_ok" == *"completed OK!"* ]]; then
        sleep 15   # let repman finish receiving and write the metadata
        say "   $(echo "$done_ok" | tail -1)"
        break
    fi
done
$N1 "sudo docker exec -u repman ${CLUSTER}..repman.container.repman ls -la --time-style=+%H:%M $BKD/mariabackup.xbtream.gz $BKD/mariabackup.meta.json" | awk '{print "   " $5, $6, $7}'

say "2. db1 reference state"
$N1 "env $ENVR bash -s" <<'EOF'
/tmp/dbstate.sh db1 > /tmp/state-db1.txt
echo "   tables=$(grep -c "^T " /tmp/state-db1.txt) $(grep "^C " /tmp/state-db1.txt | cut -c3-)"
EOF

say "3. physical reseed of db2"
T1=$($N2 'date -u +%s')
$N1 "/tmp/rmapi.sh GET /api/clusters/$CLUSTER/servers/$DB2_ID/actions/reseed/physicalbackup" >/dev/null
RESULT=""
for i in $(seq 1 120); do
    sleep 10
    r=$($N2 "sudo docker exec ${CLUSTER}..db2.container.db sh -c 'stat -c %Y $J/reseedmariabackup.out 2>/dev/null; tail -1 $J/reseedmariabackup.out 2>/dev/null'" 2>/dev/null)
    ts=$(echo "$r" | head -1)
    if [[ "${ts:-0}" -gt "$T1" && "$r" == *"Partial restore"* ]]; then RESULT=$(echo "$r" | tail -1); break; fi
done
say "   ${RESULT:-no result within 20 minutes}"
if [[ "$RESULT" != *"completed successfully"* ]]; then
    # An aborted or failed restore is the end of the cycle: replication will
    # not resume and there is nothing to compare.
    $N2 "sudo docker exec ${CLUSTER}..db2.container.db sh -c \"grep -E 'SKIPPED|Objects not restored|aborted|ERROR:|FAILED' $J/reseed.out | cut -c1-200 | tail -25\"" 2>/dev/null
    say "FAIL: the reseed of db2 did not complete successfully; replication was not checked"
    exit 1
fi
$N2 "sudo docker exec ${CLUSTER}..db2.container.db sh -c \"grep -E '^\\[20' $J/reseed.out | awk -v t=\$(date -u -d @$T1 +%H:%M) '{print}' | grep -E 'SKIPPED|Objects not restored|aborted|ERROR:|FAILED' | cut -c1-200 | tail -25\""

say "4. waiting for replication to resume on db2"
for i in $(seq 1 36); do
    st=$($N1 "env CLUSTER=$CLUSTER bash -s" 2>/dev/null <<'EOF'
/tmp/rmapi.sh GET /api/clusters/$CLUSTER/topology/servers >/dev/null
python3 -c '
import json
print([s["state"] for s in json.load(open("/tmp/rmapi.out")) if s["host"].startswith("db2")][0])'
EOF
)
    [[ "$st" == "Slave" ]] && break
    sleep 5
done
say "   db2 state in repman: ${st:-unknown}"

say "5. comparing db2 with db1"
$N1 "env $ENVR bash -s" <<'EOF'
/tmp/dbstate.sh db2 > /tmp/state-db2.txt
echo "   tables db1=$(grep -c "^T " /tmp/state-db1.txt) db2=$(grep -c "^T " /tmp/state-db2.txt)"
echo "   table checksums identical: $(diff <(grep "^T " /tmp/state-db1.txt) <(grep "^T " /tmp/state-db2.txt) >/dev/null && echo YES || echo NO)"
diff <(grep "^T " /tmp/state-db1.txt) <(grep "^T " /tmp/state-db2.txt) | sed "s/^/   /" | head -20
echo "   db1: $(grep -E "^(C|G) " /tmp/state-db1.txt | cut -c3- | tr "\n" " ")"
echo "   db2: $(grep -E "^(C|G) " /tmp/state-db2.txt | cut -c3- | tr "\n" " ")"
EOF
