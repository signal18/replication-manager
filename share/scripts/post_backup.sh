#!/bin/bash
#
# Post-backup script
# Usage: post_backup.sh <clustername> <hostname> <port> <backup-path>
#
# Example:
#   post_backup.sh mycluster db1.example.com 3306 /var/lib/replication-manager/backups/mycluster/db1_3306/mysqldump.sql.gz

CLUSTER_NAME="$1"
HOSTNAME="$2"
PORT="$3"
BACKUP_PATH="$4"

# Customize this directory:
DEST_DIR="/opt/custom-backups/${CLUSTER_NAME}/${HOSTNAME}_${PORT}"

mkdir -p "$DEST_DIR"

if [[ ! -f "$BACKUP_PATH" ]]; then
    echo "Error: backup file not found: $BACKUP_PATH"
    exit 2
fi

# Copy with timestamped filename
TS=$(date +"%Y%m%d_%H%M%S")
BASENAME=$(basename "$BACKUP_PATH")
DEST_FILE="${DEST_DIR}/${TS}_${BASENAME}"

echo "Copying backup to: $DEST_FILE"
cp -f "$BACKUP_PATH" "$DEST_FILE"

echo "Backup copy completed successfully."
