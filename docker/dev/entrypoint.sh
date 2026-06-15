#!/bin/bash

WORKDIR=/go/src/github.com/signal18/replication-manager
DEVDIR=$WORKDIR/docker/dev

for script in run.sh stop.sh restart.sh wait.sh; do
    [ ! -e "$WORKDIR/$script" ] && ln -s "$DEVDIR/$script" "$WORKDIR/$script"
done

exec "$@"
