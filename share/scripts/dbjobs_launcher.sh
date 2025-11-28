#!/bin/bash
# This is an old script and replaced by dbjobs_launcher_with_sigterm
# This script is given as sample and might be overwritten on upgrade

TMP_DIR=%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%

cleanup_run_dirs() {
    local base_dir="${1:?Error: base directory argument is required.}"

    # Ensure directory exists
    if [ ! -d "$base_dir" ]; then
        echo "Error: Directory '$base_dir' does not exist."
        return 1
    fi

    # Find and count .run directories
    local count
    count=$(find "$base_dir" -type d -name "*.run" 2>/dev/null | wc -l)

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

while true; do /bin/bash /docker-entrypoint-initdb.d/dbjobs_new; sleep 60; done