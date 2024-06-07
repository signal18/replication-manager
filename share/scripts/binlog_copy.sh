#!/bin/bash
# This script is given as sample and will be overwrite on upgrade

# set exit on error
set -e

# Binlog Copy Script only get the previous binlog to backup
echo "Binlog copy script args"
echo "Script:$0, Cluster:$1, Server:$2, MySQL Port:$3, SSH Port: $4, Source Binlog Path: $5, Destination Path (Repman): $6, Binlog Filename: $7"

echo "Write dummy logs as binlog" > "$6/$7"

echo "Write dummy logs as binlog completed"
