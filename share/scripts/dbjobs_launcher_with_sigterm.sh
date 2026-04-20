#!/bin/bash
# This script is given as sample and might be overwritten on upgrade
# This script handles SIGTERM signals to gracefully terminate the job processing loop.

TMP_DIR=%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%

trap 'sigterm_handler' SIGTERM

function sigterm_handler() {
    kill -TERM 1
    sleep 1
    exit 0
}

cleanup_run_dirs() {
    local base_dir="${1:?Error: base directory argument is required.}"

    # Ensure directory exists
    if [ ! -d "$base_dir" ]; then
        echo "Error: Directory '$base_dir' does not exist."
        return 1
    fi

    # Find and count .run directories
    local count
    count=$(find "$base_dir" -type d -name "*.run" | wc -l)

    if [ "$count" -eq 0 ]; then
        echo "No .run directories found under $base_dir."
        return 0
    fi

    echo "Found $count .run directories under $base_dir."
    echo "Deleting..."

    # Delete safely
    find "$base_dir" -type d -name "*.run" -print0 | xargs -0 rm -rf --

    echo "Done! Deleted $count directories."
    return 0
}

cleanup_run_dirs "$TMP_DIR"

while true
do 
    /bin/bash /docker-entrypoint-initdb.d/dbjobs_new
    # give change to trap docker sigterm
    for _ in {1..120}; do sleep 0.5 ; done
done