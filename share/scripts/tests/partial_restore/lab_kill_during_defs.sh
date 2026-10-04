#!/bin/bash
# Run on the node of db2 (CLUSTER set in the environment) while a reseed of db2
# runs: waits for the temporary definition server, then kills the process that
# owns the job (the PID in the job's .run folder, with SIGKILL: the job dies as
# a whole, like an OOM kill or a timeout would), leaving that server running.
# Killing "every dbjobs_new process" is not reliable: several runs of the same
# script are alive at once (job, log follower, jobs-check), and the owner may
# not be among the ones matched.
C=${CLUSTER:?set CLUSTER}..db2.container.jobs
SOCK=/var/lib/mysql/.system/mrm_defs_run/mariadbd.sock
for i in $(seq 1 600); do
    sudo docker exec $C test -S $SOCK 2>/dev/null && break
    sleep 0.3
done
sudo docker exec $C test -S $SOCK || { echo "temporary server never seen"; exit 1; }
sudo docker exec $C bash -c '
J=/var/lib/mysql/.system/jobs
owner=$(cat $J/reseed*.run/pid 2>/dev/null | head -1)
[[ "$owner" =~ ^[0-9]+$ ]] || { echo "no owner PID in $J/reseed*.run"; exit 1; }
echo "killing the job owner, pid $owner"
kill -9 "$owner"
sleep 2
echo "owner alive after the kill: $(kill -0 "$owner" 2>/dev/null && echo yes || echo no)"
n=0; for p in /proc/[0-9]*; do tr "\0" " " <"$p/cmdline" 2>/dev/null | grep -q "[m]rm_defs_ro" && n=$((n+1)); done
echo "temporary servers alive after the kill: $n"
ls -d $J/*.run 2>/dev/null'
