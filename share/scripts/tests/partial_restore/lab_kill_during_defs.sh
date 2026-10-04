#!/bin/bash
# Run on the node of db2 (CLUSTER set in the environment) while a reseed of db2 runs: waits for the temporary
# definition server, then kills every dbjobs_new process (the job dies as a
# whole, like an OOM kill or a timeout would), leaving that server running.
C=${CLUSTER:?set CLUSTER}..db2.container.jobs
for i in $(seq 1 600); do
    sudo docker exec $C test -S /var/lib/mysql/.system/mrm_defs_run/mariadbd.sock 2>/dev/null && break
    sleep 0.3
done
sudo docker exec $C test -S /var/lib/mysql/.system/mrm_defs_run/mariadbd.sock || { echo "temporary server never seen"; exit 1; }
sudo docker exec $C bash -c '
pids=()
for p in /proc/[0-9]*; do
  c=$(tr "\0" " " <"$p/cmdline" 2>/dev/null)
  [[ "$c" == *"/docker-entrypoint-initdb.d/dbjobs_new "* ]] && pids+=("${p#/proc/}")
done
echo "killing dbjobs_new pids: ${pids[*]}"
kill -9 "${pids[@]}"
sleep 2
n=0; for p in /proc/[0-9]*; do tr "\0" " " <"$p/cmdline" 2>/dev/null | grep -q "[m]rm_defs_ro" && n=$((n+1)); done
echo "temporary servers alive after the kill: $n"
ls -d /var/lib/mysql/.system/jobs/*.run 2>/dev/null'
