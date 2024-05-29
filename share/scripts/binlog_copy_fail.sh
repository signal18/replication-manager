#!/bin/bash
# This script is given as sample and will be overwrite on upgrade

# set exit on error
set -e

# Binlog Copy Script only get the previous binlog to backup
echo "Binlog copy script args"
echo "Script:$0, Cluster:$1, Server:$2, MySQL Port:$3, Backup Directory: $4, Binlog:$5"

#try something failing
ls nofolder


