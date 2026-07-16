#!/bin/bash

# Name or keyword of the process to monitor
AppName="$1"

# Check if a process name/keyword was provided
if [ -z "$AppName" ]; then
    echo "Usage: $0 <AppName>"
    exit 1
fi

echo "Monitoring process: '$AppName' until it disappears..."

# Loop to monitor the process
while true; do
    # Match the actual monitor process only. Without the 'monitor' qualifier this
    # grep counts any line containing "$AppName" — a concurrent run.sh/wait.sh, the
    # start command line, etc. — so it spun for minutes on a phantom while repman
    # was already gone from ps (the fake "long graceful stop"). stop.sh matches the
    # same way (grep replication | grep monitor).
    PROCESS_COUNT=$(ps aux | grep "$AppName" | grep monitor | grep -v grep | grep -v "wait.sh" | wc -l)
    echo "$PROCESS_COUNT"
    # If process count is zero, exit the loop
    if [ "$PROCESS_COUNT" -eq 0 ]; then
        echo "Process '$AppName' is no longer running."
        break
    fi

    # Process is still running, sleep before checking again
    echo "$PROCESS_COUNT"
    sleep 5
done

echo "Done."
