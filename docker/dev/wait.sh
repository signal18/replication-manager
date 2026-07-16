#!/bin/bash

# Name or keyword of the process to monitor
AppName="$1"

# Check if a process name/keyword was provided
if [ -z "$AppName" ]; then
    echo "Usage: $0 <AppName>"
    exit 1
fi

echo "Monitoring process: '$AppName' until it disappears..."
T0=$(date +%s)

# Loop to monitor the process
while true; do
    # Match the actual monitor process only. Without the 'monitor' qualifier this
    # grep counts any line containing "$AppName" — a concurrent run.sh/wait.sh, the
    # start command line, etc. — so it spun for minutes on a phantom while repman
    # was already gone from ps. stop.sh matches the same way (grep replication |
    # grep monitor). Instrumented: print the matched ps lines each poll so we can
    # SEE what is being counted, with elapsed time; poll every 1s.
    MATCHES=$(ps aux | grep "$AppName" | grep monitor | grep -v grep | grep -v "wait.sh")
    PROCESS_COUNT=$(echo "$MATCHES" | grep -c .)
    ELAPSED=$(( $(date +%s) - T0 ))
    echo "[+${ELAPSED}s] '$AppName' count=$PROCESS_COUNT"
    [ "$PROCESS_COUNT" -gt 0 ] && echo "$MATCHES"

    # If process count is zero, exit the loop
    if [ "$PROCESS_COUNT" -eq 0 ]; then
        echo "Process '$AppName' is no longer running after ${ELAPSED}s."
        break
    fi

    # Process is still running, sleep before checking again
    sleep 1
done

echo "Done."
