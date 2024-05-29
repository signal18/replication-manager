#!/bin/bash
# This script is given as sample and will be overwrite on upgrade

# set exit on error
set -e

BACKUPDIR="/var/lib/replication-manager/backups/$1/$2_$3"

# Binlog Copy Script only get the previous binlog to backup
echo "Binlog copy script args"
echo "Script:$0, Cluster:$1, Server:$2, MySQL Port:$3, Binlog:$4"

#try something failing
ls "$BACKUPDIR/nofolder"


