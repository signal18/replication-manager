#!/bin/bash
# Send SIGINT to the repman monitor process(es).
#
# The old code did:
#   pid="$(ps ... | awk '{print $2}')"; kill -SIGINT "$pid"
# When more than one monitor matched (e.g. a leftover from a double start),
# "$pid" became a multi-line string like "1234\n5678", and the single quoted
# kill got an INVALID target — so the kill silently failed and repman NEVER
# received SIGINT. It then looked like a 20-minute "graceful stop" while repman
# was actually just running normally, never told to stop (the SIGINT handler
# stays parked on <-sigs). Signal each pid on its own, and report how many were
# found — 0 or >1 is itself worth seeing.

pids=$(ps aux | grep replication | grep monitor | grep -v grep | grep -v stop.sh | awk '{print $2}')
count=$(printf '%s\n' "$pids" | grep -c .)

if [ "$count" -eq 0 ]; then
    echo "No repman monitor process running"
    exit 0
fi

echo "Found $count repman monitor process(es): $(echo $pids | tr '\n' ' ')"
for pid in $pids; do
    echo "Sending SIGINT to PID $pid"
    kill -SIGINT "$pid"
done

exit 0
