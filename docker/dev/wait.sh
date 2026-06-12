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
    # Use ps aux and grep to search for the process, exclude the grep command itself
    PROCESS_COUNT=$(ps aux | grep "$AppName" | grep -v grep | grep -v "wait.sh" | wc -l)
    echo "$PROCESS_COUNT"
    # If process count is zero, exit the loop
    if [ "$PROCESS_COUNT" -eq 0 ]; then
        echo "Process '$AppName' is no longer running."
        break
    fi

    # Process is still running, sleep before checking again
    echo "$PROCESSES"
    sleep 5
done

echo "Done."
