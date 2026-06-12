#!/bin/bash
kill_pid(){
    echo "Killing PID $1 with SIGINT"
    kill -SIGINT "$1"
    ps aux | grep replication | grep monitor | grep -v grep
}

pid="$(ps aux | grep replication | grep monitor | grep -v grep | awk '{print $2}')"
if [ -n "$pid" ]; then
    kill_pid "$pid"
else 
    echo "No PID for repman"
fi 

exit 0
